package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/s-visser/gd-sync/internal/remote/sshtest"
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
	w.write(w.repo, ".gd-sync", "SITE=test\nSITE_TYPE="+siteType+"\nREMOTE_ROOT="+w.server+
		"\nTRACKED="+tracked+"\nPUBLISH_DIR="+publishDir+"\n")
	w.write(w.repo, ".gd-sync.local", "SSH_HOST="+w.srv.Host+"\nSSH_USER=test\nSSH_PORT="+w.srv.Port+"\n")
	w.write(w.repo, ".gitignore", ".gd-sync.local\nwp-content/uploads/\nwp-content/cache/\n*.log\ndist/\n")
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
	defer func() { ui.Yes = false }()
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
	if err := w.gd("deploy"); err == nil || !strings.Contains(err.Error(), "changed these since your last sync") {
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

func TestDeployNeedsSync(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content", "")
	w.write(w.repo, "wp-content/a.php", "a")
	w.git("add", "-A")
	w.git("commit", "-qm", "a")
	if err := w.gd("deploy"); err == nil || !strings.Contains(err.Error(), "never synced") {
		t.Errorf("deploy without a sync: err = %v", err)
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
