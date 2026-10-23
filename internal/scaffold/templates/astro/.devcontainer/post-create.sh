#!/usr/bin/env bash
set -euo pipefail
cd /workspace

sudo chown -R node:node /workspace/node_modules 2>/dev/null || true

# Pick the package manager the repo actually uses
if   [ -f pnpm-lock.yaml ]; then PM=pnpm;  INSTALL="pnpm install"
elif [ -f yarn.lock ];      then PM=yarn;  INSTALL="yarn install"
else                             PM=npm;   INSTALL="npm install"
fi

if [ -f package.json ]; then
  echo "==> Installing dependencies with $PM"
  $INSTALL
else
  echo "==> No package.json yet — scaffolding a new Astro site"
  npm create astro@latest . -- --template minimal --no-install --no-git --skip-houston --yes
  npm install
fi

# Astro must listen on 0.0.0.0 to be reachable from the host
if [ -f astro.config.mjs ] && ! grep -q '0\.0\.0\.0' astro.config.mjs; then
  echo "==> Note: add  server: { host: '0.0.0.0' }  to astro.config.mjs"
  echo "    (or always start with: npm run dev -- --host 0.0.0.0)"
fi

# git pushes over https with the shared gh login, once there is one.
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
  gh auth setup-git >/dev/null 2>&1 || true
  GH_NOTE="GitHub       logged in (gh)"
else
  GH_NOTE="GitHub       not logged in yet — run once: gh auth login && gh auth setup-git"
fi

cat <<EOF

  Ready.
    Dev server   npm run dev -- --host 0.0.0.0     -> ${LOCAL_URL:-http://localhost:4321}
    Real build   npm run build                      -> ./dist
    Preview it   npm run preview -- --host 0.0.0.0 --port 4322  -> http://localhost:${PREVIEW_PORT:-4322}
    $GH_NOTE

  Deploys ship ./dist to the server's web root. Push to main and the
  worker builds and uploads it for you.
EOF
