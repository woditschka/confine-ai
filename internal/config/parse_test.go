package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripJSONC(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// Single-line comments.
		{
			name: "line comment at end of line",
			in:   `{"key": "value"} // comment`,
			want: `{"key": "value"} `,
		},
		{
			name: "line comment on its own line",
			in: `{
// this is a comment
"key": "value"
}`,
			want: `{
"key": "value"
}`,
		},
		{
			name: "multiple line comments",
			in: `{
// first comment
"a": 1, // inline
// second comment
"b": 2
}`,
			want: `{
"a": 1,
"b": 2
}`,
		},

		// Block comments.
		{
			name: "single-line block comment",
			in:   `{"key": /* comment */ "value"}`,
			want: `{"key":  "value"}`,
		},
		{
			name: "multiline block comment",
			in: `{
/* this
   is a
   multiline comment */
"key": "value"
}`,
			want: `{

"key": "value"
}`,
		},
		{
			name: "block comment before value",
			in:   `{"key": /* inline */ "value"}`,
			want: `{"key":  "value"}`,
		},

		// String safety: comments inside strings are preserved.
		{
			name: "double slash inside string",
			in:   `{"url": "http://example.com"}`,
			want: `{"url": "http://example.com"}`,
		},
		{
			name: "block comment syntax inside string",
			in:   `{"pattern": "/* not a comment */"}`,
			want: `{"pattern": "/* not a comment */"}`,
		},
		{
			name: "escaped quote inside string",
			in:   `{"msg": "she said \"hello\" // world"}`,
			want: `{"msg": "she said \"hello\" // world"}`,
		},
		{
			name: "escaped quote before closing quote",
			in:   `{"a": "test\""}`,
			want: `{"a": "test\""}`,
		},
		{
			name: "escaped backslash before quote",
			in:   `{"a": "path\\"}`,
			want: `{"a": "path\\"}`,
		},

		// Trailing commas.
		{
			name: "trailing comma in object",
			in:   `{"a": 1, "b": 2,}`,
			want: `{"a": 1, "b": 2}`,
		},
		{
			name: "trailing comma in array",
			in:   `{"a": [1, 2, 3,]}`,
			want: `{"a": [1, 2, 3]}`,
		},
		{
			name: "trailing comma with whitespace before closing",
			in: `{
  "a": 1,
  "b": 2,
}`,
			want: `{
  "a": 1,
  "b": 2
}`,
		},
		{
			name: "nested trailing commas",
			in: `{
  "obj": {"x": 1,},
  "arr": [1, 2,],
}`,
			want: `{
  "obj": {"x": 1},
  "arr": [1, 2]
}`,
		},
		{
			name: "comma followed by normal element not removed",
			in:   `{"a": 1, "b": 2}`,
			want: `{"a": 1, "b": 2}`,
		},

		// Composition: comments and trailing commas together.
		{
			name: "comments and trailing commas combined",
			in: `{
  // a comment
  "name": "test", // inline comment
  "items": [
    "one",
    "two", /* trailing */
  ],
}`,
			want: `{
  "name": "test",
  "items": [
    "one",
    "two"
  ]
}`,
		},

		// Edge cases.
		{
			name: "empty object",
			in:   `{}`,
			want: `{}`,
		},
		{
			name: "empty input",
			in:   ``,
			want: ``,
		},
		{
			name: "whitespace only around JSON",
			in:   `  {"key": "value"}  `,
			want: `  {"key": "value"}  `,
		},
		{
			name: "slash not followed by slash or star",
			in:   `{"a": 1/2}`,
			want: `{"a": 1/2}`,
		},
		{
			name: "realistic devcontainer.json",
			in: `{
  // Development container configuration
  "name": "my-project",
  "image": "mcr.microsoft.com/devcontainers/base:ubuntu",

  /* Mount configuration */
  "mounts": [
    "source=${localWorkspaceFolder}/.claude,target=/home/node/.claude,type=bind",
  ],

  "containerEnv": {
    "TZ": "UTC",
  },

  // Remote user
  "remoteUser": "node",
}`,
			// Blank lines between comma+comment sequences are collapsed
			// because the comment-only line and its surrounding whitespace
			// are removed together. The output is valid JSON.
			want: `{
  "name": "my-project",
  "image": "mcr.microsoft.com/devcontainers/base:ubuntu",
  "mounts": [
    "source=${localWorkspaceFolder}/.claude,target=/home/node/.claude,type=bind"
  ],

  "containerEnv": {
    "TZ": "UTC"
  },
  "remoteUser": "node"
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stripJSONC([]byte(tt.in))
			if err != nil {
				t.Fatalf("stripJSONC(%q) unexpected error: %v", tt.in, err)
			}
			if string(got) != tt.want {
				t.Errorf("stripJSONC(%q)\ngot:  %q\nwant: %q", tt.in, string(got), tt.want)
			}
		})
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantKeys []string // top-level keys expected in the parsed JSON
		wantErr  string   // substring in error message, empty means no error
	}{
		{
			name:     "valid JSON",
			content:  `{"name": "test", "image": "ubuntu"}`,
			wantKeys: []string{"name", "image"},
		},
		{
			name: "JSONC with line comments",
			content: `{
  // project name
  "name": "test"
}`,
			wantKeys: []string{"name"},
		},
		{
			name: "JSONC with block comments",
			content: `{
  /* image config */
  "image": "ubuntu"
}`,
			wantKeys: []string{"image"},
		},
		{
			name: "JSONC with trailing commas",
			content: `{
  "name": "test",
  "image": "ubuntu",
}`,
			wantKeys: []string{"name", "image"},
		},
		{
			name: "full JSONC features combined",
			content: `{
  // name
  "name": "test", // inline
  /* build config */
  "image": "ubuntu",
  "mounts": [
    "source=x,target=y",
  ],
}`,
			wantKeys: []string{"name", "image", "mounts"},
		},
		{
			name:    "invalid JSON after stripping",
			content: `{"name": }`,
			wantErr: "parse config:",
		},
		{
			name:    "unterminated block comment",
			content: `{"name": "test" /* unclosed comment`,
			wantErr: "unterminated block comment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "devcontainer.json")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			got, err := Parse(path)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Parse(%q) = %v, want error containing %q", path, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Parse(%q) error = %q, want error containing %q", path, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", path, err)
			}

			if got.Path != path {
				t.Errorf("Parse(%q).Path = %q, want %q", path, got.Path, path)
			}

			// Verify the JSON is valid and contains expected keys.
			var m map[string]json.RawMessage
			if err := json.Unmarshal(got.JSON, &m); err != nil {
				t.Fatalf("json.Unmarshal(got.JSON): %v\nJSON: %s", err, string(got.JSON))
			}
			for _, key := range tt.wantKeys {
				if _, ok := m[key]; !ok {
					t.Errorf("Parse(%q).JSON missing key %q, got keys: %v", path, key, mapKeys(m))
				}
			}
		})
	}
}

func TestParse_FileNotFound(t *testing.T) {
	_, err := Parse("/nonexistent/devcontainer.json")
	if err == nil {
		t.Fatal("Parse(nonexistent) = nil error, want error")
	}
	if !strings.Contains(err.Error(), "parse config:") {
		t.Errorf("Parse(nonexistent) error = %q, want error containing %q", err.Error(), "parse config:")
	}
}

func TestParse_FileTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devcontainer.json")

	// Create a file that exceeds maxConfigFileSize.
	data := bytes.Repeat([]byte{' '}, maxConfigFileSize+1)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Parse(path)
	if err == nil {
		t.Fatal("Parse(oversized) = nil error, want error")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("Parse(oversized) error = %q, want error containing %q", err.Error(), "exceeds maximum size")
	}
}

// FuzzStripJSONC exercises the JSONC scanner with arbitrary input to catch
// panics and unterminated-block-comment detection.
func FuzzStripJSONC(f *testing.F) {
	// Seed corpus: valid JSONC patterns.
	f.Add([]byte(`{"key": "value"}`))
	f.Add([]byte("{\"key\": \"value\", // comment\n}"))
	f.Add([]byte(`{"key": /* block */ "value"}`))
	f.Add([]byte(`{"a": 1, "b": 2,}`))
	f.Add([]byte(`"hello \"world\""`))
	f.Add([]byte(`// just a comment`))
	f.Add([]byte(`/* unterminated`))
	f.Add([]byte{})

	f.Fuzz(func(_ *testing.T, data []byte) {
		// stripJSONC must not panic on any input. Errors (e.g.,
		// unterminated block comments) are expected for some inputs.
		stripJSONC(data) //nolint:errcheck // fuzz target tests for panics
	})
}

// mapKeys returns the keys of a map for diagnostic output.
func mapKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
