package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s-visser/gd-sync/internal/migrate"
	"github.com/s-visser/gd-sync/internal/ui"
)

// This stand-in verifies the SSH orchestration. The PHP runner itself is
// exercised separately with PHP; this script never connects to a real DB.
func (w *world) fakeWP() {
	w.t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		w.t.Skip("needs python3")
	}
	w.write(w.srv.Home, "wp-cli.phar", `#!/usr/bin/env python3
import sys, os, json, re, base64
from pathlib import Path
args=sys.argv[1:]
root=Path.cwd()
state=root/'.fake-db.json'
log=root/'.wp-log'
with log.open('a') as f: f.write(json.dumps(args)+'\n')
db=json.loads(state.read_text()) if state.exists() else {'revision':'', 'value':'original'}
def save(): state.write_text(json.dumps(db))
if args[:2]==['db','export']:
 if (root/'.fail-backup').exists(): sys.exit(1)
 Path(args[2]).write_text(json.dumps(db))
elif args[:2]==['db','import']:
 db=json.load(sys.stdin); save()
elif args[:2]==['option','update']:
 db['value']=args[3];save()
elif args[:2]==['option','get']:
 print(db['value'])
elif args[0]=='eval':
 print('gd-sync-revision:'+db['revision'])
elif args[0]=='eval-file':
 script=sys.stdin.read()
 val=lambda name: re.search(r'\$gd_sync_'+name+r" = '([^']*)';",script).group(1)
 src=base64.b64decode(re.search(r"base64_decode\('([^']*)'\)",script).group(1)).decode()
 if db['revision']!=val('from'): sys.exit(2)
 if 'FAIL_MIGRATION' in src: sys.exit(3)
 # Deployment has uploaded the corresponding code before the migration.
 if 'REQUIRE_NEW_CODE' in src and (root/'wp-content/theme.php').read_text()!='new': sys.exit(4)
 db['revision']=val('to');save()
`)
	if err := os.Chmod(filepath.Join(w.srv.Home, "wp-cli.phar"), 0o755); err != nil {
		w.t.Fatal(err)
	}
}
func (w *world) dbRevision() string {
	var db map[string]string
	_ = json.Unmarshal([]byte(w.read(w.server, ".fake-db.json")), &db)
	return db["revision"]
}
func (w *world) addMigration(rev, down, extra string) {
	w.write(w.repo, migrate.Dir+"/"+migrate.FileName(rev, "test"), migrate.Template(rev, down, "Test", "2026-10-23")+"\n// "+extra)
}
func TestWPBackupAndRestore(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content", "")
	w.fakeWP()
	// Values remain one argument; shell syntax in them is never interpreted.
	value := "hello ' $(touch hacked); --yes"
	if err := run([]string{"-y", "wp", "option", "update", "example", value}); err != nil {
		t.Fatal(err)
	}
	ui.Yes = false
	if _, err := os.Stat(filepath.Join(w.server, "hacked")); !os.IsNotExist(err) {
		t.Fatal("WP value executed by shell")
	}
	backups, err := filepath.Glob(filepath.Join(w.srv.Home, "gd-sync-backups", "test-db-*.sql"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups %v: %v", backups, err)
	}
	w.mustGD("db", "--restore", filepath.Base(backups[0]))
	if !strings.Contains(w.read(w.server, ".fake-db.json"), "original") {
		t.Fatal("database not restored")
	}
	// Read-only built-in commands don't create another backup.
	before, _ := filepath.Glob(filepath.Join(w.srv.Home, "gd-sync-backups", "*.sql"))
	if err := run([]string{"wp", "option", "get", "example"}); err != nil {
		t.Fatal(err)
	}
	after, _ := filepath.Glob(filepath.Join(w.srv.Home, "gd-sync-backups", "*.sql"))
	if len(before) != len(after) {
		t.Fatal("read made backup")
	}
	w.write(w.server, ".fail-backup", "1")
	if err := run([]string{"-y", "wp", "option", "update", "example", "should not run"}); err == nil {
		t.Fatal("write continued after backup failure")
	}
	ui.Yes = false
	if strings.Contains(w.read(w.server, ".fake-db.json"), "should not run") {
		t.Fatal("write ran")
	}
}
func TestDeployMigrationsAndRetry(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content", "")
	w.fakeWP()
	w.write(w.server, "wp-content/theme.php", "old")
	w.mustGD("sync")
	w.write(w.repo, "wp-content/theme.php", "new")
	w.addMigration("aaaaaaaaaaaa", "", "REQUIRE_NEW_CODE")
	w.addMigration("bbbbbbbbbbbb", "aaaaaaaaaaaa", "FAIL_MIGRATION")
	w.addMigration("cccccccccccc", "bbbbbbbbbbbb", "")
	w.git("add", "-A")
	w.git("commit", "-qm", "Code and migrations")
	err := w.gd("deploy")
	if err == nil || !strings.Contains(err.Error(), "gd-sync db --restore") {
		t.Fatalf("expected recovery instructions: %v", err)
	}
	if w.dbRevision() != "aaaaaaaaaaaa" {
		t.Fatalf("revision after failure: %s", w.dbRevision())
	}
	if w.read(w.server, "wp-content/theme.php") != "new" {
		t.Fatal("files were not uploaded first")
	}
	if _, err := os.Stat(filepath.Join(w.server, "_migrations")); !os.IsNotExist(err) {
		t.Fatal("migration source uploaded to public web root")
	}
	if w.read(w.srv.Home, ".gd-sync/test/deployed") != "<missing>" {
		t.Fatal("failed deploy marked successful")
	}
	files, _ := filepath.Glob(filepath.Join(w.srv.Home, "gd-sync-backups", "*.tar.gz"))
	if len(files) != 1 {
		t.Fatal("no file backup")
	}
	w.addMigration("bbbbbbbbbbbb", "aaaaaaaaaaaa", "")
	w.git("add", "-A")
	w.git("commit", "-qm", "Fix migration")
	// The files now already match live. Pending migrations must still run.
	w.mustGD("deploy")
	if w.dbRevision() != "cccccccccccc" {
		t.Fatal(w.dbRevision())
	}
	log := w.read(w.server, ".wp-log")
	count := strings.Count(log, "eval-file")
	w.mustGD("deploy")
	if strings.Count(w.read(w.server, ".wp-log"), "eval-file") != count {
		t.Fatal("migrations ran twice")
	}
	// Explicit down revisions support real rollback and subsequent upgrade.
	w.mustGD("migrate", "downgrade", "base", "--live")
	if w.dbRevision() != "" {
		t.Fatal("downgrade did not reach base")
	}
	w.mustGD("migrate", "upgrade", "--live")
	if w.dbRevision() != "cccccccccccc" {
		t.Fatal("upgrade did not reach head")
	}
}
func TestDeployStopsBeforeUploadOnBackupFailure(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content", "")
	w.fakeWP()
	w.write(w.server, "wp-content/theme.php", "old")
	w.mustGD("sync")
	w.write(w.repo, "wp-content/theme.php", "new")
	w.addMigration("aaaaaaaaaaaa", "", "")
	w.git("add", "-A")
	w.git("commit", "-qm", "Code and migration")
	w.write(w.server, ".fail-backup", "1")
	if err := w.gd("deploy"); err == nil {
		t.Fatal("expected failure")
	}
	if w.read(w.server, "wp-content/theme.php") != "old" {
		t.Fatal("uploaded despite failed backup")
	}
}
func TestMigrateNewAndSnapshotSkip(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content", "")
	w.fakeWP()
	w.mustGD("migrate", "new", "First change")
	w.mustGD("migrate", "new", "Second change")
	if err := inSite(nil, w.repo, func(c *ctx) error {
		chain, err := c.migrations("")
		if err == nil && len(chain) != 2 {
			t.Fatalf("chain length %d", len(chain))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	w.git("add", "-A")
	w.git("commit", "-qm", "[server-sync] snapshot")
	w.mustGD("deploy")
	if w.read(w.server, ".wp-log") != "<missing>" {
		t.Fatal("snapshot ran WP-CLI")
	}
}
func TestWPReadOnly(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want bool
	}{
		{[]string{"option", "get", "home"}, true},
		{[]string{"plugin", "list", "--format=json"}, true},
		{[]string{"custom", "get"}, false},
		{[]string{"db", "query", "SELECT 1"}, false},
		{[]string{"option", "get", "home", "--exec=evil()"}, false},
		{[]string{"option", "update", "home", "value"}, false},
	} {
		if got := wpReadOnly(tt.args); got != tt.want {
			t.Errorf("%v: %v", tt.args, got)
		}
	}
}

func TestRestorePreservesOldestSelectedBackup(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content", "")
	w.fakeWP()
	selected := "test-db-20000101-000000.000000000.sql"
	for i := 0; i < 20; i++ {
		name := selected
		if i > 0 {
			name = "test-db-20000101-000000.0000000" + string(rune('a'+i)) + ".sql"
		}
		w.write(w.srv.Home, "gd-sync-backups/"+name, `{"revision":"", "value":"chosen"}`)
		old := time.Now().Add(-time.Duration(30-i) * time.Hour)
		if err := os.Chtimes(filepath.Join(w.srv.Home, "gd-sync-backups", name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	w.mustGD("db", "--restore", selected)
	if w.read(w.srv.Home, "gd-sync-backups/"+selected) == "<missing>" {
		t.Fatal("selected backup pruned before import")
	}
	if !strings.Contains(w.read(w.server, ".fake-db.json"), "chosen") {
		t.Fatal("wrong database restored")
	}
	for _, bad := range []string{"other-site-db-123.sql", "../test-db-123.sql", "/tmp/test-db-123.sql"} {
		if err := w.gd("db", "--restore", bad); err == nil {
			t.Errorf("accepted backup %s", bad)
		}
	}
}
func TestFileBackupFailureStopsDeploy(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content", "")
	w.write(w.server, "wp-content/theme.php", "old")
	w.mustGD("sync")
	w.write(w.repo, "wp-content/theme.php", "new")
	w.git("add", "-A")
	w.git("commit", "-qm", "New code")
	// A file occupying the backup directory forces archive creation to fail.
	w.write(w.srv.Home, "gd-sync-backups", "blocked")
	if err := w.gd("deploy"); err == nil || !strings.Contains(err.Error(), "File backup failed") {
		t.Fatalf("expected backup failure: %v", err)
	}
	if w.read(w.server, "wp-content/theme.php") != "old" {
		t.Fatal("uploaded without file backup")
	}
}
func TestLocalWPAndMigrateNeverConnectToLive(t *testing.T) {
	w := newWorld(t, "wordpress", "wp-content", "")
	w.fakeWP()
	bin := t.TempDir()
	src, err := os.ReadFile(filepath.Join(w.srv.Home, "wp-cli.phar"))
	if err != nil {
		t.Fatal(err)
	}
	src = []byte(strings.Replace(string(src), "args=sys.argv[1:]", "args=sys.argv[1:]\nassert args.pop(0) == '--path=/var/www/html'", 1))
	if err := os.WriteFile(filepath.Join(bin, "wp"), src, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GD_SYNC_CONTAINER", "1")
	w.addMigration("aaaaaaaaaaaa", "", "")
	w.mustGD("migrate")
	if err := run([]string{"wp", "--local", "option", "update", "example", "--yes"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.read(w.repo, ".fake-db.json"), "aaaaaaaaaaaa") {
		t.Fatal("local migration not applied")
	}
	if !strings.Contains(w.read(w.repo, ".fake-db.json"), "--yes") {
		t.Fatal("WP-CLI flag value consumed by gd-sync")
	}
	if w.read(w.server, ".wp-log") != "<missing>" {
		t.Fatal("local operation reached live")
	}
}
