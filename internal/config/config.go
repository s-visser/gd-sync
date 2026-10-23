// Package config holds the machine-wide settings and the registry of sites.
//
// A site's own settings live in its repo (see package site). What lives here
// is only what makes "gd-sync new" stop asking the same questions, plus a list
// of folders so that "gd-sync list" and --all have something to walk.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Global is the machine-wide settings file, config.toml.
type Global struct {
	ProjectsDir string // where new site folders are created
	GitOwner    string // GitHub user or org that new repos belong to
	GitPrivate  string // "true" or "false" for new repos
	DefaultType string // "wordpress" or "astro"
	Path        string
}

// Keys lists the settings in the order they are shown and saved.
var Keys = []string{"projects_dir", "git_owner", "git_private", "default_type"}

// globalFile is config.toml as written on disk.
type globalFile struct {
	ProjectsDir *string `toml:"projects_dir"`
	GitOwner    *string `toml:"git_owner"`
	GitPrivate  *bool   `toml:"git_private"`
	DefaultType *string `toml:"default_type"`
}

// Home is the folder holding the settings and the registry:
// $GD_SYNC_HOME, else $XDG_CONFIG_HOME/gd-sync, else ~/.config/gd-sync
// (%AppData%\gd-sync on Windows).
func Home() string {
	if h := os.Getenv("GD_SYNC_HOME"); h != "" {
		return h
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "gd-sync")
	}
	if runtime.GOOS == "windows" {
		if d, err := os.UserConfigDir(); err == nil {
			return filepath.Join(d, "gd-sync")
		}
	}
	return filepath.Join(UserHome(), ".config", "gd-sync")
}

// UserHome is the user's home folder, or "." if it can't be found.
func UserHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// ExpandHome turns a leading ~ into the home folder.
func ExpandHome(p string) string {
	if p == "~" {
		return UserHome()
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(UserHome(), p[2:])
	}
	return p
}

// Load reads the settings, falling back to defaults for anything unset.
func Load() (*Global, error) {
	g := &Global{
		ProjectsDir: filepath.Join(UserHome(), "Projects"),
		GitPrivate:  "true",
		DefaultType: "wordpress",
		Path:        os.Getenv("GD_SYNC_CONFIG"),
	}
	if g.Path == "" {
		g.Path = filepath.Join(Home(), "config.toml")
	}
	var f globalFile
	if err := ReadTOML(g.Path, &f); err != nil {
		return nil, err
	}
	for k, v := range map[string]*string{"projects_dir": f.ProjectsDir, "git_owner": f.GitOwner, "default_type": f.DefaultType} {
		if v != nil {
			g.Set(k, *v)
		}
	}
	if f.GitPrivate != nil {
		g.GitPrivate = strconv.FormatBool(*f.GitPrivate)
	}
	return g, nil
}

// Get returns a setting by key, or ok=false for an unknown key.
func (g *Global) Get(key string) (string, bool) {
	switch key {
	case "projects_dir":
		return g.ProjectsDir, true
	case "git_owner":
		return g.GitOwner, true
	case "git_private":
		return g.GitPrivate, true
	case "default_type":
		return g.DefaultType, true
	}
	return "", false
}

// ErrUnknownKey is what Set returns for a setting that doesn't exist.
var ErrUnknownKey = errors.New("unknown setting")

// Set changes a setting by key.
func (g *Global) Set(key, value string) error {
	switch key {
	case "projects_dir":
		g.ProjectsDir = ExpandHome(value)
	case "git_owner":
		g.GitOwner = value
	case "git_private":
		b, err := parseYesNo(value)
		if err != nil {
			return err
		}
		g.GitPrivate = strconv.FormatBool(b)
	case "default_type":
		g.DefaultType = value
	default:
		return ErrUnknownKey
	}
	return nil
}

func parseYesNo(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "y", "1":
		return true, nil
	case "false", "no", "n", "0":
		return false, nil
	}
	return false, fmt.Errorf("%q is not true or false", v)
}

func (g *Global) Save() error {
	var b strings.Builder
	b.WriteString("# gd-sync — machine-wide settings. Per-site settings live in each repo's .gd-sync.toml.\n\n")
	b.WriteString("# Where new site folders are created\n")
	fmt.Fprintf(&b, "projects_dir = %s\n\n", String(g.ProjectsDir))
	b.WriteString("# GitHub user or organisation new repos belong to, and whether they're private\n")
	fmt.Fprintf(&b, "git_owner = %s\n", String(g.GitOwner))
	fmt.Fprintf(&b, "git_private = %s\n\n", g.GitPrivate)
	b.WriteString("# The site type 'gd-sync new' suggests: \"wordpress\" or \"astro\"\n")
	fmt.Fprintf(&b, "default_type = %s\n", String(g.DefaultType))
	if err := os.MkdirAll(filepath.Dir(g.Path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(g.Path, []byte(b.String()), 0o644)
}
