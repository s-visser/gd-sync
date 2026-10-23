// Package remote talks to the server over SSH. One connection is opened per
// run and every command gets its own session on it, so a password is asked
// for at most once.
package remote

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type Conn struct {
	client *ssh.Client
	Target string // user@host:port, for messages
	Typed  bool   // a password was typed: no SSH key got in
}

// Dial connects and authenticates: the key files in ~/.ssh that need no
// passphrase first, on their own. Only if none of them gets in does it go on
// to the SSH agent, the passphrase-protected key files, then a password
// prompt. Unknown host keys are added to ~/.ssh/known_hosts; a changed one is
// refused.
//
// The plain key files go first because an agent can stand in the way: the
// one a JetBrains IDE runs inside a devcontainer asks you which key to hand
// over, and a signature it refuses ends the key login, so it would skip
// straight to the password.
func Dial(host, user, port string) (*Conn, error) {
	addr := net.JoinHostPort(host, port)
	hostKey, algos, err := hostKeyCheck(addr)
	if err != nil {
		return nil, err
	}
	cfg := func(auth ...ssh.AuthMethod) *ssh.ClientConfig {
		return &ssh.ClientConfig{
			User:              user,
			Auth:              auth,
			HostKeyCallback:   hostKey,
			HostKeyAlgorithms: algos,
			Timeout:           20 * time.Second,
		}
	}
	target := user + "@" + addr
	if plain := plainKeySigners(); len(plain) > 0 {
		client, err := ssh.Dial("tcp", addr, cfg(ssh.PublicKeys(plain...)))
		if err == nil {
			return &Conn{client: client, Target: target}, nil
		}
		if !strings.Contains(err.Error(), "unable to authenticate") {
			return nil, fmt.Errorf("Cannot reach %s: %w", target, err)
		}
	}
	var typed bool
	client, err := ssh.Dial("tcp", addr, cfg(authMethods(user, host, &typed)...))
	if err != nil {
		return nil, fmt.Errorf("Cannot reach %s: %w", target, err)
	}
	return &Conn{client: client, Target: target, Typed: typed}, nil
}

func (c *Conn) Close() error { return c.client.Close() }

// Run runs a shell command on the server. stdin and stdout may be nil; the
// server's stderr goes to ours.
func (c *Conn) Run(cmd string, stdin io.Reader, stdout io.Writer) error {
	s, err := c.client.NewSession()
	if err != nil {
		return err
	}
	defer s.Close()
	s.Stdin, s.Stdout, s.Stderr = stdin, stdout, os.Stderr
	if stdout == nil {
		s.Stdout = io.Discard
	}
	return s.Run(cmd)
}

// Output runs a command and returns its stdout.
func (c *Conn) Output(cmd string, stdin io.Reader) ([]byte, error) {
	var b bytes.Buffer
	err := c.Run(cmd, stdin, &b)
	return b.Bytes(), err
}

// Quote makes s a single shell word for the server's POSIX shell.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QuoteAll quotes each word and joins them with spaces.
func QuoteAll(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = Quote(w)
	}
	return strings.Join(q, " ")
}
