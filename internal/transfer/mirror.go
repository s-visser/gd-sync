package transfer

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/s-visser/gd-sync/internal/gitx"
)

// The mirror is the rest of the live site, copied into the repo's folder so
// it runs locally. It only ever holds files git ignores, so it can't touch
// your work or show up in a commit. What it placed is remembered per
// machine, in .git/gd-sync/mirror, so a file is only ever deleted locally if
// the mirror put it there and nobody changed it since.

// MirrorPath is where a repo keeps the list of files the mirror placed.
func MirrorPath(repo gitx.Repo) (string, error) {
	d, err := repo.GitDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "gd-sync", "mirror"), nil
}

// LoadManifest reads a saved manifest; a missing file is an empty one.
func LoadManifest(path string) (Manifest, error) {
	m := Manifest{}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		p := strings.Split(sc.Text(), "\t")
		if len(p) != 3 {
			continue
		}
		size, _ := strconv.ParseInt(p[1], 10, 64)
		mtime, _ := strconv.ParseInt(p[2], 10, 64)
		m[p[0]] = File{size, mtime}
	}
	return m, sc.Err()
}

// Save writes the manifest, sorted so it diffs sensibly.
func (m Manifest) Save(path string) error {
	var b bytes.Buffer
	for _, p := range sortedKeys(m) {
		fmt.Fprintf(&b, "%s\t%d\t%d\n", p, m[p].Size, m[p].Mtime)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// Ignored reports which of the paths git ignores and doesn't track.
func Ignored(repo gitx.Repo, paths []string) (map[string]bool, error) {
	ign := map[string]bool{}
	if len(paths) == 0 {
		return ign, nil
	}
	out, err := repo.Input(gitx.NUL(paths), "check-ignore", "-z", "--stdin")
	if err != nil && gitx.ExitCode(err) != 1 { // 1 only means nothing was ignored
		return nil, err
	}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			ign[p] = true
		}
	}
	return ign, nil
}

// LocalMatches reports whether the local copy of a path has the given size
// and mtime — the same quick check the server side uses.
func LocalMatches(repoDir, p string, f File) bool {
	st, err := os.Lstat(filepath.Join(repoDir, filepath.FromSlash(p)))
	return err == nil && st.Mode().IsRegular() && st.Size() == f.Size && st.ModTime().Unix() == f.Mtime
}

// MirrorPlan decides what the mirror fetches and removes. A server file is
// fetched unless the local copy already has its size and mtime. A local file
// is removed when the server no longer has it, but only if the mirror placed
// it and it is still exactly what was placed.
func MirrorPlan(repoDir string, server, placed Manifest) (fetch, remove []string) {
	for _, p := range sortedKeys(server) {
		if !LocalMatches(repoDir, p, server[p]) {
			fetch = append(fetch, p)
		}
	}
	for _, p := range sortedKeys(placed) {
		if _, ok := server[p]; !ok && LocalMatches(repoDir, p, placed[p]) {
			remove = append(remove, p)
		}
	}
	return fetch, remove
}

// PlaceMirrored moves fetched files into place with the server's mtime, so
// the next mirror can skip them. A file that can't be placed — a name this
// computer can't hold, say — is reported and skipped, not fatal.
func PlaceMirrored(repoDir string, fetched map[string]Fetched, server Manifest, warn func(string, error)) []string {
	var placed []string
	for _, p := range sortedKeys(fetched) {
		dst := filepath.Join(repoDir, filepath.FromSlash(p))
		err := os.MkdirAll(filepath.Dir(dst), 0o755)
		if err == nil {
			err = os.Rename(fetched[p].File, dst)
		}
		if err == nil {
			t := time.Unix(server[p].Mtime, 0)
			err = os.Chtimes(dst, t, t)
		}
		if err != nil {
			warn(p, err)
			continue
		}
		placed = append(placed, p)
	}
	return placed
}

// RemoveLocal deletes files from the repo's folder, and any folders that
// leaves empty.
func RemoveLocal(repoDir string, paths []string) {
	for _, p := range paths {
		dst := filepath.Join(repoDir, filepath.FromSlash(p))
		if os.Remove(dst) != nil {
			continue
		}
		for d := filepath.Dir(dst); d != repoDir && strings.HasPrefix(d, repoDir); d = filepath.Dir(d) {
			if os.Remove(d) != nil {
				break
			}
		}
	}
}

// Size adds up the sizes of the given paths.
func (m Manifest) Size(paths []string) int64 {
	var n int64
	for _, p := range paths {
		n += m[p].Size
	}
	return n
}

// Paths lists the manifest's paths, sorted.
func (m Manifest) Paths() []string { return sortedKeys(m) }
