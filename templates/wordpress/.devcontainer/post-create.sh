#!/usr/bin/env bash
# Runs once when the devcontainer is created.
# Gets you from "empty container" to "working copy of the live site" unattended.
set -euo pipefail

WP=/var/www/html
LOCAL_URL="${LOCAL_URL:-http://localhost:8080}"
DB=(--dbhost=db --dbname=wordpress --dbuser=wordpress --dbpass=wordpress)
wpc() { wp --path="$WP" --allow-root "$@"; }

echo "==> Waiting for the database"
for i in $(seq 1 60); do
  mysqladmin ping -h db -u wordpress -pwordpress --silent >/dev/null 2>&1 && break
  sleep 2
done

echo "==> Waiting for WordPress core to unpack"
for i in $(seq 1 60); do [ -f "$WP/wp-settings.php" ] && break; sleep 2; done

if [ ! -f "$WP/wp-config.php" ]; then
  echo "==> Writing wp-config.php"
  wpc config create "${DB[@]}" --skip-check --force \
    --extra-php <<'PHP'
define('WP_DEBUG', true);
define('WP_DEBUG_LOG', true);
define('WP_DEBUG_DISPLAY', false);
define('DISALLOW_FILE_EDIT', true);
define('AUTOMATIC_UPDATER_DISABLED', true);
define('WP_ENVIRONMENT_TYPE', 'local');
PHP
fi

# ---------------------------------------------------------------------------
# If gd pulled a database dump, use it. Otherwise install a blank site.
# ---------------------------------------------------------------------------
DUMP="$(ls -1t /workspace/_db/*.sql 2>/dev/null | head -1 || true)"

if [ -n "$DUMP" ]; then
  echo "==> Importing the live database: $(basename "$DUMP")"
  wpc db import "$DUMP"

  if [ -n "${LIVE_URL:-}" ]; then
    echo "==> Rewriting URLs: $LIVE_URL -> $LOCAL_URL"
    lh="${LIVE_URL#http://}"; lh="${lh#https://}"; lh="${lh%/}"
    dh="${LOCAL_URL#http://}"; dh="${dh#https://}"; dh="${dh%/}"
    wpc search-replace "$LIVE_URL" "$LOCAL_URL" --all-tables --skip-columns=guid --quiet || true
    wpc search-replace "//$lh" "//$dh"          --all-tables --skip-columns=guid --quiet || true
    wpc option update home    "$LOCAL_URL" --quiet || true
    wpc option update siteurl "$LOCAL_URL" --quiet || true
  fi

  echo "==> Creating a local admin (dev / dev)"
  wpc user create dev dev@example.test --role=administrator --user_pass=dev --quiet 2>/dev/null \
    || wpc user update dev --user_pass=dev --quiet 2>/dev/null || true

elif ! wpc core is-installed 2>/dev/null; then
  echo "==> No dump found — installing a blank WordPress"
  wpc core install \
    --url="$LOCAL_URL" --title="Local dev" \
    --admin_user=dev --admin_password=dev --admin_email=dev@example.test \
    --skip-email
fi

# ---------------------------------------------------------------------------
# Local-only hygiene: nothing should reach the outside world from here.
# ---------------------------------------------------------------------------
echo "==> Making the local copy safe to poke at"
wpc config set WP_ENVIRONMENT_TYPE local --quiet 2>/dev/null || true
# discourage search engines, kill caching/CDN plugins that fight local dev
wpc option update blog_public 0 --quiet 2>/dev/null || true
for p in wordfence w3-total-cache wp-super-cache wp-rocket really-simple-ssl; do
  wpc plugin deactivate "$p" --quiet 2>/dev/null || true
done

# activate whatever we're actually here to work on
for dir in "$WP"/wp-content/plugins/*/; do
  name="$(basename "$dir")"
  [ -f "/workspace/wp-content/plugins/$name/$name.php" ] \
    || [ -d "/workspace/wp-content/plugins/$name" ] || continue
  wpc plugin activate "$name" --quiet 2>/dev/null \
    && echo "    activated plugin: $name" || true
done

chmod -R a+rwX "$WP/wp-content/uploads" 2>/dev/null || true

cat <<EOF

  Ready.
    Site       $LOCAL_URL
    Admin      $LOCAL_URL/wp-admin   (dev / dev)
    Mail       http://localhost:8025
    Database   127.0.0.1:13306  wordpress / wordpress

  Your repo is mounted live — edit in the IDE, refresh the browser.
EOF
