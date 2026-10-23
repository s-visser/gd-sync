ARG GD_SYNC_VERSION=latest

# gd-sync itself, the version that wrote this folder (GD_SYNC_VERSION in .env),
# so it runs inside the container too. Straight from GitHub, not the Go proxy,
# which can answer "latest" with an older commit for a while. Best effort: the
# site builds without it.
FROM golang:1 AS gd-sync
ARG GD_SYNC_VERSION
RUN set -eux; mkdir -p /out; \
    flags=; [ "$GD_SYNC_VERSION" = latest ] || flags="-X github.com/s-visser/gd-sync/internal/app.Version=$GD_SYNC_VERSION"; \
    CGO_ENABLED=0 GOBIN=/out GOPRIVATE=github.com/s-visser/gd-sync go install -ldflags "-s -w $flags" github.com/s-visser/gd-sync/cmd/gd-sync@"$GD_SYNC_VERSION" \
      || echo "gd-sync $GD_SYNC_VERSION could not be installed — skipped"

FROM mcr.microsoft.com/devcontainers/javascript-node:22-bookworm

# Quality-of-life tools for debugging a static build, and git, ssh and gh for
# the IDE and gd-sync, which also work from inside here.
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get install -y --no-install-recommends \
        less jq git openssh-client \
    && rm -rf /var/lib/apt/lists/*
COPY gh-install.sh /usr/local/bin/gh-install
RUN sh /usr/local/bin/gh-install && git config --system --add safe.directory '*'
COPY --from=gd-sync /out/ /usr/local/bin/

# pnpm and yarn via corepack, so whichever lockfile the site uses just works
RUN corepack enable

# The gh folder exists up front so the shared gh volume belongs to node.
RUN install -d -o node -g node /home/node/.config /home/node/.config/gh

USER node
ENV ASTRO_TELEMETRY_DISABLED=1
