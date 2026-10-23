FROM mcr.microsoft.com/devcontainers/php:8.3-bookworm

# Tools the workflow needs: mysql client for DB work, node for any build
# step a theme might have.
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get install -y --no-install-recommends \
        default-mysql-client less jq unzip \
    && rm -rf /var/lib/apt/lists/*

# WP-CLI
RUN curl -sSL -o /usr/local/bin/wp \
      https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar \
    && chmod +x /usr/local/bin/wp

# Composer (themes/plugins with dependencies)
RUN curl -sSL https://getcomposer.org/installer | php -- \
      --install-dir=/usr/local/bin --filename=composer

USER vscode

# WP-CLI needs to know where WordPress lives and how to reach the DB
ENV WP_CLI_CACHE_DIR=/home/vscode/.wp-cli/cache
