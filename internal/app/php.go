package app

import (
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/s-visser/gd-sync/internal/remote"
	"github.com/s-visser/gd-sync/internal/scaffold"
	"github.com/s-visser/gd-sync/internal/site"
	"github.com/s-visser/gd-sync/internal/ui"
)

// phpSupport is when each PHP version stopped getting security fixes, from
// php.net. A version gd-sync doesn't know yet counts as supported.
var phpSupport = map[string]string{
	"5.6": "2018-12-31", "7.0": "2019-01-10", "7.1": "2019-12-01", "7.2": "2020-11-30",
	"7.3": "2021-12-06", "7.4": "2022-11-28", "8.0": "2023-11-26", "8.1": "2025-12-31",
	"8.2": "2026-12-31", "8.3": "2027-12-31", "8.4": "2028-12-31", "8.5": "2029-12-31",
}

// oldestImage is the oldest PHP the official wordpress image was built for.
const oldestImage = "5.6"

// defaultPHP is used when the live version can't be found out.
const defaultPHP = "8.3"

// cpanelUserdata is where cPanel keeps each domain's settings. Tests point
// it elsewhere.
var cpanelUserdata = "/var/cpanel/userdata"

// phpProbe prints "source<TAB>version<TAB>full version" for the first place
// on the server that says which PHP serves the web root ($1): a handler in
// .htaccess (nearest folder first, as Apache reads them), the domain's
// cPanel setting, the CloudLinux PHP Selector, and last the command-line
// PHP. The full version, e.g. 8.5.12, comes from asking that PHP itself,
// and is left empty when its binary isn't where cPanel and CloudLinux put it.
const phpProbe = `root=$1; userdata=$2/$(id -un)
found() {
  n=$(printf %s "$2" | tr -d .)
  full=
  for php in /opt/cpanel/ea-php$n/root/usr/bin/php /opt/alt/php$n/usr/bin/php; do
    [ -x "$php" ] && full=$("$php" -n -r 'echo PHP_VERSION;' 2>/dev/null) && break
  done
  printf '%s\t%s\t%s\n' "$1" "$2" "$full"
  exit
}
d=$root
while :; do
  if [ -f "$d/.htaccess" ]; then
    v=$(grep -v '^[[:space:]]*#' "$d/.htaccess" | grep -oiE 'x-httpd-(ea-|alt-)?php[0-9]+|lsphp[0-9]+' | head -1 | grep -oE '[0-9]+$')
    [ -n "$v" ] && found "handler in $d/.htaccess" "$v"
  fi
  [ "$d" = "$HOME" ] || [ "$d" = / ] || [ -z "$d" ] && break
  d=$(dirname "$d")
done
for f in "$userdata"/*; do
  case "$f" in *.cache|*_SSL|*/main|*/cache) continue ;; esac
  grep -qx "documentroot: $root" "$f" 2>/dev/null || continue
  v=$(sed -n 's/^phpversion:[[:space:]]*[a-z-]*php\([0-9][0-9]*\).*/\1/p' "$f" | head -1)
  [ -n "$v" ] && found 'cPanel setting for the domain' "$v"
done
v=$(selectorctl --user-current 2>/dev/null | awk 'NR==1{print $1}')
case "$v" in [0-9]*.[0-9]*) found 'CloudLinux PHP Selector' "$v" ;; esac
v=$(php -r 'echo PHP_VERSION;' 2>/dev/null)
[ -n "$v" ] && printf 'command-line PHP (may differ from the website)\t%s\t%s\n' "${v%.*}" "$v"
exit 0
`

var poweredBy = regexp.MustCompile(`PHP/(\d+\.\d+)((?:\.\d+)?)`)

// livePHP finds the PHP version the live site runs, e.g. "8.5", and says
// where from. full is the exact release, e.g. "8.5.12", or the same as
// version when that can't be found out.
func (c *ctx) livePHP(conn *remote.Conn) (version, full, source string) {
	var server phpFinding
	out, err := conn.Output("sh -s -- "+remote.QuoteAll([]string{c.s.RemoteRoot, cpanelUserdata}), strings.NewReader(phpProbe))
	if err == nil {
		if f := strings.Split(strings.TrimRight(string(out), "\r\n"), "\t"); len(f) >= 2 {
			server.source, server.version = f[0], phpDotted(f[1])
			if len(f) > 2 && strings.HasPrefix(f[2], server.version+".") {
				server.full = f[2]
			}
		}
	}
	web := phpFinding{source: "the live site's X-Powered-By header"}
	web.version, web.full = headerPHP(c.s.LiveURL)
	got, disagree := pickPHP(server, web)
	if disagree {
		ui.Warn("%s answers with PHP %s, but the server's %s says PHP %s — going by the server.", c.s.LiveURL, web.version, server.source, server.version)
		if server.source == selectorSource {
			ui.Warn("The selector is the server-wide version, so this domain looks like it has its own PHP set,")
			ui.Warn("which is probably a mistake on the server. If %s is what it should run, change the", server.version)
			ui.Warn("domain's version: cPanel -> MultiPHP Manager, or an AddHandler line in its .htaccess.")
		} else {
			ui.Warn("Something in front of it — a cache, a CDN, another handler — may be answering instead. Check it.")
		}
	}
	if got.full == "" {
		got.full = got.version
	}
	return got.version, got.full, got.source
}

// selectorSource is what phpProbe calls the CloudLinux PHP Selector.
const selectorSource = "CloudLinux PHP Selector"

// phpFinding is one answer to "which PHP does the site run".
type phpFinding struct{ version, full, source string }

// pickPHP decides between what the server's settings say and what the site
// answers with. The server's settings win: on GoDaddy the PHP Selector's
// server-wide version is the one that runs. Only the command-line PHP, the
// probe's last resort, is a weaker guess than the site's own header.
// disagree reports that both answered, with different versions.
func pickPHP(server, site phpFinding) (got phpFinding, disagree bool) {
	switch {
	case server.version == "":
		return site, false
	case site.version == "":
		return server, false
	case site.version == server.version:
		if server.full == "" {
			server.full = site.full
		}
		return server, false
	case strings.HasPrefix(server.source, "command-line PHP"):
		return site, false
	}
	return server, true
}

// headerPHP reads the PHP version from a site's X-Powered-By header: "8.5",
// and the exact release when the header has it, "8.5.12". Empty when the
// site doesn't say.
func headerPHP(url string) (version, full string) {
	if url == "" {
		return "", ""
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", ""
	}
	resp.Body.Close()
	m := poweredBy.FindStringSubmatch(resp.Header.Get("X-Powered-By"))
	if m == nil {
		return "", ""
	}
	if m[2] == "" {
		return m[1], ""
	}
	return m[1], m[1] + m[2]
}

// phpDotted turns cPanel's "81" or "56" into "8.1" or "5.6"; "8.5" stays.
func phpDotted(v string) string {
	if strings.Contains(v, ".") || len(v) < 2 {
		return v
	}
	return v[:1] + "." + v[1:]
}

// phpForImage is the version the devcontainer can actually build: the live
// one, or the oldest with an image when live is older still. Old versions
// are used as they are, with a warning — local should match live, not
// quietly differ from it.
func phpForImage(v string) string {
	if v == "" {
		ui.Warn("Could not find out which PHP the live site runs — using PHP %s locally", defaultPHP)
		ui.Info("Set php_version in %s by hand, then: gd-sync scaffold && gd-sync up", site.File)
		return defaultPHP
	}
	if phpOlder(v, oldestImage) {
		ui.Warn("The live site runs PHP %s. No container image exists for it, so locally it's PHP %s — the closest there is.", v, oldestImage)
		ui.Warn("Code that works locally may still break live. Upgrade PHP on the server when you can.")
		return oldestImage
	}
	warnUnsupportedPHP(v)
	return v
}

// warnUnsupportedPHP says so when a PHP version no longer gets security fixes.
func warnUnsupportedPHP(v string) {
	end, ok := phpSupport[v]
	if !ok || end >= time.Now().Format("2006-01-02") {
		return
	}
	ui.Warn("PHP %s has had no security fixes since %s. The local site runs it too, to match live,", v, end)
	ui.Warn("but the server should be upgraded: GoDaddy dashboard -> cPanel -> Select PHP Version.")
}

// phpOlder compares "major.minor" versions.
func phpOlder(a, b string) bool {
	var am, an, bm, bn int
	fmt.Sscanf(a, "%d.%d", &am, &an)
	fmt.Sscanf(b, "%d.%d", &bm, &bn)
	return am < bm || am == bm && an < bn
}

// phpIniProbe prints "setting<TAB>value<TAB>where from" for each of the
// settings in $3 that the web root ($1) gets: from the web root's .user.ini,
// else from the php.ini of the site's PHP version ($2). get_cfg_var reads
// the ini files rather than what the command line runs with, which always
// has max_execution_time 0. Only the PHP builds cPanel and CloudLinux put
// in /opt share their php.ini with the website; a plain php can have one
// of its own for the command line, so with that only .user.ini counts.
// Settings PHP is left to default on are not printed.
const phpIniProbe = `root=$1; n=$(printf %s "$2" | tr -d .)
php=php; base=0
for p in /opt/alt/php$n/usr/bin/php /opt/cpanel/ea-php$n/root/usr/bin/php; do
  [ -x "$p" ] && php=$p && base=1 && break
done
"$php" -r '
$root = $argv[1]; $base = $argv[3] === "1";
$name = ini_get("user_ini.filename");
$user = $name === false || $name === "" ? array() : @parse_ini_file($root . "/" . $name, false, INI_SCANNER_RAW);
if (!is_array($user)) $user = array();
foreach (explode(",", $argv[2]) as $k) {
  if (array_key_exists($k, $user)) { echo $k, "\t", $user[$k], "\t", $name, "\n"; continue; }
  $v = $base ? get_cfg_var($k) : false;
  if ($v !== false && $v !== "") echo $k, "\t", $v, "\t", php_ini_loaded_file(), "\n";
}' -- "$root" "$3" "$base" 2>/dev/null
exit 0
`

// livePHPIni reads the limits the live site's PHP runs with, and where each
// comes from. Empty when the server can't say.
func (c *ctx) livePHPIni(conn *remote.Conn, version string) (ini, from map[string]string) {
	ini, from = map[string]string{}, map[string]string{}
	out, err := conn.Output("sh -s -- "+remote.QuoteAll([]string{c.s.RemoteRoot, version, strings.Join(scaffold.PHPSettings, ",")}), strings.NewReader(phpIniProbe))
	if err != nil {
		return ini, from
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) < 3 || !slices.Contains(scaffold.PHPSettings, f[0]) {
			continue
		}
		if v := strings.TrimSpace(f[1]); scaffold.PHPValue(v) {
			ini[f[0]], from[f[0]] = v, f[2]
		}
	}
	return ini, from
}

// phpIniLine is the settings for a message: "memory_limit 1024M (.user.ini), ...".
func phpIniLine(ini, from map[string]string) string {
	var parts []string
	for _, k := range scaffold.PHPSettings {
		if v, ok := ini[k]; ok {
			p := k + " " + v
			if from[k] != "" {
				p += " (" + from[k] + ")"
			}
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ", ")
}

// checkPHP compares the live PHP, its version and its limits, with the
// site's php_version and php_ini. With update, it rewrites .gd-sync.toml and
// the devcontainer to match.
func (c *ctx) checkPHP(conn *remote.Conn, update bool) (changed bool, err error) {
	live, full, source := c.livePHP(conn)
	if live == "" {
		phpForImage("")
		return false, nil
	}
	want := phpForImage(live)
	match := fmt.Sprintf("PHP %s, the same as live (%s)", full, source)
	if want != live {
		match = fmt.Sprintf("PHP %s, the closest there is to live's %s (%s)", want, full, source)
	}
	ini, from := c.livePHPIni(conn, live)
	// When the server can't say, the limits already written stay.
	sameIni := len(ini) == 0 || maps.Equal(ini, c.s.PHPIni)
	if len(ini) == 0 {
		ui.Warn("Could not read the live site's PHP limits — the local site keeps its own")
	}
	if want == c.s.PHPVersion && sameIni {
		ui.OK("%s", match)
		if len(ini) > 0 {
			ui.OK("PHP limits the same as live: %s", phpIniLine(ini, from))
		}
		return false, nil
	}
	if !update {
		if want != c.s.PHPVersion {
			local := c.s.PHPVersion
			if local == "" {
				local = defaultPHP
			}
			ui.Warn("The live site runs PHP %s (%s), the local site PHP %s.", full, source, local)
		}
		if !sameIni {
			ui.Warn("The live site's PHP limits differ from the local site's: %s", phpIniLine(ini, from))
		}
		ui.Info("Match it with: gd-sync php")
		return false, nil
	}
	c.s.PHPVersion = want
	if len(ini) > 0 {
		c.s.PHPIni = ini
	}
	if err := c.s.Write(); err != nil {
		return false, err
	}
	if err := scaffold.Devcontainer(c.s); err != nil {
		return false, err
	}
	ui.OK("%s — written to %s", match, site.File)
	if len(ini) > 0 {
		ui.OK("PHP limits the same as live: %s", phpIniLine(ini, from))
	}
	return true, nil
}

// cmdPHP sets the devcontainer to the PHP version and limits the live site runs with.
func cmdPHP(c *ctx) error {
	if c.s.Type != "wordpress" {
		ui.Info("%s has no PHP.", c.s.Name)
		return nil
	}
	conn, err := c.server()
	if err != nil {
		return err
	}
	ui.Step("Finding the live site's PHP version")
	changed, err := c.checkPHP(conn, true)
	if err != nil || !changed {
		return err
	}
	ui.Info("Commit %s, then rebuild the local site: gd-sync up", site.File)
	return nil
}
