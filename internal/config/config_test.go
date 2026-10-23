package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

// Whatever goes in comes back out of a TOML parser unchanged.
func TestStringRoundTrip(t *testing.T) {
	in := []string{"", `C:\Sites\a "b"`, "tab\there\nnew line", "\x01\x7f", "wp-content/themes/acme custom", "é ✓"}
	doc := "one = " + String(in[1]) + "\nall = " + List(in) + "\nnone = " + List(nil) + "\n"
	var v struct {
		One  string
		All  []string
		None []string
	}
	if _, err := toml.Decode(doc, &v); err != nil {
		t.Fatalf("%v\n%s", err, doc)
	}
	if v.One != in[1] || len(v.All) != len(in) || len(v.None) != 0 {
		t.Fatalf("got %+v", v)
	}
	for i := range in {
		if v.All[i] != in[i] {
			t.Errorf("%q came back as %q", in[i], v.All[i])
		}
	}
}

func TestSetValidates(t *testing.T) {
	g := &Global{}
	if err := g.Set("git_private", "no"); err != nil || g.GitPrivate != "false" {
		t.Errorf("git_private no: %v, %q", err, g.GitPrivate)
	}
	if err := g.Set("git_private", "maybe"); err == nil {
		t.Error("git_private should only take true or false")
	}
	if err := g.Set("nope", "x"); err != ErrUnknownKey {
		t.Errorf("unknown key: %v", err)
	}
}

// gd-sync list and --all walk the registry: a site folder must stay on it.
func TestRegistryKeepsSites(t *testing.T) {
	t.Setenv("GD_SYNC_REGISTRY", filepath.Join(t.TempDir(), "repos"))
	site, gone := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(site, ".gd-sync.toml"), []byte("name = \"a\"\n"), 0o644)
	RegistryAdd(site)
	RegistryAdd(gone)
	if got := RegistryList(); len(got) != 1 || got[0] != site {
		t.Errorf("RegistryList = %v", got)
	}
}

// The server defaults survive a save and load; the port must be a port.
func TestGlobalSSHDefaults(t *testing.T) {
	t.Setenv("GD_SYNC_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	g, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if g.SSHPort != "22" || g.SSHHost != "" {
		t.Errorf("defaults: %+v", g)
	}
	if err := g.Set("ssh_port", "port"); err == nil {
		t.Error("ssh_port should be a number")
	}
	g.Set("ssh_host", "1.2.3.4")
	g.Set("ssh_user", "acme")
	g.Set("ssh_port", "2222")
	if err := g.Save(); err != nil {
		t.Fatal(err)
	}
	if g, err = Load(); err != nil || g.SSHHost != "1.2.3.4" || g.SSHUser != "acme" || g.SSHPort != "2222" {
		t.Errorf("reloaded: %+v, %v", g, err)
	}
}
