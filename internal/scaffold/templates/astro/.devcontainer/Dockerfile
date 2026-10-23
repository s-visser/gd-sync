FROM mcr.microsoft.com/devcontainers/javascript-node:22-bookworm

# Quality-of-life tools for debugging a static build.
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get install -y --no-install-recommends \
        less jq \
    && rm -rf /var/lib/apt/lists/*

# pnpm and yarn via corepack, so whichever lockfile the site uses just works
RUN corepack enable

USER node
ENV ASTRO_TELEMETRY_DISABLED=1
