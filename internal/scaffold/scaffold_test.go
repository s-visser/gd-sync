package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s-visser/gd-sync/internal/site"
)

func TestAllWordPress(t *testing.T) {
	s := &site.Site{
		Repo: t.TempDir(), Name: "acme", Type: "wordpress", RemoteRoot: "/home/u/public_html",
		Tracked: []string{"wp-content/plugins/acme-custom", "wp-content/themes/acme"},
		LiveURL: "https://acme.nl", SSHHost: "h", SSHUser: "u", SSHPort: "22",
	}
	s.Defaults()
	if err := All(s); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(s.Repo, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	dc := read(".devcontainer/docker-compose.yml")
	for _, want := range []string{
		"- ..:/var/www/html:cached",
		"- ./wp-config.local.php:/var/www/html/wp-config.php:ro",
		"- ./htaccess:/var/www/html/.htaccess:ro",
	} {
		if !strings.Contains(dc, want) {
			t.Errorf("docker-compose.yml lacks %q", want)
		}
	}
	if !strings.Contains(read(".devcontainer/wp-config.local.php"), "DB_HOST', 'db'") {
		t.Error("no local wp-config.php")
	}
	if strings.Contains(read(".devcontainer/devcontainer.json"), "__SITE__") {
		t.Error("placeholders left in the devcontainer")
	}
	if !strings.Contains(read(".devcontainer/devcontainer.json"), `"acme (WordPress)"`) {
		t.Error("site name not filled in")
	}
	if !strings.Contains(read(".devcontainer/devcontainer.json"), `[8080, 8025, 13306]`) {
		t.Error("ports not filled in")
	}
	if env := read(".devcontainer/.env"); !strings.Contains(env, "WP_PORT=8080\nDB_PORT=13306\nMAIL_PORT=8025\n") {
		t.Errorf(".env lacks the ports:\n%s", env)
	}
	if read(".gitattributes") == "" || !strings.Contains(read(".gitattributes"), "* -text") {
		t.Error(".gitattributes must turn off line-ending conversion")
	}
	if !strings.Contains(read("README.md"), "# acme.nl") {
		t.Error("README not written for the domain")
	}
	if !strings.Contains(read("README.md"), "`dev` / `dev`") {
		t.Error("README lacks the local admin login")
	}
	for _, f := range []string{"deploy.yml", "sync-from-server.yml", "verify-main.yml"} {
		wf := read(".github/workflows/" + f)
		if strings.Contains(wf, "__GD_SYNC_VERSION__") || strings.Contains(wf, "rsync") {
			t.Errorf("%s: version placeholder left, or still uses rsync", f)
		}
	}
	if !strings.Contains(read(".github/workflows/deploy.yml"), "gd-sync/cmd/gd-sync@latest") {
		t.Error("deploy.yml doesn't install gd-sync")
	}
	if !strings.Contains(read(".run/1_Sync_main_from_server.run.xml"), `value="gd-sync sync"`) {
		t.Error("run configuration doesn't call gd-sync")
	}

	// The settings round-trip through the files.
	back, err := site.Load(s.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != "acme" || back.SSHHost != "h" || len(back.Tracked) != 2 || back.LocalURL != "http://localhost:8080" {
		t.Errorf("reloaded site = %+v", back)
	}

	// The site's own README is never overwritten.
	os.WriteFile(filepath.Join(s.Repo, "README.md"), []byte("mine"), 0o644)
	if err := All(s); err != nil {
		t.Fatal(err)
	}
	if read("README.md") != "mine" {
		t.Error("README overwritten")
	}
}

func TestComposeName(t *testing.T) {
	if got := composeName("WolfpackVision.com"); got != "wolfpackvision-com" {
		t.Errorf("composeName = %q", got)
	}
}

func TestEmbeddedTemplatesIncludeDotFolders(t *testing.T) {
	for _, p := range []string{"wordpress/.devcontainer/post-create.sh", "astro/.devcontainer/Dockerfile"} {
		if _, err := embedded.Open("templates/" + p); err != nil {
			t.Errorf("%s not embedded: %v", p, err)
		}
	}
}

// The folder holds the whole live site. Git must see only gd-sync's files
// and the paths you maintain — or only gd-sync's files, with none.
func TestGitignoreKeepsOnlyMaintainedPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	scaffolding := []string{
		".gitignore", ".gitattributes", ".gd-sync.toml", ".devcontainer/docker-compose.yml",
		".github/workflows/deploy.yml", ".run/x.run.xml", "GD-SYNC.md", "README.md",
	}
	mirror := []string{
		"index.php", "wp-config.php", ".htaccess", "wp-includes/version.php", "wp-admin/a.php",
		"wp-content/plugins/akismet/akismet.php", "wp-content/themes/genesis/style.css",
		"wp-content/uploads/2024/x.jpg", "wp-content/index.php", "maps/sitemap.xml",
		"wp-content/themes/acme-old/style.css", "wp-content/plugins/acme custom2/x.php",
		".gd-sync.local.toml", "_db/dump.sql",
	}
	for _, c := range []struct {
		tracked    []string
		maintained []string
		ignoredToo []string // inside a maintained path, still never committed
	}{
		{
			tracked:    []string{"wp-content/plugins/acme custom", "wp-content/themes/acme", "robots.txt", "wp-content/themes/acme/inc"},
			maintained: []string{"wp-content/plugins/acme custom/x.php", "wp-content/themes/acme/style.css", "wp-content/themes/acme/inc/a.php", "robots.txt"},
			ignoredToo: []string{"wp-content/themes/acme/node_modules/m.js", "wp-content/plugins/acme custom/debug.log", "wp-content/themes/acme/a.pot:Zone.Identifier"},
		},
		{tracked: nil},
	} {
		dir := t.TempDir()
		if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		s := &site.Site{Repo: dir, Name: "acme", Type: "wordpress", Tracked: c.tracked}
		if err := Gitignore(s); err != nil {
			t.Fatal(err)
		}
		all := append(append(append(append([]string{}, scaffolding...), mirror...), c.maintained...), c.ignoredToo...)
		for _, f := range all {
			p := filepath.Join(dir, filepath.FromSlash(f))
			os.MkdirAll(filepath.Dir(p), 0o755)
			if f != ".gitignore" {
				os.WriteFile(p, []byte("x"), 0o644)
			}
		}
		out, err := exec.Command("git", "-C", dir, "ls-files", "--others", "--exclude-standard").Output()
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			seen[f] = true
		}
		for _, f := range append(append([]string{}, scaffolding...), c.maintained...) {
			if !seen[f] {
				t.Errorf("tracked=%q: git ignores %s, which it should keep", c.tracked, f)
			}
		}
		for _, f := range append(append([]string{}, mirror...), c.ignoredToo...) {
			if seen[f] {
				t.Errorf("tracked=%q: git sees %s, which belongs to the mirror", c.tracked, f)
			}
		}
	}
}

// What .gd-sync.toml keeps out of git or in LFS, git must see the same way.
func TestKeptOutAndLFS(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	p := "wp-content/plugins/shop/"
	s := &site.Site{
		Repo: dir, Name: "acme", Type: "wordpress", Tracked: []string{"wp-content/plugins/shop"},
		Ignore: []string{p + "auth/key [1].json"},
		Vendor: []site.Vendor{{Path: p + "vendor", Build: "composer install"}},
		LFS:    []string{p + "media/intro video.mp4", p + "media/a*b.bin"},
	}
	if err := Gitignore(s); err != nil {
		t.Fatal(err)
	}
	if err := Gitattributes(s); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"auth/key [1].json", "vendor/autoload.php", "shop.php", "media/intro video.mp4", "media/a*b.bin", "media/aXb.bin"} {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(filepath.FromSlash(p+f))), 0o755)
		os.WriteFile(filepath.Join(dir, filepath.FromSlash(p+f)), []byte("x"), 0o644)
	}
	out, _ := exec.Command("git", "-C", dir, "ls-files", "--others", "--exclude-standard").Output()
	seen := string(out)
	for _, f := range []string{"auth/key", "vendor/"} {
		if strings.Contains(seen, f) {
			t.Errorf("git sees %s, which is kept out:\n%s", f, seen)
		}
	}
	if !strings.Contains(seen, p+"shop.php") {
		t.Errorf("git ignores shop.php:\n%s", seen)
	}
	for f, want := range map[string]string{"media/intro video.mp4": "lfs", "media/a*b.bin": "lfs", "media/aXb.bin": "unspecified"} {
		out, err := exec.Command("git", "-C", dir, "check-attr", "filter", "--", p+f).Output()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(strings.TrimSpace(string(out)), ": "+want) {
			t.Errorf("filter for %s: %s, want %s", f, out, want)
		}
	}
	if err := s.Write(); err != nil {
		t.Fatal(err)
	}
	back, err := site.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Vendor) != 1 || back.Vendor[0] != s.Vendor[0] || len(back.LFS) != 2 || len(back.Ignore) != 1 {
		t.Errorf("read back %+v", back)
	}
}
