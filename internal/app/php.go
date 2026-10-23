package app

import (
	"fmt"
	"net/http"
	"regexp"
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
// version when that can't be found out. The site's own X-Powered-By header
// wins: it is what visitors get. When the server says something else, both
// are shown.
func (c *ctx) livePHP(conn *remote.Conn) (version, full, source string) {
	out, err := conn.Output("sh -s -- "+remote.QuoteAll([]string{c.s.RemoteRoot, cpanelUserdata}), strings.NewReader(phpProbe))
	if err == nil {
		if f := strings.Split(strings.TrimRight(string(out), "\r\n"), "\t"); len(f) >= 2 {
			source, version = f[0], phpDotted(f[1])
			if len(f) > 2 && strings.HasPrefix(f[2], version+".") {
				full = f[2]
			}
		}
	}
	if v, f := headerPHP(c.s.LiveURL); v != "" {
		if version != "" && version != v {
			ui.Warn("The server's %s says PHP %s, but %s answers with PHP %s — going by the site", source, version, c.s.LiveURL, v)
		}
		if f == "" && v == version {
			f = full
		}
		version, full, source = v, f, "the live site's X-Powered-By header"
	}
	if full == "" {
		full = version
	}
	return version, full, source
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

// checkPHP compares the live PHP with the site's PHP_VERSION. With update,
// it rewrites .gd-sync.toml and the devcontainer's .env to match.
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
	if want == c.s.PHPVersion {
		ui.OK("%s", match)
		return false, nil
	}
	if !update {
		local := c.s.PHPVersion
		if local == "" {
			local = defaultPHP
		}
		ui.Warn("The live site runs PHP %s (%s), the local site PHP %s.", full, source, local)
		ui.Info("Match it with: gd-sync php")
		return false, nil
	}
	c.s.PHPVersion = want
	if err := c.s.Write(); err != nil {
		return false, err
	}
	if err := scaffold.Devcontainer(c.s); err != nil {
		return false, err
	}
	ui.OK("%s — written to %s", match, site.File)
	return true, nil
}

// cmdPHP sets the devcontainer to the PHP version the live site runs.
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
