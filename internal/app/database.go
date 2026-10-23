package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/s-visser/gd-sync/internal/remote"
	"github.com/s-visser/gd-sync/internal/ui"
)

// wpRun keeps arguments separate locally and quotes every word on SSH. In
// particular, eval code and option values must never become shell commands.
func (c *ctx) wpRun(local bool, args []string, in io.Reader, out io.Writer) error {
	if !local {
		conn, err := c.server()
		if err != nil {
			return err
		}
		return conn.Run("cd "+remote.Quote(c.s.RemoteRoot)+" && ~/wp-cli.phar "+remote.QuoteAll(args), in, out)
	}
	argv := append([]string{"--path=/var/www/html"}, args...)
	var cmd *exec.Cmd
	if inContainer() {
		cmd = exec.Command("wp", argv...)
	} else {
		dc := filepath.Join(c.s.Repo, ".devcontainer")
		argv = append([]string{"compose", "--env-file", filepath.Join(dc, ".env"), "-f", filepath.Join(dc, "docker-compose.yml"), "exec", "-T", "workspace", "wp"}, argv...)
		cmd = exec.Command("docker", argv...)
	}
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = c.s.Repo, in, out, os.Stderr
	return cmd.Run()
}

func (c *ctx) prepareWP(local bool) error {
	if local {
		return nil
	}
	conn, err := c.server()
	if err != nil {
		return err
	}
	return ensureWPCLI(conn)
}

// Unknown commands (including plugin commands) are treated as writes. Only
// known built-in read operations with no execution/target overrides are exempt.
func wpReadOnly(args []string) bool {
	if len(args) < 2 {
		return false
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--") && !strings.HasPrefix(a, "--format=") && !strings.HasPrefix(a, "--fields=") && !strings.HasPrefix(a, "--field=") {
			return false
		}
	}
	allowed := map[string]string{
		"option": "get list", "post": "get list", "user": "get list", "term": "get list", "comment": "get list",
		"plugin": "list status is-active is-installed", "theme": "list status is-active is-installed",
		"core": "version is-installed", "db": "size tables prefix columns", "cli": "version info", "config": "get list",
	}
	return strings.Contains(" "+allowed[args[0]]+" ", " "+args[1]+" ")
}

func cmdWP(c *ctx, args []string) error {
	local := false
	if len(args) > 0 && args[0] == "--local" {
		local = true
		args = args[1:]
	}
	if len(args) == 0 {
		return errors.New("Usage: gd-sync [-y] wp [--local] <WP-CLI command> [arguments]")
	}
	if c.s.Type != "wordpress" {
		ui.Info("%s has no WordPress database.", c.s.Name)
		return nil
	}
	// A target override could bypass the backup of this site's database.
	for _, a := range args {
		if strings.HasPrefix(a, "@") || a == "--path" || strings.HasPrefix(a, "--path=") || a == "--ssh" || strings.HasPrefix(a, "--ssh=") || a == "--http" || strings.HasPrefix(a, "--http=") {
			return fmt.Errorf("%s overrides this site's WP-CLI target; run gd-sync from the intended site", a)
		}
	}
	if local {
		return c.wpRun(true, args, os.Stdin, os.Stdout)
	}
	ui.Info("LIVE %s — %s", c.s.Name, c.s.LiveURL)
	var backup string
	if !wpReadOnly(args) {
		if !ui.Confirm("Run this WP-CLI command on LIVE (database backup first)?") {
			return nil
		}
		unlock, err := c.lockLive()
		if err != nil {
			return err
		}
		defer unlock()
		if err := c.prepareWP(false); err != nil {
			return err
		}
		backup, err = c.backupDB()
		if err != nil {
			return err
		}
	} else if err := c.prepareWP(false); err != nil {
		return err
	}
	if err := c.wpRun(false, args, os.Stdin, os.Stdout); err != nil {
		if backup != "" {
			return fmt.Errorf("WP-CLI failed: %w\nDatabase backup: %s\nRestore with: gd-sync db --restore %s", err, backup, backup)
		}
		return err
	}
	return nil
}

// A site-wide lock also prevents two deploys from interleaving their files
// and database changes. A disconnected process leaves a visible lock to inspect.
func (c *ctx) lockLive() (func(), error) {
	conn, err := c.server()
	if err != nil {
		return nil, err
	}
	lock := c.serverRecord("write-lock")
	if err := conn.Run("mkdir -p ~/.gd-sync/"+remote.Quote(c.s.Name)+" && mkdir "+lock, nil, nil); err != nil {
		return nil, fmt.Errorf("Could not lock live site %s: %w. Another write may be running. If a previous process stopped, inspect and remove %s on the server", c.s.Name, err, lock)
	}
	return func() {
		if err := conn.Run("rmdir "+lock, nil, nil); err != nil {
			ui.Warn("Could not release %s: %v", lock, err)
		}
	}, nil
}

func (c *ctx) backupPrefix() string { return c.s.Name + "-db-" }
func backupPath(name string) string { return "~/gd-sync-backups/" + remote.Quote(name) }

// Database backups are private, uniquely named, and only published after a
// successful nonempty export. A backup failure always stops the live write.
func (c *ctx) backupDB(protect ...string) (string, error) {
	conn, err := c.server()
	if err != nil {
		return "", err
	}
	name := c.backupPrefix() + stamp("20060102-150405.000000000") + ".sql"
	p := backupPath(name)
	ui.Step("Backing up the LIVE database")
	cmd := "umask 077 && mkdir -p ~/gd-sync-backups && cd " + remote.Quote(c.s.RemoteRoot) + " && ~/wp-cli.phar db export " + p + ".partial --single-transaction --quick --default-character-set=utf8mb4 --skip-plugins --skip-themes && test -s " + p + ".partial && mv " + p + ".partial " + p
	if err := conn.Run(cmd, nil, os.Stdout); err != nil {
		_ = conn.Run("rm -f -- "+p+".partial", nil, nil)
		return "", fmt.Errorf("Database backup failed; live write stopped: %w", err)
	}
	ui.OK("~/gd-sync-backups/%s", name)
	// Fixed suffix and quoted prefix keep site names from becoming shell syntax.
	keep := ""
	if len(protect) > 0 {
		keep = "if test \"${f##*/}\" = " + remote.Quote(protect[0]) + "; then continue; fi; "
	}
	prune := "ls -1t ~/gd-sync-backups/" + remote.Quote(c.backupPrefix()) + "*.sql 2>/dev/null | tail -n +21 | while IFS= read -r f; do " + keep + "rm -f -- \"$f\"; done"
	if err := conn.Run(prune, nil, nil); err != nil {
		ui.Warn("Could not prune database backups: %v", err)
	}
	return name, nil
}

func cmdDBRestore(c *ctx, name string) error {
	if c.s.Type != "wordpress" {
		ui.Info("%s has no database.", c.s.Name)
		return nil
	}
	conn, err := c.server()
	if err != nil {
		return err
	}
	unlock, err := c.lockLive()
	if err != nil {
		return err
	}
	defer unlock()
	if name == "" {
		out, err := conn.Output("ls -1t ~/gd-sync-backups/"+remote.Quote(c.backupPrefix())+"*.sql 2>/dev/null | head -n 1", nil)
		if err != nil {
			return err
		}
		name = filepath.Base(strings.TrimSpace(string(out)))
	}
	// Accept a printed ~/gd-sync-backups path or just the filename. Never import
	// an arbitrary uploaded/local SQL file into live through this command.
	name = strings.TrimPrefix(name, "~/gd-sync-backups/")
	if filepath.Base(name) != name || strings.ContainsAny(name, "/\\\r\n") || !strings.HasPrefix(name, c.backupPrefix()) || !strings.HasSuffix(name, ".sql") {
		return errors.New("Choose this site's backup from ~/gd-sync-backups (gd-sync db --restore [filename])")
	}
	if err := conn.Run("test -s "+backupPath(name), nil, nil); err != nil {
		return fmt.Errorf("No nonempty database backup named %s", name)
	}
	ui.Warn("Restoring LIVE %s — %s from %s replaces its database, including its migration revision.", c.s.Name, c.s.LiveURL, name)
	if !ui.Confirm("Restore this LIVE database backup?") {
		return nil
	}
	if err := c.prepareWP(false); err != nil {
		return err
	}
	rescue, err := c.backupDB(name)
	if err != nil {
		return err
	}
	// Stream the selected file through stdin: WP-CLI needs a real server path,
	// and ~ inside a quoted WP-CLI argument would otherwise stay literal.
	cmd := "cd " + remote.Quote(c.s.RemoteRoot) + " && ~/wp-cli.phar db import - < " + backupPath(name)
	if err := conn.Run(cmd, nil, os.Stdout); err != nil {
		return fmt.Errorf("Restore failed: %w\nPre-restore backup: %s", err, rescue)
	}
	if err := c.wpRun(false, []string{"cache", "flush"}, nil, os.Stdout); err != nil {
		return fmt.Errorf("Database restored, but cache flush failed: %w", err)
	}
	ui.OK("Restored %s (pre-restore backup: %s)", name, rescue)
	return nil
}

func (c *ctx) wpOutput(local bool, args []string, in io.Reader) (string, error) {
	var b bytes.Buffer
	err := c.wpRun(local, args, in, &b)
	return strings.TrimSpace(b.String()), err
}
