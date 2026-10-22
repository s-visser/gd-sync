#!/usr/bin/env bash
#
# gd — GoDaddy cPanel site toolkit. The server is the source of truth.
#
# Every site is its own folder, its own Git repo, its own config. Run gd from
# anywhere inside a site's folder and it works out which site you mean by
# reading that repo's own .gdsync file. No site name to remember or type.
#
#   gd new                  create a new site: folder, repo, config, container
#   gd adopt                register a folder you already have
#   gd list                 every site gd knows about
#   gd config               machine-wide settings (projects folder, GitHub owner)
#
#   gd sync                 update main with the CURRENT SERVER CONTENTS
#   gd merge [branch]       sync main from server, merge branch in, go live
#   gd status               what differs between server and main (read-only)
#   gd deploy               push main to the server by hand
#
#   gd db                   download the live database into _db/
#   gd up / gd down         start / stop the devcontainer
#
#   gd shell                SSH into the server
#   gd key                  install your SSH key (stop typing passwords)
#   gd secrets              push SSH secrets to GitHub so the workers can run
#   gd scaffold             regenerate devcontainer + workflows + run configs
#
# Add --all to sync/status/db to run every registered site.

set -euo pipefail

GD_HOME="${GD_HOME:-${XDG_CONFIG_HOME:-$HOME/.config}/gd}"
CONFIG="${GD_CONFIG:-$GD_HOME/config}"
REGISTRY="${GD_REGISTRY:-$GD_HOME/repos}"
STATE_DIR="${GD_STATE:-$HOME/.local/state/gd}"
TPL="${GD_TEMPLATES:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/templates}"
SELF="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"

# ---------------------------------------------------------------- output ----
if [ -t 1 ]; then
  B=$'\033[1m'; DIM=$'\033[2m'; R=$'\033[31m'; G=$'\033[32m'; Y=$'\033[33m'; C=$'\033[36m'; N=$'\033[0m'
else B=""; DIM=""; R=""; G=""; Y=""; C=""; N=""; fi
say()  { printf '%s\n' "$*"; }
step() { printf '%s==>%s %s%s%s\n' "$C" "$N" "$B" "$*" "$N"; }
info() { printf '    %s%s%s\n' "$DIM" "$*" "$N"; }
warn() { printf '%s !  %s%s\n' "$Y" "$*" "$N" >&2; }
die()  { printf '%s x  %s%s\n' "$R" "$*" "$N" >&2; exit 1; }
ok()   { printf '%s ok %s%s\n' "$G" "$*" "$N"; }

confirm() {
  local reply
  [ "${ASSUME_YES:-0}" = "1" ] && return 0
  [ -t 0 ] || return 1
  printf '%s%s%s [y/N] ' "$B" "$1" "$N"; read -r reply || return 1
  case "$reply" in [yY]|[yY][eE][sS]) return 0 ;; *) return 1 ;; esac
}
ask() { # prompts on stderr so the answer can be captured cleanly
  local p="$1" d="${2:-}" a
  if [ -n "$d" ]; then printf '%s %s[%s]%s ' "$p" "$DIM" "$d" "$N" >&2
  else printf '%s ' "$p" >&2; fi
  read -r a || true
  printf '%s' "${a:-$d}"
}

# ================================================================= config ====
# A site's settings live in its own repo, in two files:
#
#   .gdsync         committed. What the site is and where it deploys.
#                   The GitHub workers read this same file.
#   .gdsync.local   gitignored. How to reach the server from this machine.
#
# There is no central config file. The registry is only a list of folders,
# so that `gd list` and `--all` have something to walk.
# =============================================================================

# ------------------------------------------------------------ global cfg ----
# Machine-wide preferences, so `gd new` can stop asking the same two questions.
#   PROJECTS_DIR  where new site folders are created
#   GIT_OWNER     GitHub user or org that new repos belong to
#   GIT_PRIVATE   true/false for new repos
#   DEFAULT_TYPE  wordpress or astro

PROJECTS_DIR="$HOME/Projects"
GIT_OWNER=""
GIT_PRIVATE="true"
DEFAULT_TYPE="wordpress"

load_config() {
  [ -f "$CONFIG" ] || return 0
  local line k v
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in ''|\#*) continue;; *=*) ;; *) continue;; esac
    k="${line%%=*}"; v="${line#*=}"
    k="$(printf '%s' "$k" | tr -d '[:space:]')"
    v="${v#"${v%%[![:space:]]*}"}"; v="${v%"${v##*[![:space:]]}"}"
    case "$k" in
      PROJECTS_DIR) PROJECTS_DIR="${v/#\~/$HOME}";;
      GIT_OWNER)    GIT_OWNER="$v";;
      GIT_PRIVATE)  GIT_PRIVATE="$v";;
      DEFAULT_TYPE) DEFAULT_TYPE="$v";;
    esac
  done < "$CONFIG"
}

write_config() {
  mkdir -p "$(dirname "$CONFIG")"
  cat > "$CONFIG" <<EOF
# gd — machine-wide settings. Per-site settings live in each repo's .gdsync.
PROJECTS_DIR=$PROJECTS_DIR
GIT_OWNER=$GIT_OWNER
GIT_PRIVATE=$GIT_PRIVATE
DEFAULT_TYPE=$DEFAULT_TYPE
EOF
}

cmd_config() {
  case "${1:-}" in
    ""|--list|list)
      printf '%sGlobal settings%s  (%s)\n\n' "$B" "$N" "$CONFIG"
      printf '  %-14s %s\n' PROJECTS_DIR "$PROJECTS_DIR"
      printf '  %-14s %s\n' GIT_OWNER    "${GIT_OWNER:-<unset>}"
      printf '  %-14s %s\n' GIT_PRIVATE  "$GIT_PRIVATE"
      printf '  %-14s %s\n' DEFAULT_TYPE "$DEFAULT_TYPE"
      say ""
      info "change one:  gd config PROJECTS_DIR ~/Sites"
      info "or run the short setup again:  gd config --setup"
      ;;
    --setup|setup)
      step "gd setup"
      PROJECTS_DIR="$(ask 'Where should new site folders go?' "$PROJECTS_DIR")"
      PROJECTS_DIR="${PROJECTS_DIR/#\~/$HOME}"
      GIT_OWNER="$(ask 'GitHub owner for new repos (user or org):' "$GIT_OWNER")"
      GIT_PRIVATE="$(ask 'Make new repos private? (true/false):' "$GIT_PRIVATE")"
      DEFAULT_TYPE="$(ask 'Default site type (wordpress/astro):' "$DEFAULT_TYPE")"
      write_config; ok "Saved to $CONFIG"
      ;;
    *)
      local k="$1" v="${2:-}"
      k="$(printf '%s' "$k" | tr '[:lower:]' '[:upper:]')"
      [ -n "$v" ] || die "usage: gd config $k <value>"
      case "$k" in
        PROJECTS_DIR) PROJECTS_DIR="${v/#\~/$HOME}";;
        GIT_OWNER)    GIT_OWNER="$v";;
        GIT_PRIVATE)  GIT_PRIVATE="$v";;
        DEFAULT_TYPE) DEFAULT_TYPE="$v";;
        *) die "Unknown setting '$k' — try: gd config";;
      esac
      write_config; ok "$k = $v"
      ;;
  esac
}

find_repo() { # walk up from a directory looking for .gdsync
  local d="${1:-$PWD}"
  d="$(cd "$d" 2>/dev/null && pwd)" || return 1
  while [ -n "$d" ] && [ "$d" != "/" ]; do
    [ -f "$d/.gdsync" ] && { printf '%s' "$d"; return 0; }
    d="$(dirname "$d")"
  done
  return 1
}

load_site() { # from an explicit repo path, or inferred from $PWD
  repo="${1:-}"
  if [ -z "$repo" ]; then
    repo="$(find_repo)" || die "Not inside a gd site.
  cd into one of your site folders, or create one with: gd new
  (gd list shows the sites gd knows about)"
  fi
  [ -f "$repo/.gdsync" ] || die "No .gdsync in $repo"

  site=""; type="wordpress"; remote_root=""; tracked=""
  live_url=""; local_url=""; build_cmd=""; publish_dir=""; exclude=""
  ssh_host=""; ssh_user=""; ssh_port="22"

  local f line k v
  for f in "$repo/.gdsync" "$repo/.gdsync.local"; do
    [ -f "$f" ] || continue
    while IFS= read -r line || [ -n "$line" ]; do
      case "$line" in ''|\#*) continue;; esac
      case "$line" in *=*) ;; *) continue;; esac
      k="${line%%=*}"; v="${line#*=}"
      k="$(printf '%s' "$k" | tr -d '[:space:]')"
      v="${v#"${v%%[![:space:]]*}"}"; v="${v%"${v##*[![:space:]]}"}"
      case "$k" in
        SITE) site="$v";;           SITE_TYPE) type="$v";;
        REMOTE_ROOT) remote_root="$v";; TRACKED) tracked="$v";;
        LIVE_URL) live_url="$v";;   LOCAL_URL) local_url="$v";;
        BUILD_CMD) build_cmd="$v";; PUBLISH_DIR) publish_dir="$v";;
        EXCLUDE) exclude="$v";;
        SSH_HOST) ssh_host="$v";;   SSH_USER) ssh_user="$v";; SSH_PORT) ssh_port="$v";;
      esac
    done < "$f"
  done

  [ -n "$site" ] || die "$repo/.gdsync has no SITE"
  remote_root="${remote_root%/}"
  SSH_TARGET="$ssh_user@$ssh_host"

  case "$type" in
    astro)     [ -n "$build_cmd" ]   || build_cmd="npm ci && npm run build"
               [ -n "$publish_dir" ] || publish_dir="dist"
               [ -n "$local_url" ]   || local_url="http://localhost:4321" ;;
    wordpress) [ -n "$local_url" ]   || local_url="http://localhost:8080" ;;
  esac
}

need_server() { # only the commands that actually talk to the server
  local r up
  for r in ssh_host ssh_user remote_root; do
    up="$(printf '%s' "$r" | tr '[:lower:]' '[:upper:]')"
    [ -n "${!r}" ] || die "$site has no $up on this machine.
  Connection details live in $repo/.gdsync.local — run 'gd adopt' to fill them in."
  done
}

# ------------------------------------------------------------- registry -----
registry_add() {
  mkdir -p "$(dirname "$REGISTRY")"; touch "$REGISTRY"
  grep -qxF -- "$1" "$REGISTRY" 2>/dev/null || printf '%s\n' "$1" >> "$REGISTRY"
}
registry_list() { # prints live repo paths, pruning any that have gone away
  [ -f "$REGISTRY" ] || return 0
  local p kept=""
  while IFS= read -r p || [ -n "$p" ]; do
    [ -n "$p" ] || continue
    [ -f "$p/.gdsync" ] && { printf '%s\n' "$p"; kept="$kept$p"$'\n'; }
  done < "$REGISTRY"
  printf '%s' "$kept" > "$REGISTRY"
}

# ------------------------------------------------------------------ ssh -----
ssh_opts() {
  mkdir -p "$STATE_DIR/cm"
  printf '%s' "-p $ssh_port -o ControlMaster=auto -o ControlPath=$STATE_DIR/cm/%%r@%%h-%%p -o ControlPersist=10m -o StrictHostKeyChecking=accept-new"
}
# shellcheck disable=SC2046
rsh() { ssh $(ssh_opts) "$SSH_TARGET" "$@"; }
rsync_e() { printf '%s' "ssh $(ssh_opts)"; }

default_excludes() {
  printf '%s\n' .git .github .devcontainer .gdsync .gdsync.local .gitignore \
    .idea .vscode .run .DS_Store node_modules _db '*.log' '.env' \
    'wp-content/cache' 'wp-content/uploads'
  local x; for x in $exclude; do printf '%s\n' "$x"; done
}
rsync_args() {
  local a=(-az --delete --no-perms --no-owner --no-group --omit-dir-times) e
  while IFS= read -r e; do a+=(--exclude "$e"); done < <(default_excludes)
  printf '%s\n' "${a[@]}"
}
remote_is_dir() { rsh "test -d '$remote_root/$1'" 2>/dev/null; }

# Keep the 3 newest dumps. (BSD xargs has no -r, so do it the portable way.)
prune_dumps() {
  local f
  ls -1t "$repo/_db/${site}-"*.sql 2>/dev/null | tail -n +4 | while IFS= read -r f; do
    [ -n "$f" ] && rm -f -- "$f"
  done
}

check_tools() {
  local m="" t; for t in ssh rsync git; do command -v "$t" >/dev/null || m="$m $t"; done
  [ -z "$m" ] || die "Missing required tools:$m"
}
ensure_remote_wpcli() {
  rsh "test -x ~/wp-cli.phar" && return 0
  step "Installing WP-CLI on the server (one time)"
  rsh "cd ~ && curl -sS -o wp-cli.phar https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar && chmod +x wp-cli.phar" \
    || die "Could not install WP-CLI on the server"
}

# ------------------------------------------------------------------ repo -----
in_repo() { git -C "$repo" "$@"; }
current_branch() { in_repo branch --show-current 2>/dev/null || echo main; }

require_clean_tree() {
  [ -z "$(in_repo status --porcelain)" ] && return 0
  warn "Uncommitted changes in $repo"
  in_repo status --short | sed 's/^/    /'
  confirm "Stash them and continue?" || die "Commit or stash first."
  in_repo stash push -u -m "gd auto-stash $(date +%F_%H%M)"; STASHED=1
}
restore_stash() {
  [ "${STASHED:-0}" = "1" ] || return 0
  step "Restoring your stashed changes"
  in_repo stash pop || warn "Stash conflicts — resolve in $repo"
  STASHED=0
}

# --------------------------------------------------------------- scaffold ---
write_gitignore() {
  if [ "$type" = "astro" ]; then
    cat > "$repo/.gitignore" <<'EOF'
node_modules/
dist/
.astro/
.env
.env.production
.DS_Store
.idea/
_db/

# connection details for this machine only
.gdsync.local
EOF
  else
    cat > "$repo/.gitignore" <<'EOF'
# WordPress core and anything we do not author — this is what stops core
# updates from ever making this repo "outdated".
/wp-admin/
/wp-includes/
/wp-*.php
/index.php
/license.txt
/readme.html
/xmlrpc.php
wp-config.php
wp-content/uploads/
wp-content/cache/
wp-content/upgrade/
wp-content/backup*/
wp-content/*.log
wp-content/debug.log

.idea/
.vscode/
.DS_Store
node_modules/
*.log
.env
_db/

# connection details for this machine only
.gdsync.local
EOF
  fi
}

write_manifest() {
  cat > "$repo/.gdsync" <<EOF
# What this site is and where it deploys. This file IS the config — gd reads it
# to work out which site you are in, and the GitHub workers read it too.
SITE=$site
SITE_TYPE=$type
REMOTE_ROOT=$remote_root
TRACKED=$tracked
BUILD_CMD=$build_cmd
PUBLISH_DIR=$publish_dir
LIVE_URL=$live_url
LOCAL_URL=$local_url
EXCLUDE=$exclude
EOF
  cat > "$repo/.gdsync.local" <<EOF
# How to reach the server from this machine. Not committed.
# The GitHub workers use repository secrets instead — see: gd secrets
SSH_HOST=$ssh_host
SSH_USER=$ssh_user
SSH_PORT=$ssh_port
EOF
}

write_docs() {
  # GD.md is the tool's own cheat sheet. Regenerated on every scaffold, so keep
  # it about concepts and the handful of commands that won't churn — anything
  # version-specific belongs behind `gd help`.
  cat > "$repo/GD.md" <<'EOF'
# Working on this site

This project is managed by **gd**. Run gd from anywhere inside this folder —
it works out which site you mean, the way git does.

## The one rule

The live server is the source of truth. `main` mirrors the server.
You work on `develop`. Never edit files on the server by hand if you can help it.

## A normal session

```
gd sync        # pull the server's current state onto main
gd db          # pull the live database (WordPress sites)
gd up          # start the local site in Docker
               # ... work on develop ...
gd merge       # sync again, merge develop, go live
```

Start with `gd sync`. The site may have changed since you last looked —
a plugin update, or someone editing through the hosting panel.

## Commands

| | |
|---|---|
| `gd sync` | update `main` with what's on the server right now |
| `gd status` | what differs between server and your copy (changes nothing) |
| `gd db` | download the live database into `_db/` |
| `gd up` / `gd down` | start / stop the local Docker site |
| `gd merge` | the safe way to go live: sync, merge, deploy |
| `gd deploy` | upload by hand, skipping GitHub |
| `gd shell` | SSH into the server |
| `gd help` | everything else |

## Files in here

| | |
|---|---|
| `.gdsync` | this site's config — committed, also read by the GitHub workers |
| `.gdsync.local` | your SSH details — not committed |
| `_db/` | database dumps — not committed |
| `.devcontainer/` | the local Docker environment |
| `.github/workflows/` | the sync and deploy workers |

## If something looks wrong

`gd status` shows the difference without touching anything.

Every deploy backs the server up first, to `~/gd-backups/` on the server.
`gd shell`, then unpack the tarball you want over the web root.

## Working on a different machine

```
git clone <this repo>
cd <folder> && gd adopt
```

`gd adopt` asks for the SSH details, since those aren't committed.
EOF

  # The project README is the site's own — write it once and leave it alone.
  [ -f "$repo/README.md" ] && return 0
  local domain kind
  domain="$live_url"; domain="${domain#http://}"; domain="${domain#https://}"; domain="${domain%/}"
  case "$type" in
    wordpress) kind="WordPress";;
    astro)     kind="Astro (static)";;
    *)         kind="$type";;
  esac
  cat > "$repo/README.md" <<EOF
# $domain

| | |
|---|---|
| Live | $live_url |
| Local | $local_url |
| Stack | $kind |
| Hosting | GoDaddy cPanel |

See [GD.md](GD.md) for how to pull, run and deploy this site.

## About this site

_What it's for, who owns it, anything worth knowing a year from now._

## Custom code

_What lives in the tracked paths and why._

## Gotchas

_Anything that bit you last time._
EOF
}

write_devcontainer() {
  local src="$TPL/$type/.devcontainer"
  [ -d "$src" ] || { warn "No devcontainer template for type '$type'"; return 0; }
  mkdir -p "$repo/.devcontainer"
  cp -R "$src/." "$repo/.devcontainer/"
  chmod +x "$repo/.devcontainer/post-create.sh" 2>/dev/null || true
  sed -i.bak "s|__SITE__|$site|g" "$repo/.devcontainer/devcontainer.json" \
    && rm -f "$repo/.devcontainer/devcontainer.json.bak"

  if [ "$type" = "wordpress" ]; then
    local mounts="" p
    for p in $tracked; do mounts="$mounts      - ../$p:/var/www/html/$p:cached\n"; done
    awk -v m="$mounts" '/# __TRACKED_MOUNTS__/{printf "%s", m; next} {print}' \
      "$repo/.devcontainer/docker-compose.yml" > "$repo/.devcontainer/.dc.tmp" \
      && mv "$repo/.devcontainer/.dc.tmp" "$repo/.devcontainer/docker-compose.yml"
  fi

  cat > "$repo/.devcontainer/.env" <<EOF
COMPOSE_PROJECT_NAME=$site
LIVE_URL=$live_url
LOCAL_URL=$local_url
EOF
}

write_workflows() {
  mkdir -p "$repo/.github/workflows"
  cp "$TPL/github/deploy.yml" "$TPL/github/sync-from-server.yml" \
     "$TPL/github/verify-main.yml" "$repo/.github/workflows/"
}

write_runconfigs() {
  local dir="$repo/.run"; mkdir -p "$dir"
  _cfg() {
    cat > "$dir/${1// /_}.run.xml" <<XML
<component name="ProjectRunConfigurationManager">
  <configuration default="false" name="$1" type="ShConfigurationType">
    <option name="SCRIPT_TEXT" value="" />
    <option name="INDEPENDENT_SCRIPT_PATH" value="true" />
    <option name="SCRIPT_PATH" value="$SELF" />
    <option name="SCRIPT_OPTIONS" value="$2" />
    <option name="INDEPENDENT_SCRIPT_WORKING_DIRECTORY" value="true" />
    <option name="SCRIPT_WORKING_DIRECTORY" value="\$PROJECT_DIR\$" />
    <option name="INDEPENDENT_INTERPRETER_PATH" value="true" />
    <option name="INTERPRETER_PATH" value="/bin/bash" />
    <option name="INTERPRETER_OPTIONS" value="" />
    <option name="EXECUTE_IN_TERMINAL" value="true" />
    <option name="EXECUTE_SCRIPT_FILE" value="true" />
    <envs />
    <method v="2" />
  </configuration>
</component>
XML
  }
  # No site name in any of these — the working directory decides.
  _cfg "1 Sync main from server" "sync"
  _cfg "2 Pull database"         "db"
  _cfg "3 Server diff"           "status"
  _cfg "4 Merge and go live"     "merge"
  _cfg "5 Deploy by hand"        "deploy"
}

cmd_scaffold() {
  load_site
  write_gitignore; write_manifest; write_docs; write_devcontainer; write_workflows; write_runconfigs
  registry_add "$repo"
  ok "Scaffolding refreshed for $site"
  info "GD.md, devcontainer, .github/workflows, .run, .gdsync, .gitignore"
}

# ================================================================== core =====
cmd_sync() {
  load_site "${1:-}"; check_tools
  if [ "$type" = "astro" ]; then
    info "$site is a static Astro site — the repo is the source, nothing to pull back."
    return 0
  fi
  need_server
  [ -n "$tracked" ] || die "$site has no TRACKED paths in .gdsync"

  local back; back="$(current_branch)"; [ "$back" = "main" ] && back=""
  require_clean_tree

  step "Updating main with the current server contents"
  in_repo checkout -q main

  local p src a args=()
  while IFS= read -r a; do args+=("$a"); done < <(rsync_args)
  for p in $tracked; do
    if remote_is_dir "$p"; then src="$remote_root/$p/"; mkdir -p "$repo/$p"
    else src="$remote_root/$p"; mkdir -p "$(dirname "$repo/$p")"; fi
    info "$p"
    rsync "${args[@]}" -e "$(rsync_e)" "$SSH_TARGET:$src" "$repo/$p" || die "rsync failed for $p"
  done

  in_repo add -A
  if in_repo diff --cached --quiet; then
    ok "main already matches the server"
  else
    in_repo -c user.email=gd@local -c user.name=gd \
      commit -qm "[server-sync] Live server state $(date '+%Y-%m-%d %H:%M')"
    ok "main updated from the server"
    in_repo diff --stat HEAD~1 HEAD | sed 's/^/    /'
  fi

  in_repo remote get-url origin >/dev/null 2>&1 && {
    in_repo push -q origin main 2>/dev/null && info "pushed main to origin" \
      || warn "could not push main to origin"; } || true

  if [ -n "$back" ] && [ "${KEEP_BRANCH:-0}" != "1" ]; then
    in_repo checkout -q "$back"; restore_stash
  fi
}

cmd_merge() {
  load_site; check_tools; need_server
  local from="${1:-develop}"
  in_repo rev-parse --verify -q "$from" >/dev/null || die "No branch '$from' in this repo"
  [ "$from" = "main" ] && die "Nothing to merge — 'main' is the target, not the source"

  step "Step 1 of 3 — refreshing main from the live server"
  KEEP_BRANCH=1 cmd_sync "$repo"

  step "Step 2 of 3 — merging $from into main"
  in_repo checkout -q main
  if ! in_repo merge --no-ff --no-edit "$from"; then
    warn "Conflict between your work and the live server."
    in_repo diff --name-only --diff-filter=U | sed 's/^/    /'
    info "Resolve in your IDE, then: git merge --continue && gd merge"
    exit 1
  fi
  ok "merged"

  step "Step 3 of 3 — going live"
  if in_repo remote get-url origin >/dev/null 2>&1; then
    in_repo push origin main && ok "Pushed. The deploy worker is updating the server."
  else
    warn "No git remote — deploying directly instead."
    ASSUME_YES=1 cmd_deploy
  fi
  in_repo checkout -q "$from"
  in_repo merge -q --no-edit main || true
  restore_stash
}

cmd_status() {
  load_site "${1:-}"; check_tools
  [ "$type" = "astro" ] && { info "$site is static — compare by building locally."; return 0; }
  need_server
  step "Server vs your working copy of $site (read-only)"
  info "note: files only you have show as 'deleting' — that's your unmerged work"
  local p src out a any=0 args=()
  while IFS= read -r a; do args+=("$a"); done < <(rsync_args)
  for p in $tracked; do
    if remote_is_dir "$p"; then src="$remote_root/$p/"; else src="$remote_root/$p"; fi
    out="$(rsync "${args[@]}" --dry-run --itemize-changes -e "$(rsync_e)" \
          "$SSH_TARGET:$src" "$repo/$p" 2>/dev/null | grep -v '^$' || true)"
    [ -n "$out" ] && { any=1; printf '  %s%s%s\n' "$B" "$p" "$N"; printf '%s\n' "$out" | sed 's/^/      /'; }
  done
  [ "$any" = "0" ] && ok "Identical" || info "Run 'gd sync' to fold these into main"
}

cmd_deploy() {
  load_site; check_tools; need_server
  local branch; branch="$(current_branch)"
  [ "$branch" = "main" ] || warn "You are on '$branch'. main is what the server mirrors."

  local p src dst out a any=0 args=()
  while IFS= read -r a; do args+=("$a"); done < <(rsync_args)

  if [ -n "$publish_dir" ]; then
    [ -d "$repo/$publish_dir" ] || die "No build output at $publish_dir — run the build first"
    step "Deploying $site  [$publish_dir -> web root]"
  else
    step "Deploying $site  [$branch -> server]"
  fi

  say ""; info "Dry run:"
  if [ -n "$publish_dir" ]; then
    out="$(rsync "${args[@]}" --dry-run --itemize-changes -e "$(rsync_e)" \
          "$repo/$publish_dir/" "$SSH_TARGET:$remote_root/" 2>/dev/null | grep -v '^$' || true)"
    [ -n "$out" ] && { any=1; printf '%s\n' "$out" | sed 's/^/      /'; }
  else
    for p in $tracked; do
      [ -e "$repo/$p" ] || { warn "$p missing locally — skipped"; continue; }
      if [ -d "$repo/$p" ]; then src="$repo/$p/"; dst="$remote_root/$p/"; else src="$repo/$p"; dst="$remote_root/$p"; fi
      out="$(rsync "${args[@]}" --dry-run --itemize-changes -e "$(rsync_e)" \
            "$src" "$SSH_TARGET:$dst" 2>/dev/null | grep -v '^$' || true)"
      [ -n "$out" ] && { any=1; printf '  %s%s%s\n' "$B" "$p" "$N"; printf '%s\n' "$out" | sed 's/^/      /'; }
    done
  fi
  [ "$any" = "0" ] && { ok "Server already matches — nothing to deploy"; return 0; }

  say ""; confirm "Push these to the LIVE site?" || { say "Cancelled."; return 0; }

  step "Backing up the server's current files"
  local stamp; stamp="$(date +%Y%m%d-%H%M%S)"
  rsh "mkdir -p ~/gd-backups && cd '$remote_root' && tar czf ~/gd-backups/${site}-${stamp}.tar.gz ${tracked:-.} 2>/dev/null" \
    && ok "~/gd-backups/${site}-${stamp}.tar.gz" || warn "Backup failed — continuing"

  step "Uploading"
  if [ -n "$publish_dir" ]; then
    rsync "${args[@]}" -e "$(rsync_e)" "$repo/$publish_dir/" "$SSH_TARGET:$remote_root/" || die "Upload failed"
  else
    for p in $tracked; do
      [ -e "$repo/$p" ] || continue
      if [ -d "$repo/$p" ]; then src="$repo/$p/"; dst="$remote_root/$p/"; else src="$repo/$p"; dst="$remote_root/$p"; fi
      info "$p"; rsync "${args[@]}" -e "$(rsync_e)" "$src" "$SSH_TARGET:$dst" || die "Upload failed for $p"
    done
  fi
  ok "Deployed"
}

cmd_db() {
  load_site "${1:-}"
  [ "$type" = "wordpress" ] || { info "$site has no database."; return 0; }
  need_server; ensure_remote_wpcli
  mkdir -p "$repo/_db"
  local stamp dump; stamp="$(date +%Y%m%d-%H%M%S)"; dump="$repo/_db/${site}-${stamp}.sql"

  step "Exporting the live database"
  rsh "cd '$remote_root' && ~/wp-cli.phar db export - --single-transaction --quick --default-character-set=utf8mb4 --skip-plugins --skip-themes" \
    > "$dump" || die "Export failed"
  [ -s "$dump" ] || die "Export produced an empty file"
  ok "$(du -h "$dump" | cut -f1) -> _db/$(basename "$dump")"
  prune_dumps

  local dc="$repo/.devcontainer/docker-compose.yml"
  if command -v docker >/dev/null && [ -f "$dc" ] \
     && [ -n "$(docker compose -f "$dc" ps -q db 2>/dev/null)" ]; then
    step "Loading it into the running devcontainer"
    docker compose -f "$dc" exec -T workspace bash .devcontainer/post-create.sh >/dev/null 2>&1 \
      && ok "Local site now matches live" || info "Restart the container to import: gd down && gd up"
  else
    info "Start the devcontainer and it imports automatically: gd up"
  fi
}

cmd_up()   { load_site; docker compose --env-file "$repo/.devcontainer/.env" -f "$repo/.devcontainer/docker-compose.yml" up -d; ok "$site is up — $local_url"; }
cmd_down() { load_site; docker compose --env-file "$repo/.devcontainer/.env" -f "$repo/.devcontainer/docker-compose.yml" down; ok "$site stopped"; }
# shellcheck disable=SC2046
cmd_shell(){ load_site; need_server; exec ssh $(ssh_opts) "$SSH_TARGET"; }

cmd_key() {
  load_site; need_server
  local key="$HOME/.ssh/id_ed25519"
  [ -f "$key.pub" ] || { step "Creating an SSH key"; ssh-keygen -t ed25519 -f "$key" -N "" -C "gd-sync"; }
  step "Installing it on $SSH_TARGET (cPanel password once)"
  if command -v ssh-copy-id >/dev/null; then ssh-copy-id -p "$ssh_port" -i "$key.pub" "$SSH_TARGET"
  else ssh -p "$ssh_port" "$SSH_TARGET" "mkdir -p ~/.ssh && chmod 700 ~/.ssh && cat >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys" < "$key.pub"; fi
  ok "Done — no more password prompts"
}

cmd_secrets() {
  load_site; need_server
  command -v gh >/dev/null || die "GitHub CLI (gh) not installed — https://cli.github.com"
  local key="$HOME/.ssh/id_ed25519"
  [ -f "$key" ] || die "No SSH key at $key — run: gd key"
  step "Pushing SSH secrets to GitHub"
  ( cd "$repo" && gh secret set GD_SSH_KEY < "$key" \
    && gh secret set GD_SSH_HOST --body "$ssh_host" \
    && gh secret set GD_SSH_USER --body "$ssh_user" \
    && gh secret set GD_SSH_PORT --body "$ssh_port" )
  ok "GD_SSH_KEY, GD_SSH_HOST, GD_SSH_USER, GD_SSH_PORT set"
  info "Run 'gd key' first if you haven't, or the key won't be authorised."
}

# =================================================================== new =====
cmd_new() {
  command -v git >/dev/null || die "git is required"
  say ""
  printf '%sNew site%s — a few questions, then everything gets built.\n\n' "$B" "$N"

  local domain name stype host user port root trk rp ghname
  domain="$(ask 'Domain (e.g. acme.nl):')"
  [ -n "$domain" ] || die "A domain is required"

  name="$(ask 'Short name for this site:' "$(printf '%s' "${domain%%.*}" | tr -cd 'a-zA-Z0-9-')")"
  [ -n "$name" ] || die "A short name is required"

  rp="$(ask 'Local folder:' "$PROJECTS_DIR/$name")"
  rp="${rp/#\~/$HOME}"
  [ -f "$rp/.gdsync" ] && die "$rp is already a gd site — use 'gd adopt' there instead"

  ghname="$(ask 'GitHub repo (owner/name, or "none"):' "${GIT_OWNER:+$GIT_OWNER/$name}")"
  case "$ghname" in none|None|NONE|-|"") ghname="";; esac

  stype="$(ask 'Type — wordpress or astro:' "$DEFAULT_TYPE")"
  case "$stype" in wordpress|astro) ;; *) die "Type must be 'wordpress' or 'astro'";; esac

  printf '\n' >&2
  info "SSH: GoDaddy dashboard -> your hosting -> Settings -> SSH access -> Manage"
  host="$(ask 'SSH host (IP or server name):')"
  user="$(ask 'SSH username (cPanel username):')"
  port="$(ask 'SSH port:' '22')"
  root="$(ask 'Remote web root:' "/home/$user/public_html")"

  trk=""
  if [ "$stype" = "wordpress" ]; then
    printf '\n' >&2
    info "Which paths do YOU author? Space separated, relative to the web root."
    info "Core and uploads stay out — that is what keeps this repo evergreen."
    trk="$(ask 'Tracked paths:' "wp-content/plugins/${name}-custom")"
  fi

  # ---- build it -----------------------------------------------------------
  site="$name"; type="$stype"; repo="$rp"
  ssh_host="$host"; ssh_user="$user"; ssh_port="$port"; remote_root="${root%/}"
  tracked="$trk"; live_url="https://$domain"; exclude=""
  if [ "$stype" = "astro" ]; then
    build_cmd="npm ci && npm run build"; publish_dir="dist"; local_url="http://localhost:4321"
  else
    build_cmd=""; publish_dir=""; local_url="http://localhost:8080"
  fi
  SSH_TARGET="$ssh_user@$ssh_host"
  check_tools

  step "Creating $repo"
  mkdir -p "$repo" "$repo/_db"
  [ -d "$repo/.git" ] || { git -C "$repo" init -q; git -C "$repo" symbolic-ref HEAD refs/heads/main; }
  write_gitignore; write_manifest; write_docs; write_devcontainer; write_workflows; write_runconfigs
  registry_add "$repo"

  in_repo add -A
  in_repo rev-parse --verify -q HEAD >/dev/null 2>&1 \
    || in_repo -c user.email=gd@local -c user.name=gd commit -qm "Scaffold $name"
  in_repo rev-parse --verify -q main >/dev/null 2>&1 || in_repo branch -M main
  ok "repo created"

  step "Testing SSH"
  rsh "echo ok" >/dev/null || die "Cannot reach $SSH_TARGET:$ssh_port
  Enable it: GoDaddy dashboard -> Settings tab -> SSH access -> Manage -> toggle on.
  Your cPanel password is the SSH password.
  Everything else is written — correct $repo/.gdsync.local, then run: gd sync"
  ok "SSH works"
  rsh "test -d '$remote_root'" || die "Remote path not found: $remote_root
  Fix REMOTE_ROOT in $repo/.gdsync, then run: gd sync"

  confirm "Install an SSH key so you stop typing the password?" && cmd_key || true

  if [ "$stype" = "wordpress" ]; then
    KEEP_BRANCH=1 cmd_sync "$repo"
    confirm "Download the live database now?" && cmd_db "$repo" || true
  fi

  in_repo rev-parse --verify -q develop >/dev/null 2>&1 || in_repo branch develop main
  in_repo checkout -q develop

  if [ -n "$ghname" ]; then
    if command -v gh >/dev/null; then
      step "Setting up GitHub"
      in_repo remote get-url origin >/dev/null 2>&1 \
        || ( cd "$repo" && gh repo create "$ghname" \
               "$([ "$GIT_PRIVATE" = "false" ] && echo --public || echo --private)" \
               --source=. --remote=origin --push ) \
        || warn "Could not create $ghname — add the remote by hand"
      in_repo push -q -u origin main develop 2>/dev/null || true
      cmd_secrets || warn "Set secrets later with: gd secrets"
    else
      warn "GitHub CLI not installed — create $ghname yourself, then: gd secrets"
    fi
  fi

  say ""
  ok "$name is ready."
  say ""
  info "cd $repo"
  info "Open it in your JetBrains IDE and let it start the devcontainer, or: gd up"
  info "From inside that folder gd needs no arguments: gd sync, gd merge, gd db"
}

# Register a folder that already has a .gdsync (a clone on a new machine,
# or a site set up by a colleague).
cmd_adopt() {
  local rp="${1:-$PWD}"
  rp="$(cd "$rp" 2>/dev/null && pwd)" || die "No such folder: ${1:-$PWD}"
  rp="$(find_repo "$rp")" || die "$rp has no .gdsync — set a site up with: gd new"

  repo="$rp"; load_site "$rp"
  if [ -z "$ssh_host" ] || [ -z "$ssh_user" ]; then
    step "$site has no connection details on this machine"
    info "SSH: GoDaddy dashboard -> your hosting -> Settings -> SSH access -> Manage"
    ssh_host="$(ask 'SSH host:' "$ssh_host")"
    ssh_user="$(ask 'SSH username:' "$ssh_user")"
    ssh_port="$(ask 'SSH port:' "${ssh_port:-22}")"
    write_manifest
    ok "wrote $repo/.gdsync.local"
  fi
  registry_add "$repo"
  ok "$site registered"
  info "$repo"
}

cmd_list() {
  local any=0 p
  printf '%sSites%s\n\n' "$B" "$N"
  while IFS= read -r p; do
    any=1
    ( load_site "$p" 2>/dev/null \
      && printf '  %s%-16s%s %-10s %s\n     %s%s%s\n' "$B" "$site" "$N" "$type" "$live_url" "$DIM" "$p" "$N" ) \
      || printf '  %s%s%s %s(unreadable)%s\n' "$B" "$p" "$N" "$R" "$N"
  done < <(registry_list)
  [ "$any" = "1" ] || info "none yet — run: gd new"
}

usage() { awk 'NR<3 {next} /^#/ {sub(/^# ?/,""); print; next} {exit}' "$SELF"; }

# ================================================================== main =====
load_config

[ $# -ge 1 ] || { usage; exit 1; }
CMD="$1"; shift || true
ARGS=(); for a in "$@"; do case "$a" in -y|--yes) ASSUME_YES=1;; *) ARGS+=("$a");; esac; done
set -- "${ARGS[@]+"${ARGS[@]}"}"

for_all() {
  local c="$1" p
  while IFS= read -r p; do
    printf '\n%s---- %s ----%s\n' "$C" "$(basename "$p")" "$N"
    ( "$c" "$p" ) || warn "$(basename "$p") failed — continuing"
  done < <(registry_list)
}

case "$CMD" in
  new)       cmd_new ;;
  config)    cmd_config "$@" ;;
  adopt)     cmd_adopt "${1:-$PWD}" ;;
  list|ls)   cmd_list ;;
  sync|status|db)
             if [ "${1:-}" = "--all" ]; then for_all "cmd_$CMD"; else "cmd_$CMD"; fi ;;
  merge)     cmd_merge "${1:-develop}" ;;
  deploy)    cmd_deploy ;;
  up)        cmd_up ;;
  down)      cmd_down ;;
  shell)     cmd_shell ;;
  key)       cmd_key ;;
  secrets)   cmd_secrets ;;
  scaffold)  cmd_scaffold ;;
  help|-h|--help) usage ;;
  *)         die "Unknown command '$CMD' — try: gd help" ;;
esac
