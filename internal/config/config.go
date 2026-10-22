// Package config holds the machine-wide settings and the registry of sites.
//
// A site's own settings live in its repo (see package site). What lives here
// is only what makes "gd-sync new" stop asking the same questions, plus a list
// of folders so that "gd-sync list" and --all have something to walk.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Global is the machine-wide settings file.
type Global struct {
	ProjectsDir string // where new site folders are created
	GitOwner    string // GitHub user or org that new repos belong to
	GitPrivate  string // "true" or "false" for new repos
	DefaultType string // "wordpress" or "astro"
	Path        string
}

// Keys lists the settings in the order they are shown and saved.
var Keys = []string{"PROJECTS_DIR", "GIT_OWNER", "GIT_PRIVATE", "DEFAULT_TYPE"}

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
		g.Path = filepath.Join(Home(), "config")
	}
	kv, err := ReadKeyValues(g.Path)
	if err != nil {
		return nil, err
	}
	for k, v := range kv {
		g.Set(k, v)
	}
	return g, nil
}

// Get returns a setting by key, or ok=false for an unknown key.
func (g *Global) Get(key string) (string, bool) {
	switch key {
	case "PROJECTS_DIR":
		return g.ProjectsDir, true
	case "GIT_OWNER":
		return g.GitOwner, true
	case "GIT_PRIVATE":
		return g.GitPrivate, true
	case "DEFAULT_TYPE":
		return g.DefaultType, true
	}
	return "", false
}

// Set changes a setting by key, and reports whether the key exists.
func (g *Global) Set(key, value string) bool {
	switch key {
	case "PROJECTS_DIR":
		g.ProjectsDir = ExpandHome(value)
	case "GIT_OWNER":
		g.GitOwner = value
	case "GIT_PRIVATE":
		g.GitPrivate = value
	case "DEFAULT_TYPE":
		g.DefaultType = value
	default:
		return false
	}
	return true
}

func (g *Global) Save() error {
	var b strings.Builder
	b.WriteString("# gd-sync — machine-wide settings. Per-site settings live in each repo's .gd-sync.\n")
	for _, k := range Keys {
		v, _ := g.Get(k)
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	if err := os.MkdirAll(filepath.Dir(g.Path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(g.Path, []byte(b.String()), 0o644)
}

// ReadKeyValues parses a KEY=value file: blank lines and # comments are
// skipped, whitespace around keys and values is trimmed. A missing file is
// an empty map, not an error.
func ReadKeyValues(path string) (map[string]string, error) {
	kv := map[string]string{}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return kv, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		kv[strings.Join(strings.Fields(k), "")] = strings.TrimSpace(v)
	}
	return kv, sc.Err()
}
