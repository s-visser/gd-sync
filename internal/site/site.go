// Package site reads and writes a site's own settings, which live in its repo
// in two TOML files:
//
//	.gd-sync.toml         committed. What the site is and where it deploys.
//	                      The GitHub workers read this same file.
//	.gd-sync.local.toml   gitignored. How to reach the server from this machine.
//
// There is no central config: run gd-sync anywhere inside a site's folder and
// it finds the site by walking up to the nearest .gd-sync.toml.
package site

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/s-visser/gd-sync/internal/config"
)

// The settings files.
const (
	File      = ".gd-sync.toml"
	LocalFile = ".gd-sync.local.toml"
)

type Site struct {
	Repo string `toml:"-"` // the site's folder, absolute

	Name       string   `toml:"name"`
	Type       string   `toml:"type"` // "wordpress" or "astro"
	RemoteRoot string   `toml:"remote_root"`
	Tracked    []string `toml:"tracked"` // paths under RemoteRoot that the repo mirrors
	BuildCmd   string   `toml:"build_cmd"`
	PublishDir string   `toml:"publish_dir"`
	LiveURL    string   `toml:"live_url"`
	LocalURL   string   `toml:"local_url"`
	Exclude    []string `toml:"exclude"`
	PHPVersion string   `toml:"php_version"` // what the live site runs, e.g. "8.3"; the devcontainer uses the same

	// The ports the local site takes on this computer. Give every site its own
	// to run several at once. Defaults fills in what the file leaves out.
	Port        int `toml:"port"`         // the website
	DBPort      int `toml:"db_port"`      // wordpress: MariaDB
	MailPort    int `toml:"mail_port"`    // wordpress: Mailpit
	PreviewPort int `toml:"preview_port"` // astro: astro preview

	// Inside the tracked paths, but not kept in git the usual way.
	Ignore []string `toml:"ignore"` // left out of git; the server's copy is never synced or deployed
	LFS    []string `toml:"lfs"`    // in git through Git LFS
	Vendor []Vendor `toml:"vendor"` // built on deploy rather than kept in git

	SSHHost string `toml:"-"`
	SSHUser string `toml:"-"`
	SSHPort string `toml:"-"`
}

// Vendor is a dependency folder, such as a plugin's Composer vendor/, that
// git leaves out. Deploy runs Build in the folder above it, on main's files,
// and makes the server's copy match what that built.
type Vendor struct {
	Path  string `toml:"path"`
	Build string `toml:"build"` // empty: never built, the server's copy is left alone
}

// KeptOut is every path inside the tracked ones that git leaves out: the
// ignored paths and the vendor folders.
func (s *Site) KeptOut() []string {
	out := append([]string{}, s.Ignore...)
	for _, v := range s.Vendor {
		out = append(out, v.Path)
	}
	return out
}

// Builds are the vendor folders that deploy builds.
func (s *Site) Builds() []Vendor {
	var out []Vendor
	for _, v := range s.Vendor {
		if v.Build != "" {
			out = append(out, v)
		}
	}
	return out
}

// localFile is .gd-sync.local.toml.
type localFile struct {
	SSHHost string `toml:"ssh_host"`
	SSHUser string `toml:"ssh_user"`
	SSHPort int    `toml:"ssh_port"`
}

// ErrNotInSite is returned when no .gd-sync.toml is found above a folder.
var ErrNotInSite = errors.New(`Not inside a gd-sync site.
  cd into one of your site folders, or create one with: gd-sync new
  (gd-sync list shows the sites gd-sync knows about)`)

// Is reports whether dir holds a site.
func Is(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, File))
	return err == nil && !fi.IsDir()
}

// Find walks up from dir to the nearest folder holding a .gd-sync.toml.
func Find(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if Is(d) {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", ErrNotInSite
		}
		d = parent
	}
}

// Load reads a site from its folder, filling in the defaults for its type.
func Load(repo string) (*Site, error) {
	if !Is(repo) {
		return nil, fmt.Errorf("No %s in %s", File, repo)
	}
	s := &Site{Repo: repo, Type: "wordpress"}
	if err := s.read(); err != nil {
		return nil, err
	}
	if s.SSHPort == "" {
		s.SSHPort = "22"
	}
	if s.Name == "" {
		return nil, fmt.Errorf("%s has no name", filepath.Join(repo, File))
	}
	s.RemoteRoot = strings.TrimSuffix(s.RemoteRoot, "/")
	s.Defaults()
	if err := s.checkPorts(); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(repo, File), err)
	}
	return s, nil
}

// checkPorts refuses ports that can't work: out of range, or one used twice.
func (s *Site) checkPorts() error {
	seen := map[int]bool{}
	for _, p := range s.Ports() {
		if p < 1 || p > 65535 {
			return fmt.Errorf("port %d is not a port number — set port, db_port, mail_port or preview_port to one between 1 and 65535", p)
		}
		if seen[p] {
			return fmt.Errorf("port %d is used twice — every port must differ", p)
		}
		seen[p] = true
	}
	return nil
}

func (s *Site) read() error {
	if err := config.ReadTOML(filepath.Join(s.Repo, File), s); err != nil {
		return err
	}
	var l localFile
	if err := config.ReadTOML(filepath.Join(s.Repo, LocalFile), &l); err != nil {
		return err
	}
	s.SSHHost, s.SSHUser = l.SSHHost, l.SSHUser
	if l.SSHPort != 0 {
		s.SSHPort = strconv.Itoa(l.SSHPort)
	}
	return nil
}

// The ports a site gets when its file names none.
const (
	defaultWordPressPort = 8080
	defaultDBPort        = 13306
	defaultMailPort      = 8025
	defaultAstroPort     = 4321
)

// Defaults fills in what a site type implies when the file leaves it out.
//
// Without a port, the site keeps the one in a localhost local_url, as files
// from before port existed have. The other ports follow port: db_port and
// mail_port move as far from their defaults as port moves from 8080, and
// preview_port is the one after port. A local_url on localhost always uses
// port, so changing port is enough.
func (s *Site) Defaults() {
	base := defaultWordPressPort
	if s.Type == "astro" {
		base = defaultAstroPort
		if s.BuildCmd == "" {
			s.BuildCmd = "npm ci && npm run build"
		}
		if s.PublishDir == "" {
			s.PublishDir = "dist"
		}
	}
	if s.Port == 0 {
		s.Port = base
		if p := localPort(s.LocalURL); p != 0 {
			s.Port = p
		}
	}
	if s.LocalURL == "" || localPort(s.LocalURL) != 0 {
		s.LocalURL = localURL(s.LocalURL, s.Port)
	}
	switch s.Type {
	case "astro":
		if s.PreviewPort == 0 {
			s.PreviewPort = s.Port + 1
		}
	case "wordpress":
		if s.DBPort == 0 {
			s.DBPort = defaultDBPort + s.Port - defaultWordPressPort
		}
		if s.MailPort == 0 {
			s.MailPort = defaultMailPort + s.Port - defaultWordPressPort
		}
	}
}

// Ports are every port the local site takes on this computer.
func (s *Site) Ports() []int {
	switch s.Type {
	case "astro":
		return []int{s.Port, s.PreviewPort}
	case "wordpress":
		return []int{s.Port, s.DBPort, s.MailPort}
	}
	return []int{s.Port}
}

// localPort is the port of a URL on this computer, or 0 when the URL points
// anywhere else, or names no port.
func localPort(raw string) int {
	u, err := url.Parse(raw)
	if err != nil || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") {
		return 0
	}
	p, _ := strconv.Atoi(u.Port())
	return p
}

// localURL is raw, or http://localhost when it's empty, on the given port.
func localURL(raw string, port int) string {
	u, err := url.Parse(raw)
	if raw == "" || err != nil {
		u = &url.URL{Scheme: "http", Host: "localhost"}
	}
	u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
	return u.String()
}

// Target is user@host, for messages.
func (s *Site) Target() string { return s.SSHUser + "@" + s.SSHHost }

// NeedServer checks that this machine knows how to reach the server.
func (s *Site) NeedServer() error {
	local := filepath.Join(s.Repo, LocalFile)
	for _, f := range []struct{ name, val string }{
		{"ssh_host", s.SSHHost}, {"ssh_user", s.SSHUser},
	} {
		if f.val == "" {
			return fmt.Errorf(`%s has no %s on this machine.
  Connection details live in %s — run 'gd-sync adopt' to fill them in.`, s.Name, f.name, local)
		}
	}
	if s.RemoteRoot == "" {
		return fmt.Errorf("%s has no remote_root — set it in %s", s.Name, filepath.Join(s.Repo, File))
	}
	return nil
}

// Write saves both settings files.
func (s *Site) Write() error {
	var b strings.Builder
	b.WriteString("# What this site is and where it deploys. This file IS the config — gd-sync reads it\n")
	b.WriteString("# to work out which site you are in, and the GitHub workers read it too.\n\n")
	fmt.Fprintf(&b, "name = %s\n", config.String(s.Name))
	fmt.Fprintf(&b, "type = %s  # \"wordpress\" or \"astro\"\n\n", config.String(s.Type))
	fmt.Fprintf(&b, "live_url = %s\n", config.String(s.LiveURL))
	if localPort(s.LocalURL) == 0 {
		fmt.Fprintf(&b, "local_url = %s\n", config.String(s.LocalURL))
	}
	b.WriteString("\n# The ports the local site takes on this computer. Give every site its own to run\n")
	b.WriteString("# several at once, then run 'gd-sync scaffold' (and rebuild the devcontainer).\n")
	fmt.Fprintf(&b, "port = %d\n", s.Port)
	switch s.Type {
	case "astro":
		fmt.Fprintf(&b, "preview_port = %d  # astro preview\n", s.PreviewPort)
	case "wordpress":
		fmt.Fprintf(&b, "db_port = %d  # MariaDB\n", s.DBPort)
		fmt.Fprintf(&b, "mail_port = %d  # Mailpit\n", s.MailPort)
	}
	b.WriteString("\n")
	b.WriteString("# The site's folder on the server\n")
	fmt.Fprintf(&b, "remote_root = %s\n\n", config.String(s.RemoteRoot))
	b.WriteString("# The paths you maintain, relative to remote_root. git keeps these; gd-sync\n")
	b.WriteString("# mirrors the rest of the site locally. Run 'gd-sync scaffold' after changing it.\n")
	fmt.Fprintf(&b, "tracked = %s\n\n", config.List(s.Tracked))
	b.WriteString("# Never synced or deployed, on top of gd-sync's own list (rsync patterns)\n")
	fmt.Fprintf(&b, "exclude = %s\n", config.List(s.Exclude))
	if len(s.Ignore)+len(s.LFS) > 0 {
		b.WriteString("\n# Inside the tracked paths, but kept out of git. The server's copy is never\n")
		b.WriteString("# synced or deployed, only downloaded with the rest of the site.\n")
		fmt.Fprintf(&b, "ignore = %s\n\n", config.List(s.Ignore))
		b.WriteString("# Kept in git through Git LFS: large binary files.\n")
		fmt.Fprintf(&b, "lfs = %s\n", config.List(s.LFS))
	}
	if s.BuildCmd != "" || s.PublishDir != "" || s.Type == "astro" {
		b.WriteString("\n# How the site is built, and the folder the build puts on the server\n")
		fmt.Fprintf(&b, "build_cmd = %s\n", config.String(s.BuildCmd))
		fmt.Fprintf(&b, "publish_dir = %s\n", config.String(s.PublishDir))
	}
	if s.Type == "wordpress" {
		b.WriteString("\n# The live site's PHP version — the devcontainer runs the same. Check with: gd-sync php\n")
		fmt.Fprintf(&b, "php_version = %s\n", config.String(s.PHPVersion))
	}
	for i, v := range s.Vendor {
		if i == 0 {
			b.WriteString("\n# Dependency folders kept out of git. Before every deploy uploads anything, build\n")
			b.WriteString("# runs in the folder above path, on main's files, and the server's copy of path is\n")
			b.WriteString("# made to match what it built. Without a build the server's copy is left alone.\n")
		}
		fmt.Fprintf(&b, "[[vendor]]\npath = %s\nbuild = %s\n", config.String(v.Path), config.String(v.Build))
	}

	if err := os.WriteFile(filepath.Join(s.Repo, File), []byte(b.String()), 0o644); err != nil {
		return err
	}
	return s.writeLocal()
}

func (s *Site) writeLocal() error {
	port := s.SSHPort
	if _, err := strconv.Atoi(port); err != nil {
		port = "22"
	}
	local := fmt.Sprintf(`# How to reach the server from this machine. Not committed.
# The GitHub workers use repository secrets instead — see: gd-sync secrets

ssh_host = %s
ssh_user = %s
ssh_port = %s
`, config.String(s.SSHHost), config.String(s.SSHUser), port)
	return os.WriteFile(filepath.Join(s.Repo, LocalFile), []byte(local), 0o644)
}
