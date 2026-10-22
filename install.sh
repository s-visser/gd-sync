#!/usr/bin/env bash
# Installs gd as a global command. Written for macOS; works on Linux too.
#
#   ./install.sh            install to ~/.local/bin (no sudo)
#   ./install.sh --system   install to /usr/local/bin (asks for sudo)
#
# gd stays where you cloned it — only a symlink is installed, so `git pull`
# here updates the command everywhere.

set -euo pipefail

SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET_DIR="$HOME/.local/bin"
[ "${1:-}" = "--system" ] && TARGET_DIR="/usr/local/bin"

B=$'\033[1m'; DIM=$'\033[2m'; G=$'\033[32m'; Y=$'\033[33m'; R=$'\033[31m'; N=$'\033[0m'
ok()   { printf '%s ok %s%s\n' "$G" "$*" "$N"; }
warn() { printf '%s !  %s%s\n' "$Y" "$*" "$N"; }
die()  { printf '%s x  %s%s\n' "$R" "$*" "$N" >&2; exit 1; }
step() { printf '\n%s==> %s%s\n' "$B" "$*" "$N"; }

# ---------------------------------------------------------------- checks ----
step "Checking what's here"

case "$(uname -s)" in
  Darwin) OS=macos ;;
  Linux)  OS=linux ;;
  *)      die "Unsupported OS: $(uname -s). gd targets macOS and Linux." ;;
esac
ok "$OS"

# macOS ships bash 3.2. gd is written to run on it, but say so plainly.
if [ "${BASH_VERSINFO[0]}" -lt 4 ]; then
  warn "bash ${BASH_VERSION} — gd supports this, but if you hit anything odd:  brew install bash"
fi

MISSING=""
for t in git ssh rsync; do command -v "$t" >/dev/null || MISSING="$MISSING $t"; done
if [ -n "$MISSING" ]; then
  if [ "$OS" = macos ]; then
    die "Missing:$MISSING
  git and ssh come with Xcode command line tools:  xcode-select --install
  rsync:  brew install rsync    (Apple's bundled rsync is ancient but usable)"
  else
    die "Missing:$MISSING — install them with your package manager"
  fi
fi
ok "git, ssh, rsync"

for t in docker gh mysql; do
  if command -v "$t" >/dev/null; then ok "$t"
  else
    case "$t" in
      docker) warn "docker not found — devcontainers won't start.   brew install --cask docker" ;;
      gh)     warn "gh not found — GitHub setup will be manual.      brew install gh" ;;
      mysql)  printf '    %smysql client not found (optional).            brew install mysql-client%s\n' "$DIM" "$N" ;;
    esac
  fi
done

# ---------------------------------------------------------------- install ---
step "Installing gd"

chmod +x "$SRC/gd"
chmod +x "$SRC"/templates/*/.devcontainer/post-create.sh 2>/dev/null || true

mkdir -p "$TARGET_DIR" 2>/dev/null || sudo mkdir -p "$TARGET_DIR"
if [ -w "$TARGET_DIR" ]; then
  ln -sf "$SRC/gd" "$TARGET_DIR/gd"
else
  sudo ln -sf "$SRC/gd" "$TARGET_DIR/gd"
fi
ok "$TARGET_DIR/gd -> $SRC/gd"

# ------------------------------------------------------------------ PATH ----
if ! printf '%s' ":$PATH:" | grep -q ":$TARGET_DIR:"; then
  step "Adding $TARGET_DIR to your PATH"
  LINE="export PATH=\"$TARGET_DIR:\$PATH\""
  for rc in "$HOME/.zshrc" "$HOME/.bash_profile" "$HOME/.bashrc"; do
    [ -f "$rc" ] || continue
    grep -qF "$TARGET_DIR" "$rc" || printf '\n# added by gd install\n%s\n' "$LINE" >> "$rc"
    ok "updated $(basename "$rc")"
  done
  warn "Open a new terminal, or run:  $LINE"
fi

# ---------------------------------------------------------------- config ----
CFG="${XDG_CONFIG_HOME:-$HOME/.config}/gd/config"
if [ ! -f "$CFG" ]; then
  step "First-time setup"
  "$SRC/gd" config --setup
else
  step "Existing settings kept"
  "$SRC/gd" config
fi

cat <<EOF

${B}Done.${N}

  gd new        set up a domain
  gd list       what gd knows about
  gd help       everything else

gd works like git: run it inside one of your site folders and it acts on that
site. Outside one, it'll tell you so.
EOF
