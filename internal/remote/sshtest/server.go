// Package sshtest is an SSH server for tests. It accepts any public key and
// runs each command with bash in a home folder of its own, standing in for a
// cPanel server.
package sshtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

type Server struct {
	Host, Port string
	Home       string // the server user's home folder

	listener net.Listener
	wg       sync.WaitGroup
	gnuTar   bool
}

// Start runs a server until the test ends.
func Start(t testing.TB) *Server { return start(t, false) }

// StartWithHostCert runs a server that also offers a CA-signed certificate
// of its host key, as GoDaddy's servers do.
func StartWithHostCert(t testing.TB) *Server { return start(t, true) }

func start(t testing.TB, hostCert bool) *Server {
	t.Helper()
	signer := newSigner(t)
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil },
	}
	cfg.AddHostKey(signer)
	if hostCert {
		cert := &ssh.Certificate{Key: signer.PublicKey(), CertType: ssh.HostCert, ValidBefore: ssh.CertTimeInfinity}
		if err := cert.SignCert(rand.Reader, newSigner(t)); err != nil {
			t.Fatal(err)
		}
		certSigner, err := ssh.NewCertSigner(cert, signer)
		if err != nil {
			t.Fatal(err)
		}
		cfg.AddHostKey(certSigner)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(l.Addr().String())
	out, _ := exec.Command("tar", "--version").Output()
	s := &Server{Host: host, Port: port, Home: t.TempDir(), listener: l, gnuTar: strings.Contains(string(out), "GNU")}
	go s.serve(cfg)
	t.Cleanup(func() { l.Close(); s.wg.Wait() })
	return s
}

func newSigner(t testing.TB) ssh.Signer {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func (s *Server) serve(cfg *ssh.ServerConfig) {
	for {
		c, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(c, cfg)
		}()
	}
}

func (s *Server) handle(c net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "sessions only")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go s.session(ch, creqs)
	}
}

func (s *Server) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			req.Reply(false, nil)
			continue
		}
		var p struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &p); err != nil {
			req.Reply(false, nil)
			return
		}
		req.Reply(true, nil)
		ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{s.run(p.Command, ch)}))
		return
	}
}

func (s *Server) run(command string, ch ssh.Channel) uint32 {
	if !s.gnuTar { // BSD tar (macOS) lacks this GNU option; it only affects folder modes
		command = strings.ReplaceAll(command, "--no-overwrite-dir", "")
	}
	cmd := exec.Command("bash", "-c", command)
	cmd.Dir = s.Home
	cmd.Env = append(os.Environ(), "HOME="+s.Home)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = ch, ch, ch.Stderr()
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return uint32(ee.ExitCode())
	}
	if err != nil {
		return 127
	}
	return 0
}
