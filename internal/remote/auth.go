package remote

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/s-visser/gd-sync/internal/config"
	"github.com/s-visser/gd-sync/internal/ui"
)

// KeyFiles are the private keys tried, in order, from ~/.ssh.
var KeyFiles = []string{"id_ed25519", "id_ecdsa", "id_rsa"}

// SSHDir is ~/.ssh (%USERPROFILE%\.ssh on Windows).
func SSHDir() string { return filepath.Join(config.UserHome(), ".ssh") }

func authMethods(user, host string) []ssh.AuthMethod {
	var methods []ssh.AuthMethod
	if a := dialAgent(); a != nil {
		methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(a).Signers))
	}
	methods = append(methods,
		ssh.PublicKeysCallback(keyFileSigners),
		ssh.RetryableAuthMethod(ssh.PasswordCallback(func() (string, error) {
			return ui.Secret(fmt.Sprintf("Password for %s@%s: ", user, host))
		}), 3),
		ssh.RetryableAuthMethod(ssh.KeyboardInteractive(func(_, _ string, qs []string, echo []bool) ([]string, error) {
			answers := make([]string, len(qs))
			for i, q := range qs {
				if echo[i] {
					answers[i] = ui.Ask(q, "")
				} else {
					a, err := ui.Secret(q)
					if err != nil {
						return nil, err
					}
					answers[i] = a
				}
			}
			return answers, nil
		}), 3),
	)
	return methods
}

// keyFileSigners loads the key files that exist, asking for a passphrase
// when one is protected. Only called if the agent didn't get us in.
func keyFileSigners() ([]ssh.Signer, error) {
	var signers []ssh.Signer
	for _, name := range KeyFiles {
		path := filepath.Join(SSHDir(), name)
		pem, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		s, err := ssh.ParsePrivateKey(pem)
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			pass, perr := ui.Secret(fmt.Sprintf("Passphrase for %s: ", path))
			if perr != nil {
				continue
			}
			s, err = ssh.ParsePrivateKeyWithPassphrase(pem, []byte(pass))
		}
		if err != nil {
			ui.Warn("Skipping %s: %v", path, err)
			continue
		}
		signers = append(signers, s)
	}
	return signers, nil
}

// hostKeyCheck returns a host key callback with "accept new" behaviour, and
// the key algorithms already on file for this host, so the server is asked
// for the key we know rather than one we'd wrongly call a mismatch.
func hostKeyCheck(addr string) (ssh.HostKeyCallback, []string, error) {
	file := filepath.Join(SSHDir(), "known_hosts")
	if err := os.MkdirAll(SSHDir(), 0o700); err != nil {
		return nil, nil, err
	}
	if f, err := os.OpenFile(file, os.O_CREATE, 0o600); err == nil {
		f.Close()
	}
	known, err := knownhosts.New(file)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", file, err)
	}

	var algos []string
	var ke *knownhosts.KeyError
	if err := known(addr, &net.TCPAddr{}, probeKey{}); errors.As(err, &ke) {
		for _, k := range ke.Want {
			algos = append(algos, algorithmsFor(k.Key.Type())...)
		}
	}

	cb := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := known(hostname, remote, key)
		var ke *knownhosts.KeyError
		if !errors.As(err, &ke) {
			return err
		}
		if len(ke.Want) > 0 {
			return fmt.Errorf(`The host key for %s has CHANGED since you last connected.
  This may mean someone is intercepting the connection, or the server was rebuilt.
  If you're sure it's legitimate, remove the old entry from %s`, hostname, file)
		}
		line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
		f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString(line + "\n")
		ui.Info("Added %s to known hosts (%s)", hostname, ssh.FingerprintSHA256(key))
		return err
	}
	return cb, algos, nil
}

// probeKey is a key no host has, used to read which keys are on file.
type probeKey struct{}

func (probeKey) Type() string                        { return "gd-sync-probe" }
func (probeKey) Marshal() []byte                     { return []byte("gd-sync-probe") }
func (probeKey) Verify([]byte, *ssh.Signature) error { return errors.New("probe") }

func algorithmsFor(keyType string) []string {
	if keyType == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{keyType}
}
