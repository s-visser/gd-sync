package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s-visser/gd-sync/internal/site"
	"github.com/s-visser/gd-sync/internal/ui"
	"github.com/s-visser/gd-sync/internal/vet"
)

// answer runs gd-sync as if someone were at the terminal, typing input.
func (w *world) answer(input string, args ...string) {
	w.t.Helper()
	ui.SetInput(strings.NewReader(input))
	defer ui.SetInput(os.Stdin)
	defer func() { noMirror = false }()
	if err := run(args); err != nil {
		w.t.Fatalf("gd-sync %v: %v", args, err)
	}
}

func TestVendorCredentialsAndBinaries(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content/plugins/shop wp-content/themes/t", "")
	lfs := exec.Command("git", "lfs", "version").Run() == nil
	shop := "wp-content/plugins/shop/"
	hero := "wp-content/themes/t/hero.bin"
	key := shop + "auth/key.json"
	w.write(w.server, shop+"shop.php", "<?php // v1\n")
	w.write(w.server, shop+"composer.json", `{"require":{}}`)
	w.write(w.server, shop+"vendor/autoload.php", "old")
	w.write(w.server, shop+"vendor/stale.php", "stale")
	w.write(w.server, key, `{"type": "service_account", "private_key": "x"}`)
	w.write(w.server, hero, "\x00"+strings.Repeat("b", vet.LargeBinary))
	w.write(w.server, "wp-content/themes/t/style.css", "body{}\n")

	// Nobody to ask: the questionable files wait outside git.
	w.mustGD("sync")
	files := w.git("ls-files")
	if !strings.Contains(files, shop+"shop.php") {
		t.Fatalf("main lacks the plugin:\n%s", files)
	}
	for _, held := range []string{"vendor", "key.json", "hero.bin"} {
		if strings.Contains(files, held) {
			t.Errorf("%s went into git without anyone deciding:\n%s", held, files)
		}
	}

	// Someone answers: vendor out, credential out, binaries in LFS (or out).
	w.answer("1\n1\n1\n", "sync")
	files = w.git("ls-files")
	for _, out := range []string{"vendor", "key.json"} {
		if strings.Contains(files, out) {
			t.Errorf("%s should be kept out of git:\n%s", out, files)
		}
	}
	gi := w.read(w.repo, ".gitignore")
	for _, want := range []string{"/" + shop + "vendor\n", "/" + key + "\n"} {
		if !strings.Contains(gi, want) {
			t.Errorf(".gitignore lacks %q:\n%s", want, gi)
		}
	}
	if lfs {
		if got := w.git("show", "main:"+hero); !strings.HasPrefix(got, "version https://git-lfs") {
			t.Errorf("%s should be an LFS pointer in main, got %.60q", hero, got)
		}
		if !strings.Contains(w.read(w.repo, ".gitattributes"), "/"+hero+" filter=lfs") {
			t.Error(".gitattributes does not send hero.bin through LFS")
		}
	} else if strings.Contains(files, "hero.bin") {
		t.Error("hero.bin should be kept out of git")
	}
	s, err := site.Load(w.repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Vendor) != 1 || s.Vendor[0].Path != shop+"vendor" || s.Vendor[0].Build != composerBuild {
		t.Errorf("vendor = %+v", s.Vendor)
	}
	// What git leaves out still comes down, so the site runs locally.
	if w.read(w.repo, shop+"vendor/autoload.php") != "old" || !strings.Contains(w.read(w.repo, key), "service_account") {
		t.Error("the server's vendor folder and credential were not copied in")
	}

	// Decided: later syncs don't ask, and find nothing new, even from scratch.
	head := w.git("rev-parse", "HEAD")
	os.Remove(filepath.Join(w.repo, ".git", "gd-sync", "server"))
	w.mustGD("sync")
	if w.git("rev-parse", "HEAD") != head {
		t.Errorf("a sync after deciding made a commit:\n%s", w.git("show", "--stat", "HEAD"))
	}

	// Deploy builds the vendor folder from main before uploading anything,
	// and makes the server's copy exactly what it built.
	s.Vendor[0].Build = "mkdir -p vendor && printf new > vendor/autoload.php && cp composer.json vendor/from-main.json"
	if err := s.Write(); err != nil {
		t.Fatal(err)
	}
	w.write(w.repo, shop+"shop.php", "<?php // v2\n")
	w.git("commit", "-qam", "v2")
	w.mustGD("deploy")
	if got := w.read(w.server, shop+"shop.php"); got != "<?php // v2\n" {
		t.Errorf("server shop.php = %q", got)
	}
	if w.read(w.server, shop+"vendor/autoload.php") != "new" || w.read(w.server, shop+"vendor/from-main.json") != `{"require":{}}` {
		t.Error("the built vendor folder did not go up")
	}
	if w.read(w.server, shop+"vendor/stale.php") != "<missing>" {
		t.Error("a file the build didn't make is still in the server's vendor folder")
	}
	if !strings.Contains(w.read(w.server, key), "service_account") {
		t.Error("deploy touched the credential")
	}

	// A failing build stops the deploy before anything is uploaded.
	s.Vendor[0].Build = "exit 3"
	s.Write()
	w.write(w.repo, shop+"shop.php", "<?php // v3\n")
	w.git("commit", "-qam", "v3")
	if err := w.gd("deploy"); err == nil || !strings.Contains(err.Error(), "nothing was deployed") {
		t.Errorf("deploy with a failing build: err = %v", err)
	}
	if got := w.read(w.server, shop+"shop.php"); got != "<?php // v2\n" {
		t.Errorf("a failed build still uploaded shop.php: %q", got)
	}
	s.Vendor[0].Build = ""
	s.Write()
	w.git("commit", "-qam", "no build")

	// An LFS file goes up as its content, never as the pointer.
	if lfs {
		w.write(w.repo, hero, "\x00new binary")
		w.git("add", hero)
		w.git("commit", "-qm", "new hero")
		w.mustGD("deploy")
		if got := w.read(w.server, hero); got != "\x00new binary" {
			t.Errorf("server %s = %.60q", hero, got)
		}
	}
}
