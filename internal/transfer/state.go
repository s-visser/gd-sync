package transfer

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/s-visser/gd-sync/internal/gitx"
	"github.com/s-visser/gd-sync/internal/remote"
	"github.com/s-visser/gd-sync/internal/ui"
)

// File is what the server says about one of its files.
type File struct{ Size, Mtime int64 }

// Manifest is the server's files, by slash-separated path.
type Manifest map[string]File

// Known is what the server held at the last sync, with the blob it matched.
type Known struct {
	Size, Mtime int64
	Blob        string
}

// State is the last known server state, by path.
type State map[string]Known

// Blob is one file in main: its git object id and mode.
type Blob struct{ ID, Mode string }

// Tree is main's files, by path.
type Tree map[string]Blob

func itoa(n int) string { return strconv.Itoa(n) }

// ListRemote asks the server for its files under the matcher's roots.
func ListRemote(conn *remote.Conn, root string, m Matcher) (Manifest, error) {
	cmd := "cd " + remote.Quote(root) + " && perl - " + remote.QuoteAll(m.listerArgs())
	out, err := conn.Output(cmd, strings.NewReader(lister))
	if err != nil {
		return nil, fmt.Errorf("Could not list the server's files: %w", err)
	}
	man := Manifest{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			continue
		}
		size, _ := strconv.ParseInt(f[1], 10, 64)
		mtime, _ := strconv.ParseInt(f[2], 10, 64)
		man[f[0]] = File{size, mtime}
	}
	return man, nil
}

// DropIgnored removes the paths git ignores and main doesn't track, so a
// server file the repo deliberately leaves out never counts as new.
func DropIgnored(repo gitx.Repo, man Manifest) error {
	ign, err := Ignored(repo, sortedKeys(man))
	for p := range ign {
		delete(man, p)
	}
	return err
}

// MainTree lists main's files that belong to the transfer.
func MainTree(repo gitx.Repo, m Matcher) (Tree, error) { return TreeAt(repo, "main", m) }

// TreeAt lists the files that belong to the transfer in any commit. An
// empty ref is an empty tree.
func TreeAt(repo gitx.Repo, ref string, m Matcher) (Tree, error) {
	t := Tree{}
	if ref == "" {
		return t, nil
	}
	out, err := repo.Output("ls-tree", "-r", "-z", ref)
	if err != nil {
		return nil, err
	}
	for _, rec := range strings.Split(out, "\x00") {
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 || f[1] != "blob" || f[0] == "120000" || !m.Keep(path) {
			continue
		}
		t[path] = Blob{ID: f[2], Mode: f[0]}
	}
	return t, nil
}

// StatePath is where a repo keeps the last known server state.
func StatePath(repo gitx.Repo) (string, error) {
	d, err := repo.GitDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "gd-sync", "server"), nil
}

// LoadState reads the state file; a missing file is an empty state.
func LoadState(path string) (State, error) {
	s := State{}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		p := strings.Split(sc.Text(), "\t")
		if len(p) != 4 {
			continue
		}
		size, _ := strconv.ParseInt(p[1], 10, 64)
		mtime, _ := strconv.ParseInt(p[2], 10, 64)
		s[p[0]] = Known{size, mtime, p[3]}
	}
	return s, sc.Err()
}

// Save writes the state file, sorted so it diffs sensibly.
func (s State) Save(path string) error {
	var b bytes.Buffer
	for _, p := range sortedKeys(s) {
		k := s[p]
		fmt.Fprintf(&b, "%s\t%d\t%d\t%s\n", p, k.Size, k.Mtime, k.Blob)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// Settle builds the state to remember: every server file whose content is
// known to match the given tree, except the paths in leaveOut.
func Settle(server Manifest, t Tree, leaveOut []string) State {
	out := map[string]bool{}
	for _, p := range leaveOut {
		out[p] = true
	}
	s := State{}
	for p, f := range server {
		if b, ok := t[p]; ok && !out[p] {
			s[p] = Known{f.Size, f.Mtime, b.ID}
		}
	}
	return s
}

// WarnCaseClashes warns about server paths that differ only in case, which
// can't both exist in one folder on Windows or macOS.
func WarnCaseClashes(man Manifest) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		return
	}
	seen := map[string]string{}
	for _, p := range sortedKeys(man) {
		l := strings.ToLower(p)
		if q, ok := seen[l]; ok {
			ui.Warn("%s and %s differ only in case — this computer can only hold one of them", q, p)
		}
		seen[l] = p
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
