package app

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		ui.Info("If your sites share a server, its SSH details save typing them for every site.")
		ui.Info("SSH: GoDaddy dashboard -> your hosting -> Settings -> SSH access -> Manage")
		g.Set("ssh_host", ui.Ask("SSH host (IP or server name, empty for none):", g.SSHHost))
		g.Set("ssh_user", ui.Ask("SSH username (cPanel username):", g.SSHUser))
		for {
			err := g.Set("ssh_port", ui.Ask("SSH port:", g.SSHPort))
			if err == nil {
				break
			}
			ui.Warn("%v", err)
		}
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
	sites := make([]*site.Site, len(paths))
	width := 16
	for i, p := range paths {
		if s, err := site.Load(p); err == nil {
			sites[i], width = s, max(width, len(s.Name))
		}
	}
	for i, p := range paths {
		s := sites[i]
		if s == nil {
			fmt.Printf("  %s%s%s %s(unreadable)%s\n", ui.B, p, ui.N, ui.R, ui.N)
			continue
		}
		fmt.Printf("  %s%-*s%s  %-10s %s\n", ui.B, width, s.Name, ui.N, s.Type, s.LiveURL)
		fmt.Printf("  %-*s  %-10s %s\n     %s%s%s\n", width, "", "", s.LocalURL, ui.DIM, p, ui.N)
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
	if c.s.Type == "wordpress" {
		if err := c.vetUntracked(); err != nil {
			return err
		}
	}
	if out, _ := c.git.Output("ls-files", "--others", "--exclude-standard", "--directory"); strings.TrimSpace(out) != "" {
		ui.Info("Not in git yet — newly maintained paths show up here. Commit them on develop:")
		ui.List("", "", strings.Fields(out))
		ui.Info("git add -A && git commit -m 'Maintain ...', then: gd-sync merge")
	}
	return nil
}

// cmdAdopt registers a folder that already has a .gd-sync.toml: a clone on a new
// machine, or a site set up by a colleague.
func cmdAdopt(g *config.Global, dir string) error {
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
		s.SSHHost = ui.Ask("SSH host:", or(s.SSHHost, g.SSHHost))
		s.SSHUser = ui.Ask("SSH username:", or(s.SSHUser, g.SSHUser))
		s.SSHPort = ui.Ask("SSH port:", g.SSHPort)
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
	stype := ui.Ask("Type — wordpress or astro:", g.DefaultType)
	if stype != "wordpress" && stype != "astro" {
		return errors.New("Type must be 'wordpress' or 'astro'")
	}

	fmt.Fprintln(os.Stderr)
	if g.SSHHost == "" {
		ui.Info("SSH: GoDaddy dashboard -> your hosting -> Settings -> SSH access -> Manage")
		ui.Info("Same server for all your sites? Save it once with: gd-sync config --setup")
	}
	s := &site.Site{Repo: dir, Name: name, Type: stype, LiveURL: "https://" + domain}
	s.SSHHost = ui.Ask("SSH host (IP or server name):", g.SSHHost)
	s.SSHUser = ui.Ask("SSH username (cPanel username):", g.SSHUser)
	s.SSHPort = ui.Ask("SSH port:", g.SSHPort)
	s.RemoteRoot = strings.TrimSuffix(ui.Ask("Remote web root:", "/home/"+s.SSHUser+"/public_html/"+domain), "/")
	if stype == "wordpress" {
		fmt.Fprintln(os.Stderr)
		ui.Info("Which paths do YOU maintain? Space separated, relative to the web root,")
		ui.Info("e.g. wp-content/plugins/%s-custom. Only these go into git; the rest of", strings.SplitN(name, ".", 2)[0])
		ui.Info("the site is downloaded too, so it runs locally, but git ignores it.")
		ui.Info("Leave empty to start with none — add them to tracked in %s later.", site.File)
		s.Tracked = strings.Fields(ui.Ask("Maintained paths:", ""))
	}
	// git is always used locally; this only decides whether it goes to GitHub.
	// A WordPress site with nothing maintained has little worth publishing.
	ghName := ""
	if ui.ConfirmDefault("Create a GitHub repo for it?", stype == "astro" || len(s.Tracked) > 0) {
		ghDefault := ""
		if g.GitOwner != "" {
			ghDefault = g.GitOwner + "/" + name
		}
		ghName = ui.Ask("GitHub repo (owner/name):", ghDefault)
	}
	fmt.Fprintln(os.Stderr)
	ui.Info("The port the local site runs on. Every site needs its own to run at the same time;")
	ui.Info("the default is the first one no other site uses.")
	askPort(s)
	switch stype {
	case "wordpress":
		ui.Info("Local site %s, database on %d, mail on %d (ports in %s)", s.LocalURL, s.DBPort, s.MailPort, site.File)
	case "astro":
		ui.Info("Local site %s, preview on %d (ports in %s)", s.LocalURL, s.PreviewPort, site.File)
	}

	// ---- build it -----------------------------------------------------------
	// Nothing is committed or pushed: the folder is yours to review first.
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
	ui.OK("folder and git repo created — nothing committed")

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
		if _, err := c.checkPHP(conn, true); err != nil {
			return err
		}
	}

	if conn.Typed && ui.Confirm("Install an SSH key so you stop typing the password?") {
		if err := cmdKey(c); err != nil {
			ui.Fail(err)
		}
	}
	if stype == "wordpress" {
		c.mirrorAll = true
		if err := c.mirror(conn); err != nil {
			return err
		}
		c.mirrorAll = false
		if err := c.vetUntracked(); err != nil {
			return err
		}
		if ui.Confirm("Download the live database now?") {
			if err := cmdDB(c); err != nil {
				ui.Fail(err)
			}
		}
	}

	if ghName != "" {
		c.githubRepo(ghName)
	}

	ui.Say("")
	ui.OK("%s is set up. Nothing is committed or pushed yet.", name)
	ui.Say("")
	ui.Info("cd %s", dir)
	ui.Info("Check what git will take in — gd-sync's files, and the paths you maintain:  git status")
	firstCommitHelp(stype, git.HasRemote())
	ui.Info("Then open it in your JetBrains IDE and let it start the devcontainer, or: gd-sync up")
	return nil
}

// askPort asks which port the new site runs on, offering the first free one.
// The ports that follow it — database, mail, preview — must be free too.
func askPort(s *site.Site) {
	use := portUse()
	freePorts(s, use)
	safe := s.Port
	for {
		a := ui.Ask("Local port for the website:", strconv.Itoa(safe))
		p, err := strconv.Atoi(a)
		if err != nil || p < 1 || p > 65535 {
			ui.Warn("%q is not a port number", a)
			continue
		}
		s.Port, s.DBPort, s.MailPort, s.PreviewPort = p, 0, 0, 0
		s.Defaults()
		if p == safe {
			return
		}
		if q, why := clash(s, use); q != 0 {
			ui.Warn("Port %d won't work — %s. Press enter for %d.", q, why, safe)
			continue
		}
		return
	}
}

// portUse says why a new site can't have a port — another site has it, or
// something on this computer listens on it — or "" when it's free.
func portUse() func(int) string {
	owner := map[int]string{}
	for _, repo := range config.RegistryList() {
		if other, err := site.Load(repo); err == nil {
			for _, p := range other.Ports() {
				owner[p] = other.Name
			}
		}
	}
	return func(p int) string {
		if p < 1 || p > 65535 {
			return "it is not a port number"
		}
		if o := owner[p]; o != "" {
			return o + " uses it"
		}
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err != nil {
			return "something on this computer listens on it"
		}
		l.Close()
		return ""
	}
}

// clash is the first of a site's ports that use rules out, and why.
func clash(s *site.Site, use func(int) string) (int, string) {
	for _, p := range s.Ports() {
		if why := use(p); why != "" {
			return p, why
		}
	}
	return 0, ""
}

// freePorts gives a new site the first ports, counting up from its type's
// defaults, that use doesn't rule out, so it runs next to the others.
func freePorts(s *site.Site, use func(int) string) {
	s.Defaults()
	for try := 0; try < 100; try++ {
		if p, _ := clash(s, use); p == 0 {
			return
		}
		s.Port, s.DBPort, s.MailPort, s.PreviewPort = s.Port+1, 0, 0, 0
		s.Defaults()
	}
}

// firstCommitHelp says how to make a new site's first commit, and to push it
// when the site has a remote to push to.
func firstCommitHelp(stype string, pushed bool) {
	if pushed {
		ui.Info("Then make the first commit and push it:")
	} else {
		ui.Info("Then make the first commit:")
	}
	if stype == "wordpress" {
		ui.Info(`  git add -A && git commit -m "[server-sync] Live server state"`)
	} else {
		ui.Info(`  git add -A && git commit -m "Scaffold"`)
	}
	if pushed {
		ui.Info("  git branch develop && git push -u origin main develop")
	} else {
		ui.Info("  git branch develop")
	}
	if stype == "wordpress" {
		ui.Info("[server-sync] tells gd-sync and the deploy worker that main is what the server has.")
	}
}

// githubRepo creates the site's GitHub repo, empty, and adds it as origin.
// A repo that already exists is left alone: it may hold history, and
// pushing this folder into it is a decision for you.
func (c *ctx) githubRepo(name string) {
	if _, err := exec.LookPath("gh"); err != nil {
		ui.Warn("GitHub CLI not installed — create %s yourself, add it as origin, then: gd-sync secrets", name)
		return
	}
	ui.Step("Setting up GitHub")
	if c.git.HasRemote() {
		ui.Info("origin is already set")
	} else if exec.Command("gh", "repo", "view", name).Run() == nil {
		ui.Warn("%s already exists on GitHub, and may have history — leaving it alone.", name)
		ui.Warn("To use it anyway: git remote add origin %s", c.githubURL(name))
		ui.Warn("Or pick another name: gh repo create <owner/name> --private, then add that as origin.")
		ui.Info("Then: gd-sync secrets")
		return
	} else {
		vis := "--private"
		if c.cfg.GitPrivate == "false" {
			vis = "--public"
		}
		cmd := exec.Command("gh", "repo", "create", name, vis)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if cmd.Run() != nil {
			ui.Warn("Could not create %s — create it yourself, add it as origin, then: gd-sync secrets", name)
			return
		}
		if err := c.git.Quiet("remote", "add", "origin", c.githubURL(name)); err != nil {
			ui.Fail(err)
			return
		}
		ui.OK("created %s, empty, and added it as origin", name)
	}
	if err := cmdSecrets(c); err != nil {
		ui.Fail(err)
		ui.Warn("Set secrets later with: gd-sync secrets")
	}
}

// githubURL is the clone URL for a repo, over the protocol gh is set to use.
func (c *ctx) githubURL(name string) string {
	if out, _ := exec.Command("gh", "config", "get", "git_protocol").Output(); strings.TrimSpace(string(out)) == "ssh" {
		return "git@github.com:" + name + ".git"
	}
	return "https://github.com/" + name + ".git"
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

// or is a, or b when a is empty.
func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
