package vet

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestScan(t *testing.T) {
	dir := t.TempDir()
	var files []File
	add := func(p string, content []byte) {
		local := filepath.Join(dir, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(local), 0o755)
		os.WriteFile(local, content, 0o644)
		files = append(files, File{Path: p, Local: local, Size: int64(len(content))})
	}
	big := append([]byte{0}, bytes.Repeat([]byte("x"), LargeBinary)...)
	plugin := "wp-content/plugins/shop/"

	add(plugin+"composer.json", []byte(`{"require":{}}`))
	add(plugin+"vendor/autoload.php", []byte("<?php"))
	add(plugin+"vendor/google/auth/src/Credentials/ServiceAccountCredentials.php", []byte("<?php"))
	add(plugin+"vendor/grpc/bin/plugin", big)
	add("wp-content/themes/t/lib/vendor/x.js", []byte("x"))
	add("wp-content/themes/kept/vendor/new.js", []byte("x"))
	add(plugin+"auth/gtranslate.json", []byte(`{"type": "service_account", "project_id": "p"}`))
	add(plugin+"keys/server.key", []byte("anything"))
	add(plugin+".env.production", []byte("A=1"))
	add(plugin+".env.example", []byte("A="))
	add(plugin+"classes/ServiceAccountCredentials.php", []byte("<?php class X {}"))
	add(plugin+"certs/roots.pem", []byte("-----BEGIN CERTIFICATE-----\n"))
	add(plugin+"inc/config.php", []byte("<?php $k = '-----BEGIN RSA PRIVATE KEY-----';"))
	add(plugin+"credentials.yml", []byte("user: x"))
	add("wp-content/themes/t/hero.mp4", big)
	add("wp-content/themes/t/icon.png", []byte{0x89, 'P', 'N', 'G', 0})
	add("wp-content/themes/t/big.css", bytes.Repeat([]byte("a{}"), LargeBinary))
	add("vendor/top-level.php", []byte("<?php"))

	has := map[string]bool{}
	for _, f := range files {
		has[f.Path] = true
	}
	r := Scan(files, func(p string) bool { return has[p] }, func(d string) bool { return d == "wp-content/themes/kept/vendor" })

	var vendors []string
	for _, v := range r.Vendors {
		vendors = append(vendors, v.Path)
	}
	if want := []string{"wp-content/plugins/shop/vendor", "wp-content/themes/t/lib/vendor"}; !reflect.DeepEqual(vendors, want) {
		t.Errorf("vendors = %q, want %q", vendors, want)
	}
	if !r.Vendors[0].Composer || r.Vendors[1].Composer || len(r.Vendors[0].Files) != 3 {
		t.Errorf("vendor details: %+v", r.Vendors)
	}
	wantCreds := []string{
		plugin + ".env.production", plugin + "auth/gtranslate.json", plugin + "credentials.yml",
		plugin + "inc/config.php", plugin + "keys/server.key",
	}
	if !reflect.DeepEqual(r.Credentials, wantCreds) {
		t.Errorf("credentials = %q, want %q", r.Credentials, wantCreds)
	}
	if len(r.Binaries) != 1 || r.Binaries[0].Path != "wp-content/themes/t/hero.mp4" {
		t.Errorf("binaries = %+v", r.Binaries)
	}
}
