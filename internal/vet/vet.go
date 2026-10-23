// Package vet looks at files about to go into git for three things that
// don't belong there as plain files: dependency folders (vendor/), files that
// look like credentials, and large binaries.
package vet

import (
	"bytes"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

// LargeBinary is the size from which a binary file is worth a question.
const LargeBinary = 1 << 20

// HeadSize is how much of each file Scan reads: enough to recognise a
// credential.
const HeadSize = 64 << 10

// File is one file about to go into git.
type File struct {
	Path  string // slash-separated, relative to the web root
	Local string // where its content is on this computer
	Size  int64
}

// Vendor is a dependency folder found among the files.
type Vendor struct {
	Path     string
	Composer bool // composer.json sits next to it, so it can be rebuilt
	Files    []string
}

// Report is what Scan found. A file is reported at most once: files inside
// a vendor folder are only reported as part of it.
type Report struct {
	Vendors     []Vendor
	Credentials []string
	Binaries    []File
}

func (r Report) Empty() bool {
	return len(r.Vendors)+len(r.Credentials)+len(r.Binaries) == 0
}

// Scan checks the files. has reports whether a path exists in the repo or
// among the files, and inGit whether git already holds anything under a
// folder — a vendor folder git already keeps is left as it is. read, which
// may be nil, is called once per file with the bytes of it that Scan looks
// at — min(Size, HeadSize) — so the calls add up to a known total.
func Scan(files []File, has func(string) bool, inGit func(dir string) bool, read func(int64)) Report {
	var r Report
	vendors := map[string]*Vendor{}
	var rest []File
	for _, f := range files {
		if dir := vendorDir(f.Path); dir != "" && !inGit(dir) {
			v := vendors[dir]
			if v == nil {
				v = &Vendor{Path: dir, Composer: has(path.Join(path.Dir(dir), "composer.json"))}
				vendors[dir] = v
			}
			v.Files = append(v.Files, f.Path)
			if read != nil {
				read(min(f.Size, HeadSize)) // not read, but done with all the same
			}
			continue
		}
		rest = append(rest, f)
	}
	for _, v := range vendors {
		r.Vendors = append(r.Vendors, *v)
	}
	sort.Slice(r.Vendors, func(i, j int) bool { return r.Vendors[i].Path < r.Vendors[j].Path })

	for _, f := range rest {
		head, binary := peek(f)
		if read != nil {
			read(min(f.Size, HeadSize))
		}
		switch {
		case Credential(f.Path, head, binary):
			r.Credentials = append(r.Credentials, f.Path)
		case binary && f.Size >= LargeBinary:
			r.Binaries = append(r.Binaries, f)
		}
	}
	sort.Strings(r.Credentials)
	sort.Slice(r.Binaries, func(i, j int) bool { return r.Binaries[i].Path < r.Binaries[j].Path })
	return r
}

// vendorDir is the topmost vendor/ folder a path is in, or "". A vendor
// folder at the very top of the web root is left alone: that is the site's
// own layout, not a dependency folder inside something you maintain.
func vendorDir(p string) string {
	parts := strings.Split(p, "/")
	for i := 1; i < len(parts)-1; i++ {
		if parts[i] == "vendor" {
			return strings.Join(parts[:i+1], "/")
		}
	}
	return ""
}

// peek reads the start of a file, enough to recognise a credential, and
// says whether it is binary the way git decides: a NUL in the first 8000
// bytes.
func peek(f File) (head []byte, binary bool) {
	fh, err := os.Open(f.Local)
	if err != nil {
		return nil, false
	}
	defer fh.Close()
	head, _ = io.ReadAll(io.LimitReader(fh, HeadSize))
	n := min(len(head), 8000)
	return head, bytes.IndexByte(head[:n], 0) >= 0
}

// keyFiles are file names and extensions that are credentials whatever is in
// them.
var (
	keyNames = map[string]bool{
		"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
		".htpasswd": true, ".netrc": true, ".pgpass": true, "auth.json": true,
	}
	keyExts = map[string]bool{
		".key": true, ".p12": true, ".pfx": true, ".jks": true, ".keystore": true, ".ppk": true, ".kdbx": true,
	}
	// configExts are where a file named after a secret keeps it; a .php file
	// called that is far more likely code that handles credentials.
	configExts = map[string]bool{
		"": true, ".json": true, ".yml": true, ".yaml": true, ".ini": true, ".txt": true,
		".xml": true, ".conf": true, ".cfg": true, ".toml": true, ".env": true,
	}
	secretWords = []string{"credential", "secret", "service-account", "service_account", "serviceaccount"}

	secretContent = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----` +
		`|"type"\s*:\s*"service_account"` +
		`|\bAKIA[0-9A-Z]{16}\b` +
		`|\bgh[pousr]_[A-Za-z0-9]{36,}` +
		`|\b[sr]k_live_[0-9A-Za-z]{20,}` +
		`|\bxox[abprs]-[0-9A-Za-z-]{10,}`)
)

// Credential reports whether a file looks like it holds a secret, by its
// name or by what it starts with.
func Credential(p string, head []byte, binary bool) bool {
	name := strings.ToLower(path.Base(p))
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	switch {
	case keyNames[name] || keyExts[ext]:
		return true
	case strings.HasPrefix(name, ".env.") && !strings.Contains(name, "example") && !strings.Contains(name, "sample") && !strings.Contains(name, "dist"):
		return true
	case configExts[ext]:
		for _, w := range secretWords {
			if strings.Contains(stem, w) {
				return true
			}
		}
	}
	return !binary && secretContent.Match(head)
}
