package sanitize

import "testing"

func TestControlChars(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "clean string", input: "hello", want: "hello"},
		{name: "empty string", input: "", want: ""},
		{name: "newline replaced", input: "a\nb", want: "a\ufffdb"},
		{name: "tab replaced", input: "a\tb", want: "a\ufffdb"},
		{name: "null byte replaced", input: "a\x00b", want: "a\ufffdb"},
		{name: "DEL replaced", input: "a\x7fb", want: "a\ufffdb"},
		{name: "printable ASCII preserved", input: " ~abc123!@#", want: " ~abc123!@#"},
		{name: "unicode preserved", input: "caf\u00e9", want: "caf\u00e9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ControlChars(tt.input)
			if got != tt.want {
				t.Errorf("ControlChars(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
