package transfer

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/s-visser/gd-sync/internal/gitx"
	"github.com/s-visser/gd-sync/internal/remote"
	"github.com/s-visser/gd-sync/internal/ui"
)

// Fetched is a server file saved locally, with its git blob id, and the
// blob id git holds for it instead when it goes through Git LFS.
type Fetched struct{ File, Blob, Pointer string }

// ID is the blob id main holds for this file when it matches.
func (f Fetched) ID(lfs bool) string {
	if lfs {
		return f.Pointer
	}
	return f.Blob
}

// Fetch downloads the given server files as one tar stream into dir. Files
// are saved under numbered names, so no server path ever has to be a valid
// local name until it is placed in the repo. Progress may be nil.
func Fetch(conn *remote.Conn, root string, paths []string, dir string, progress *ui.Progress) (map[string]Fetched, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := conn.Run("cd "+remote.Quote(root)+" && tar czf - --null -T -", gitx.NUL(paths), pw)
		pw.CloseWithError(err)
		done <- err
	}()

	got := map[string]Fetched{}
	err := readTar(pr, func(name string, size int64, r io.Reader) error {
		file := filepath.Join(dir, strconv.Itoa(len(got)))
		blob, pointer, err := saveHashed(file, size, counter{r, progress})
		if err == nil {
			got[name] = Fetched{File: file, Blob: blob, Pointer: pointer}
			progress.File()
		}
		return err
	})
	io.Copy(io.Discard, pr)
	if rerr := <-done; err == nil && rerr != nil {
		err = rerr // tar on the server reports files that vanished
	}
	return got, err
}

// counter reports bytes read to a progress bar.
type counter struct {
	r io.Reader
	p *ui.Progress
}

func (c counter) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.p.Add(int64(n))
	return n, err
}

func readTar(r io.Reader, each func(name string, size int64, r io.Reader) error) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if err := each(strings.TrimPrefix(h.Name, "./"), h.Size, tr); err != nil {
			return err
		}
	}
}

// saveHashed writes r to file and returns its git blob id, and the blob id
// of its Git LFS pointer.
func saveHashed(file string, size int64, r io.Reader) (blob, pointer string, err error) {
	f, err := os.Create(file)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	h, h256 := sha1.New(), sha256.New()
	fmt.Fprintf(h, "blob %d\x00", size)
	if _, err := io.Copy(io.MultiWriter(f, h, h256), r); err != nil {
		return "", "", err
	}
	p := lfsPointer(hex.EncodeToString(h256.Sum(nil)), size)
	pointer, _ = BlobID(strings.NewReader(p), int64(len(p)))
	return hex.EncodeToString(h.Sum(nil)), pointer, f.Close()
}

// lfsPointer is what Git LFS stores in git in place of a file's content.
func lfsPointer(sha256hex string, size int64) string {
	return fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", sha256hex, size)
}

var pointerSize = regexp.MustCompile(`(?m)^size (\d+)$`)

// pointerContentSize is the size of the file an LFS pointer stands for.
func pointerContentSize(pointer []byte) (int64, bool) {
	if !bytes.HasPrefix(pointer, []byte("version https://git-lfs.github.com/spec/")) {
		return 0, false
	}
	m := pointerSize.FindSubmatch(pointer)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(string(m[1]), 10, 64)
	return n, err == nil
}

// BlobID is git's object id for content of the given size.
func BlobID(r io.Reader, size int64) (string, error) {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", size)
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Place moves fetched files to their path in the repo.
func Place(repoDir string, fetched map[string]Fetched, paths []string) error {
	for _, p := range paths {
		dst := filepath.Join(repoDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.Rename(fetched[p].File, dst); err != nil {
			return fmt.Errorf("could not write %s: %w", p, err)
		}
	}
	return nil
}

// upload streams a tar built by write into the server's root.
func upload(conn *remote.Conn, root string, write func(*tar.Writer) error) error {
	pr, pw := io.Pipe()
	built := make(chan error, 1)
	go func() {
		gz := gzip.NewWriter(pw)
		tw := tar.NewWriter(gz)
		err := write(tw)
		if err == nil {
			err = tw.Close()
		}
		if err == nil {
			err = gz.Close()
		}
		pw.CloseWithError(err)
		built <- err
	}()
	err := conn.Run("cd "+remote.Quote(root)+" && tar xzmf - --no-overwrite-dir", pr, nil)
	pr.Close() // unblocks the writer if the server stopped reading
	if berr := <-built; berr != nil && berr != io.ErrClosedPipe {
		err = berr
	}
	if err != nil {
		return fmt.Errorf("Upload failed: %w", err)
	}
	return nil
}

// UploadBlobs ships files straight from git's object store, so what goes
// live is exactly main, whatever the working tree looks like. Files lfs
// reports as Git LFS ones go up as their content, never as the pointer.
func UploadBlobs(conn *remote.Conn, root string, repo gitx.Repo, t Tree, paths []string, lfs func(string) bool) error {
	if len(paths) == 0 {
		return nil
	}
	cmd := exec.Command("git", "-C", repo.Dir, "cat-file", "--batch")
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	out := bufio.NewReader(outPipe)
	now := time.Now()

	err = upload(conn, root, func(tw *tar.Writer) error {
		for _, p := range paths {
			b := t[p]
			fmt.Fprintln(in, b.ID)
			head, err := out.ReadString('\n')
			if err != nil {
				return err
			}
			f := strings.Fields(head)
			if len(f) != 3 || f[1] != "blob" {
				return fmt.Errorf("git cat-file: unexpected %q for %s", strings.TrimSpace(head), p)
			}
			size, _ := strconv.ParseInt(f[2], 10, 64)
			mode := int64(0o644)
			if b.Mode == "100755" {
				mode = 0o755
			}
			if lfs != nil && lfs(p) && size < 1024 {
				pointer := make([]byte, size+1) // and the newline after the object
				if _, err := io.ReadFull(out, pointer); err != nil {
					return err
				}
				if real, ok := pointerContentSize(pointer[:size]); ok {
					if err := tw.WriteHeader(&tar.Header{Name: p, Mode: mode, Size: real, ModTime: now, Typeflag: tar.TypeReg}); err != nil {
						return err
					}
					if err := smudge(repo, p, pointer[:size], tw, real); err != nil {
						return err
					}
					continue
				}
				if err := tw.WriteHeader(&tar.Header{Name: p, Mode: mode, Size: size, ModTime: now, Typeflag: tar.TypeReg}); err != nil {
					return err
				}
				if _, err := tw.Write(pointer[:size]); err != nil {
					return err
				}
				continue
			}
			if err := tw.WriteHeader(&tar.Header{Name: p, Mode: mode, Size: size, ModTime: now, Typeflag: tar.TypeReg}); err != nil {
				return err
			}
			if _, err := io.CopyN(tw, out, size); err != nil {
				return err
			}
			if _, err := out.Discard(1); err != nil { // the newline after each object
				return err
			}
		}
		return nil
	})
	in.Close()
	if werr := cmd.Wait(); err == nil {
		err = werr
	}
	return err
}

// smudge writes the content a Git LFS pointer stands for, which git-lfs
// downloads first if this computer doesn't have it yet.
func smudge(repo gitx.Repo, p string, pointer []byte, w io.Writer, size int64) error {
	cmd := exec.Command("git", "-C", repo.Dir, "lfs", "smudge", "--", p)
	cmd.Stdin = bytes.NewReader(pointer)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s is in Git LFS, and git-lfs could not run: %w", p, err)
	}
	_, err = io.CopyN(w, out, size)
	io.Copy(io.Discard, out)
	if werr := cmd.Wait(); err == nil && werr != nil {
		err = fmt.Errorf("git lfs smudge %s: %v %s", p, werr, strings.TrimSpace(errb.String()))
	}
	return err
}

// UploadDir ships files from a local folder, such as a static build.
func UploadDir(conn *remote.Conn, root, dir string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	return upload(conn, root, func(tw *tar.Writer) error {
		for _, p := range paths {
			f, err := os.Open(filepath.Join(dir, filepath.FromSlash(p)))
			if err != nil {
				return err
			}
			st, err := f.Stat()
			if err == nil {
				mode := int64(0o644)
				if st.Mode().Perm()&0o111 != 0 {
					mode = 0o755
				}
				err = tw.WriteHeader(&tar.Header{Name: p, Mode: mode, Size: st.Size(), ModTime: st.ModTime(), Typeflag: tar.TypeReg})
			}
			if err == nil {
				_, err = io.Copy(tw, f)
			}
			f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// RemoveRemote deletes files on the server.
func RemoveRemote(conn *remote.Conn, root string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := conn.Run("cd "+remote.Quote(root)+" && xargs -0 rm -f --", gitx.NUL(paths), nil); err != nil {
		return fmt.Errorf("Removing files on the server failed: %w", err)
	}
	return nil
}

// ListLocal lists a local folder's files, slash-separated, that the matcher
// keeps.
func ListLocal(dir string, m Matcher) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if m.Excluded(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}
