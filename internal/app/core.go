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
	"github.com/s-visser/gd-sync/internal/site"
	"github.com/s-visser/gd-sync/internal/transfer"
	"github.com/s-visser/gd-sync/internal/ui"
	"github.com/s-visser/gd-sync/internal/vet"
)

func stamp(layout string) string { return time.Now().Format(layout) }

// cmdSync makes main equal to the live server's maintained paths, commits
// that as a [server-sync] snapshot, then refreshes the local copy of the
// rest of the site.
func cmdSync(c *ctx) error {
	if c.s.Type == "astro" {
		ui.Info("%s is a static Astro site — the repo is the source, nothing to pull back.", c.s.Name)
		return nil
	}
	conn, err := c.server()
	if err != nil {
		return err
	}
	if len(c.s.Tracked) == 0 {
		ui.Info("%s has no maintained paths (tracked in %s) — main holds only gd-sync's files", c.s.Name, site.File)
	} else if err := c.syncMain(conn); err != nil {
		return err
	}
	if c.noMirror {
		return nil
	}
	if err := c.mirror(conn); err != nil {
		return err
	}
	_, err = c.checkPHP(conn, false)
	return err
}

// syncMain is the snapshot half of cmdSync.
func (c *ctx) syncMain(conn *remote.Conn) error {
	if err := c.needMain(); err != nil {
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

	if err := c.ensureLFS(); err != nil {
		return err
	}
	held, err := c.vetNew(cmp)
	if err != nil {
		return err
	}
	var put []string
	for _, p := range append(append([]string{}, cmp.New...), cmp.Changed...) {
		if !held[p] && !c.keptOut(p) {
			put = append(put, p)
		}
	}
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

	if c.git.Status(site.File, ".gitignore", ".gitattributes") != "" {
		if err := c.git.Quiet("add", "--", site.File, ".gitignore", ".gitattributes"); err != nil {
			return err
		}
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

// vetNew checks the files the server has that main doesn't yet, before any
// of them go in.
func (c *ctx) vetNew(cmp *transfer.Comparison) (map[string]bool, error) {
	if len(cmp.New) == 0 {
		return map[string]bool{}, nil
	}
	var files []vet.File
	isNew := map[string]bool{}
	for _, p := range cmp.New {
		files = append(files, vet.File{Path: p, Local: cmp.Fetched[p].File, Size: cmp.Server[p].Size})
		isNew[p] = true
	}
	has := func(p string) bool { _, ok := cmp.Main[p]; return ok || isNew[p] }
	inGit := func(dir string) bool {
		for p := range cmp.Main {
			if strings.HasPrefix(p, dir+"/") {
				return true
			}
		}
		return false
	}
	return c.vetFiles(files, has, inGit)
}

// needMain stops a command that works on main before main has a commit:
// gd-sync new leaves the first one to you.
func (c *ctx) needMain() error {
	if c.git.OK("rev-parse", "--verify", "-q", "main^{commit}") {
		return nil
	}
	ui.Warn("main has no commits yet. Check what git will take in with: git status")
	firstCommitHelp(c.s.Type, c.git.HasRemote())
	return errors.New("Make the first commit on main, then run this again")
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
	cmp, err := transfer.Compare(conn, c.git, c.s.RemoteRoot, c.matcher(c.s.Tracked), known, wd, c.isLFS)
	return cmp, statePath, err
}

// cmdMerge is the only sanctioned route into main: server first, then your
// branch, then live.
func cmdMerge(c *ctx, from string) error {
	if _, err := c.server(); err != nil {
		return err
	}
	if err := c.needMain(); err != nil {
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
	c.noMirror = true
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
	if err := c.needMain(); err != nil {
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
	if err := c.needMain(); err != nil {
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

// deployMain ships what changed in main since the last commit known to be
// live, checking each file against what the server holds right now. It needs
// no memory of earlier runs, so it works the same from your computer and from
// a fresh GitHub runner.
func (c *ctx) deployMain(conn *remote.Conn) error {
	ui.Step("Deploying %s  [main -> server]", c.s.Name)
	m := c.matcher(c.s.Tracked)
	base, err := c.deployBase(conn)
	if err != nil {
		return err
	}
	baseTree, err := transfer.TreeAt(c.git, base, m)
	if err != nil {
		return err
	}
	main, err := transfer.MainTree(c.git, m)
	if err != nil {
		return err
	}
	changed := transfer.ChangedPaths(baseTree, main)
	if len(changed) == 0 {
		ui.OK("Server already matches main — nothing to deploy")
		return c.recordDeployed(conn)
	}

	server, err := transfer.ListRemote(conn, c.s.RemoteRoot, m)
	if err != nil {
		return err
	}
	wd, err := c.workDir()
	if err != nil {
		return err
	}
	blobs, err := transfer.ServerBlobs(conn, c.s.RemoteRoot, changed, server, wd, c.isLFS)
	if err != nil {
		return err
	}
	up, del, drift := transfer.DeployPlan(baseTree, main, blobs)
	if len(drift) > 0 {
		ui.List("", "", drift)
		return errors.New(`The server changed these since they were last synced or deployed. Deploying would overwrite that.
  Run 'gd-sync merge' to fold the server's changes in and deploy in one go.`)
	}
	if len(up)+len(del) == 0 {
		ui.OK("Server already matches main — nothing to deploy")
		return c.recordDeployed(conn)
	}
	builds, err := c.buildVendors(conn)
	if err != nil {
		return err
	}

	ui.Say("")
	var added, modified []string
	for _, p := range up {
		if _, ok := blobs[p]; ok {
			modified = append(modified, p)
		} else {
			added = append(added, p)
		}
	}
	ui.List(ui.G, "+ ", added)
	ui.List(ui.Y, "~ ", modified)
	ui.List(ui.R, "- ", del)
	for _, b := range builds {
		ui.Info("%s, as built: %d to upload, %d to remove", b.v.Path, len(b.up), len(b.del))
	}
	ui.Say("")
	if !ui.Confirm("Push these to the LIVE site?") {
		ui.Say("Cancelled.")
		return nil
	}

	c.backup(conn)
	ui.Step("Uploading")
	if err := transfer.UploadBlobs(conn, c.s.RemoteRoot, c.git, main, up, c.isLFS); err != nil {
		return err
	}
	if err := transfer.RemoveRemote(conn, c.s.RemoteRoot, del); err != nil {
		return err
	}
	for _, b := range builds {
		if err := transfer.UploadDir(conn, c.s.RemoteRoot, b.dir, b.up); err != nil {
			return err
		}
		if err := transfer.RemoveRemote(conn, c.s.RemoteRoot, b.del); err != nil {
			return err
		}
	}
	if err := c.recordDeployed(conn); err != nil {
		return err
	}
	c.forget(append(up, del...))
	ui.OK("Deployed")
	return nil
}

// deployBase is the most recent commit known to match the server: the last
// deploy (recorded on the server itself) or the last [server-sync] snapshot,
// whichever came later in main's history. With neither, it's an empty tree,
// so every file in main counts as new and nothing is ever deleted.
func (c *ctx) deployBase(conn *remote.Conn) (string, error) {
	isCommit := func(ref string) bool { return ref != "" && c.git.OK("cat-file", "-e", ref+"^{commit}") }

	deployed := ""
	if out, err := conn.Output("cat "+c.serverRecord("deployed")+" 2>/dev/null || true", nil); err == nil {
		if sha := strings.TrimSpace(string(out)); isCommit(sha) && c.git.OK("merge-base", "--is-ancestor", sha, "main") {
			deployed = sha
		}
	}
	synced, _ := c.git.Output("log", "-1", "--first-parent", "--format=%H", "--fixed-strings", "--grep=[server-sync]", "main")
	if synced = strings.TrimSpace(synced); !isCommit(synced) {
		synced = ""
	}

	switch {
	case deployed == "":
		return synced, nil
	case synced == "" || c.git.OK("merge-base", "--is-ancestor", synced, deployed):
		return deployed, nil
	}
	return synced, nil
}

// serverRecord is a file in ~/.gd-sync/<site>/ on the server, where gd-sync
// keeps what every machine and every worker needs to agree on.
func (c *ctx) serverRecord(name string) string {
	return "~/.gd-sync/" + remote.Quote(c.s.Name) + "/" + name
}

func (c *ctx) writeServerRecord(conn *remote.Conn, name, content string) error {
	cmd := "mkdir -p ~/.gd-sync/" + remote.Quote(c.s.Name) + " && cat > " + c.serverRecord(name)
	if err := conn.Run(cmd, strings.NewReader(content), nil); err != nil {
		return fmt.Errorf("Could not record the deploy on the server: %w", err)
	}
	return nil
}

func (c *ctx) recordDeployed(conn *remote.Conn) error {
	sha, err := c.git.Output("rev-parse", "main")
	if err != nil {
		return err
	}
	return c.writeServerRecord(conn, "deployed", sha)
}

// forget drops paths from the local sync state, so the next sync looks at
// them again rather than trusting what it knew before the deploy.
func (c *ctx) forget(paths []string) {
	statePath, err := transfer.StatePath(c.git)
	if err != nil {
		return
	}
	known, err := transfer.LoadState(statePath)
	if err != nil || len(known) == 0 {
		return
	}
	for _, p := range paths {
		delete(known, p)
	}
	_ = known.Save(statePath)
}

// deployBuild ships a static build. It isn't in git, so the whole build goes
// up each time. Files the previous deploy put there that the new build no
// longer has are removed; anything else on the server is left alone. The
// list of deployed files lives on the server, so every machine and worker
// sees the same one.
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
	sort.Strings(up)

	prev, err := conn.Output("cat "+c.serverRecord("published")+" 2>/dev/null || true", nil)
	if err != nil {
		return err
	}
	now := map[string]bool{}
	for _, p := range up {
		now[p] = true
	}
	var del []string
	for _, p := range strings.Split(string(prev), "\n") {
		if p != "" && !now[p] {
			del = append(del, p)
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
	if err := c.writeServerRecord(conn, "published", strings.Join(up, "\n")+"\n"); err != nil {
		return err
	}
	ui.OK("Deployed")
	return nil
}

// backup tars the server's current files into ~/gd-sync-backups first,
// keeping the newest 20 for this site.
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
	prune := fmt.Sprintf("ls -1t ~/gd-sync-backups/%s-*.tar.gz 2>/dev/null | tail -n +21 | while IFS= read -r f; do rm -f -- \"$f\"; done", c.s.Name)
	_ = conn.Run(prune, nil, nil)
}
