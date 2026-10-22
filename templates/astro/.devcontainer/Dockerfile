FROM mcr.microsoft.com/devcontainers/javascript-node:22-bookworm

# rsync so you can deploy from inside the container if you want to;
# the rest is quality-of-life for debugging a static build.
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get install -y --no-install-recommends \
        rsync less jq \
    && rm -rf /var/lib/apt/lists/*

# pnpm and yarn via corepack, so whichever lockfile the site uses just works
RUN corepack enable

USER node
ENV ASTRO_TELEMETRY_DISABLED=1
