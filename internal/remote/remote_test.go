package remote_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
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

// An agent that's running but doesn't hold the server's key — the usual
// state on a Mac — must not stop the key files from being tried.
func TestKeyFileUsedAfterEmptyAgent(t *testing.T) {
	setupHome(t)
	srv := sshtest.Start(t)
	dir, err := os.MkdirTemp("/tmp", "agent") // short: socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go agent.ServeAgent(agent.NewKeyring(), c)
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", sock)

	conn, err := remote.Dial(srv.Host, "test", srv.Port)
	if err != nil {
		t.Fatalf("with an empty agent running: %v", err)
	}
	conn.Close()
}

// refusingAgent holds a key but won't sign with it, like the agent a
// JetBrains IDE runs in a devcontainer while its "which key?" dialog is open.
type refusingAgent struct{ agent.Agent }

func (refusingAgent) Sign(ssh.PublicKey, []byte) (*ssh.Signature, error) {
	return nil, errors.New("refused")
}

// A key file that needs no passphrase is used on its own: an agent that
// refuses to sign must not push the login on to a password prompt, and isn't
// even asked.
func TestKeyFileUsedBeforeRefusingAgent(t *testing.T) {
	setupHome(t)
	srv := sshtest.Start(t)
	dir, err := os.MkdirTemp("/tmp", "agent")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	keyring := agent.NewKeyring()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	keyring.Add(agent.AddedKey{PrivateKey: priv})
	var asked atomic.Bool
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			asked.Store(true)
			go agent.ServeAgent(refusingAgent{keyring}, c)
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", sock)

	conn, err := remote.Dial(srv.Host, "test", srv.Port)
	if err != nil {
		t.Fatalf("with a refusing agent running: %v", err)
	}
	if conn.Typed {
		t.Error("a password was asked for")
	}
	conn.Close()
	if asked.Load() {
		t.Error("the agent was asked, though the key file got in")
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
