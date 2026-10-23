<?php
// The local site's wp-config.php. The devcontainer mounts it over the live
// one, which stays untouched in the folder — it holds the live database
// password and only works on the server. Written by gd-sync; regenerate with:
// gd-sync scaffold

// Use the live site's table prefix, so the imported database just works.
$table_prefix = 'wp_';
$live = @file_get_contents('/gd-sync/site/wp-config.php');
if ($live && preg_match('/^\s*\$table_prefix\s*=\s*[\'"]([A-Za-z0-9_]+)[\'"]/m', $live, $m)) {
	$table_prefix = $m[1];
}

define('DB_NAME', 'wordpress');
define('DB_USER', 'wordpress');
define('DB_PASSWORD', 'wordpress');
define('DB_HOST', 'db');
define('DB_CHARSET', 'utf8mb4');
define('DB_COLLATE', '');

// Local only, so fixed keys are fine.
foreach (['AUTH_KEY', 'SECURE_AUTH_KEY', 'LOGGED_IN_KEY', 'NONCE_KEY',
          'AUTH_SALT', 'SECURE_AUTH_SALT', 'LOGGED_IN_SALT', 'NONCE_SALT'] as $k) {
	define($k, 'gd-sync-local-' . $k);
}

$local = getenv('LOCAL_URL') ?: 'http://localhost:8080';
define('WP_HOME', $local);
define('WP_SITEURL', $local);

define('WP_DEBUG', true);
define('WP_DEBUG_LOG', true);
define('WP_DEBUG_DISPLAY', false);
// Under WP-CLI a fatal error otherwise only says "There has been a critical
// error". Print the real one instead; notices and deprecations stay in the log.
if (defined('WP_CLI') && WP_CLI) {
	define('WP_DISABLE_FATAL_ERROR_HANDLER', true);
	register_shutdown_function(function () {
		$e = error_get_last();
		if ($e && in_array($e['type'], [E_ERROR, E_PARSE, E_CORE_ERROR, E_COMPILE_ERROR, E_USER_ERROR], true)) {
			fwrite(STDERR, "PHP Fatal error: {$e['message']} in {$e['file']}:{$e['line']}\n");
		}
	});
}
define('DISALLOW_FILE_EDIT', true);
define('AUTOMATIC_UPDATER_DISABLED', true);
define('WP_ENVIRONMENT_TYPE', 'local');
define('FS_METHOD', 'direct');
/* send all mail to Mailpit instead of the outside world */
define('WP_MAIL_SMTP_HOST', 'mailpit');
define('WP_MAIL_SMTP_PORT', 1025);

if (!defined('ABSPATH')) {
	define('ABSPATH', __DIR__ . '/');
}
require_once ABSPATH . 'wp-settings.php';
