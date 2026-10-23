#!/bin/sh
# Installs Debian packages on any wordpress:php* image, old ones included.
# The Debian releases under old PHP images move to archive.debian.org, and
# sometimes only halfway, so: try as is, then the archive, then without
# security updates. "mysql-client" means whichever MySQL client the release has.
set -u

sources() { ls /etc/apt/sources.list /etc/apt/sources.list.d/* 2>/dev/null; }

try() {
	apt-get -o Acquire::Check-Valid-Until=false update >/tmp/apt.log 2>&1 || { cat /tmp/apt.log; return 1; }
	if grep -q '^[EW]: Failed to fetch' /tmp/apt.log; then cat /tmp/apt.log; return 1; fi
	pkgs=""
	for p in "$@"; do
		if [ "$p" = mysql-client ] && apt-cache show default-mysql-client >/dev/null 2>&1; then
			p=default-mysql-client
		fi
		pkgs="$pkgs $p"
	done
	apt-get install -y --no-install-recommends $pkgs
}

try "$@" && exit 0

echo "==> Old Debian release: using archive.debian.org"
# The archive is signed with keys newer than the release's own keyring.
cp /usr/share/gd-sync/debian-archive-keyring.gpg /etc/apt/trusted.gpg.d/gd-sync-debian-archive.gpg
for f in $(sources); do
	sed -i -e 's|//deb.debian.org|//archive.debian.org|g' \
	       -e 's|//security.debian.org|//archive.debian.org|g' \
	       -e '/-updates/d' "$f"
done
try "$@" && exit 0

echo "==> Security updates for this release are gone: installing without them"
for f in $(sources); do sed -i -e '/security/d' "$f"; done
try "$@"
