package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/s-visser/gd-sync/internal/migrate"
	"github.com/s-visser/gd-sync/internal/ui"
)

func (c *ctx) migrations(ref string) (migrate.Chain, error) {
	files := map[string][]byte{}
	if ref != "" {
		out, err := c.git.Output("ls-tree", "-rz", "--name-only", ref, "--", migrate.Dir+"/")
		if err != nil {
			return nil, err
		}
		for _, p := range strings.Split(out, "\x00") {
			if !strings.HasSuffix(p, ".php") {
				continue
			}
			name := strings.TrimPrefix(p, migrate.Dir+"/")
			if strings.Contains(name, "/") {
				return nil, fmt.Errorf("Migration %s must be directly inside %s", p, migrate.Dir)
			}
			src, err := c.git.Output("show", ref+":"+p)
			if err != nil {
				return nil, err
			}
			files[name] = []byte(src)
		}
	} else {
		entries, err := os.ReadDir(filepath.Join(c.s.Repo, migrate.Dir))
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".php") {
				continue
			}
			if !e.Type().IsRegular() {
				return nil, fmt.Errorf("Migration %s must be a regular PHP file", e.Name())
			}
			src, err := os.ReadFile(filepath.Join(c.s.Repo, migrate.Dir, e.Name()))
			if err != nil {
				return nil, err
			}
			files[e.Name()] = src
		}
	}
	return migrate.Load(files)
}

// Read through SQL rather than option get: a missing option is base, whereas
// a failed connection/bootstrap must remain an error. A marker isolates the value
// from ordinary plugin output and never silently treats errors as base.
func (c *ctx) currentRevision(local bool) (string, error) {
	code := `if (version_compare(PHP_VERSION, '7.0', '<')) { WP_CLI::error('gd-sync migrations require PHP 7.0 or newer.'); } global $wpdb; $v = $wpdb->get_var($wpdb->prepare("SELECT option_value FROM {$wpdb->options} WHERE option_name = %s", '` + migrate.Option + `')); if ($wpdb->last_error !== '') { WP_CLI::error($wpdb->last_error); } echo "\ngd-sync-revision:" . ($v === null ? '' : $v) . "\n";`
	out, err := c.wpOutput(local, []string{"eval", code}, nil)
	if err != nil {
		return "", fmt.Errorf("Could not read database revision: %w", err)
	}
	const marker = "gd-sync-revision:"
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, marker) {
			return strings.TrimPrefix(line, marker), nil
		}
	}
	return "", fmt.Errorf("WP-CLI did not return a database revision: %s", out)
}

func (c *ctx) migrationPlan(chain migrate.Chain, local bool, target string) ([]migrate.Step, error) {
	if err := c.prepareWP(local); err != nil {
		return nil, err
	}
	current, err := c.currentRevision(local)
	if err != nil {
		return nil, err
	}
	from, err := chain.Position(current)
	if err != nil {
		return nil, err
	}
	to, err := chain.Target(from, target)
	if err != nil {
		return nil, err
	}
	return chain.Plan(from, to), nil
}

func printMigrations(steps []migrate.Step) {
	for _, s := range steps {
		ui.Info("%s", s.String())
	}
}

func (c *ctx) applyMigrations(local bool, steps []migrate.Step, backup string) error {
	for _, s := range steps {
		ui.Step("%s", s.String())
		if err := c.wpRun(local, []string{"eval-file", "-"}, strings.NewReader(migrate.Script(s)), os.Stdout); err != nil {
			if local {
				return fmt.Errorf("Migration %s failed: %w. Later migrations were not run; inspect the local database before retrying", s.Revision, err)
			}
			return fmt.Errorf("Migration %s failed: %w\nLater migrations were not run. Files may already be live; inspect the database before retrying.\nDatabase backup: %s\nRestore with: gd-sync db --restore %s", s.Revision, err, backup, backup)
		}
	}
	return nil
}

func cmdMigrate(c *ctx, args []string) error {
	if c.s.Type != "wordpress" {
		ui.Info("%s has no database migrations.", c.s.Name)
		return nil
	}
	live := false
	var rest []string
	for _, a := range args {
		switch a {
		case "--live":
			live = true
		case "--local":
		default:
			rest = append(rest, a)
		}
	}
	command := "upgrade"
	if len(rest) > 0 {
		command = rest[0]
		rest = rest[1:]
	}
	if command != "new" && command != "history" && command != "current" && command != "status" && command != "upgrade" && command != "downgrade" {
		return errors.New("Usage: gd-sync migrate [new <message>|history|current|status|upgrade [revision]|downgrade [revision]] [--live]")
	}
	ref := ""
	if live {
		ref = "main"
	}
	chain, err := c.migrations(ref)
	if err != nil {
		return err
	}
	switch command {
	case "new":
		if live {
			return errors.New("Create migrations locally, test them, then merge them into main")
		}
		if len(rest) == 0 {
			return errors.New("Usage: gd-sync migrate new <message>")
		}
		rev := migrate.NewRevision()
		name := migrate.FileName(rev, strings.Join(rest, " "))
		dir := filepath.Join(c.s.Repo, migrate.Dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(migrate.Template(rev, chain.Head(), strings.Join(rest, " "), stamp("2006-01-02 15:04:05")))
		cerr := f.Close()
		if err != nil {
			return err
		}
		if cerr != nil {
			return cerr
		}
		ui.OK("Created %s/%s (follows %s)", migrate.Dir, name, migrate.Label(chain.Head()))
		ui.Info("Commit this file. If git ignores it, run gd-sync scaffold.")
		return nil
	case "history":
		if len(rest) > 0 {
			return errors.New("Usage: gd-sync migrate history [--live]")
		}
		for _, m := range chain {
			ui.Info("%s -> %s  %s", migrate.Label(m.DownRevision), m.Revision, m.Title)
		}
		return nil
	}
	if len(rest) > 1 || ((command == "current" || command == "status") && len(rest) > 0) {
		return fmt.Errorf("Unexpected arguments to migrate %s", command)
	}
	var unlock func()
	if live {
		ui.Info("LIVE %s — %s (migrations from committed main)", c.s.Name, c.s.LiveURL)
		if command == "upgrade" || command == "downgrade" {
			unlock, err = c.lockLive()
			if err != nil {
				return err
			}
			defer unlock()
		}
	} else {
		ui.Info("LOCAL %s — %s", c.s.Name, c.s.LocalURL)
	}
	if err := c.prepareWP(!live); err != nil {
		return err
	}
	current, err := c.currentRevision(!live)
	if err != nil {
		return err
	}
	from, err := chain.Position(current)
	if err != nil {
		return err
	}
	if command == "current" || command == "status" {
		ui.Info("Database: %s; head: %s; pending: %d", migrate.Label(current), migrate.Label(chain.Head()), len(chain)-from)
		if command == "status" {
			printMigrations(chain.Plan(from, len(chain)))
		}
		return nil
	}
	target := "head"
	if command == "downgrade" {
		target = "-1"
	}
	if len(rest) > 0 {
		target = rest[0]
	}
	to, err := chain.Target(from, target)
	if err != nil {
		return err
	}
	if command == "upgrade" && to < from {
		return errors.New("Target is behind the database; use migrate downgrade")
	}
	if command == "downgrade" && to > from {
		return errors.New("Target is ahead of the database; use migrate upgrade")
	}
	steps := chain.Plan(from, to)
	if len(steps) == 0 {
		ui.OK("Database already at %s", migrate.Label(current))
		return nil
	}
	printMigrations(steps)
	backup := ""
	if live {
		if !ui.Confirm("Apply these migrations to LIVE (database backup first)?") {
			return nil
		}
		backup, err = c.backupDB()
		if err != nil {
			return err
		}
	}
	if err := c.applyMigrations(!live, steps, backup); err != nil {
		return err
	}
	ui.OK("Database at %s", migrate.Label(steps[len(steps)-1].To))
	return nil
}

func (c *ctx) statusMigrations() error {
	chain, err := c.migrations("")
	if err != nil {
		return err
	}
	if len(chain) == 0 {
		return nil
	}
	steps, err := c.migrationPlan(chain, false, "head")
	if err != nil {
		return err
	}
	ui.Info("Pending live migrations: %d (working branch)", len(steps))
	printMigrations(steps)
	return nil
}
