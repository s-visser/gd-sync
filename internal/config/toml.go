package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// ReadTOML decodes a TOML file into v. A missing file leaves v as it is.
func ReadTOML(path string, v any) error {
	_, err := toml.DecodeFile(path, v)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// String writes s as a TOML basic string.
func String(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// List writes a TOML array of strings, one per line so diffs stay readable.
func List(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteString("[\n")
	for _, it := range items {
		b.WriteString("  " + String(it) + ",\n")
	}
	b.WriteString("]")
	return b.String()
}
