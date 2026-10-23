package migrate

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Run with installed PHP, or opt in to an already available Docker image:
// GD_SYNC_TEST_PHP_DOCKER=php:8.3-cli go test ./internal/migrate
func runPHP(t *testing.T, script string) string {
	t.Helper()
	var cmd *exec.Cmd
	if php, err := exec.LookPath("php"); err == nil {
		cmd = exec.Command(php)
	} else if image := os.Getenv("GD_SYNC_TEST_PHP_DOCKER"); image != "" {
		cmd = exec.Command("docker", "run", "--rm", "--network=none", "-i", image, "php")
	} else {
		t.Skip("PHP runner test needs php or GD_SYNC_TEST_PHP_DOCKER")
		return ""
	}
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PHP: %v\n%s", err, out)
	}
	return string(out)
}

// The harness supplies the WordPress interfaces used by the runner, while
// executing the actual generated PHP and migration closures in PHP itself.
const phpHarness = `<?php
const DB_NAME = 'test';
class WP_CLI { static function error($s) { throw new RuntimeException($s); } }
function wp_cache_delete($key, $group) {}
class Database {
 public $options = 'custom_options';
 public $last_error = '';
 public $revision = '';
 public $before = '';
 public $locked = false;
 public $deny_lock = false;
 public $fail_commit = false;
 public $fail_record = false;
 function prepare($sql, ...$args) { foreach ($args as $arg) { $sql = preg_replace('/%s/', "'".$arg."'", $sql, 1); } return $sql; }
 function get_var($sql) {
  if (strpos($sql, 'GET_LOCK') !== false) { $this->locked = !$this->deny_lock; return $this->locked ? '1' : '0'; }
  if (strpos($sql, 'RELEASE_LOCK') !== false) { $this->locked = false; return '1'; }
  return $this->revision;
 }
 function query($sql) {
  $this->last_error = '';
  if ($sql === 'START TRANSACTION') { $this->before = $this->revision; }
  if ($sql === 'ROLLBACK') { $this->revision = $this->before; }
  if ($sql === 'BROKEN QUERY' || ($this->fail_commit && $sql === 'COMMIT')) { $this->last_error = 'SQL failed'; return false; }
  if (strpos($sql, 'INSERT INTO') === 0) {
   if ($this->fail_record) { $this->last_error = 'Metadata write failed'; return false; }
   preg_match("/VALUES \\('[^']*', '([^']*)'/", $sql, $m); $this->revision = $m[1];
  }
  if (strpos($sql, 'DELETE FROM') === 0) { $this->revision = ''; }
  return 1;
 }
}
$wpdb = new Database();
`

func TestPHPRunner(t *testing.T) {
	for _, tt := range []struct{ name, body, setup, from, to, want string }{
		{"upgrade", "echo 'up;';", "", "", "aaaaaaaaaaaa", "OK:aaaaaaaaaaaa:free"},
		{"downgrade", "echo 'down;';", `$wpdb->revision='aaaaaaaaaaaa';`, "aaaaaaaaaaaa", "", "OK::free"},
		{"throws", "throw new RuntimeException('failed migration');", "", "", "aaaaaaaaaaaa", "ERROR::free"},
		{"sql_error", "gd_sync_query('BROKEN QUERY');", "", "", "aaaaaaaaaaaa", "ERROR::free"},
		{"metadata_failure", "", `$wpdb->fail_record=true;`, "", "aaaaaaaaaaaa", "ERROR::free"},
		{"commit_failure", "", `$wpdb->fail_commit=true;`, "", "aaaaaaaaaaaa", "ERROR::free"},
		{"revision_changed", "echo 'MUST NOT RUN';", `$wpdb->revision='bbbbbbbbbbbb';`, "", "aaaaaaaaaaaa", "ERROR:bbbbbbbbbbbb:free"},
		{"lock_busy", "echo 'MUST NOT RUN';", `$wpdb->deny_lock=true;`, "", "aaaaaaaaaaaa", "ERROR::free"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			src := []byte("<?php\n$upgrade = function () {" + tt.body + "};\n$downgrade = function () {" + tt.body + "};")
			step := Step{Migration: Migration{Revision: "aaaaaaaaaaaa", File: "aaaaaaaaaaaa_test.php", Source: src}, Up: tt.name != "downgrade", From: tt.from, To: tt.to}
			script := strings.TrimPrefix(Script(step), "<?php")
			code := phpHarness + tt.setup + "\ntry {\n" + script + "\necho 'OK';\n} catch (Throwable $e) { echo 'ERROR'; } echo ':' . $wpdb->revision . ':' . ($wpdb->locked ? 'locked' : 'free');"
			out := runPHP(t, code)
			if !strings.Contains(out, tt.want) || strings.Contains(out, "MUST NOT RUN") {
				t.Fatalf("wanted %s, got %s", tt.want, out)
			}
		})
	}
}
