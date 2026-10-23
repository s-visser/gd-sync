package remote_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/s-visser/gd-sync/internal/remote"
	"github.com/s-visser/gd-sync/internal/remote/sshtest"
)

func setupHome(t *testing.T) string {
	if runtime.GOOS == "windows" {
		t.Skip("the test server runs commands with bash")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SSH_AUTH_SOCK", "")
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), pem.EncodeToMemory(block), 0o600)
	return home
}

func TestRunAndAcceptNewHost(t *testing.T) {
	home := setupHome(t)
	srv := sshtest.Start(t)

	conn, err := remote.Dial(srv.Host, "test", srv.Port)
	if err != nil {
		t.Fatal(err)
	}
	out, err := conn.Output("cat; echo "+remote.Quote("it's quoted"), strings.NewReader("from stdin\n"))
	conn.Close()
	if err != nil || string(out) != "from stdin\nit's quoted\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	kh, _ := os.ReadFile(filepath.Join(home, ".ssh", "known_hosts"))
	if !strings.Contains(string(kh), srv.Port) {
		t.Errorf("host not added to known_hosts: %q", kh)
	}

	// The second connection checks against what was recorded.
	conn, err = remote.Dial(srv.Host, "test", srv.Port)
	if err != nil {
		t.Fatalf("reconnecting to a known host: %v", err)
	}
	conn.Close()
}

func TestAcceptNewHostOfferingCertificate(t *testing.T) {
	home := setupHome(t)
	srv := sshtest.StartWithHostCert(t)

	for range 2 {
		conn, err := remote.Dial(srv.Host, "test", srv.Port)
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}
	kh, _ := os.ReadFile(filepath.Join(home, ".ssh", "known_hosts"))
	if strings.Contains(string(kh), "cert") || strings.Count(string(kh), "\n") != 1 {
		t.Errorf("known_hosts should hold the one plain host key: %q", kh)
	}
}

func TestRefuseChangedHostKey(t *testing.T) {
	home := setupHome(t)
	srv := sshtest.Start(t)

	other, _, _ := ed25519.GenerateKey(rand.Reader)
	pub, _ := ssh.NewPublicKey(other)
	addr := knownhosts.Normalize(net.JoinHostPort(srv.Host, srv.Port))
	os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(knownhosts.Line([]string{addr}, pub)+"\n"), 0o600)

	conn, err := remote.Dial(srv.Host, "test", srv.Port)
	if err == nil {
		conn.Close()
		t.Fatal("connected despite a changed host key")
	}
	if !strings.Contains(err.Error(), "CHANGED") {
		t.Errorf("err = %v", err)
	}
}
