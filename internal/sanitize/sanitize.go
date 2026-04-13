// Package sanitize provides text sanitization helpers shared across packages.
package sanitize

import "strings"

// ControlChars replaces control characters (bytes < 0x20 and DEL) with the
// Unicode replacement character. Prevents log injection and terminal escape
// sequences in user-supplied values such as label values and field names.
func ControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '\ufffd'
		}
		return r
	}, s)
}
