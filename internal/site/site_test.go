package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, toml string) (*Site, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, File), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(dir)
}

func TestPorts(t *testing.T) {
	for _, c := range []struct {
		name, toml, url string
		ports           []int
	}{
		{"wordpress defaults", `name = "a"`, "http://localhost:8080", []int{8080, 13306, 8025}},
		{"astro defaults", "name = \"a\"\ntype = \"astro\"", "http://localhost:4321", []int{4321, 4322}},
		{"the others follow port", "name = \"a\"\nport = 8081", "http://localhost:8081", []int{8081, 13307, 8026}},
		{"set by hand", "name = \"a\"\nport = 9000\ndb_port = 3307\nmail_port = 9025", "http://localhost:9000", []int{9000, 3307, 9025}},
		{"old files keep local_url's port", "name = \"a\"\nlocal_url = \"http://localhost:8082\"", "http://localhost:8082", []int{8082, 13308, 8027}},
		{"localhost follows port", "name = \"a\"\nlocal_url = \"http://localhost:8080\"\nport = 8083", "http://localhost:8083", []int{8083, 13309, 8028}},
		{"other hosts stay", "name = \"a\"\nlocal_url = \"http://acme.test\"\nport = 8084", "http://acme.test", []int{8084, 13310, 8029}},
	} {
		s, err := load(t, c.toml)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if s.LocalURL != c.url || !equal(s.Ports(), c.ports) {
			t.Errorf("%s: local_url %s, ports %v; want %s, %v", c.name, s.LocalURL, s.Ports(), c.url, c.ports)
		}
	}

	for _, bad := range []string{"name = \"a\"\nport = 70000", "name = \"a\"\nport = 9000\ndb_port = 9000"} {
		if _, err := load(t, bad); err == nil {
			t.Errorf("%q should not load", bad)
		}
	}
}

// The ports are written out, and local_url only when it isn't on localhost.
func TestWritePorts(t *testing.T) {
	s := &Site{Repo: t.TempDir(), Name: "a", Type: "wordpress", Port: 8090}
	s.Defaults()
	if err := s.Write(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(s.Repo, File))
	for _, want := range []string{"port = 8090\n", "db_port = 13316", "mail_port = 8035"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s lacks %q:\n%s", File, want, b)
		}
	}
	if strings.Contains(string(b), "local_url") {
		t.Errorf("local_url on localhost should follow port, not be written:\n%s", b)
	}
	back, err := Load(s.Repo)
	if err != nil || back.LocalURL != "http://localhost:8090" || !equal(back.Ports(), s.Ports()) {
		t.Errorf("reloaded: %v %+v", err, back)
	}
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
