package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewConfirm(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "empty line is default yes", input: "\n", want: true},
		{name: "y confirms", input: "y\n", want: true},
		{name: "Y confirms", input: "Y\n", want: true},
		{name: "yes confirms", input: "yes\n", want: true},
		{name: "n declines", input: "n\n", want: false},
		{name: "N declines", input: "N\n", want: false},
		{name: "no declines", input: "no\n", want: false},
		{name: "arbitrary text declines", input: "maybe\n", want: false},
		{name: "EOF declines", input: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := strings.NewReader(tt.input)
			var w bytes.Buffer
			confirm := newConfirm(r, &w)

			got := confirm("Create? [Y/n] ")

			if got != tt.want {
				t.Errorf("newConfirm() = %v, want %v", got, tt.want)
			}

			// Prompt should be written to the writer.
			if !strings.Contains(w.String(), "Create?") {
				t.Errorf("prompt not written to writer: %q", w.String())
			}
		})
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"with space", "'with space'"},
		{"it's", `'it'\''s'`},
		{"", "''"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := shellQuote(tt.input)
			if got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
