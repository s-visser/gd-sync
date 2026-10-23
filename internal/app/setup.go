package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/s-visser/gd-sync/internal/config"
	"github.com/s-visser/gd-sync/internal/gitx"
	"github.com/s-visser/gd-sync/internal/remote"
	"github.com/s-visser/gd-sync/internal/scaffold"
	"github.com/s-visser/gd-sync/internal/site"
	"github.com/s-visser/gd-sync/internal/ui"
)

func cmdConfig(g *config.Global, args []string) error {
	switch {
	case len(args) == 0 || args[0] == "--list" || args[0] == "list":
		fmt.Printf("%sGlobal settings%s  (%s)\n\n", ui.B, ui.N, g.Path)
		for _, k := range config.Keys {
			v, _ := g.Get(k)
			if v == "" {
				v = "<unset>"
			}
			fmt.Printf("  %-14s %s\n", k, v)
		}
		ui.Say("")
		ui.Info("change one:  gd-sync config projects_dir ~/Sites")
		ui.Info("or run the short setup again:  gd-sync config --setup")
		return nil
	case args[0] == "--setup" || args[0] == "setup":
		ui.Step("gd-sync setup")
		g.Set("projects_dir", ui.Ask("Where should new site folders go?", g.ProjectsDir))
		g.Set("git_owner", ui.Ask("GitHub owner for new repos (user or org):", g.GitOwner))
		for {
			err := g.Set("git_private", ui.Ask("Make new repos private? (true/false):", g.GitPrivate))
			if err == nil {
				break
			}
			ui.Warn("%v", err)
		}
		g.Set("default_type", ui.Ask("Default site type (wordpress/astro):", g.DefaultType))
		if err := g.Save(); err != nil {
			return err
		}
		ui.OK("Saved to %s", g.Path)
		return nil
	}
	k := args[0]
	if len(args) < 2 || args[1] == "" {
		return fmt.Errorf("usage: gd-sync config %s <value>", k)
	}
	if err := g.Set(k, args[1]); errors.Is(err, config.ErrUnknownKey) {
		return fmt.Errorf("Unknown setting '%s' — try: gd-sync config", k)
	} else if err != nil {
		return fmt.Errorf("%s: %w", k, err)
	}
	if err := g.Save(); err != nil {
		return err
	}
	ui.OK("%s = %s", k, args[1])
	return nil
}

func cmdList() error {
	fmt.Printf("%sSites%s\n\n", ui.B, ui.N)
	paths := config.RegistryList()
	for _, p := range paths {
		s, err := site.Load(p)
		if err != nil {
			fmt.Printf("  %s%s%s %s(unreadable)%s\n", ui.B, p, ui.N, ui.R, ui.N)
			continue
		}
		fmt.Printf("  %s%-16s%s %-10s %s\n     %s%s%s\n", ui.B, s.Name, ui.N, s.Type, s.LiveURL, ui.DIM, p, ui.N)
	}
	if len(paths) == 0 {
		ui.Info("none yet — run: gd-sync new")
	}
	return nil
}

func cmdScaffold(c *ctx) error {
	gitSettings(c.git)
	if err := scaffold.All(c.s); err != nil {
		return err
	}
	if err := config.RegistryAdd(c.s.Repo); err != nil {
		return err
	}
	ui.OK("Scaffolding refreshed for %s", c.s.Name)
	ui.Info("GD-SYNC.md, devcontainer, .github/workflows, .run, %s, .gitignore, .gitattributes", site.File)
	if out, _ := c.git.Output("ls-files", "--others", "--exclude-standard", "--directory"); strings.TrimSpace(out) != "" {
		ui.Info("Not in git yet — newly maintained paths show up here. Commit them on develop:")
		ui.List("", "", strings.Fields(out))
		ui.Info("git add -A && git commit -m 'Maintain ...', then: gd-sync merge")
	}
	return nil
}

// cmdAdopt registers a folder that already has a .gd-sync.toml: a clone on a new
// machine, or a site set up by a colleague.
func cmdAdopt(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("No such folder: %s", dir)
	}
	repo, err := site.Find(abs)
	if err != nil {
		return fmt.Errorf("%s has no %s — set a site up with: gd-sync new", abs, site.File)
	}
	s, err := site.Load(repo)
	if err != nil {
		return err
	}
	gitSettings(gitx.Repo{Dir: repo})
	if s.SSHHost == "" || s.SSHUser == "" {
		ui.Step("%s has no connection details on this machine", s.Name)
		ui.Info("SSH: GoDaddy dashboard -> your hosting -> Settings -> SSH access -> Manage")
		s.SSHHost = ui.Ask("SSH host:", s.SSHHost)
		s.SSHUser = ui.Ask("SSH username:", s.SSHUser)
		s.SSHPort = ui.Ask("SSH port:", s.SSHPort)
		if err := s.Write(); err != nil {
			return err
		}
		ui.OK("wrote %s", filepath.Join(repo, site.LocalFile))
	}
	if err := config.RegistryAdd(repo); err != nil {
		return err
	}
	ui.OK("%s registered", s.Name)
	ui.Info("%s", repo)
	return nil
}

// cmdNew asks a few questions, then builds everything: folder, repo,
// settings, devcontainer, workflows, the first server snapshot, GitHub.
func cmdNew(g *config.Global) error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is required")
	}
	ui.Say("")
	fmt.Printf("%sNew site%s — a few questions, then everything gets built.\n\n", ui.B, ui.N)

	domain := bareDomain(ui.Ask("Domain (e.g. acme.nl):", ""))
	if domain == "" {
		return errors.New("A domain is required")
	}
	name := siteName(ui.Ask("Name for this site:", siteName(domain)))
	if name == "" {
		return errors.New("A name is required")
	}
	dir := config.ExpandHome(ui.Ask("Local folder:", filepath.Join(g.ProjectsDir, name)))
	dir, _ = filepath.Abs(dir)
	if err := emptyOrMissing(dir); err != nil {
		return err
	}
	ghDefault := ""
	if g.GitOwner != "" {
		ghDefault = g.GitOwner + "/" + name
	}
	ghName := ui.Ask(`GitHub repo (owner/name, or "none"):`, ghDefault)
	switch strings.ToLower(ghName) {
	case "none", "-":
		ghName = ""
	}
	stype := ui.Ask("Type — wordpress or astro:", g.DefaultType)
	if stype != "wordpress" && stype != "astro" {
		return errors.New("Type must be 'wordpress' or 'astro'")
	}

	fmt.Fprintln(os.Stderr)
	ui.Info("SSH: GoDaddy dashboard -> your hosting -> Settings -> SSH access -> Manage")
	s := &site.Site{Repo: dir, Name: name, Type: stype, LiveURL: "https://" + domain}
	s.SSHHost = ui.Ask("SSH host (IP or server name):", "")
	s.SSHUser = ui.Ask("SSH username (cPanel username):", "")
	s.SSHPort = ui.Ask("SSH port:", "22")
	s.RemoteRoot = strings.TrimSuffix(ui.Ask("Remote web root:", "/home/"+s.SSHUser+"/public_html/"+domain), "/")
	if stype == "wordpress" {
		fmt.Fprintln(os.Stderr)
		ui.Info("Which paths do YOU maintain? Space separated, relative to the web root,")
		ui.Info("e.g. wp-content/plugins/%s-custom. Only these go into git; the rest of", strings.SplitN(name, ".", 2)[0])
		ui.Info("the site is downloaded too, so it runs locally, but git ignores it.")
		ui.Info("Leave empty to start with none — add them to tracked in %s later.", site.File)
		s.Tracked = strings.Fields(ui.Ask("Maintained paths:", ""))
	}
	s.Defaults()

	// ---- build it -----------------------------------------------------------
	ui.Step("Creating %s", dir)
	if err := os.MkdirAll(filepath.Join(dir, "_db"), 0o755); err != nil {
		return err
	}
	git := gitx.Repo{Dir: dir}
	if !fileExists(filepath.Join(dir, ".git")) {
		if err := git.Quiet("init", "-q"); err != nil {
			return err
		}
		_ = git.Quiet("symbolic-ref", "HEAD", "refs/heads/main")
	}
	gitSettings(git)
	if err := scaffold.All(s); err != nil {
		return err
	}
	if err := config.RegistryAdd(dir); err != nil {
		return err
	}
	if err := git.Quiet("add", "-A"); err != nil {
		return err
	}
	if !git.OK("rev-parse", "--verify", "-q", "HEAD") {
		if err := git.Commit("Scaffold " + name); err != nil {
			return err
		}
	}
	if !git.OK("rev-parse", "--verify", "-q", "main") {
		_ = git.Quiet("branch", "-M", "main")
	}
	ui.OK("repo created")

	c := &ctx{cfg: g, s: s, git: git}
	defer c.close()

	ui.Step("Testing SSH")
	conn, err := c.server()
	if err != nil {
		return fmt.Errorf(`%v
  Enable it: GoDaddy dashboard -> Settings tab -> SSH access -> Manage -> toggle on.
  Your cPanel password is the SSH password.
  Everything else is written — correct %s, then run: gd-sync sync`, err, filepath.Join(dir, site.LocalFile))
	}
	ui.OK("SSH works")
	if conn.Run("test -d "+remote.Quote(s.RemoteRoot), nil, nil) != nil {
		return fmt.Errorf(`Remote path not found: %s
  Fix remote_root in %s, then run: gd-sync sync`, s.RemoteRoot, filepath.Join(dir, site.File))
	}

	if stype == "wordpress" {
		ui.Step("Matching the live site's PHP version")
		if changed, err := c.checkPHP(conn, true); err != nil {
			return err
		} else if changed {
			if err := git.Quiet("add", "-A"); err != nil {
				return err
			}
			if err := git.Commit("Use PHP " + s.PHPVersion + ", as the live site does"); err != nil {
				return err
			}
		}
	}

	if ui.Confirm("Install an SSH key so you stop typing the password?") {
		if err := cmdKey(c); err != nil {
			ui.Fail(err)
		}
	}
	if stype == "wordpress" {
		c.keepBranch = true
		if err := cmdSync(c); err != nil {
			return err
		}
		if ui.Confirm("Download the live database now?") {
			if err := cmdDB(c); err != nil {
				ui.Fail(err)
			}
		}
	}

	if !git.OK("rev-parse", "--verify", "-q", "develop") {
		_ = git.Quiet("branch", "develop", "main")
	}
	if err := git.Quiet("checkout", "-q", "develop"); err != nil {
		return err
	}

	if ghName != "" {
		if _, err := exec.LookPath("gh"); err != nil {
			ui.Warn("GitHub CLI not installed — create %s yourself, then: gd-sync secrets", ghName)
		} else {
			ui.Step("Setting up GitHub")
			if !git.HasRemote() {
				vis := "--private"
				if g.GitPrivate == "false" {
					vis = "--public"
				}
				cmd := exec.Command("gh", "repo", "create", ghName, vis, "--source=.", "--remote=origin", "--push")
				cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
				if cmd.Run() != nil {
					ui.Warn("Could not create %s — add the remote by hand", ghName)
				}
			}
			_ = git.Quiet("push", "-q", "-u", "origin", "main", "develop")
			if err := cmdSecrets(c); err != nil {
				ui.Fail(err)
				ui.Warn("Set secrets later with: gd-sync secrets")
			}
		}
	}

	ui.Say("")
	ui.OK("%s is ready.", name)
	ui.Say("")
	ui.Info("cd %s", dir)
	ui.Info("Open it in your JetBrains IDE and let it start the devcontainer, or: gd-sync up")
	ui.Info("From inside that folder gd-sync needs no arguments: gd-sync sync, gd-sync merge, gd-sync db")
	return nil
}

// bareDomain turns what was typed — maybe a URL — into a domain:
// "https://Acme.nl/" becomes "acme.nl".
func bareDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if _, rest, ok := strings.Cut(s, "://"); ok {
		s = rest
	}
	s, _, _ = strings.Cut(s, "/")
	return s
}

// siteName keeps what is safe in a folder, a repo name and a shell command:
// letters, digits, dots, dashes and underscores.
func siteName(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '.' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return -1
	}, s)
}

// emptyOrMissing refuses a folder that already has something in it, the way
// git clone does.
func emptyOrMissing(dir string) error {
	if site.Is(dir) {
		return fmt.Errorf("%s is already a gd-sync site — use 'gd-sync adopt' there instead", dir)
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not an empty folder", dir)
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
