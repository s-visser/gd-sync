package app

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/s-visser/gd-sync/internal/remote"
	"github.com/s-visser/gd-sync/internal/site"
	"github.com/s-visser/gd-sync/internal/transfer"
	"github.com/s-visser/gd-sync/internal/ui"
)

// vendorBuild is one vendor folder built for a deploy: the files under dir,
// at their paths in the site, and what the server needs to match them.
type vendorBuild struct {
	v       site.Vendor
	dir     string
	up, del []string // relative to the web root
}

// buildVendors builds every vendor folder from main's files, each in a
// scratch folder, and works out what the server's copy needs. It runs
// before a deploy uploads anything: if a build fails, nothing goes live.
// The vendor folder belongs to the build, so the server ends up with
// exactly what it made — files the build didn't make are removed.
func (c *ctx) buildVendors(conn *remote.Conn) ([]vendorBuild, error) {
	var out []vendorBuild
	for _, v := range c.s.Builds() {
		ui.Step("Building %s from main", v.Path)
		wd, err := c.workDir()
		if err != nil {
			return nil, err
		}
		pkg := path.Dir(v.Path)
		if err := transfer.Export(c.git, "main", pkg, wd); err != nil {
			return nil, err
		}
		ui.Info("%s: %s", pkg, v.Build)
		if err := shell(filepath.Join(wd, filepath.FromSlash(pkg)), v.Build); err != nil {
			return nil, fmt.Errorf(`Building %s failed, so nothing was deployed: %w
  The build runs on your computer, or the deploy worker. Change it under [[vendor]] in %s.`, v.Path, err, site.File)
		}
		built := filepath.Join(wd, filepath.FromSlash(v.Path))
		if st, err := os.Stat(built); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("Building %s made no %s folder, so nothing was deployed", pkg, path.Base(v.Path))
		}
		files, err := transfer.ListLocal(built, transfer.NewMatcher(nil, nil))
		if err != nil {
			return nil, err
		}
		local, err := transfer.LocalBlobs(built, files)
		if err != nil {
			return nil, err
		}
		server, err := transfer.RemoteBlobs(conn, c.s.RemoteRoot, v.Path)
		if err != nil {
			return nil, err
		}
		b := vendorBuild{v: v, dir: wd}
		for _, p := range files {
			if server[p] != local[p] {
				b.up = append(b.up, v.Path+"/"+p)
			}
		}
		for p := range server {
			if _, ok := local[p]; !ok {
				b.del = append(b.del, v.Path+"/"+p)
			}
		}
		sort.Strings(b.del)
		ui.OK("built %s: %d file(s)", v.Path, len(files))
		out = append(out, b)
	}
	return out, nil
}

// shell runs a command line the way a terminal would, in dir.
func shell(dir, command string) error {
	cmd := exec.Command("sh", "-c", command)
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", command)
	}
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
	return cmd.Run()
}
