// Package scaffold writes the files gd-sync puts in every site: .gitignore,
// .gitattributes, the settings files, GD-SYNC.md, the devcontainer, the GitHub
// workflows and the JetBrains run configurations.
//
// The templates are built into the binary. Set GD_SYNC_TEMPLATES to a folder
// to use templates from disk instead, while working on them.
package scaffold

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/s-visser/gd-sync/internal/site"
	"github.com/s-visser/gd-sync/internal/ui"
)

//go:embed all:templates
var embedded embed.FS

func templates() fs.FS {
	if dir := os.Getenv("GD_SYNC_TEMPLATES"); dir != "" {
		return os.DirFS(dir)
	}
	sub, _ := fs.Sub(embedded, "templates")
	return sub
}

// All writes every scaffold file. The site's README is written only once.
func All(s *site.Site) error {
	for _, step := range []func(*site.Site) error{
		Gitignore, Gitattributes, func(s *site.Site) error { return s.Write() },
		Docs, Devcontainer, Workflows, RunConfigs,
	} {
		if err := step(s); err != nil {
			return err
		}
	}
	return nil
}

func write(s *site.Site, name, content string) error {
	p := filepath.Join(s.Repo, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func Gitignore(s *site.Site) error {
	if s.Type == "astro" {
		return write(s, ".gitignore", gitignoreAstro)
	}
	return write(s, ".gitignore", gitignoreWordPress)
}

// Gitattributes stops git from touching line endings: the server's bytes are
// the truth, and a CRLF checkout on Windows must never be deployed.
func Gitattributes(s *site.Site) error {
	return write(s, ".gitattributes", "# The server's bytes are the truth — git must not rewrite line endings.\n* -text\n")
}

// Docs writes GD-SYNC.md, the tool's own cheat sheet, and the site's README
// if it doesn't have one yet.
func Docs(s *site.Site) error {
	if err := write(s, "GD-SYNC.md", cheatSheet); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(s.Repo, "README.md")); err == nil {
		return nil
	}
	domain := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(s.LiveURL, "http://"), "https://"), "/")
	kind := map[string]string{"wordpress": "WordPress", "astro": "Astro (static)"}[s.Type]
	if kind == "" {
		kind = s.Type
	}
	return write(s, "README.md", fmt.Sprintf(readme, domain, s.LiveURL, s.LocalURL, kind))
}

// Devcontainer copies the template for the site's type and fills in the
// site name and, for WordPress, a bind mount per tracked path.
func Devcontainer(s *site.Site) error {
	tpl := templates()
	root := s.Type + "/.devcontainer"
	if _, err := fs.Stat(tpl, root); err != nil {
		ui.Warn("No devcontainer template for type '%s'", s.Type)
		return nil
	}
	err := fs.WalkDir(tpl, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(tpl, p)
		if err != nil {
			return err
		}
		content := string(b)
		switch path.Base(p) {
		case "devcontainer.json":
			content = strings.ReplaceAll(content, "__SITE__", s.Name)
		case "docker-compose.yml":
			var mounts strings.Builder
			for _, t := range s.Tracked {
				fmt.Fprintf(&mounts, "      - ../%s:/var/www/html/%s:cached\n", t, t)
			}
			content = replaceLine(content, "# __TRACKED_MOUNTS__", mounts.String())
		}
		return write(s, ".devcontainer/"+strings.TrimPrefix(p, root+"/"), content)
	})
	if err != nil {
		return err
	}
	return write(s, ".devcontainer/.env", fmt.Sprintf("COMPOSE_PROJECT_NAME=%s\nLIVE_URL=%s\nLOCAL_URL=%s\n", s.Name, s.LiveURL, s.LocalURL))
}

// replaceLine swaps every line containing marker for repl.
func replaceLine(content, marker, repl string) string {
	lines := strings.SplitAfter(content, "\n")
	for i, l := range lines {
		if strings.Contains(l, marker) {
			lines[i] = repl
		}
	}
	return strings.Join(lines, "")
}

func Workflows(s *site.Site) error {
	for _, name := range []string{"deploy.yml", "sync-from-server.yml", "verify-main.yml"} {
		b, err := fs.ReadFile(templates(), "github/"+name)
		if err != nil {
			return err
		}
		if err := write(s, ".github/workflows/"+name, string(b)); err != nil {
			return err
		}
	}
	return nil
}

// RunConfigs adds JetBrains run configurations for the everyday commands.
// No site name in any of them — the working directory decides.
func RunConfigs(s *site.Site) error {
	for _, c := range []struct{ name, args string }{
		{"1 Sync main from server", "sync"},
		{"2 Pull database", "db"},
		{"3 Server diff", "status"},
		{"4 Merge and go live", "merge"},
		{"5 Deploy by hand", "deploy"},
	} {
		file := ".run/" + strings.ReplaceAll(c.name, " ", "_") + ".run.xml"
		if err := write(s, file, fmt.Sprintf(runConfig, c.name, c.args)); err != nil {
			return err
		}
	}
	return nil
}
