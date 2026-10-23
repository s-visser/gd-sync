package scaffold

import (
	"os"
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
		"- ../wp-content/plugins/acme-custom:/var/www/html/wp-content/plugins/acme-custom:cached",
		"- ../wp-content/themes/acme:/var/www/html/wp-content/themes/acme:cached",
	} {
		if !strings.Contains(dc, want) {
			t.Errorf("docker-compose.yml lacks %q", want)
		}
	}
	if strings.Contains(dc, "__TRACKED_MOUNTS__") || strings.Contains(read(".devcontainer/devcontainer.json"), "__SITE__") {
		t.Error("placeholders left in the devcontainer")
	}
	if !strings.Contains(read(".devcontainer/devcontainer.json"), `"acme (WordPress)"`) {
		t.Error("site name not filled in")
	}
	if read(".gitattributes") == "" || !strings.Contains(read(".gitattributes"), "* -text") {
		t.Error(".gitattributes must turn off line-ending conversion")
	}
	if !strings.Contains(read("README.md"), "# acme.nl") {
		t.Error("README not written for the domain")
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

func TestEmbeddedTemplatesIncludeDotFolders(t *testing.T) {
	for _, p := range []string{"wordpress/.devcontainer/post-create.sh", "astro/.devcontainer/Dockerfile"} {
		if _, err := embedded.Open("templates/" + p); err != nil {
			t.Errorf("%s not embedded: %v", p, err)
		}
	}
}
