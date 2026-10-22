package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The registry is only a list of site folders, one per line. Delete it and
// nothing breaks; "gd-sync adopt" adds a folder back.

func registryPath() string {
	if p := os.Getenv("GD_SYNC_REGISTRY"); p != "" {
		return p
	}
	return filepath.Join(Home(), "repos")
}

func readRegistry() []string {
	b, err := os.ReadFile(registryPath())
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func writeRegistry(paths []string) error {
	p := registryPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	s := strings.Join(paths, "\n")
	if s != "" {
		s += "\n"
	}
	return os.WriteFile(p, []byte(s), 0o644)
}

// RegistryAdd records a site folder, once.
func RegistryAdd(repo string) error {
	paths := readRegistry()
	if slices.Contains(paths, repo) {
		return nil
	}
	return writeRegistry(append(paths, repo))
}

// RegistryList returns the registered folders that still hold a site,
// dropping the ones that have gone away.
func RegistryList() []string {
	all := readRegistry()
	var kept []string
	for _, p := range all {
		if _, err := os.Stat(filepath.Join(p, ".gd-sync")); err == nil {
			kept = append(kept, p)
		}
	}
	if len(kept) != len(all) {
		_ = writeRegistry(kept)
	}
	return kept
}
