// Package ui is everything gd-sync prints and asks: coloured status lines on
// stdout, warnings on stderr, and the few questions it puts to the user.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Colour codes, empty when stdout is not a terminal.
var B, DIM, R, G, Y, C, N string

// Yes answers every confirm with yes (the -y flag).
var Yes bool

func init() {
	if term.IsTerminal(int(os.Stdout.Fd())) && enableColour() {
		B, DIM, R, G, Y, C, N = "\033[1m", "\033[2m", "\033[31m", "\033[32m", "\033[33m", "\033[36m", "\033[0m"
	}
}

func Say(format string, a ...any) { fmt.Printf(format+"\n", a...) }
func Step(format string, a ...any) {
	fmt.Printf("%s==>%s %s%s%s\n", C, N, B, fmt.Sprintf(format, a...), N)
}
func Info(format string, a ...any) { fmt.Printf("    %s%s%s\n", DIM, fmt.Sprintf(format, a...), N) }
func OK(format string, a ...any)   { fmt.Printf("%s ok %s%s\n", G, fmt.Sprintf(format, a...), N) }
func Warn(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "%s !  %s%s\n", Y, fmt.Sprintf(format, a...), N)
}

// Fail prints an error the way the rest of the output looks.
func Fail(err error) { fmt.Fprintf(os.Stderr, "%s x  %s%s\n", R, err, N) }

// List prints each line indented, in the given colour, with an optional marker.
func List(colour, marker string, lines []string) {
	for _, l := range lines {
		fmt.Printf("    %s%s%s%s\n", colour, marker, l, N)
	}
}

var stdin = bufio.NewReader(os.Stdin)

// scripted is set when answers come from SetInput rather than a person.
var scripted bool

// SetInput makes Ask, Confirm and Choose read their answers from r, as if a
// person were typing them. For tests; os.Stdin puts things back.
func SetInput(r io.Reader) {
	stdin = bufio.NewReader(r)
	scripted = r != os.Stdin
}

// interactive reports whether someone is there to answer.
func interactive() bool { return scripted || term.IsTerminal(int(os.Stdin.Fd())) }

func readLine() (string, bool) {
	s, err := stdin.ReadString('\n')
	if err != nil && s == "" {
		return "", false
	}
	return strings.TrimRight(s, "\r\n"), true
}

// Confirm asks a yes/no question. Without a terminal, the answer is no.
func Confirm(q string) bool {
	if Yes {
		return true
	}
	if !interactive() {
		return false
	}
	fmt.Printf("%s%s%s [y/N] ", B, q, N)
	a, _ := readLine()
	switch strings.ToLower(strings.TrimSpace(a)) {
	case "y", "yes":
		return true
	}
	return false
}

// Choose asks the user to pick one of the options and returns its index; the
// first option is the default. It returns -1 when nobody can answer — no
// terminal, or -y, which answers yes/no questions but can't pick for you.
func Choose(q string, options ...string) int {
	if Yes || !interactive() {
		return -1
	}
	fmt.Printf("%s%s%s\n", B, q, N)
	for i, o := range options {
		fmt.Printf("    %d) %s\n", i+1, o)
	}
	for {
		fmt.Printf("  Choice %s[1]%s ", DIM, N)
		a, ok := readLine()
		a = strings.TrimSpace(a)
		if a == "" || !ok {
			return 0
		}
		for i := range options {
			if a == fmt.Sprint(i+1) {
				return i
			}
		}
		Warn("Type a number from 1 to %d", len(options))
	}
}

// Ask prompts on stderr and returns the answer, or def when it is empty.
func Ask(prompt, def string) string {
	if def != "" {
		fmt.Fprintf(os.Stderr, "%s %s[%s]%s ", prompt, DIM, def, N)
	} else {
		fmt.Fprintf(os.Stderr, "%s ", prompt)
	}
	a, _ := readLine()
	if a = strings.TrimSpace(a); a == "" {
		return def
	}
	return a
}

// Secret prompts for a password without echoing it.
func Secret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return string(b), err
}
