package satpanel

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner runs a system command and returns its standard output. Tests replace it.
type Runner interface {
	Run(ctx context.Context, stdin string, name string, args ...string) (string, error)
}

type execRunner struct{}

// Run executes name with args in the C locale (nmcli and journalctl otherwise
// answer in the board's language). The error carries the command's own message
// but never its arguments, which may hold a Wi-Fi password.
func (execRunner) Run(ctx context.Context, stdin, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C", "LANGUAGE=C")
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		msg = strings.TrimPrefix(msg, "Error: ")
		return out.String(), fmt.Errorf("%s", msg)
	}
	return out.String(), nil
}

// splitTerse splits one line of `nmcli -t` output on unescaped colons;
// nmcli writes a literal colon as \: and a backslash as \\.
func splitTerse(line string) []string {
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
		case c == ':':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(parts, cur.String())
}

// lines returns the non-empty lines of out.
func lines(out string) []string {
	var l []string
	for _, s := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(s) != "" {
			l = append(l, s)
		}
	}
	return l
}

// splitList splits a list value such as "1.1.1.1, 8.8.8.8" or "a b".
func splitList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '|' }) {
		if f = strings.TrimSpace(f); f != "" && f != "--" {
			out = append(out, f)
		}
	}
	return out
}
