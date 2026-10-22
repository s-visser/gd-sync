// Package gitx runs git against one repo. gd-sync drives the real git
// binary rather than a library, so it behaves exactly like the git the user
// runs by hand, hooks and config included.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type Repo struct{ Dir string }

func (r Repo) cmd(args ...string) *exec.Cmd {
	c := exec.Command("git", append([]string{"-C", r.Dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return c
}

// Run runs git with its output going straight to the terminal.
func (r Repo) Run(args ...string) error {
	c := r.cmd(args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return wrap(c.Run(), args, "")
}

// Quiet runs git and discards its output, keeping stderr for the error.
func (r Repo) Quiet(args ...string) error {
	_, err := r.Output(args...)
	return err
}

// Output runs git and returns its stdout.
func (r Repo) Output(args ...string) (string, error) {
	return r.Input(nil, args...)
}

// Input runs git with the given stdin and returns its stdout.
func (r Repo) Input(stdin io.Reader, args ...string) (string, error) {
	c := r.cmd(args...)
	var out, errb bytes.Buffer
	c.Stdin, c.Stdout, c.Stderr = stdin, &out, &errb
	err := c.Run()
	return out.String(), wrap(err, args, errb.String())
}

// Stream runs git with its stdout connected to w.
func (r Repo) Stream(stdin io.Reader, w io.Writer, args ...string) error {
	c := r.cmd(args...)
	var errb bytes.Buffer
	c.Stdin, c.Stdout, c.Stderr = stdin, w, &errb
	return wrap(c.Run(), args, errb.String())
}

// OK reports whether a git command succeeds, for questions like "does this
// branch exist".
func (r Repo) OK(args ...string) bool { return r.Quiet(args...) == nil }

// Error is a failed git command, worded by git's own stderr when it said
// anything.
type Error struct {
	Cmd    string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return "git " + e.Cmd + ": " + e.Stderr
	}
	return fmt.Sprintf("git %s: %v", e.Cmd, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func wrap(err error, args []string, stderr string) error {
	if err == nil {
		return nil
	}
	return &Error{Cmd: args[0], Stderr: strings.TrimSpace(stderr), Err: err}
}

// ExitCode is the exit status of a failed command, or -1.
func ExitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// CurrentBranch is the checked-out branch, or "main" when detached.
func (r Repo) CurrentBranch() string {
	b, err := r.Output("branch", "--show-current")
	if b = strings.TrimSpace(b); err != nil || b == "" {
		return "main"
	}
	return b
}

func (r Repo) HasRemote() bool { return r.OK("remote", "get-url", "origin") }

// Status is "git status --porcelain"; empty means a clean tree.
func (r Repo) Status(args ...string) string {
	s, _ := r.Output(append([]string{"status", "--porcelain"}, args...)...)
	return strings.TrimSpace(s)
}

// GitDir is the absolute path of the .git folder.
func (r Repo) GitDir() (string, error) {
	s, err := r.Output("rev-parse", "--absolute-git-dir")
	return strings.TrimSpace(s), err
}

// Commit commits what is staged as gd-sync itself, so server snapshots are
// recognisable and don't need a configured identity.
func (r Repo) Commit(msg string) error {
	return r.Quiet("-c", "user.email=gd-sync@local", "-c", "user.name=gd-sync", "commit", "-q", "-m", msg)
}

// NUL joins paths for git's --pathspec-file-nul and tar's --null.
func NUL(paths []string) io.Reader {
	var b bytes.Buffer
	for _, p := range paths {
		b.WriteString(p)
		b.WriteByte(0)
	}
	return &b
}
