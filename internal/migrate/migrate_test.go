package migrate

import (
	"strings"
	"testing"
)

func fixture(rev, down string) (string, []byte) {
	return FileName(rev, "example"), []byte(Template(rev, down, "Example", "2026-10-23"))
}
func TestChainAndPlanning(t *testing.T) {
	files := map[string][]byte{}
	// Deliberately choose revisions that don't sort in dependency order.
	revs := []string{"cccccccccccc", "aaaaaaaaaaaa", "bbbbbbbbbbbb"}
	down := ""
	for _, r := range revs {
		name, src := fixture(r, down)
		files[name] = src
		down = r
	}
	c, err := Load(files)
	if err != nil {
		t.Fatal(err)
	}
	if c.Head() != revs[2] {
		t.Fatal(c.Head())
	}
	for i, r := range revs {
		if c[i].Revision != r {
			t.Fatal(c)
		}
	}
	for _, tt := range []struct {
		current int
		target  string
		want    int
	}{{0, "head", 3}, {2, "base", 0}, {2, "-1", 1}, {1, "+2", 3}, {0, "aaaa", 2}} {
		got, err := c.Target(tt.current, tt.target)
		if err != nil || got != tt.want {
			t.Fatalf("target %s = %d, %v", tt.target, got, err)
		}
	}
	for _, target := range []string{"missing", "-4", "+4", "-bad"} {
		if _, err := c.Target(1, target); err == nil {
			t.Errorf("accepted %s", target)
		}
	}
	if _, err := c.Position("dddddddddddd"); err == nil {
		t.Fatal("accepted unknown DB revision")
	}
	up := c.Plan(0, 3)
	if len(up) != 3 || !up[0].Up || up[0].From != "" || up[2].To != revs[2] {
		t.Fatal(up)
	}
	downSteps := c.Plan(3, 0)
	if len(downSteps) != 3 || downSteps[0].Up || downSteps[0].From != revs[2] || downSteps[2].To != "" {
		t.Fatal(downSteps)
	}
}
func TestInvalidGraphs(t *testing.T) {
	for _, tt := range []struct {
		name  string
		links [][2]string
	}{
		{"roots", [][2]string{{"aaaaaaaaaaaa", ""}, {"bbbbbbbbbbbb", ""}}},
		{"missing", [][2]string{{"aaaaaaaaaaaa", ""}, {"bbbbbbbbbbbb", "cccccccccccc"}}},
		{"branch", [][2]string{{"aaaaaaaaaaaa", ""}, {"bbbbbbbbbbbb", "aaaaaaaaaaaa"}, {"cccccccccccc", "aaaaaaaaaaaa"}}},
		{"cycle", [][2]string{{"aaaaaaaaaaaa", ""}, {"bbbbbbbbbbbb", "cccccccccccc"}, {"cccccccccccc", "bbbbbbbbbbbb"}}},
		{"self", [][2]string{{"aaaaaaaaaaaa", "aaaaaaaaaaaa"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string][]byte{}
			for _, l := range tt.links {
				n, s := fixture(l[0], l[1])
				files[n] = s
			}
			if _, err := Load(files); err == nil {
				t.Fatal("accepted invalid graph")
			}
		})
	}
	n, s := fixture("aaaaaaaaaaaa", "")
	if _, err := Load(map[string][]byte{n: s, "aaaaaaaaaaaa_duplicate.php": s}); err == nil {
		t.Fatal("duplicate revision accepted")
	}
}
func TestParseAndTemplate(t *testing.T) {
	rev := NewRevision()
	src := Template(rev, "", "A title */ with comment", "2026-10-23")
	name := FileName(rev, "A title with comment")
	m, err := Parse(name, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if m.Revision != rev || m.DownRevision != "" || m.Title != "A title * / with comment" {
		t.Fatal(m)
	}
	for _, bad := range []string{strings.Replace(src, "$down_revision = null;", "$down_revision = getenv('REV');", 1), src + "\n$revision = 'aaaaaaaaaaaa';", strings.ReplaceAll(src, rev, "not-a-revision")} {
		if _, err := Parse(name, []byte(bad)); err == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
	c := Chain{{Revision: "abcdef000000"}, {Revision: "abcdef111111"}}
	if _, err := c.Target(0, "abc"); err == nil {
		t.Fatal("accepted ambiguous target")
	}
}
