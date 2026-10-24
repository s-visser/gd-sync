// Package transfer moves files between a site's repo and its server without
// rsync. The server lists its files (path, size, mtime) with a small Perl
// script — every cPanel server has Perl — and file contents travel as a single
// gzipped tar stream over SSH.
//
// What the server held at the last sync is remembered per machine, in
// .git/gd-sync/server, as "path size mtime blob" lines. A file is only fetched
// when its size or mtime moved since then, or main no longer holds that blob.
// Fetched files are hashed and compared with main, so a file that was touched
// but not changed never shows up as a change.
package transfer

import (
	"regexp"
	"strings"
)

// DefaultExcludes are never synced or deployed, whatever the site says.
var DefaultExcludes = []string{
	"/AGENTS.md",
	".git", ".github", ".devcontainer", ".gd-sync.toml", ".gd-sync.local.toml", ".gitignore", ".gitattributes",
	".idea", ".vscode", ".run", ".DS_Store", "node_modules", "_db", "_migrations", "*.log", ".env",
	"wp-content/cache", "wp-content/uploads",
	"*:Zone.Identifier", // Windows download metadata, copied over as a file; not a valid name on Windows
}

// MirrorExcludes are never copied into the local mirror of the whole site:
// the repo's own files at the top, git folders, caches and server logs.
// Uploads are mirrored, so the local site has its images.
var MirrorExcludes = []string{
	"/.github", "/.devcontainer", "/.gd-sync.toml", "/.gd-sync.local.toml", "/.gitignore", "/.gitattributes",
	"/.idea", "/.vscode", "/.run", "/_db", "/_migrations", "/README.md", "/GD-SYNC.md", "/AGENTS.md",
	".git", ".DS_Store", "*.log", "error_log", "wp-content/cache", "*:Zone.Identifier",
}

// Matcher decides which paths belong to a transfer: those under one of the
// roots and not excluded. Excludes follow rsync: a pattern matches a whole
// name or a trailing run of path components, a leading / anchors it to the
// top, and * and ? never cross a slash. The Perl lister on the server
// applies the same rules.
type Matcher struct {
	roots    []string
	excludes []string
	res      []*regexp.Regexp
}

func NewMatcher(roots, excludes []string) Matcher {
	m := Matcher{roots: roots, excludes: excludes}
	for _, e := range excludes {
		start := `(?:^|/)`
		if strings.HasPrefix(e, "/") {
			e, start = e[1:], `^`
		}
		r := regexp.QuoteMeta(e)
		r = strings.ReplaceAll(r, `\*`, `[^/]*`)
		r = strings.ReplaceAll(r, `\?`, `[^/]`)
		m.res = append(m.res, regexp.MustCompile(start+r+`$`))
	}
	return m
}

// Keep reports whether a slash-separated path is part of the transfer.
func (m Matcher) Keep(p string) bool { return m.under(p) && !m.Excluded(p) }

func (m Matcher) under(p string) bool {
	for _, r := range m.roots {
		if r == "." || p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}

// Excluded reports whether the path, or any folder above it, is excluded.
func (m Matcher) Excluded(p string) bool {
	parts := strings.Split(p, "/")
	for i := 1; i <= len(parts); i++ {
		if m.skip(strings.Join(parts[:i], "/")) {
			return true
		}
	}
	return false
}

func (m Matcher) skip(p string) bool {
	for _, r := range m.res {
		if r.MatchString(p) {
			return true
		}
	}
	return false
}

// listerArgs are the Perl lister's arguments: the number of roots, the
// roots, then the excludes.
func (m Matcher) listerArgs() []string {
	args := []string{itoa(len(m.roots))}
	args = append(args, m.roots...)
	return append(args, m.excludes...)
}

// lister prints "path<TAB>size<TAB>mtime" for every file under the roots it
// is given, skipping excluded ones and symlinks. Names with a tab or newline
// in them are reported on stderr and skipped.
const lister = `use strict; use warnings; use File::Find;
my $n = shift @ARGV; my @roots = splice @ARGV, 0, $n;
my @ex = map { my $top = s{^/}{}; my $r = quotemeta; $r =~ s/\\\*/[^\/]*/g; $r =~ s/\\\?/[^\/]/g; $top ? qr{^$r$} : qr{(?:^|/)$r$} } @ARGV;
sub skip { my $p = shift; for my $r (@ex) { return 1 if $p =~ $r } 0 }
sub excluded { my @c = split m{/}, shift; for my $i (1 .. @c) { return 1 if skip(join '/', @c[0 .. $i - 1]) } 0 }
sub emit {
  (my $p = shift) =~ s{^\./}{};
  if ($p =~ /[\t\n]/) { print STDERR "skipped, tab or newline in name: $p\n"; return }
  my @s = lstat $p; print "$p\t$s[7]\t$s[9]\n";
}
for my $t (@roots) {
  next if !-e $t || excluded($t);
  if (-f $t && !-l $t) { emit($t); next }
  next unless -d $t;
  find({ no_chdir => 1, wanted => sub {
    (my $p = $File::Find::name) =~ s{^\./}{};
    if ($p ne '.' && skip($p)) { $File::Find::prune = 1; return }
    emit($p) if -f $_ && !-l $_;
  } }, $t);
}
`
