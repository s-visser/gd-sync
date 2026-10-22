package transfer

import (
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
// the last known state, fetches those into workDir and hashes them.
func Compare(conn *remote.Conn, repo gitx.Repo, root string, m Matcher, known State, workDir string) (*Comparison, error) {
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
		ui.Info("fetching %d file(s) whose size or date moved", len(cand))
		got, err := Fetch(conn, root, cand, workDir)
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
			case main[p].ID != f.Blob:
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

// DeployPlan decides what a deploy of main does to the server: upload what
// main changed since the last known state, delete what main removed. Drift
// lists the files among those that the server changed in the meantime —
// deploying them would overwrite someone's edit, so the deploy must stop.
// Files gd-sync never knew about are never deleted.
func DeployPlan(known State, server Manifest, main Tree) (up, del, drift []string) {
	for p, b := range main {
		k, seen := known[p]
		if seen && k.Blob == b.ID {
			continue
		}
		up = append(up, p)
		f, onServer := server[p]
		if onServer != seen || (onServer && (f.Size != k.Size || f.Mtime != k.Mtime)) {
			drift = append(drift, p)
		}
	}
	for p, k := range known {
		f, onServer := server[p]
		if _, inMain := main[p]; inMain || !onServer {
			continue
		}
		del = append(del, p)
		if f.Size != k.Size || f.Mtime != k.Mtime {
			drift = append(drift, p)
		}
	}
	sort.Strings(up)
	sort.Strings(del)
	sort.Strings(drift)
	return up, del, drift
}

// AfterDeploy is the state once a deploy went through: uploaded files carry
// main's blob at whatever size and mtime the server gave them, and other
// files keep their known state while the server still agrees.
func AfterDeploy(known State, server Manifest, main Tree, uploaded []string) State {
	up := map[string]bool{}
	for _, p := range uploaded {
		up[p] = true
	}
	s := State{}
	for p, f := range server {
		if up[p] {
			s[p] = Known{f.Size, f.Mtime, main[p].ID}
		} else if k, ok := known[p]; ok && k.Size == f.Size && k.Mtime == f.Mtime {
			s[p] = k
		}
	}
	return s
}

func concat(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}
