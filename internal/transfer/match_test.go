package transfer

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestMatcherKeep(t *testing.T) {
	m := NewMatcher([]string{"wp-content", "robots.txt"}, append(DefaultExcludes, "*.bak", "wp-content/themes/old"))
	for path, want := range map[string]bool{
		"wp-content/plugins/a/a.php":          true,
		"wp-content/themes/t/style.css":       true,
		"robots.txt":                          true,
		"robots.txt.orig":                     false, // not under a root
		"wp-config.php":                       false,
		"wp-content/uploads/2024/x.jpg":       false, // excluded folder
		"wp-content/cache":                    false,
		"wp-content/debug.log":                false, // *.log anywhere
		"wp-content/plugins/a/node_modules/x": false,
		"wp-content/plugins/a/.DS_Store":      false,
		"wp-content/plugins/a/x.bak":          false,
		"wp-content/plugins/a/x.bak/keep":     false, // a folder named like the pattern
		"wp-content/themes/old/style.css":     false,
		"wp-content/themes/older/style.css":   true, // * never crosses into a longer name
		"wp-content/plugins/uploads/x":        true, // only wp-content/uploads is excluded
	} {
		if got := m.Keep(path); got != want {
			t.Errorf("Keep(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestMatcherAnchored(t *testing.T) {
	m := NewMatcher([]string{"."}, MirrorExcludes)
	for path, want := range map[string]bool{
		"AGENTS.md":                          false,
		"wp-content/plugins/a/AGENTS.md":     true,
		"README.md":                          false, // the repo's own README
		"wp-content/plugins/a/README.md":     true,  // anchored: only at the top
		".devcontainer/devcontainer.json":    false,
		"wp-content/plugins/a/.devcontainer": true,
		"wp-content/uploads/2024/x.jpg":      true, // the mirror has the images
		"error_log":                          false,
		"wp-admin/error_log":                 false,
		"wp-content/cache/page.html":         false,
		"wp-config.php":                      true,
	} {
		if got := m.Keep(path); got != want {
			t.Errorf("Keep(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestMatcherDotRoot(t *testing.T) {
	m := NewMatcher([]string{"."}, DefaultExcludes)
	if !m.Keep("index.html") || !m.Keep("blog/a.html") {
		t.Error("everything should be under root .")
	}
	if m.Keep("AGENTS.md") || m.Keep(".gd-sync.toml") || m.Keep(".git/config") {
		t.Error("gd-sync's own files must never be transferred")
	}
}

// The Perl lister runs on the server and must agree with the Go matcher.
func TestListerAgreesWithMatcher(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("no perl")
	}
	dir := t.TempDir()
	files := []string{
		"wp-content/plugins/a/a.php", "wp-content/plugins/a/x.bak", "wp-content/plugins/a/node_modules/m.js",
		"wp-content/uploads/x.jpg", "wp-content/debug.log", "wp-content/themes/t/my file.txt",
		"wp-content/themes/old/s.css", "wp-content/themes/older/s.css", "wp-config.php", "robots.txt",
		"AGENTS.md", "wp-content/plugins/a/AGENTS.md", "README.md", "wp-content/plugins/a/README.md", "error_log", "wp-admin/error_log", ".gd-sync.toml",
		".devcontainer/x", "wp-content/plugins/a/.devcontainer/y", "wp-content/cache/c.html",
	}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
	}
	for _, m := range []Matcher{
		NewMatcher([]string{"wp-content", "robots.txt", "missing"}, append(DefaultExcludes, "*.bak", "wp-content/themes/old")),
		NewMatcher([]string{"."}, MirrorExcludes),
	} {
		listerAgrees(t, dir, files, m)
	}
}

func listerAgrees(t *testing.T, dir string, files []string, m Matcher) {
	t.Helper()
	cmd := exec.Command("perl", append([]string{"-"}, m.listerArgs()...)...)
	cmd.Dir, cmd.Stdin = dir, strings.NewReader(lister)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		listed = append(listed, strings.Split(l, "\t")[0])
	}
	var want []string
	for _, f := range files {
		if m.Keep(f) {
			want = append(want, f)
		}
	}
	sort.Strings(listed)
	sort.Strings(want)
	if !slices.Equal(listed, want) {
		t.Errorf("perl listed %v\nmatcher keeps %v", listed, want)
	}
}
