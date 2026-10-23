// Package app is gd-sync's commands. Each one acts on the site whose folder
// you're in, the way git acts on the repo you're in.
package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/s-visser/gd-sync/internal/config"
	"github.com/s-visser/gd-sync/internal/gitx"
	"github.com/s-visser/gd-sync/internal/remote"
	"github.com/s-visser/gd-sync/internal/scaffold"
	"github.com/s-visser/gd-sync/internal/site"
	"github.com/s-visser/gd-sync/internal/transfer"
	"github.com/s-visser/gd-sync/internal/ui"
)

// Version is set at build time.
var Version = "dev"

const usage = `gd-sync — GoDaddy cPanel site toolkit. The server is the source of truth.

Every site is its own folder, its own Git repo, its own config. Run gd-sync from
anywhere inside a site's folder and it works out which site you mean by
reading that repo's own .gd-sync.toml file. No site name to remember or type.

  gd-sync new             create a new site: folder, repo, config, container
  gd-sync adopt           register a folder you already have
  gd-sync list            every site gd-sync knows about
  gd-sync config          machine-wide settings (projects folder, GitHub owner)

  gd-sync sync            update main with the CURRENT SERVER CONTENTS, and
                          the local copy of the rest of the site
  gd-sync merge [branch]  sync main from server, merge branch in, go live
  gd-sync status          what differs between server and main (read-only)
  gd-sync deploy          push main to the server by hand

  gd-sync db              download the live database into _db/
  gd-sync up / down       start / stop the devcontainer
  gd-sync php             make the devcontainer run the live site's PHP version

  gd-sync shell           SSH into the server
  gd-sync key             install your SSH key (stop typing passwords)
  gd-sync secrets         push SSH secrets to GitHub so the workers can run
  gd-sync scaffold        regenerate devcontainer + workflows + run configs

Add --all to sync/status/db to run every registered site.
Add -y to answer yes to every question.
Add --no-mirror to sync to update main only, leaving the local copy alone.`

// noMirror is --no-mirror: sync updates main only. The GitHub workers use it.
var noMirror bool

// Main runs gd-sync with the given arguments and returns the exit code.
func Main(args []string) int {
	if Version != "dev" {
		scaffold.ToolVersion = "v" + strings.TrimPrefix(Version, "v")
	}
	if err := run(args); err != nil {
		ui.Fail(err)
		return 1
	}
	return 0
}

func run(args []string) error {
	var rest []string
	for _, a := range args {
		switch a {
		case "-y", "--yes":
			ui.Yes = true
		case "--no-mirror":
			noMirror = true
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		fmt.Println(usage)
		return errors.New("no command given")
	}
	cmd, rest := rest[0], rest[1:]
	arg := func(i int, def string) string {
		if i < len(rest) {
			return rest[i]
		}
		return def
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	switch cmd {
	case "help", "-h", "--help":
		fmt.Println(usage)
		return nil
	case "version", "--version":
		fmt.Println("gd-sync", Version)
		return nil
	case "config":
		return cmdConfig(cfg, rest)
	case "list", "ls":
		return cmdList()
	case "new":
		return cmdNew(cfg)
	case "adopt":
		return cmdAdopt(arg(0, "."))
	case "sync", "status", "db":
		run := map[string]func(*ctx) error{"sync": cmdSync, "status": cmdStatus, "db": cmdDB}[cmd]
		if arg(0, "") == "--all" {
			return forAll(cfg, run)
		}
		return inSite(cfg, "", run)
	case "merge":
		return inSite(cfg, "", func(c *ctx) error { return cmdMerge(c, arg(0, "develop")) })
	}

	if run, ok := map[string]func(*ctx) error{
		"deploy": cmdDeploy, "up": cmdUp, "down": cmdDown, "shell": cmdShell,
		"key": cmdKey, "secrets": cmdSecrets, "scaffold": cmdScaffold, "php": cmdPHP,
	}[cmd]; ok {
		return inSite(cfg, "", run)
	}
	return fmt.Errorf("Unknown command '%s' — try: gd-sync help", cmd)
}

// ctx is one site for the length of one command.
type ctx struct {
	cfg  *config.Global
	s    *site.Site
	git  gitx.Repo
	conn *remote.Conn

	stashed    bool
	keepBranch bool // sync stays on main, for merge
	noMirror   bool // sync leaves the local copy of the site alone
	work       []string
}

// inSite opens the site in dir (or the one you're in), runs fn, and cleans up.
func inSite(cfg *config.Global, dir string, fn func(*ctx) error) error {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		if dir, err = site.Find(wd); err != nil {
			return err
		}
	}
	s, err := site.Load(dir)
	if err != nil {
		return err
	}
	c := &ctx{cfg: cfg, s: s, git: gitx.Repo{Dir: s.Repo}, noMirror: noMirror}
	defer c.close()
	return fn(c)
}

func forAll(cfg *config.Global, fn func(*ctx) error) error {
	for _, p := range config.RegistryList() {
		fmt.Printf("\n%s---- %s ----%s\n", ui.C, filepath.Base(p), ui.N)
		if err := inSite(cfg, p, fn); err != nil {
			ui.Fail(err)
			ui.Warn("%s failed — continuing", filepath.Base(p))
		}
	}
	return nil
}

func (c *ctx) close() {
	if c.conn != nil {
		c.conn.Close()
	}
	for _, d := range c.work {
		os.RemoveAll(d)
	}
}

// server connects on first use and reuses the connection after that.
func (c *ctx) server() (*remote.Conn, error) {
	if c.conn != nil {
		return c.conn, nil
	}
	if err := c.s.NeedServer(); err != nil {
		return nil, err
	}
	conn, err := remote.Dial(c.s.SSHHost, c.s.SSHUser, c.s.SSHPort)
	if err != nil {
		return nil, err
	}
	c.conn = conn
	return conn, nil
}

// workDir is a scratch folder inside .git, on the same disk as the repo, so
// fetched files can be moved into place rather than copied.
func (c *ctx) workDir() (string, error) {
	gd, err := c.git.GitDir()
	if err != nil {
		return "", err
	}
	base := filepath.Join(gd, "gd-sync")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	d, err := os.MkdirTemp(base, "tmp.")
	if err == nil {
		c.work = append(c.work, d)
	}
	return d, err
}

func (c *ctx) matcher(roots []string) transfer.Matcher {
	return transfer.NewMatcher(roots, append(append([]string{}, transfer.DefaultExcludes...), c.s.Exclude...))
}

// gitSettings makes git keep the server's bytes exactly: no line-ending
// conversion anywhere, and long paths allowed on Windows.
func gitSettings(g gitx.Repo) {
	set := map[string]string{"core.autocrlf": "false"}
	if runtime.GOOS == "windows" {
		set["core.longpaths"] = "true"
	}
	for k, v := range set {
		if cur, _ := g.Output("config", "--local", k); strings.TrimSpace(cur) != v {
			_ = g.Quiet("config", "--local", k, v)
		}
	}
}

// requireCleanTree offers to stash uncommitted changes, so a sync never
// mixes them into main.
func (c *ctx) requireCleanTree() error {
	st := c.git.Status()
	if st == "" {
		return nil
	}
	ui.Warn("Uncommitted changes in %s", c.s.Repo)
	ui.List("", "", strings.Split(st, "\n"))
	if !ui.Confirm("Stash them and continue?") {
		return errors.New("Commit or stash first.")
	}
	if err := c.git.Quiet("stash", "push", "-u", "-m", "gd-sync auto-stash "+stamp("2006-01-02_1504")); err != nil {
		return err
	}
	c.stashed = true
	return nil
}

func (c *ctx) restoreStash() {
	if !c.stashed {
		return
	}
	ui.Step("Restoring your stashed changes")
	if err := c.git.Run("stash", "pop"); err != nil {
		ui.Warn("Stash conflicts — resolve in %s", c.s.Repo)
	}
	c.stashed = false
}
