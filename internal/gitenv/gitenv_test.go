package gitenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestReadIdentity(t *testing.T) {
	tests := []struct {
		name      string
		gitConfig string // contents of a temporary gitconfig file
		gitOnPath bool   // whether git binary is available
		wantName  string
		wantEmail string
		wantErr   string // substring in error, empty means no error
	}{
		{
			name:      "both name and email present",
			gitConfig: "[user]\n\tname = Alice\n\temail = alice@example.com\n",
			gitOnPath: true,
			wantName:  "Alice",
			wantEmail: "alice@example.com",
		},
		{
			name:      "name with whitespace trimmed",
			gitConfig: "[user]\n\tname = Alice Smith \n\temail = alice@example.com\n",
			gitOnPath: true,
			wantName:  "Alice Smith",
			wantEmail: "alice@example.com",
		},
		{
			name:      "missing name",
			gitConfig: "[user]\n\temail = alice@example.com\n",
			gitOnPath: true,
			wantErr:   "user.name",
		},
		{
			name:      "missing email",
			gitConfig: "[user]\n\tname = Alice\n",
			gitOnPath: true,
			wantErr:   "user.email",
		},
		{
			name:      "both missing",
			gitConfig: "[core]\n\tautocrlf = false\n",
			gitOnPath: true,
			wantErr:   "user.name",
		},
		{
			name:      "empty config file",
			gitConfig: "",
			gitOnPath: true,
			wantErr:   "user.name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.gitOnPath {
				t.Setenv("PATH", "/nonexistent")
			}

			tmpFile := filepath.Join(t.TempDir(), "gitconfig")
			if err := os.WriteFile(tmpFile, []byte(tt.gitConfig), 0o644); err != nil {
				t.Fatalf("WriteFile(%q) error: %v", tmpFile, err)
			}
			t.Setenv("GIT_CONFIG_GLOBAL", tmpFile)
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

			gotName, gotEmail, err := ReadIdentity(t.Context())

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ReadIdentity() = (%q, %q, nil), want error containing %q", gotName, gotEmail, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("ReadIdentity() error = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("ReadIdentity() unexpected error: %v", err)
			}
			if gotName != tt.wantName {
				t.Errorf("ReadIdentity() name = %q, want %q", gotName, tt.wantName)
			}
			if gotEmail != tt.wantEmail {
				t.Errorf("ReadIdentity() email = %q, want %q", gotEmail, tt.wantEmail)
			}
		})
	}

	t.Run("git not on PATH", func(t *testing.T) {
		t.Setenv("PATH", "/nonexistent")
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

		_, _, err := ReadIdentity(t.Context())
		if err == nil {
			t.Fatal("ReadIdentity() = nil error, want error when git is not on PATH")
		}
		if !strings.Contains(err.Error(), "user.name") {
			t.Errorf("ReadIdentity() error = %q, want containing %q", err.Error(), "user.name")
		}
	})
}

func TestMergeInto(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		gitName  string
		gitEmail string
		want     map[string]string
	}{
		{
			name:     "nil env",
			env:      nil,
			gitName:  "Alice",
			gitEmail: "alice@example.com",
			want: map[string]string{
				"GIT_AUTHOR_NAME":     "Alice",
				"GIT_COMMITTER_NAME":  "Alice",
				"GIT_AUTHOR_EMAIL":    "alice@example.com",
				"GIT_COMMITTER_EMAIL": "alice@example.com",
			},
		},
		{
			name:     "empty env",
			env:      map[string]string{},
			gitName:  "Alice",
			gitEmail: "alice@example.com",
			want: map[string]string{
				"GIT_AUTHOR_NAME":     "Alice",
				"GIT_COMMITTER_NAME":  "Alice",
				"GIT_AUTHOR_EMAIL":    "alice@example.com",
				"GIT_COMMITTER_EMAIL": "alice@example.com",
			},
		},
		{
			name: "existing keys preserved",
			env: map[string]string{
				"GIT_AUTHOR_NAME":  "Bob",
				"GIT_AUTHOR_EMAIL": "bob@example.com",
			},
			gitName:  "Alice",
			gitEmail: "alice@example.com",
			want: map[string]string{
				"GIT_AUTHOR_NAME":     "Bob",
				"GIT_COMMITTER_NAME":  "Alice",
				"GIT_AUTHOR_EMAIL":    "bob@example.com",
				"GIT_COMMITTER_EMAIL": "alice@example.com",
			},
		},
		{
			name: "all keys present skips all",
			env: map[string]string{
				"GIT_AUTHOR_NAME":     "Bob",
				"GIT_COMMITTER_NAME":  "Bob",
				"GIT_AUTHOR_EMAIL":    "bob@example.com",
				"GIT_COMMITTER_EMAIL": "bob@example.com",
			},
			gitName:  "Alice",
			gitEmail: "alice@example.com",
			want: map[string]string{
				"GIT_AUTHOR_NAME":     "Bob",
				"GIT_COMMITTER_NAME":  "Bob",
				"GIT_AUTHOR_EMAIL":    "bob@example.com",
				"GIT_COMMITTER_EMAIL": "bob@example.com",
			},
		},
		{
			name: "other keys preserved",
			env: map[string]string{
				"TZ": "UTC",
			},
			gitName:  "Alice",
			gitEmail: "alice@example.com",
			want: map[string]string{
				"TZ":                  "UTC",
				"GIT_AUTHOR_NAME":     "Alice",
				"GIT_COMMITTER_NAME":  "Alice",
				"GIT_AUTHOR_EMAIL":    "alice@example.com",
				"GIT_COMMITTER_EMAIL": "alice@example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeInto(tt.env, tt.gitName, tt.gitEmail)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("MergeInto() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
