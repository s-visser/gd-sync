package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/s-visser/gd-sync/internal/gitx"
	"github.com/s-visser/gd-sync/internal/remote"
	"github.com/s-visser/gd-sync/internal/transfer"
	"github.com/s-visser/gd-sync/internal/ui"
)

func stamp(layout string) string { return time.Now().Format(layout) }

// cmdSync makes main equal to the live server and commits that as a
// [server-sync] snapshot.
func cmdSync(c *ctx) error {
	if c.s.Type == "astro" {
		ui.Info("%s is a static Astro site — the repo is the source, nothing to pull back.", c.s.Name)
		return nil
	}
	if len(c.s.Tracked) == 0 {
		return fmt.Errorf("%s has no TRACKED paths in .gd-sync", c.s.Name)
	}
	conn, err := c.server()
	if err != nil {
		return err
	}
	gitSettings(c.git)

	back := c.git.CurrentBranch()
	if back == "main" {
		back = ""
	}
	if err := c.requireCleanTree(); err != nil {
		return err
	}

	ui.Step("Updating main with the current server contents")
	if err := c.git.Quiet("checkout", "-q", "main"); err != nil {
		return err
	}
	cmp, statePath, err := c.compare(conn)
	if err != nil {
		return err
	}

	put := append(append([]string{}, cmp.New...), cmp.Changed...)
	if len(put) > 0 {
		if err := transfer.Place(c.s.Repo, cmp.Fetched, put); err != nil {
			return err
		}
		if _, err := c.git.Input(gitx.NUL(put), "--literal-pathspecs", "add", "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
			return err
		}
	}
	if len(cmp.Gone) > 0 {
		if _, err := c.git.Input(gitx.NUL(cmp.Gone), "--literal-pathspecs", "rm", "-q", "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
			return err
		}
	}
	if n := len(cmp.Skipped); n > 0 {
		ui.Warn("%d file(s) vanished from the server mid-sync — run sync again", n)
	}

	if c.git.OK("diff", "--cached", "--quiet") {
		ui.OK("main already matches the server")
	} else {
		if err := c.git.Commit("[server-sync] Live server state " + stamp("2006-01-02 15:04")); err != nil {
			return err
		}
		ui.OK("main updated from the server")
		if st, err := c.git.Output("diff", "--stat", "HEAD~1", "HEAD"); err == nil {
			ui.List("", "", strings.Split(strings.TrimRight(st, "\n"), "\n"))
		}
	}

	tree, err := transfer.MainTree(c.git, c.matcher(c.s.Tracked))
	if err != nil {
		return err
	}
	if err := transfer.Settle(cmp.Server, tree, cmp.Skipped).Save(statePath); err != nil {
		return err
	}

	if c.git.HasRemote() {
		if c.git.Quiet("push", "-q", "origin", "main") == nil {
			ui.Info("pushed main to origin")
		} else {
			ui.Warn("could not push main to origin")
		}
	}
	if back != "" && !c.keepBranch {
		if err := c.git.Quiet("checkout", "-q", back); err != nil {
			return err
		}
		c.restoreStash()
	}
	return nil
}

// compare holds the server up against main, using the last known state.
func (c *ctx) compare(conn *remote.Conn) (*transfer.Comparison, string, error) {
	statePath, err := transfer.StatePath(c.git)
	if err != nil {
		return nil, "", err
	}
	known, err := transfer.LoadState(statePath)
	if err != nil {
		return nil, "", err
	}
	wd, err := c.workDir()
	if err != nil {
		return nil, "", err
	}
	cmp, err := transfer.Compare(conn, c.git, c.s.RemoteRoot, c.matcher(c.s.Tracked), known, wd)
	return cmp, statePath, err
}

// cmdMerge is the only sanctioned route into main: server first, then your
// branch, then live.
func cmdMerge(c *ctx, from string) error {
	if _, err := c.server(); err != nil {
		return err
	}
	if !c.git.OK("rev-parse", "--verify", "-q", from) {
		return fmt.Errorf("No branch '%s' in this repo", from)
	}
	if from == "main" {
		return errors.New("Nothing to merge — 'main' is the target, not the source")
	}

	ui.Step("Step 1 of 3 — refreshing main from the live server")
	c.keepBranch = true
	if err := cmdSync(c); err != nil {
		return err
	}

	ui.Step("Step 2 of 3 — merging %s into main", from)
	if err := c.git.Quiet("checkout", "-q", "main"); err != nil {
		return err
	}
	if err := c.git.Run("merge", "--no-ff", "--no-edit", from); err != nil {
		files, _ := c.git.Output("diff", "--name-only", "--diff-filter=U")
		ui.Warn("Conflict between your work and the live server.")
		ui.List("", "", strings.Fields(files))
		return errors.New("Resolve in your IDE, then: git merge --continue && gd-sync deploy")
	}
	ui.OK("merged")

	ui.Step("Step 3 of 3 — going live")
	if c.git.HasRemote() {
		if err := c.git.Run("push", "origin", "main"); err != nil {
			return err
		}
		ui.OK("Pushed. The deploy worker is updating the server.")
	} else {
		ui.Warn("No git remote — deploying directly instead.")
		yes := ui.Yes
		ui.Yes = true
		err := cmdDeploy(c)
		ui.Yes = yes
		if err != nil {
			return err
		}
	}
	if err := c.git.Quiet("checkout", "-q", from); err != nil {
		return err
	}
	_ = c.git.Quiet("merge", "-q", "--no-edit", "main")
	c.restoreStash()
	return nil
}

// cmdStatus shows how the server differs from main, changing nothing in the
// repo. It does remember what it learned about unchanged files.
func cmdStatus(c *ctx) error {
	if c.s.Type == "astro" {
		ui.Info("%s is static — compare by building locally.", c.s.Name)
		return nil
	}
	conn, err := c.server()
	if err != nil {
		return err
	}
	ui.Step("Server vs main for %s (read-only)", c.s.Name)
	cmp, statePath, err := c.compare(conn)
	if err != nil {
		return err
	}
	if err := cmp.Settled().Save(statePath); err != nil {
		return err
	}

	ui.List(ui.G, "+ ", cmp.New)
	ui.List(ui.Y, "~ ", cmp.Changed)
	ui.List(ui.R, "- ", cmp.Gone)
	if len(cmp.New)+len(cmp.Changed)+len(cmp.Gone) == 0 {
		ui.OK("Identical")
		return nil
	}
	ui.Info("+ only on the server   ~ differs   - only in main")
	ui.Info("Run 'gd-sync sync' to fold these into main")
	return nil
}

// cmdDeploy ships main to the server by hand.
func cmdDeploy(c *ctx) error {
	conn, err := c.server()
	if err != nil {
		return err
	}
	if b := c.git.CurrentBranch(); b != "main" {
		return fmt.Errorf(`Deploy ships main, and you are on '%s'.
  Get your work into main first — 'gd-sync merge' does that safely.`, b)
	}
	if c.git.Status("--untracked-files=no") != "" {
		return errors.New("main has uncommitted changes — commit or stash them first")
	}
	if c.s.PublishDir != "" {
		return c.deployBuild(conn)
	}
	return c.deployMain(conn)
}

// deployMain ships what changed in main since the server was last seen, and
// refuses when the server itself moved since then: main mirrors the server,
// never the other way round.
func (c *ctx) deployMain(conn *remote.Conn) error {
	statePath, err := transfer.StatePath(c.git)
	if err != nil {
		return err
	}
	if _, err := os.Stat(statePath); err != nil {
		return fmt.Errorf(`This machine has never synced %s, so it can't tell what the server holds.
  Run 'gd-sync sync' first.`, c.s.Name)
	}
	known, err := transfer.LoadState(statePath)
	if err != nil {
		return err
	}

	ui.Step("Deploying %s  [main -> server]", c.s.Name)
	m := c.matcher(c.s.Tracked)
	server, err := transfer.ListRemote(conn, c.s.RemoteRoot, m)
	if err != nil {
		return err
	}
	if err := transfer.DropIgnored(c.git, server); err != nil {
		return err
	}
	main, err := transfer.MainTree(c.git, m)
	if err != nil {
		return err
	}

	up, del, drift := transfer.DeployPlan(known, server, main)
	if len(drift) > 0 {
		ui.List("", "", drift)
		return errors.New(`The server changed these since your last sync. Deploying would overwrite that.
  Run 'gd-sync merge' to fold the server's changes in and deploy in one go.`)
	}
	if len(up)+len(del) == 0 {
		ui.OK("Server already matches main — nothing to deploy")
		return nil
	}

	ui.Say("")
	var added, changed []string
	for _, p := range up {
		if _, ok := server[p]; ok {
			changed = append(changed, p)
		} else {
			added = append(added, p)
		}
	}
	ui.List(ui.G, "+ ", added)
	ui.List(ui.Y, "~ ", changed)
	ui.List(ui.R, "- ", del)
	ui.Say("")
	if !ui.Confirm("Push these to the LIVE site?") {
		ui.Say("Cancelled.")
		return nil
	}

	c.backup(conn)
	ui.Step("Uploading")
	if err := transfer.UploadBlobs(conn, c.s.RemoteRoot, c.git, main, up); err != nil {
		return err
	}
	if err := transfer.RemoveRemote(conn, c.s.RemoteRoot, del); err != nil {
		return err
	}

	after, err := transfer.ListRemote(conn, c.s.RemoteRoot, m)
	if err != nil {
		return err
	}
	if err := transfer.AfterDeploy(known, after, main, up).Save(statePath); err != nil {
		return err
	}
	ui.OK("Deployed")
	return nil
}

// deployBuild ships a static build. It isn't in git, so the whole build goes
// up each time. Files from the previous deploy that the new build no longer
// has are removed; anything else on the server is left alone.
func (c *ctx) deployBuild(conn *remote.Conn) error {
	dir := filepath.Join(c.s.Repo, filepath.FromSlash(c.s.PublishDir))
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("No build output at %s — run the build first", c.s.PublishDir)
	}
	ui.Step("Deploying %s  [%s -> web root]", c.s.Name, c.s.PublishDir)
	up, err := transfer.ListLocal(dir, c.matcher([]string{"."}))
	if err != nil {
		return err
	}
	if len(up) == 0 {
		return fmt.Errorf("%s is empty — run the build first", c.s.PublishDir)
	}

	gd, err := c.git.GitDir()
	if err != nil {
		return err
	}
	prevPath := filepath.Join(gd, "gd-sync", "published")
	var del []string
	if b, err := os.ReadFile(prevPath); err == nil {
		now := map[string]bool{}
		for _, p := range up {
			now[p] = true
		}
		for _, p := range strings.Split(string(b), "\n") {
			if p != "" && !now[p] {
				del = append(del, p)
			}
		}
	}

	ui.Say("")
	ui.Info("%d file(s) to upload", len(up))
	ui.List(ui.R, "- ", del)
	ui.Say("")
	if !ui.Confirm("Push these to the LIVE site?") {
		ui.Say("Cancelled.")
		return nil
	}

	c.backup(conn)
	ui.Step("Uploading")
	if err := transfer.UploadDir(conn, c.s.RemoteRoot, dir, up); err != nil {
		return err
	}
	if err := transfer.RemoveRemote(conn, c.s.RemoteRoot, del); err != nil {
		return err
	}
	sort.Strings(up)
	if err := os.MkdirAll(filepath.Dir(prevPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(prevPath, []byte(strings.Join(up, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	ui.OK("Deployed")
	return nil
}

// backup tars the server's current files into ~/gd-sync-backups first.
func (c *ctx) backup(conn *remote.Conn) {
	ui.Step("Backing up the server's current files")
	name := fmt.Sprintf("~/gd-sync-backups/%s-%s.tar.gz", c.s.Name, stamp("20060102-150405"))
	paths := "."
	if len(c.s.Tracked) > 0 {
		paths = remote.QuoteAll(c.s.Tracked)
	}
	cmd := fmt.Sprintf("mkdir -p ~/gd-sync-backups && cd %s && tar czf %s %s 2>/dev/null", remote.Quote(c.s.RemoteRoot), name, paths)
	if err := conn.Run(cmd, nil, nil); err != nil {
		ui.Warn("Backup failed — continuing")
		return
	}
	ui.OK("%s", name)
}
