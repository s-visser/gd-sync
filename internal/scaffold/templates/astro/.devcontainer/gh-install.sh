#!/bin/sh
# Installs the newest GitHub CLI from its release page: one static binary,
# so it works on every Debian release, old ones included. Best effort — only
# 'gd-sync secrets' and pushing over https need it.
set -u

fail() { echo "gh could not be installed — skipped"; exit 0; }

arch=$(dpkg --print-architecture) || fail
url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/cli/cli/releases/latest) || fail
v=${url##*/v}
tmp=$(mktemp -d)
curl -fsSL "https://github.com/cli/cli/releases/download/v${v}/gh_${v}_linux_${arch}.tar.gz" | tar -xz -C "$tmp" || fail
install -m 755 "$tmp"/gh_*/bin/gh /usr/local/bin/gh || fail
rm -rf "$tmp"
gh --version | head -1
