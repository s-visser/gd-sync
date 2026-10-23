package transfer

import (
	"fmt"
	"sort"

	"github.com/s-visser/gd-sync/internal/gitx"
	"github.com/s-visser/gd-sync/internal/remote"
	"github.com/s-visser/gd-sync/internal/ui"
)

// Comparison is the server held up against main.
type Comparison struct {
	Server Manifest
	Main   Tree

	New     []string // on the server, not in main
	Changed []string // in both, with different content
	Gone    []string // in main, no longer on the server
	Skipped []string // listed by the server, but gone before they could be fetched

	// Fetched holds, for every New and Changed path, the temp file with the
	// server's copy.
	Fetched map[string]Fetched
}

// Settled is the state to remember when main is left as it was: everything
// except the paths where main and the server disagree.
func (c *Comparison) Settled() State {
	return Settle(c.Server, c.Main, concat(c.New, c.Changed, c.Skipped))
}

// Compare lists the server, decides which files could have changed since
// the last known state, fetches those into workDir and hashes them. Files
// lfs reports as Git LFS ones are compared by their pointer.
func Compare(conn *remote.Conn, repo gitx.Repo, root string, m Matcher, known State, workDir string, lfs func(string) bool) (*Comparison, error) {
	server, err := ListRemote(conn, root, m)
	if err != nil {
		return nil, err
	}
	if err := DropIgnored(repo, server); err != nil {
		return nil, err
	}
	WarnCaseClashes(server)
	main, err := MainTree(repo, m)
	if err != nil {
		return nil, err
	}

	cand, gone := PullPlan(known, main, server)
	c := &Comparison{Server: server, Main: main, Gone: gone, Fetched: map[string]Fetched{}}
	if len(cand) > 0 {
		ui.Info("fetching %d file(s) whose size or date moved, %s", len(cand), ui.Size(server.Size(cand)))
		progress := ui.NewProgress(server.Size(cand), len(cand))
		got, err := Fetch(conn, root, cand, workDir, progress)
		progress.Done()
		if err != nil {
			ui.Warn("Some files could not be fetched — they are skipped: %v", err)
		}
		for _, p := range cand {
			f, ok := got[p]
			switch {
			case !ok:
				c.Skipped = append(c.Skipped, p)
			case main[p].ID == "":
				c.New = append(c.New, p)
				c.Fetched[p] = f
			case main[p].ID != f.ID(lfs(p)):
				c.Changed = append(c.Changed, p)
				c.Fetched[p] = f
			}
		}
	}
	return c, nil
}

// PullPlan decides what to fetch and what is gone. A server file needs
// fetching unless its size and mtime are what was last seen and main still
// holds the blob it matched then.
func PullPlan(known State, main Tree, server Manifest) (fetch, gone []string) {
	for p, f := range server {
		k, seen := known[p]
		b, inMain := main[p]
		if !seen || !inMain || k.Size != f.Size || k.Mtime != f.Mtime || k.Blob != b.ID {
			fetch = append(fetch, p)
		}
	}
	for p := range main {
		if _, ok := server[p]; !ok {
			gone = append(gone, p)
		}
	}
	sort.Strings(fetch)
	sort.Strings(gone)
	return fetch, gone
}

// DeployPlan decides what a deploy does, file by file, for every path whose
// content differs between base — the last commit known to be live — and main.
// server holds the blob each of those paths has on the server right now, or
// "" when it isn't there.
//
//   - The server already has main's version: nothing to do.
//   - The server still has base's version: upload main's, or delete the file
//     if main removed it.
//   - Anything else means the server changed since base. Deploying would
//     overwrite that, so the path is drift and the deploy must stop.
//
// A file gd-sync never put there is never deleted: it isn't in base, so it
// can only be drift.
func DeployPlan(base, main Tree, server map[string]string) (up, del, drift []string) {
	for _, p := range ChangedPaths(base, main) {
		want, was, have := main[p].ID, base[p].ID, server[p]
		switch {
		case have == want:
		case have == was && want != "":
			up = append(up, p)
		case have == was:
			del = append(del, p)
		default:
			drift = append(drift, p)
		}
	}
	return up, del, drift
}

// ChangedPaths lists the paths whose content differs between two trees.
func ChangedPaths(a, b Tree) []string {
	var out []string
	for p, x := range a {
		if b[p].ID != x.ID {
			out = append(out, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// ServerBlobs fetches the server's copy of each path that it has and returns
// their blob ids — the pointer's for Git LFS files. Paths the server doesn't
// have are left out.
func ServerBlobs(conn *remote.Conn, root string, paths []string, server Manifest, workDir string, lfs func(string) bool) (map[string]string, error) {
	var present []string
	for _, p := range paths {
		if _, ok := server[p]; ok {
			present = append(present, p)
		}
	}
	blobs := map[string]string{}
	if len(present) == 0 {
		return blobs, nil
	}
	got, err := Fetch(conn, root, present, workDir, nil)
	for _, p := range present {
		f, ok := got[p]
		if !ok {
			if err == nil {
				err = fmt.Errorf("the server's %s could not be read", p)
			}
			return nil, err
		}
		blobs[p] = f.ID(lfs(p))
	}
	return blobs, nil
}

func concat(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}
