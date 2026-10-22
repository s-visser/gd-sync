package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/s-visser/gd-sync/internal/remote"
	"github.com/s-visser/gd-sync/internal/ui"
)

// cmdDB downloads the live database into _db/ and, if the devcontainer is
// running, loads it there.
func cmdDB(c *ctx) error {
	if c.s.Type != "wordpress" {
		ui.Info("%s has no database.", c.s.Name)
		return nil
	}
	conn, err := c.server()
	if err != nil {
		return err
	}
	if err := ensureWPCLI(conn); err != nil {
		return err
	}
	dbDir := filepath.Join(c.s.Repo, "_db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return err
	}
	dump := filepath.Join(dbDir, fmt.Sprintf("%s-%s.sql", c.s.Name, stamp("20060102-150405")))

	ui.Step("Exporting the live database")
	f, err := os.Create(dump)
	if err != nil {
		return err
	}
	err = conn.Run("cd "+remote.Quote(c.s.RemoteRoot)+" && ~/wp-cli.phar db export - --single-transaction --quick --default-character-set=utf8mb4 --skip-plugins --skip-themes", nil, f)
	f.Close()
	st, serr := os.Stat(dump)
	if err != nil || serr != nil || st.Size() == 0 {
		os.Remove(dump)
		if err != nil {
			return fmt.Errorf("Export failed: %w", err)
		}
		return errors.New("Export produced an empty file")
	}
	ui.OK("%s -> _db/%s", humanSize(st.Size()), filepath.Base(dump))
	pruneDumps(dbDir, c.s.Name)

	dc := filepath.Join(c.s.Repo, ".devcontainer", "docker-compose.yml")
	if running(dc, "db") {
		ui.Step("Loading it into the running devcontainer")
		if exec.Command("docker", "compose", "-f", dc, "exec", "-T", "workspace", "bash", ".devcontainer/post-create.sh").Run() == nil {
			ui.OK("Local site now matches live")
		} else {
			ui.Info("Restart the container to import: gd-sync down && gd-sync up")
		}
	} else {
		ui.Info("Start the devcontainer and it imports automatically: gd-sync up")
	}
	return nil
}

func ensureWPCLI(conn *remote.Conn) error {
	if conn.Run("test -x ~/wp-cli.phar", nil, nil) == nil {
		return nil
	}
	ui.Step("Installing WP-CLI on the server (one time)")
	if err := conn.Run("cd ~ && curl -sS -o wp-cli.phar https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar && chmod +x wp-cli.phar", nil, nil); err != nil {
		return fmt.Errorf("Could not install WP-CLI on the server: %w", err)
	}
	return nil
}

// pruneDumps keeps the three newest dumps. Their names sort by date.
func pruneDumps(dir, name string) {
	old, _ := filepath.Glob(filepath.Join(dir, name+"-*.sql"))
	sort.Sort(sort.Reverse(sort.StringSlice(old)))
	for i := 3; i < len(old); i++ {
		os.Remove(old[i])
	}
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fK", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

// running reports whether a compose service is up.
func running(composeFile, service string) bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	if _, err := os.Stat(composeFile); err != nil {
		return false
	}
	out, err := exec.Command("docker", "compose", "-f", composeFile, "ps", "-q", service).Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func compose(c *ctx, args ...string) error {
	dc := filepath.Join(c.s.Repo, ".devcontainer")
	cmd := exec.Command("docker", append([]string{"compose", "--env-file", filepath.Join(dc, ".env"), "-f", filepath.Join(dc, "docker-compose.yml")}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func cmdUp(c *ctx) error {
	if err := compose(c, "up", "-d"); err != nil {
		return err
	}
	ui.OK("%s is up — %s", c.s.Name, c.s.LocalURL)
	return nil
}

func cmdDown(c *ctx) error {
	if err := compose(c, "down"); err != nil {
		return err
	}
	ui.OK("%s stopped", c.s.Name)
	return nil
}

// cmdShell hands over to the system ssh for a real interactive terminal.
// Windows 10 and later ship it too.
func cmdShell(c *ctx) error {
	if err := c.s.NeedServer(); err != nil {
		return err
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		return errors.New("No ssh command found. On Windows: Settings -> System -> Optional features -> OpenSSH Client")
	}
	cmd := exec.Command("ssh", "-p", c.s.SSHPort, "-o", "StrictHostKeyChecking=accept-new", c.s.Target())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// cmdKey creates ~/.ssh/id_ed25519 if there is none and adds it to the
// server's authorized_keys, so the password is needed this one last time.
func cmdKey(c *ctx) error {
	if err := c.s.NeedServer(); err != nil {
		return err
	}
	key := filepath.Join(remote.SSHDir(), "id_ed25519")
	if _, err := os.Stat(key + ".pub"); err != nil {
		ui.Step("Creating an SSH key")
		if err := newKey(key); err != nil {
			return err
		}
		ui.OK("%s", key)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		return err
	}

	ui.Step("Installing it on %s (cPanel password once)", c.s.Target())
	conn, err := c.server()
	if err != nil {
		return err
	}
	install := `mkdir -p ~/.ssh && chmod 700 ~/.ssh && touch ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys && ` +
		`k=$(cat) && { grep -qxF "$k" ~/.ssh/authorized_keys || printf '%s\n' "$k" >> ~/.ssh/authorized_keys; }`
	if err := conn.Run(install, strings.NewReader(strings.TrimSpace(string(pub))), nil); err != nil {
		return fmt.Errorf("Could not install the key: %w", err)
	}
	ui.OK("Done — no more password prompts")
	return nil
}

// newKey writes an unencrypted ed25519 key pair in OpenSSH format.
func newKey(path string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(priv, "gd-sync")
	if err != nil {
		return err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " gd-sync\n"
	return os.WriteFile(path+".pub", []byte(line), 0o644)
}

// cmdSecrets gives the GitHub workers what they need to reach the server.
func cmdSecrets(c *ctx) error {
	if err := c.s.NeedServer(); err != nil {
		return err
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return errors.New("GitHub CLI (gh) not installed — https://cli.github.com")
	}
	key := filepath.Join(remote.SSHDir(), "id_ed25519")
	priv, err := os.ReadFile(key)
	if err != nil {
		return fmt.Errorf("No SSH key at %s — run: gd-sync key", key)
	}
	ui.Step("Pushing SSH secrets to GitHub")
	for _, s := range []struct{ name, value string }{
		{"GD_SYNC_SSH_KEY", string(priv)},
		{"GD_SYNC_SSH_HOST", c.s.SSHHost},
		{"GD_SYNC_SSH_USER", c.s.SSHUser},
		{"GD_SYNC_SSH_PORT", c.s.SSHPort},
	} {
		cmd := exec.Command("gh", "secret", "set", s.name)
		cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = c.s.Repo, strings.NewReader(s.value), os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("gh secret set %s: %w", s.name, err)
		}
	}
	ui.OK("GD_SYNC_SSH_KEY, GD_SYNC_SSH_HOST, GD_SYNC_SSH_USER, GD_SYNC_SSH_PORT set")
	ui.Info("Run 'gd-sync key' first if you haven't, or the key won't be authorised.")
	return nil
}
