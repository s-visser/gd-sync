package app

import (
	"github.com/s-visser/gd-sync/internal/remote"
	"github.com/s-visser/gd-sync/internal/transfer"
	"github.com/s-visser/gd-sync/internal/ui"
)

// mirror copies the rest of the live site — core, other plugins and themes,
// uploads — into the repo's folder, so the site runs locally. It only writes
// files git ignores, so your work and your commits are never touched. Only
// files whose size or date moved are downloaded. For a new site, with
// mirrorAll, it takes the maintained paths too, for you to commit.
func (c *ctx) mirror(conn *remote.Conn) error {
	if c.mirrorAll {
		ui.Step("Downloading the site")
	} else {
		ui.Step("Updating the local copy of the whole site (git ignores it)")
	}
	m := transfer.NewMatcher([]string{"."}, append(append([]string{}, transfer.MirrorExcludes...), c.s.Exclude...))
	server, err := transfer.ListRemote(conn, c.s.RemoteRoot, m)
	if err != nil {
		return err
	}
	ign, err := transfer.Ignored(c.git, server.Paths())
	if err != nil {
		return err
	}
	for p := range server {
		if !ign[p] && !c.mirrorAll {
			delete(server, p)
		}
	}
	transfer.WarnCaseClashes(server)

	recordPath, err := transfer.MirrorPath(c.git)
	if err != nil {
		return err
	}
	placed, err := transfer.LoadManifest(recordPath)
	if err != nil {
		return err
	}
	fetch, remove := transfer.MirrorPlan(c.s.Repo, server, placed)
	// A path you started maintaining since is git's now, not the mirror's.
	if ign, err = transfer.Ignored(c.git, remove); err != nil {
		return err
	}
	var gone []string
	for _, p := range remove {
		if ign[p] {
			gone = append(gone, p)
		}
	}

	got := 0
	if len(fetch) > 0 {
		ui.Info("downloading %d file(s), %s", len(fetch), ui.Size(server.Size(fetch)))
		wd, err := c.workDir()
		if err != nil {
			return err
		}
		progress := ui.NewProgress(server.Size(fetch), len(fetch))
		fetched, err := transfer.Fetch(conn, c.s.RemoteRoot, fetch, wd, progress)
		progress.Done()
		if err != nil {
			ui.Warn("Some files could not be fetched — they are skipped: %v", err)
		}
		got = len(transfer.PlaceMirrored(c.s.Repo, fetched, server, func(p string, err error) {
			ui.Warn("Could not write %s: %v", p, err)
		}))
	}
	transfer.RemoveLocal(c.s.Repo, gone)

	now := transfer.Manifest{}
	for p, f := range server {
		if transfer.LocalMatches(c.s.Repo, p, f) {
			now[p] = f
		}
	}
	if err := now.Save(recordPath); err != nil {
		return err
	}
	if got+len(gone) == 0 {
		ui.OK("local copy already matches the live site (%d files)", len(server))
	} else {
		ui.OK("local copy updated: %d downloaded, %d removed", got, len(gone))
	}
	return nil
}
