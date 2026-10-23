package transfer

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/s-visser/gd-sync/internal/gitx"
	"github.com/s-visser/gd-sync/internal/remote"
)

// Export writes the files a commit holds under dir into dest, at the same
// paths, so a build can run on exactly what main has.
func Export(repo gitx.Repo, ref, dir, dest string) error {
	cmd := exec.Command("git", "-C", repo.Dir, "archive", "--format=tar", ref, "--", dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	tr := tar.NewReader(out)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			cmd.Wait()
			return err
		}
		name := path.Clean(h.Name)
		if h.Typeflag != tar.TypeReg || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			continue
		}
		dst := filepath.Join(dest, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			cmd.Wait()
			return err
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o755|0o644)
		if err == nil {
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			cmd.Wait()
			return err
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git archive %s %s: %w", ref, dir, err)
	}
	return nil
}

// hasher prints "path<TAB>blob id" for every file under the folder it is
// given, relative to that folder. Digest::SHA is part of Perl since 5.10.
const hasher = `use strict; use warnings; use File::Find; use Digest::SHA;
my $d = shift; exit 0 unless -d $d;
find({ no_chdir => 1, wanted => sub {
  my $p = $File::Find::name; return if !-f $p || -l $p;
  (my $r = $p) =~ s{^\Q$d\E/}{}; return if $r =~ /[\t\n]/;
  my @s = lstat $p; my $h = Digest::SHA->new(1); $h->add("blob $s[7]\0"); $h->addfile($p, "b");
  print "$r\t", $h->hexdigest, "\n";
} }, $d);
`

// RemoteBlobs hashes the server's files under dir, relative to root, the
// way git would, so they can be compared without downloading them. Paths
// are relative to dir; a missing dir is an empty map.
func RemoteBlobs(conn *remote.Conn, root, dir string) (map[string]string, error) {
	out, err := conn.Output("cd "+remote.Quote(root)+" && perl - "+remote.Quote(dir), strings.NewReader(hasher))
	if err != nil {
		return nil, fmt.Errorf("Could not hash the server's %s: %w", dir, err)
	}
	blobs := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if p, id, ok := strings.Cut(line, "\t"); ok {
			blobs[p] = id
		}
	}
	return blobs, nil
}

// LocalBlobs hashes files under dir the way git would.
func LocalBlobs(dir string, paths []string) (map[string]string, error) {
	blobs := map[string]string{}
	for _, p := range paths {
		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			return nil, err
		}
		st, err := f.Stat()
		if err == nil {
			blobs[p], err = BlobID(f, st.Size())
		}
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	return blobs, nil
}
