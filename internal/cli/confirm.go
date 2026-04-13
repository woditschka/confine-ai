package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// newConfirm builds a confirmation callback that reads from r and writes
// prompts to w.
func newConfirm(r io.Reader, w io.Writer) func(string) bool {
	scanner := bufio.NewScanner(r)
	return func(msg string) bool {
		_, _ = fmt.Fprint(w, msg)
		if !scanner.Scan() {
			return false
		}
		line := strings.TrimSpace(scanner.Text())
		return line == "" || strings.HasPrefix(strings.ToLower(line), "y")
	}
}

// shellQuote wraps s in single quotes for safe inclusion in a bash -c string.
// Single quotes inside s are escaped as '\” (end quote, escaped quote, reopen).
// Safety: callers must ensure s has been validated (e.g., via assistant.ValidateName)
// before passing user-controlled input. The allowed character set [a-z0-9-] makes
// injection impossible, but shellQuote is the last line of defense.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
