package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/s-visser/gd-sync/internal/config"
	"github.com/s-visser/gd-sync/internal/remote/sshtest"
	"github.com/s-visser/gd-sync/internal/scaffold"
	"github.com/s-visser/gd-sync/internal/site"
	"github.com/s-visser/gd-sync/internal/ui"
)

// world is a site folder, a fake server holding its web root, and a user
// home with an SSH key, all in temp folders.
type world struct {
	t            *testing.T
	repo, server string
	srv          *sshtest.Server
	clock        time.Time
}

func newWorld(t *testing.T, siteType, tracked, publishDir string) *world {
	if runtime.GOOS == "windows" {
		t.Skip("the test server runs commands with bash")
	}
	for _, tool := range []string{"git", "bash", "perl", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("needs " + tool)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("COPYFILE_DISABLE", "1") // macOS tar would add ._ files for extended attributes
	t.Setenv("GD_SYNC_HOME", filepath.Join(home, "gd-sync"))
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.com")
	}
	if err := newKey(filepath.Join(home, ".ssh", "id_ed25519")); err != nil {
		t.Fatal(err)
	}

	w := &world{t: t, srv: sshtest.Start(t), repo: t.TempDir(), clock: time.Now().Add(-time.Hour)}
	w.server = filepath.Join(w.srv.Home, "public_html")
	os.MkdirAll(w.server, 0o755)

	w.git("init", "-q")
	w.git("symbolic-ref", "HEAD", "refs/heads/main")
	w.write(w.repo, ".gd-sync.toml", "name = \"test\"\ntype = "+config.String(siteType)+"\nremote_root = "+config.String(w.server)+
		"\ntracked = "+config.List(strings.Fields(tracked))+"\npublish_dir = "+config.String(publishDir)+"\n")
	w.write(w.repo, ".gd-sync.local.toml", "ssh_host = "+config.String(w.srv.Host)+"\nssh_user = \"test\"\nssh_port = "+w.srv.Port+"\n")
	w.write(w.repo, ".gitignore", ".gd-sync.local.toml\nwp-content/uploads/\nwp-content/cache/\n*.log\ndist/\n")
	w.git("add", "-A")
	w.git("commit", "-qm", "scaffold")
	t.Chdir(w.repo)
	return w
}

// write creates a file with a fresh, distinct mtime, so the quick check by
// size and mtime always sees an edit (real edits are seconds apart).
func (w *world) write(root, rel, content string) {
	w.t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
	w.clock = w.clock.Add(time.Second)
	os.Chtimes(p, w.clock, w.clock)
}

func (w *world) read(root, rel string) string {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

func (w *world) git(args ...string) string {
	w.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", w.repo}, args...)...).CombinedOutput()
	if err != nil {
		w.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (w *world) gd(args ...string) error {
	w.t.Helper()
	defer func() { ui.Yes, noMirror = false, false }()
	return run(append(args, "-y"))
}

func (w *world) mustGD(args ...string) {
	w.t.Helper()
	if err := w.gd(args...); err != nil {
		w.t.Fatalf("gd-sync %v: %v", args, err)
	}
}

func TestSyncStatusDeploy(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content", "")
	w.write(w.server, "wp-content/plugins/a/a.php", "<?php // a\n")
	w.write(w.server, "wp-content/themes/t/style.css", "body{}\n")
	w.write(w.server, "wp-content/themes/t/my file [1].txt", "spaces and brackets\n")
	w.write(w.server, "wp-content/themes/t/crlf.txt", "windows\r\nline endings\r\n")
	w.write(w.server, "wp-content/uploads/x.jpg", "img")
	w.write(w.server, "wp-content/debug.log", "log")
	w.write(w.server, "wp-config.php", "secret")

	// First sync pulls the tracked, non-ignored files onto main.
	w.mustGD("sync")
	files := w.git("ls-files")
	for _, want := range []string{"wp-content/plugins/a/a.php", "wp-content/themes/t/my file [1].txt"} {
		if !strings.Contains(files, want) {
			t.Errorf("main lacks %s:\n%s", want, files)
		}
	}
	for _, never := range []string{"uploads", "debug.log", "wp-config"} {
		if strings.Contains(files, never) {
			t.Errorf("main should not hold %s:\n%s", never, files)
		}
	}
	if got := w.read(w.repo, "wp-content/themes/t/crlf.txt"); got != "windows\r\nline endings\r\n" {
		t.Errorf("line endings were rewritten: %q", got)
	}
	head := w.git("rev-parse", "HEAD")

	// Nothing moved: no new commit.
	w.mustGD("sync")
	if w.git("rev-parse", "HEAD") != head {
		t.Error("a sync with nothing new made a commit")
	}

	// Touched but identical: still nothing.
	later := time.Now()
	os.Chtimes(filepath.Join(w.server, "wp-content/themes/t/style.css"), later, later)
	w.mustGD("status")
	w.mustGD("sync")
	if w.git("rev-parse", "HEAD") != head {
		t.Error("a touched-but-identical file made a commit")
	}

	// Server edits: changed, new and deleted files all land on main.
	w.write(w.server, "wp-content/themes/t/style.css", "body{color:red}\n")
	w.write(w.server, "wp-content/plugins/a/new.php", "new\n")
	os.Remove(filepath.Join(w.server, "wp-content/themes/t/my file [1].txt"))
	w.mustGD("sync")
	if got := w.git("show", "main:wp-content/themes/t/style.css"); got != "body{color:red}" {
		t.Errorf("style.css on main = %q", got)
	}
	if strings.Contains(w.git("ls-files"), "my file") {
		t.Error("a file deleted on the server is still on main")
	}
	if !strings.HasPrefix(w.git("log", "-1", "--format=%s"), "[server-sync]") {
		t.Error("server snapshot commit not tagged [server-sync]")
	}

	// Deploy ships main's changes and deletions.
	w.write(w.repo, "wp-content/themes/t/style.css", "body{color:blue}\n")
	w.write(w.repo, "wp-content/themes/t/parts/footer.php", "footer\n")
	w.git("rm", "-q", "wp-content/plugins/a/new.php")
	w.git("add", "-A")
	w.git("commit", "-qm", "theme work")
	w.mustGD("deploy")
	if got := w.read(w.server, "wp-content/themes/t/style.css"); got != "body{color:blue}\n" {
		t.Errorf("server style.css = %q", got)
	}
	if got := w.read(w.server, "wp-content/themes/t/parts/footer.php"); got != "footer\n" {
		t.Errorf("server footer.php = %q", got)
	}
	if w.read(w.server, "wp-content/plugins/a/new.php") != "<missing>" {
		t.Error("a file removed from main is still on the server")
	}
	if w.read(w.server, "wp-content/uploads/x.jpg") != "img" {
		t.Error("deploy touched uploads")
	}
	backups, _ := filepath.Glob(filepath.Join(w.srv.Home, "gd-sync-backups", "*.tar.gz"))
	if len(backups) != 1 {
		t.Errorf("want one backup on the server, found %v", backups)
	}

	// Deploying again has nothing to do, and status agrees.
	w.mustGD("deploy")
	w.mustGD("status")

	// The server changes a file main also changed: deploy must refuse.
	w.write(w.server, "wp-content/themes/t/style.css", "body{color:green}\n")
	w.write(w.repo, "wp-content/themes/t/style.css", "body{color:pink}\n")
	w.git("commit", "-qam", "pink")
	if err := w.gd("deploy"); err == nil || !strings.Contains(err.Error(), "The server changed these") {
		t.Errorf("deploy over a server edit: err = %v", err)
	}
	if got := w.read(w.server, "wp-content/themes/t/style.css"); got != "body{color:green}\n" {
		t.Errorf("the server's edit was overwritten: %q", got)
	}

	// Not on main: refuse.
	w.git("checkout", "-qb", "develop")
	if err := w.gd("deploy"); err == nil || !strings.Contains(err.Error(), "Deploy ships main") {
		t.Errorf("deploy from develop: err = %v", err)
	}
}

func TestMerge(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content", "")
	w.write(w.server, "wp-content/plugins/a/a.php", "v1\n")
	w.write(w.server, "wp-content/themes/t/style.css", "body{}\n")
	w.mustGD("sync")
	w.git("branch", "develop")

	// Clean merge: your work and a server-side plugin update both go live.
	w.git("checkout", "-q", "develop")
	w.write(w.repo, "wp-content/themes/t/style.css", "body{color:pink}\n")
	w.write(w.repo, "wp-content/plugins/a/b [1].php", "x\n")
	w.git("add", "-A")
	w.git("commit", "-qm", "dev work")
	w.write(w.server, "wp-content/plugins/a/a.php", "v2 plugin update\n")
	w.mustGD("merge")

	if got := w.read(w.server, "wp-content/themes/t/style.css"); got != "body{color:pink}\n" {
		t.Errorf("server style.css = %q", got)
	}
	if got := w.read(w.server, "wp-content/plugins/a/b [1].php"); got != "x\n" {
		t.Errorf("server b [1].php = %q", got)
	}
	if got := w.read(w.server, "wp-content/plugins/a/a.php"); got != "v2 plugin update\n" {
		t.Errorf("the server's own update was lost: %q", got)
	}
	if b := w.git("branch", "--show-current"); b != "develop" {
		t.Errorf("merge left you on %s", b)
	}
	if got := w.read(w.repo, "wp-content/plugins/a/a.php"); got != "v2 plugin update\n" {
		t.Errorf("develop did not get the server's update: %q", got)
	}

	// Conflict: the server and develop changed the same line.
	w.write(w.repo, "wp-content/themes/t/style.css", "body{color:blue}\n")
	w.git("commit", "-qam", "blue")
	w.write(w.server, "wp-content/themes/t/style.css", "body{color:green}\n")
	err := w.gd("merge")
	if err == nil || !strings.Contains(err.Error(), "gd-sync deploy") {
		t.Fatalf("conflicting merge: err = %v", err)
	}

	// Resolve, continue, deploy — as the message says.
	w.write(w.repo, "wp-content/themes/t/style.css", "body{color:teal}\n")
	w.git("add", "-A")
	if out, err := exec.Command("git", "-C", w.repo, "-c", "core.editor=true", "merge", "--continue").CombinedOutput(); err != nil {
		t.Fatalf("merge --continue: %v\n%s", err, out)
	}
	w.mustGD("deploy")
	if got := w.read(w.server, "wp-content/themes/t/style.css"); got != "body{color:teal}\n" {
		t.Errorf("resolved merge didn't go live: %q", got)
	}
}

// A GitHub runner has a fresh clone: no local state at all. Deploy must work
// from git history and the record on the server alone.
func TestDeployFromFreshClone(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content", "")
	w.write(w.server, "wp-content/a.php", "a1\n")
	w.write(w.server, "wp-content/b.php", "b1\n")
	w.mustGD("sync")

	w.write(w.repo, "wp-content/a.php", "a2\n")
	w.git("commit", "-qam", "a2")
	w.mustGD("deploy")

	os.RemoveAll(filepath.Join(w.repo, ".git", "gd-sync"))
	w.write(w.repo, "wp-content/a.php", "a3\n")
	w.git("rm", "-q", "wp-content/b.php")
	w.git("commit", "-qam", "a3, drop b")
	w.mustGD("deploy")
	if got := w.read(w.server, "wp-content/a.php"); got != "a3\n" {
		t.Errorf("server a.php = %q", got)
	}
	if w.read(w.server, "wp-content/b.php") != "<missing>" {
		t.Error("b.php should be removed from the server")
	}
}

// Without any sync or deploy, nothing on the server is ever overwritten or
// deleted unseen.
func TestDeployNeverTouchesUnknownFiles(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content", "")
	w.write(w.server, "wp-content/theirs.php", "live edit\n")
	w.write(w.server, "wp-content/stranger.php", "not in git\n")
	w.write(w.repo, "wp-content/theirs.php", "my version\n")
	w.write(w.repo, "wp-content/mine.php", "new\n")
	w.git("add", "-A")
	w.git("commit", "-qm", "local work")

	if err := w.gd("deploy"); err == nil || !strings.Contains(err.Error(), "The server changed these") {
		t.Fatalf("deploy over an unknown server file: err = %v", err)
	}
	if w.read(w.server, "wp-content/theirs.php") != "live edit\n" || w.read(w.server, "wp-content/stranger.php") != "not in git\n" {
		t.Error("server files were touched")
	}
	if w.read(w.server, "wp-content/mine.php") != "<missing>" {
		t.Error("a refused deploy still uploaded something")
	}
}

func TestAstroDeploy(t *testing.T) {
	w := newWorld(t, "astro", "", "dist")
	w.write(w.server, ".htaccess", "keep me\n")
	w.write(w.repo, "dist/index.html", "i")
	w.write(w.repo, "dist/blog/a.html", "a")
	w.mustGD("deploy")
	if w.read(w.server, "blog/a.html") != "a" || w.read(w.server, "index.html") != "i" {
		t.Fatal("build not uploaded")
	}

	os.Remove(filepath.Join(w.repo, "dist/blog/a.html"))
	w.write(w.repo, "dist/blog/b.html", "b")
	w.mustGD("deploy")
	if w.read(w.server, "blog/a.html") != "<missing>" {
		t.Error("a page dropped from the build is still live")
	}
	if w.read(w.server, "blog/b.html") != "b" {
		t.Error("new page not uploaded")
	}
	if w.read(w.server, ".htaccess") != "keep me\n" {
		t.Error("deploy removed a file it never put there")
	}
}

// useScaffoldGitignore swaps the test's .gitignore for the one gd-sync
// writes for these maintained paths, and commits it.
func (w *world) useScaffoldGitignore(tracked ...string) {
	w.t.Helper()
	if err := scaffold.Gitignore(&site.Site{Repo: w.repo, Type: "wordpress", Tracked: tracked}); err != nil {
		w.t.Fatal(err)
	}
	w.git("add", "-A")
	w.git("commit", "-qm", "gitignore")
}

func (w *world) mtime(root, rel string) int64 {
	st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return -1
	}
	return st.ModTime().Unix()
}

// Sync copies the whole live site into the folder so it runs locally, while
// git — and main — only ever see the maintained paths.
func TestSyncMirrorsWholeSite(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content/plugins/mine", "")
	w.useScaffoldGitignore("wp-content/plugins/mine")
	w.write(w.server, "wp-content/plugins/mine/m.php", "mine\n")
	w.write(w.server, "wp-content/plugins/other/o.php", "other\n")
	w.write(w.server, "wp-content/themes/t/style.css", "body{}\n")
	w.write(w.server, "wp-content/uploads/x.jpg", "img")
	w.write(w.server, "wp-includes/version.php", "core\n")
	w.write(w.server, "wp-config.php", "live secrets\n")
	w.write(w.server, ".htaccess", "live rules\n")
	w.write(w.server, "README.md", "the server's readme\n")
	w.write(w.server, "error_log", "noise\n")
	w.write(w.server, "wp-content/cache/page.html", "cached\n")
	w.write(w.repo, "README.md", "the repo's readme\n")
	w.write(w.repo, "_db/dump.sql", "local dump\n")
	w.git("add", "README.md")
	w.git("commit", "-qm", "readme")
	w.git("branch", "develop")
	w.git("checkout", "-q", "develop")

	w.mustGD("sync")
	if files := w.git("ls-tree", "-r", "--name-only", "main"); strings.Contains(files, "other") || !strings.Contains(files, "wp-content/plugins/mine/m.php") {
		t.Errorf("main should hold exactly the maintained paths:\n%s", files)
	}
	for rel, want := range map[string]string{
		"wp-content/plugins/other/o.php": "other\n",
		"wp-content/themes/t/style.css":  "body{}\n",
		"wp-content/uploads/x.jpg":       "img",
		"wp-includes/version.php":        "core\n",
		"wp-config.php":                  "live secrets\n",
		".htaccess":                      "live rules\n",
		"README.md":                      "the repo's readme\n",
		"_db/dump.sql":                   "local dump\n",
		"error_log":                      "<missing>",
		"wp-content/cache/page.html":     "<missing>",
	} {
		if got := w.read(w.repo, rel); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	// A maintained path reaches develop by merging main, never via the mirror.
	if w.read(w.repo, "wp-content/plugins/mine/m.php") != "<missing>" {
		t.Error("the mirror wrote into a maintained path")
	}
	if st := w.git("status", "--porcelain"); st != "" {
		t.Errorf("the mirror left git with changes:\n%s", st)
	}
	if w.mtime(w.repo, "wp-content/themes/t/style.css") != w.mtime(w.server, "wp-content/themes/t/style.css") {
		t.Error("mirrored files should carry the server's mtime")
	}

	// Unchanged files are not downloaded again: a local edit that kept the
	// size and mtime survives.
	css := filepath.Join(w.repo, "wp-content/themes/t/style.css")
	mt := time.Unix(w.mtime(w.repo, "wp-content/themes/t/style.css"), 0)
	os.WriteFile(css, []byte("BODY{}\n"), 0o644)
	os.Chtimes(css, mt, mt)
	w.write(w.server, "wp-content/uploads/x.jpg", "new img")
	os.Remove(filepath.Join(w.server, "wp-content/plugins/other/o.php"))
	w.write(w.repo, "wp-content/plugins/local-only/l.php", "never on the server\n")

	w.mustGD("sync")
	if got := w.read(w.repo, "wp-content/themes/t/style.css"); got != "BODY{}\n" {
		t.Errorf("an unchanged file was downloaded again: %q", got)
	}
	if got := w.read(w.repo, "wp-content/uploads/x.jpg"); got != "new img" {
		t.Errorf("a changed server file was not updated: %q", got)
	}
	if w.read(w.repo, "wp-content/plugins/other/o.php") != "<missing>" {
		t.Error("a file deleted on the server is still in the local copy")
	}
	if _, err := os.Stat(filepath.Join(w.repo, "wp-content/plugins/other")); err == nil {
		t.Error("an emptied folder was left behind")
	}
	if w.read(w.repo, "wp-content/plugins/local-only/l.php") == "<missing>" {
		t.Error("the mirror deleted a file it never placed")
	}

	// --no-mirror, as the workers run it, leaves the local copy alone.
	w.write(w.server, "wp-content/uploads/y.jpg", "y")
	w.mustGD("sync", "--no-mirror")
	if w.read(w.repo, "wp-content/uploads/y.jpg") != "<missing>" {
		t.Error("--no-mirror still mirrored")
	}
}

// With nothing maintained, main holds only gd-sync's files and the whole
// site is the local copy.
func TestSyncWithNothingMaintained(t *testing.T) {
	w := newWorld(t, "wordpress", "", "")
	w.useScaffoldGitignore()
	w.write(w.server, "wp-content/plugins/a/a.php", "a\n")
	w.write(w.server, "index.php", "core\n")
	head := w.git("rev-parse", "main")

	w.mustGD("sync")
	if w.git("rev-parse", "main") != head {
		t.Error("main changed with nothing maintained")
	}
	if w.read(w.repo, "wp-content/plugins/a/a.php") != "a\n" || w.read(w.repo, "index.php") != "core\n" {
		t.Error("the site was not copied locally")
	}
	if st := w.git("status", "--porcelain"); st != "" {
		t.Errorf("git sees the local copy:\n%s", st)
	}
	w.mustGD("status")
	w.mustGD("deploy")
}

func TestNewSiteDefaults(t *testing.T) {
	for in, want := range map[string]string{
		"wolfpackvision.com":          "wolfpackvision.com",
		"https://WolfpackVision.com/": "wolfpackvision.com",
		" http://acme.nl/some/page ":  "acme.nl",
		"shop.acme.co.uk":             "shop.acme.co.uk",
	} {
		if got := bareDomain(in); got != want {
			t.Errorf("bareDomain(%q) = %q, want %q", in, got, want)
		}
	}
	if got := siteName("acme.nl; rm -rf ~"); got != "acme.nlrm-rf" {
		t.Errorf("siteName kept unsafe characters: %q", got)
	}
}

// Like git clone, new refuses a folder that already has something in it.
func TestNewRefusesNonEmptyFolder(t *testing.T) {
	dir := t.TempDir()
	if err := emptyOrMissing(filepath.Join(dir, "missing")); err != nil {
		t.Errorf("a missing folder: %v", err)
	}
	if err := emptyOrMissing(dir); err != nil {
		t.Errorf("an empty folder: %v", err)
	}
	os.WriteFile(filepath.Join(dir, ".DS_Store"), nil, 0o644)
	if err := emptyOrMissing(dir); err == nil || !strings.Contains(err.Error(), "not an empty folder") {
		t.Errorf("a folder with a file in it: %v", err)
	}
	os.WriteFile(filepath.Join(dir, ".gd-sync.toml"), nil, 0o644)
	if err := emptyOrMissing(dir); err == nil || !strings.Contains(err.Error(), "gd-sync adopt") {
		t.Errorf("an existing site should point to adopt: %v", err)
	}
}

func TestPHPVersionHelpers(t *testing.T) {
	for in, want := range map[string]string{"81": "8.1", "56": "5.6", "8.5": "8.5", "7": "7"} {
		if got := phpDotted(in); got != want {
			t.Errorf("phpDotted(%q) = %q, want %q", in, got, want)
		}
	}
	if !phpOlder("5.4", "5.6") || phpOlder("7.4", "5.6") || !phpOlder("7.4", "7.10") || phpOlder("8.5", "8.5") {
		t.Error("phpOlder compares wrongly")
	}
	// Old versions are kept, with a warning; only versions without any
	// image move — to the closest one.
	for in, want := range map[string]string{"7.4": "7.4", "5.6": "5.6", "5.4": "5.6", "8.5": "8.5", "": defaultPHP} {
		if got := phpForImage(in); got != want {
			t.Errorf("phpForImage(%q) = %q, want %q", in, got, want)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Powered-By", "PHP/7.4.33")
	}))
	defer srv.Close()
	if v, full := headerPHP(srv.URL); v != "7.4" || full != "7.4.33" {
		t.Errorf("headerPHP = %q, %q", v, full)
	}
}

// gd-sync php finds the version the server uses for the web root and
// writes it where the devcontainer reads it.
func TestPHPDetection(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content", "")
	old := cpanelUserdata
	t.Cleanup(func() { cpanelUserdata = old })
	cpanelUserdata = t.TempDir()
	me, _ := exec.Command("id", "-un").Output()
	ud := filepath.Join(cpanelUserdata, strings.TrimSpace(string(me)))

	// The domain's cPanel setting, found by its document root.
	w.write(ud, "other.example", "documentroot: /somewhere/else\nphpversion: ea-php56\n")
	w.write(ud, "acme.example.host", "documentroot: "+w.server+"\nphpversion: ea-php81\n")
	w.write(ud, "acme.example.host.cache", "documentroot: "+w.server+"\nphpversion: ea-php80\n")
	w.mustGD("php")
	if !strings.Contains(w.read(w.repo, ".gd-sync.toml"), "php_version = \"8.1\"\n") {
		t.Errorf(".gd-sync.toml:\n%s", w.read(w.repo, ".gd-sync.toml"))
	}
	if !strings.Contains(w.read(w.repo, ".devcontainer/.env"), "PHP_VERSION=8.1\n") {
		t.Errorf(".devcontainer/.env:\n%s", w.read(w.repo, ".devcontainer/.env"))
	}

	// A handler in .htaccess beats it — the nearest one, as Apache reads them,
	// and commented-out lines don't count.
	w.write(w.srv.Home, ".htaccess", "AddHandler application/x-httpd-ea-php73 .php\n")
	w.write(w.server, ".htaccess", "# AddHandler application/x-httpd-ea-php70 .php\nAddHandler application/x-httpd-alt-php74___lsphp .php\n")
	w.mustGD("php")
	if !strings.Contains(w.read(w.repo, ".gd-sync.toml"), "php_version = \"7.4\"\n") {
		t.Errorf("nearest .htaccess should win:\n%s", w.read(w.repo, ".gd-sync.toml"))
	}
	os.Remove(filepath.Join(w.server, ".htaccess"))
	w.mustGD("php")
	if !strings.Contains(w.read(w.repo, ".gd-sync.toml"), "php_version = \"7.3\"\n") {
		t.Errorf("a parent folder's .htaccess applies too:\n%s", w.read(w.repo, ".gd-sync.toml"))
	}
}

// With the server in the global settings, new needs only the domain: every
// other question takes its default. (Only the web root is typed here,
// because the test server's differs from GoDaddy's.)
func TestNewWithGlobalDefaults(t *testing.T) {
	w := newWorld(t, "wordpress", "", "")
	projects := t.TempDir()
	g := &config.Global{Path: filepath.Join(os.Getenv("GD_SYNC_HOME"), "config.toml"), GitPrivate: "true", DefaultType: "wordpress"}
	for k, v := range map[string]string{"projects_dir": projects, "ssh_host": w.srv.Host, "ssh_user": "test", "ssh_port": w.srv.Port} {
		if err := g.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.Save(); err != nil {
		t.Fatal(err)
	}
	w.write(w.server, "wp-content/themes/t/style.css", "x")

	// domain, then enter for name, folder, repo, type, host, user, port;
	// the web root; enter for the maintained paths.
	ui.SetInput(strings.NewReader("acme.example\n\n\n\n\n\n\n\n" + w.server + "\n\n"))
	t.Cleanup(func() { ui.SetInput(os.Stdin) })
	if err := run([]string{"new"}); err != nil {
		t.Fatal(err)
	}
	s, err := site.Load(filepath.Join(projects, "acme.example"))
	if err != nil {
		t.Fatal(err)
	}
	if s.SSHHost != w.srv.Host || s.SSHUser != "test" || s.SSHPort != w.srv.Port || s.RemoteRoot != w.server {
		t.Errorf("site: %+v", s)
	}
	if got := w.read(s.Repo, "wp-content/themes/t/style.css"); got != "x" {
		t.Errorf("the live site should be copied in, got %q", got)
	}
}

// The server's settings decide the PHP version; the site's header only
// when the server can't say, or only has its command-line PHP to go by.
func TestPickPHP(t *testing.T) {
	selector := phpFinding{"8.5", "8.5.1", selectorSource}
	header := phpFinding{"8.4", "8.4.25", "header"}
	if got, disagree := pickPHP(selector, header); got != selector || !disagree {
		t.Errorf("selector vs header: %+v, disagree=%v", got, disagree)
	}
	if got, disagree := pickPHP(phpFinding{"8.4", "", selectorSource}, header); got.full != "8.4.25" || disagree {
		t.Errorf("same version should take the header's full release: %+v %v", got, disagree)
	}
	cli := phpFinding{"8.1", "8.1.2", "command-line PHP (may differ from the website)"}
	if got, disagree := pickPHP(cli, header); got != header || disagree {
		t.Errorf("command-line PHP should not beat the header: %+v", got)
	}
	if got, _ := pickPHP(phpFinding{}, header); got != header {
		t.Errorf("no server answer: %+v", got)
	}
	if got, _ := pickPHP(selector, phpFinding{}); got != selector {
		t.Errorf("no header: %+v", got)
	}
}

// new builds the folder and downloads the site, maintained paths included,
// but commits and pushes nothing: the first commit is yours.
func TestNewLeavesTheFirstCommitToYou(t *testing.T) {
	w := newWorld(t, "wordpress", "", "")
	projects := t.TempDir()
	g := &config.Global{Path: filepath.Join(os.Getenv("GD_SYNC_HOME"), "config.toml"), GitPrivate: "true", DefaultType: "wordpress"}
	for k, v := range map[string]string{"projects_dir": projects, "ssh_host": w.srv.Host, "ssh_user": "test", "ssh_port": w.srv.Port} {
		g.Set(k, v)
	}
	if err := g.Save(); err != nil {
		t.Fatal(err)
	}
	w.write(w.server, "wp-content/themes/t/style.css", "body{}\n")
	w.write(w.server, "wp-content/plugins/other/other.php", "<?php\n")

	ui.SetInput(strings.NewReader("acme.example\n\n\n\n\n\n\n\n" + w.server + "\nwp-content/themes/t\n\n"))
	t.Cleanup(func() { ui.SetInput(os.Stdin) })
	if err := run([]string{"new"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(projects, "acme.example")
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := git("rev-parse", "--verify", "-q", "HEAD"); err == nil {
		t.Fatalf("new made a commit: %s", out)
	}
	if w.read(dir, "wp-content/themes/t/style.css") != "body{}\n" || w.read(dir, "wp-content/plugins/other/other.php") != "<?php\n" {
		t.Fatal("the site was not downloaded, maintained paths included")
	}
	untracked, _ := git("ls-files", "--others", "--exclude-standard")
	if !strings.Contains(untracked, "wp-content/themes/t/style.css") || strings.Contains(untracked, "other.php") {
		t.Errorf("git should offer the maintained path, and only that, besides gd-sync's files:\n%s", untracked)
	}

	// Before the first commit, sync says what to do.
	t.Chdir(dir)
	if err := w.gd("sync"); err == nil || !strings.Contains(err.Error(), "first commit") {
		t.Errorf("sync before the first commit: %v", err)
	}

	// The first commit as suggested: sync and deploy take it as the server's state.
	if out, err := git("add", "-A"); err != nil {
		t.Fatal(out)
	}
	if out, err := git("commit", "-qm", "[server-sync] Live server state"); err != nil {
		t.Fatal(out)
	}
	head, _ := git("rev-parse", "HEAD")
	w.mustGD("sync")
	w.mustGD("deploy")
	if now, _ := git("rev-parse", "HEAD"); now != head {
		t.Error("sync after the first commit found differences")
	}
}
