// Package site reads and writes a site's own settings, which live in its repo
// in two files:
//
//	.gd-sync         committed. What the site is and where it deploys.
//	                 The GitHub workers read this same file.
//	.gd-sync.local   gitignored. How to reach the server from this machine.
//
// There is no central config: run gd-sync anywhere inside a site's folder and
// it finds the site by walking up to the nearest .gd-sync.
package site

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/s-visser/gd-sync/internal/config"
)

type Site struct {
	Repo string // the site's folder, absolute

	Name       string
	Type       string // "wordpress" or "astro"
	RemoteRoot string
	Tracked    []string // paths under RemoteRoot that the repo mirrors
	BuildCmd   string
	PublishDir string
	LiveURL    string
	LocalURL   string
	Exclude    []string
	PHPVersion string // what the live site runs, e.g. "8.3"; the devcontainer uses the same

	SSHHost string
	SSHUser string
	SSHPort string
}

// ErrNotInSite is returned when no .gd-sync is found above a folder.
var ErrNotInSite = errors.New(`Not inside a gd-sync site.
  cd into one of your site folders, or create one with: gd-sync new
  (gd-sync list shows the sites gd-sync knows about)`)

// Find walks up from dir to the nearest folder holding a .gd-sync.
func Find(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, ".gd-sync")); err == nil {
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
	if _, err := os.Stat(filepath.Join(repo, ".gd-sync")); err != nil {
		return nil, fmt.Errorf("No .gd-sync in %s", repo)
	}
	s := &Site{Repo: repo, Type: "wordpress", SSHPort: "22"}
	for _, name := range []string{".gd-sync", ".gd-sync.local"} {
		kv, err := config.ReadKeyValues(filepath.Join(repo, name))
		if err != nil {
			return nil, err
		}
		for k, v := range kv {
			s.set(k, v)
		}
	}
	if s.Name == "" {
		return nil, fmt.Errorf("%s has no SITE", filepath.Join(repo, ".gd-sync"))
	}
	s.RemoteRoot = strings.TrimSuffix(s.RemoteRoot, "/")
	s.Defaults()
	return s, nil
}

func (s *Site) set(k, v string) {
	switch k {
	case "SITE":
		s.Name = v
	case "SITE_TYPE":
		s.Type = v
	case "REMOTE_ROOT":
		s.RemoteRoot = v
	case "TRACKED":
		s.Tracked = strings.Fields(v)
	case "BUILD_CMD":
		s.BuildCmd = v
	case "PUBLISH_DIR":
		s.PublishDir = v
	case "LIVE_URL":
		s.LiveURL = v
	case "LOCAL_URL":
		s.LocalURL = v
	case "EXCLUDE":
		s.Exclude = strings.Fields(v)
	case "PHP_VERSION":
		s.PHPVersion = v
	case "SSH_HOST":
		s.SSHHost = v
	case "SSH_USER":
		s.SSHUser = v
	case "SSH_PORT":
		if v != "" {
			s.SSHPort = v
		}
	}
}

// Defaults fills in what a site type implies when the file leaves it out.
func (s *Site) Defaults() {
	switch s.Type {
	case "astro":
		if s.BuildCmd == "" {
			s.BuildCmd = "npm ci && npm run build"
		}
		if s.PublishDir == "" {
			s.PublishDir = "dist"
		}
		if s.LocalURL == "" {
			s.LocalURL = "http://localhost:4321"
		}
	case "wordpress":
		if s.LocalURL == "" {
			s.LocalURL = "http://localhost:8080"
		}
	}
}

// Target is user@host, for messages.
func (s *Site) Target() string { return s.SSHUser + "@" + s.SSHHost }

// NeedServer checks that this machine knows how to reach the server.
func (s *Site) NeedServer() error {
	for _, f := range []struct{ name, val string }{
		{"SSH_HOST", s.SSHHost}, {"SSH_USER", s.SSHUser}, {"REMOTE_ROOT", s.RemoteRoot},
	} {
		if f.val == "" {
			return fmt.Errorf(`%s has no %s on this machine.
  Connection details live in %s — run 'gd-sync adopt' to fill them in.`,
				s.Name, f.name, filepath.Join(s.Repo, ".gd-sync.local"))
		}
	}
	return nil
}

// Write saves both settings files.
func (s *Site) Write() error {
	shared := fmt.Sprintf(`# What this site is and where it deploys. This file IS the config — gd-sync reads it
# to work out which site you are in, and the GitHub workers read it too.
SITE=%s
SITE_TYPE=%s
REMOTE_ROOT=%s
TRACKED=%s
BUILD_CMD=%s
PUBLISH_DIR=%s
LIVE_URL=%s
LOCAL_URL=%s
EXCLUDE=%s
`, s.Name, s.Type, s.RemoteRoot, strings.Join(s.Tracked, " "), s.BuildCmd, s.PublishDir,
		s.LiveURL, s.LocalURL, strings.Join(s.Exclude, " "))
	if s.Type == "wordpress" {
		shared += "# The live site's PHP version — the devcontainer runs the same. Check with: gd-sync php\n"
		shared += "PHP_VERSION=" + s.PHPVersion + "\n"
	}
	local := fmt.Sprintf(`# How to reach the server from this machine. Not committed.
# The GitHub workers use repository secrets instead — see: gd-sync secrets
SSH_HOST=%s
SSH_USER=%s
SSH_PORT=%s
`, s.SSHHost, s.SSHUser, s.SSHPort)
	if err := os.WriteFile(filepath.Join(s.Repo, ".gd-sync"), []byte(shared), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Repo, ".gd-sync.local"), []byte(local), 0o644)
}
