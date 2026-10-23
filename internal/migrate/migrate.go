// Package migrate is a WordPress site's database migrations, in the style of
// Alembic: each one is a PHP file in _migrations/ that names its own revision
// and the revision it follows, so the files form one chain from a root to a
// head. The live database records the revision it is at in an option, so a
// copy pulled with 'gd-sync db' knows exactly which migrations it has had.
//
// A migration file:
//
//	<?php
//	/**
//	 * Enable WooCommerce HPOS
//	 *
//	 * Revision ID: 3f9a1c2b7d40
//	 * Revises: <none>
//	 * Create Date: 2026-10-23
//	 */
//
//	$revision = '3f9a1c2b7d40';
//	$down_revision = null;
//
//	$upgrade = function () {
//	};
//
//	$downgrade = function () {
//	};
//
// gd-sync reads $revision and $down_revision itself, so both must be plain
// literals. The file lives at <revision>_<description>.php.
package migrate

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Dir is where a site's migrations live, at the top of its repo.
const Dir = "_migrations"

// Option is the WordPress option that holds the revision a database is at.
// No option means no migration has run: the base.
const Option = "gd_sync_db_revision"

type Migration struct {
	Revision     string
	DownRevision string // "" for the root
	Title        string
	File         string // the file's name in Dir
	Source       []byte
}

// Chain is every migration, root first. A database at position n has had
// the first n.
type Chain []Migration

var (
	revRE    = regexp.MustCompile(`^[0-9a-f]{12}$`)
	revVar   = regexp.MustCompile(`(?m)^\s*\$revision\s*=\s*(\S.*?)\s*;`)
	downVar  = regexp.MustCompile(`(?m)^\s*\$down_revision\s*=\s*(\S.*?)\s*;`)
	docStart = regexp.MustCompile(`(?s)^\s*<\?php\s*/\*\*?(.*?)\*/`)
)

// Parse reads one migration file.
func Parse(file string, src []byte) (Migration, error) {
	m := Migration{File: file, Source: src}
	rev, err := literal(revVar, src, file, "$revision", false)
	if err != nil {
		return m, err
	}
	if !revRE.MatchString(rev) {
		return m, fmt.Errorf("%s: $revision '%s' must be 12 lowercase hex characters, like the ones 'gd-sync migrate new' makes", file, rev)
	}
	down, err := literal(downVar, src, file, "$down_revision", true)
	if err != nil {
		return m, err
	}
	if down != "" && !revRE.MatchString(down) {
		return m, fmt.Errorf("%s: $down_revision '%s' must be null or a 12-character revision", file, down)
	}
	if down == rev {
		return m, fmt.Errorf("%s: revision %s can't follow itself", file, rev)
	}
	if !strings.HasPrefix(file, rev+"_") || !strings.HasSuffix(file, ".php") || len(file) == len(rev+"_.php") {
		return m, fmt.Errorf("%s: a migration with revision %s must be named %s_<description>.php", file, rev, rev)
	}
	m.Revision, m.DownRevision = rev, down
	m.Title = title(file, src)
	return m, nil
}

// literal reads `$name = '...';`. With nullable, `null` reads as "".
func literal(re *regexp.Regexp, src []byte, file, name string, nullable bool) (string, error) {
	all := re.FindAllSubmatch(src, -1)
	if len(all) != 1 {
		if len(all) == 0 {
			return "", fmt.Errorf("%s: no %s = '...'; line", file, name)
		}
		return "", fmt.Errorf("%s: %s is set more than once", file, name)
	}
	v := string(all[0][1])
	if nullable && strings.EqualFold(v, "null") {
		return "", nil
	}
	if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1], nil
	}
	return "", fmt.Errorf("%s: %s must be a plain string literal, not %s", file, name, v)
}

// title is the first line of the file's doc comment, or else its name.
func title(file string, src []byte) string {
	if m := docStart.FindSubmatch(src); m != nil {
		for _, l := range strings.Split(string(m[1]), "\n") {
			if l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "*")); l != "" {
				return l
			}
		}
	}
	name := strings.TrimSuffix(file, ".php")
	if i := strings.IndexByte(name, '_'); i >= 0 {
		name = name[i+1:]
	}
	return strings.ReplaceAll(name, "_", " ")
}

// Load parses the files, keyed by name, and puts them in order. Like
// Alembic with a single head, the history must be one straight line: one
// root, every migration followed by at most one other, nothing left over.
func Load(files map[string][]byte) (Chain, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		if strings.HasSuffix(name, ".php") && !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, ".") {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	byRev := map[string]Migration{}
	var roots []string
	for _, name := range names {
		m, err := Parse(name, files[name])
		if err != nil {
			return nil, err
		}
		if other, ok := byRev[m.Revision]; ok {
			return nil, fmt.Errorf("%s and %s both have revision %s", other.File, m.File, m.Revision)
		}
		byRev[m.Revision] = m
		if m.DownRevision == "" {
			roots = append(roots, m.File)
		}
	}
	if len(byRev) == 0 {
		return nil, nil
	}
	if len(roots) != 1 {
		return nil, fmt.Errorf("The migrations must have exactly one root ($down_revision = null), found %d: %s", len(roots), strings.Join(roots, ", "))
	}

	next := map[string][]string{}
	for _, m := range byRev {
		if m.DownRevision == "" {
			continue
		}
		if _, ok := byRev[m.DownRevision]; !ok {
			return nil, fmt.Errorf("%s follows %s, and no migration has that revision", m.File, m.DownRevision)
		}
		next[m.DownRevision] = append(next[m.DownRevision], m.File)
	}
	for down, files := range next {
		if len(files) > 1 {
			sort.Strings(files)
			return nil, fmt.Errorf(`%s all follow %s — the migrations branched, probably in a merge.
  Point one at the other: set its $down_revision to the other's $revision, so they run one after another.`, strings.Join(files, " and "), down)
		}
	}

	var chain Chain
	for rev := revOf(roots[0], byRev); ; {
		chain = append(chain, byRev[rev])
		n := next[rev]
		if len(n) == 0 {
			break
		}
		rev = revOf(n[0], byRev)
	}
	if len(chain) != len(byRev) {
		var lost []string
		for _, m := range byRev {
			if chain.index(m.Revision) < 0 {
				lost = append(lost, m.File)
			}
		}
		sort.Strings(lost)
		return nil, fmt.Errorf("These migrations aren't connected to the rest (they follow each other in a loop): %s", strings.Join(lost, ", "))
	}
	return chain, nil
}

func revOf(file string, byRev map[string]Migration) string {
	for rev, m := range byRev {
		if m.File == file {
			return rev
		}
	}
	return ""
}

// Head is the newest revision, or "" when there are no migrations.
func (c Chain) Head() string {
	if len(c) == 0 {
		return ""
	}
	return c[len(c)-1].Revision
}

func (c Chain) index(rev string) int {
	for i, m := range c {
		if m.Revision == rev {
			return i
		}
	}
	return -1
}

// Position is how many migrations a database at current has had.
func (c Chain) Position(current string) (int, error) {
	if current == "" {
		return 0, nil
	}
	if i := c.index(current); i >= 0 {
		return i + 1, nil
	}
	return 0, fmt.Errorf(`The database is at revision %s, and these migrations don't have it (head: %s).
  It was migrated from another branch, or a migration was deleted or renamed after it ran.
  Bring that file back, or restore an earlier database.`, current, Label(c.Head()))
}

// Target turns what you type into a position: head, base, a revision or a
// unique start of one, or a relative step from the current position such as
// -1 or +2.
func (c Chain) Target(current int, target string) (int, error) {
	switch {
	case target == "" || target == "head":
		return len(c), nil
	case target == "base":
		return 0, nil
	case strings.HasPrefix(target, "-") || strings.HasPrefix(target, "+"):
		n, err := strconv.Atoi(target)
		if err != nil {
			return 0, fmt.Errorf("'%s' is not a step like -1 or +2", target)
		}
		p := current + n
		if p < 0 || p > len(c) {
			return 0, fmt.Errorf("%s from revision %s goes past the end of the migrations", target, Label(c.at(current)))
		}
		return p, nil
	}
	found := -1
	for i, m := range c {
		if strings.HasPrefix(m.Revision, target) {
			if found >= 0 {
				return 0, fmt.Errorf("'%s' could be %s or %s — type more of it", target, c[found].Revision, m.Revision)
			}
			found = i
		}
	}
	if found < 0 {
		return 0, fmt.Errorf("No migration has revision '%s'", target)
	}
	return found + 1, nil
}

// at is the revision a database at position p is at.
func (c Chain) at(p int) string {
	if p == 0 {
		return ""
	}
	return c[p-1].Revision
}

// Step runs one migration, one way. The database must be at From, and ends
// up at To.
type Step struct {
	Migration
	Up       bool
	From, To string
}

// Plan is the steps from position from to position to: upgrades in order,
// or downgrades newest first.
func (c Chain) Plan(from, to int) []Step {
	var steps []Step
	for p := from; p < to; p++ {
		steps = append(steps, Step{Migration: c[p], Up: true, From: c.at(p), To: c.at(p + 1)})
	}
	for p := from; p > to; p-- {
		steps = append(steps, Step{Migration: c[p-1], From: c.at(p), To: c.at(p - 1)})
	}
	return steps
}

// Label shows a revision, with the empty one as "base".
func Label(rev string) string {
	if rev == "" {
		return "base"
	}
	return rev
}

func (s Step) String() string {
	arrow := "↑"
	if !s.Up {
		arrow = "↓"
	}
	return fmt.Sprintf("%s %s  %s", arrow, s.Revision, s.Title)
}

// NewRevision makes a random revision, the way Alembic does.
func NewRevision() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

// FileName is the file a new migration goes in.
func FileName(rev, message string) string {
	slug := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(message), "_"), "_")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "_")
	}
	if slug == "" {
		slug = "migration"
	}
	return rev + "_" + slug + ".php"
}

// Template is a new migration that follows down ("" for the first one).
func Template(rev, down, message, date string) string {
	downLit, revises := "null", "<none>"
	if down != "" {
		downLit, revises = "'"+down+"'", down
	}
	message = strings.ReplaceAll(strings.TrimSpace(message), "*/", "* /")
	return `<?php
/**
 * ` + message + `
 *
 * Revision ID: ` + rev + `
 * Revises: ` + revises + `
 * Create Date: ` + date + `
 *
 * Runs inside WordPress with plugins and themes loaded, through WP-CLI:
 * $wpdb, the options API and the site's plugin functions are all there.
 * Throw to fail. gd_sync_query() runs a query and throws when it fails;
 * $wpdb on its own only records the error. Each run is wrapped in a
 * transaction, but table changes (CREATE, ALTER) commit straight away, so
 * write it to be safe to run twice.
 */

$revision = '` + rev + `';
$down_revision = ` + downLit + `;

$upgrade = function () {
    global $wpdb;
};

$downgrade = function () {
    global $wpdb;
};
`
}

// Script is the PHP that 'wp eval-file -' runs for one step. It carries the
// migration's source with it, so nothing is written to the server, and it
// moves the recorded revision from From to To in the same transaction as
// the migration's own changes.
func Script(s Step) string {
	fn := "downgrade"
	if s.Up {
		fn = "upgrade"
	}
	q := func(v string) string { return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'" }
	return `<?php
// gd-sync: one migration step, run by 'wp eval-file -'.
global $wpdb;
$gd_sync_option = ` + q(Option) + `;
$gd_sync_file = ` + q(path.Join(Dir, s.File)) + `;
$gd_sync_fn = ` + q(fn) + `;
$gd_sync_from = ` + q(s.From) + `;
$gd_sync_to = ` + q(s.To) + `;
$gd_sync_src = base64_decode(` + q(base64.StdEncoding.EncodeToString(s.Source)) + `);

if (!function_exists('gd_sync_query')) {
    function gd_sync_query($sql, ...$args) {
        global $wpdb;
        $result = $wpdb->query($args ? $wpdb->prepare($sql, ...$args) : $sql);
        if ($result === false) {
            throw new RuntimeException($wpdb->last_error !== '' ? $wpdb->last_error : 'Query failed: ' . $sql);
        }
        return $result;
    }
}

// The advisory lock covers concurrent local runners too. Use the actual DB
// and options table so aliases of the same site share the same lock.
$gd_sync_lock = 'gd-sync:' . substr(hash('sha256', DB_NAME . ':' . $wpdb->options), 0, 48);
if ((string) $wpdb->get_var($wpdb->prepare('SELECT GET_LOCK(%s, 0)', $gd_sync_lock)) !== '1') {
    WP_CLI::error('Another migration is running, or the database cannot acquire a migration lock.');
}

$gd_sync_clear_cache = static function () use ($gd_sync_option) {
    wp_cache_delete($gd_sync_option, 'options');
    wp_cache_delete('alloptions', 'options');
    wp_cache_delete('notoptions', 'options');
};
$gd_sync_read_revision = static function () use ($wpdb, $gd_sync_option) {
    $value = $wpdb->get_var($wpdb->prepare("SELECT option_value FROM {$wpdb->options} WHERE option_name = %s", $gd_sync_option));
    if ($wpdb->last_error !== '') {
        throw new RuntimeException($wpdb->last_error);
    }
    return (string) $value;
};

try {
    gd_sync_query('START TRANSACTION');
    if ($gd_sync_read_revision() !== $gd_sync_from) {
        throw new RuntimeException('Database revision changed since planning; inspect the current revision before retrying.');
    }
    $gd_sync_vars = (static function ($__gd_sync_src) {
        eval('?>' . $__gd_sync_src);
        return get_defined_vars();
    })($gd_sync_src);
    $gd_sync_run = $gd_sync_vars[$gd_sync_fn] ?? null;
    if (!is_callable($gd_sync_run)) {
        throw new RuntimeException(sprintf('%s has no $%s = function () { ... };', $gd_sync_file, $gd_sync_fn));
    }
    $wpdb->last_error = '';
    $gd_sync_run();
    if ($wpdb->last_error !== '') {
        throw new RuntimeException($wpdb->last_error);
    }
    // Store metadata directly so a stale persistent option cache or an
    // option filter cannot advance the revision without saving it to SQL.
    if ($gd_sync_to === '') {
        gd_sync_query("DELETE FROM {$wpdb->options} WHERE option_name = %s", $gd_sync_option);
    } else {
        gd_sync_query("INSERT INTO {$wpdb->options} (option_name, option_value, autoload) VALUES (%s, %s, 'no') ON DUPLICATE KEY UPDATE option_value = VALUES(option_value), autoload = 'no'", $gd_sync_option, $gd_sync_to);
    }
    if ($gd_sync_read_revision() !== $gd_sync_to) {
        throw new RuntimeException('Could not record revision ' . $gd_sync_to);
    }
    gd_sync_query('COMMIT');
    $gd_sync_clear_cache();
} catch (Throwable $e) {
    $wpdb->query('ROLLBACK');
    $gd_sync_clear_cache();
    WP_CLI::error(sprintf('%s, %s: %s (line %d)', $gd_sync_file, $gd_sync_fn, $e->getMessage(), $e->getLine()));
} finally {
    $wpdb->get_var($wpdb->prepare('SELECT RELEASE_LOCK(%s)', $gd_sync_lock));
}
`
}
