package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/s-visser/gd-sync/internal/scaffold"
	"github.com/s-visser/gd-sync/internal/site"
	"github.com/s-visser/gd-sync/internal/ui"
	"github.com/s-visser/gd-sync/internal/vet"
)

// composerBuild is what deploy runs to rebuild a Composer vendor folder.
const composerBuild = "composer install --no-dev --no-interaction --no-progress --prefer-dist"

// vetFiles asks what to do with the dependency folders, credential-looking
// files and large binaries among files about to go into git, and records
// the answers in .gd-sync.toml, .gitignore and .gitattributes. It returns the
// paths to leave out of this commit: those now kept out of git, and those
// nobody was there to decide about, which wait for the next time someone is.
func (c *ctx) vetFiles(files []vet.File, has func(string) bool, inGit func(string) bool) (map[string]bool, error) {
	held := map[string]bool{}
	if len(files) == 0 {
		return held, nil
	}
	ui.Step("Checking what is about to go into git")
	var total int64
	for _, f := range files {
		total += min(f.Size, vet.HeadSize)
	}
	ui.Info("looking for vendor folders, credentials and large binaries in %d file(s)", len(files))
	progress := ui.NewProgress(total, len(files))
	r := vet.Scan(files, has, inGit, func(n int64) {
		progress.Add(n)
		progress.File()
	})
	progress.Done()
	if r.Empty() {
		ui.OK("nothing to ask about")
		return held, nil
	}
	changed, undecided := false, 0
	hold := func(paths ...string) {
		for _, p := range paths {
			held[p] = true
		}
	}

	for _, v := range r.Vendors {
		keep := "Keep it out of git; the server's copy is left as it is"
		build := ""
		if v.Composer {
			keep, build = "Keep it out of git; every deploy rebuilds it with: "+composerBuild, composerBuild
		}
		switch ui.Choose(fmt.Sprintf("%s is a dependency folder (%d files).", v.Path, len(v.Files)), keep+" (recommended)", "Keep it in git") {
		case 0:
			c.s.Vendor = append(c.s.Vendor, site.Vendor{Path: v.Path, Build: build})
			changed = true
			hold(v.Files...)
		case -1:
			undecided++
			hold(v.Files...)
		}
	}

	for _, p := range r.Credentials {
		switch ui.Choose(p+" looks like it holds a credential — a key, password or token.",
			"Keep it out of git; the server's copy is left as it is (recommended)", "Keep it in git") {
		case 0:
			c.s.Ignore = append(c.s.Ignore, p)
			changed = true
			hold(p)
		case -1:
			undecided++
			hold(p)
		}
	}

	if len(r.Binaries) > 0 {
		var paths []string
		for _, f := range r.Binaries {
			paths = append(paths, f.Path)
		}
		fmt.Printf("%s%d large binary file(s):%s\n", ui.B, len(r.Binaries), ui.N)
		for _, f := range r.Binaries {
			ui.Info("%s  %s", f.Path, ui.Size(f.Size))
		}
		options := []string{"Keep them out of git; the server's copies are left as they are", "Keep them in git as they are"}
		lfs := c.git.OK("lfs", "version")
		if lfs {
			options = append([]string{"Keep them in git through Git LFS (recommended)"}, options...)
		} else {
			ui.Info("Git LFS would keep them out of git's history, but git-lfs isn't installed: https://git-lfs.com")
		}
		choice := ui.Choose("What should git do with them?", options...)
		if !lfs && choice >= 0 {
			choice++
		}
		switch choice {
		case 0:
			c.s.LFS = append(c.s.LFS, paths...)
			changed = true
		case 1:
			c.s.Ignore = append(c.s.Ignore, paths...)
			changed = true
			hold(paths...)
		case -1:
			undecided++
			hold(paths...)
		}
	}

	if changed {
		if err := c.writeKeptOut(); err != nil {
			return nil, err
		}
		ui.OK("recorded in %s, .gitignore and .gitattributes", site.File)
	}
	if undecided > 0 {
		ui.Warn("%d file(s) held back from git until someone decides about them —", len(held))
		ui.Warn("run gd-sync again in a terminal, without -y, to answer")
	}
	return held, nil
}

// writeKeptOut rewrites the files that say what git keeps, and sets up Git
// LFS when the site uses it.
func (c *ctx) writeKeptOut() error {
	for _, step := range []func(*site.Site) error{scaffold.Gitignore, scaffold.Gitattributes, func(s *site.Site) error { return s.Write() }} {
		if err := step(c.s); err != nil {
			return err
		}
	}
	return c.ensureLFS()
}

// ensureLFS makes git run files through Git LFS when the site keeps any
// there, and makes pushes upload them.
func (c *ctx) ensureLFS() error {
	if len(c.s.LFS) == 0 {
		return nil
	}
	if !c.git.OK("lfs", "version") {
		return errors.New(`This site keeps files in Git LFS (lfs in .gd-sync.toml), and git-lfs isn't installed.
  Install it from https://git-lfs.com, then run gd-sync again.`)
	}
	if err := c.git.Quiet("lfs", "install", "--local"); err != nil {
		if out, _ := c.git.Output("config", "filter.lfs.clean"); strings.TrimSpace(out) == "" {
			return err
		}
		ui.Warn("git lfs install: %v", err)
	}
	return nil
}

// isLFS reports whether the site keeps a path in Git LFS.
func (c *ctx) isLFS(p string) bool {
	for _, l := range c.s.LFS {
		if p == l {
			return true
		}
	}
	return false
}

// keptOut reports whether a path is, or is inside, one git leaves out.
func (c *ctx) keptOut(p string) bool {
	for _, k := range c.s.KeptOut() {
		if p == k || strings.HasPrefix(p, k+"/") {
			return true
		}
	}
	return false
}

// vetUntracked checks the files in the folder that git would add but
// doesn't hold yet — a path just added to tracked, say.
func (c *ctx) vetUntracked() error {
	out, err := c.git.Output("ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return err
	}
	var files []vet.File
	for _, p := range strings.Split(out, "\x00") {
		if p == "" {
			continue
		}
		local := filepath.Join(c.s.Repo, filepath.FromSlash(p))
		if st, err := os.Lstat(local); err == nil && st.Mode().IsRegular() {
			files = append(files, vet.File{Path: p, Local: local, Size: st.Size()})
		}
	}
	has := func(p string) bool { return fileExists(filepath.Join(c.s.Repo, filepath.FromSlash(p))) }
	inGit := func(dir string) bool {
		out, _ := c.git.Output("ls-files", "--", dir)
		return strings.TrimSpace(out) != ""
	}
	_, err = c.vetFiles(files, has, inGit)
	return err
}
