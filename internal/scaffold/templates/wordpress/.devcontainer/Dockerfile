# One image for the web server and the workspace, on the PHP version the live
# site runs (PHP_VERSION in .gd-sync), so wp-cli and the site share the same
# PHP and extensions. Old versions build too: apt-install finds their Debian
# release on archive.debian.org, and older tools are picked to match.
ARG PHP_VERSION=8.3

# Debian's current signing keys: the archive re-signs old releases with them.
FROM debian:bookworm-slim AS keys

FROM wordpress:php${PHP_VERSION}-apache

COPY --from=keys /usr/share/keyrings/debian-archive-keyring.gpg /usr/share/gd-sync/debian-archive-keyring.gpg
COPY apt-install.sh /usr/local/bin/apt-install
RUN set -eux; \
    sh /usr/local/bin/apt-install less unzip mysql-client; \
    rm -rf /var/lib/apt/lists/*

# WP-CLI and Composer: the newest that still run on this PHP.
RUN set -eux; \
    id=$(php -r 'echo PHP_VERSION_ID;'); \
    if [ "$id" -ge 70224 ]; then wp=https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar; \
    else wp=https://github.com/wp-cli/wp-cli/releases/download/v2.10.0/wp-cli-2.10.0.phar; fi; \
    php -r "copy('$wp', '/usr/local/bin/wp');"; chmod +x /usr/local/bin/wp; \
    php -r "copy('https://getcomposer.org/installer', '/tmp/composer-setup.php');"; \
    if [ "$id" -ge 70205 ]; then v=; else v=--2.2; fi; \
    php /tmp/composer-setup.php $v --install-dir=/usr/local/bin --filename=composer; \
    rm /tmp/composer-setup.php

# Xdebug for the IDE, the newest release for this PHP. Best effort: the site
# runs fine without it. It only starts when asked (XDEBUG_MODE).
RUN set -eux; \
    id=$(php -r 'echo PHP_VERSION_ID;'); \
    if   [ "$id" -ge 80000 ]; then x=xdebug; \
    elif [ "$id" -ge 70200 ]; then x=xdebug-3.1.6; \
    elif [ "$id" -ge 70100 ]; then x=xdebug-2.9.8; \
    elif [ "$id" -ge 70000 ]; then x=xdebug-2.7.2; \
    else x=xdebug-2.5.5; fi; \
    if sh /usr/local/bin/apt-install $PHPIZE_DEPS; then \
        pecl install "$x" && docker-php-ext-enable xdebug || echo "Xdebug not available for this PHP — skipped"; \
        apt-get purge -y --auto-remove $PHPIZE_DEPS || true; \
    fi; \
    rm -rf /var/lib/apt/lists/* /tmp/pear
ENV XDEBUG_MODE=off

# The user the IDE works as in the workspace container.
RUN id vscode >/dev/null 2>&1 || useradd -m -s /bin/bash -u 1000 vscode
ENV WP_CLI_CACHE_DIR=/home/vscode/.wp-cli/cache
