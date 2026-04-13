package assistant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid names.
		{name: "lowercase alpha", input: "claude", wantErr: false},
		{name: "another assistant", input: "copilot", wantErr: false},
		{name: "third assistant", input: "opencode", wantErr: false},
		{name: "with hyphen", input: "my-assistant", wantErr: false},
		{name: "two chars alpha", input: "a1", wantErr: false},
		{name: "two chars numeric", input: "11", wantErr: false},
		{name: "all digits", input: "123", wantErr: false},
		{name: "hyphen in middle", input: "a-b", wantErr: false},
		{name: "multiple hyphens", input: "my-cool-assistant", wantErr: false},
		{name: "max length 64 chars", input: strings.Repeat("a", 64), wantErr: false},

		// Invalid names.
		{name: "empty string", input: "", wantErr: true},
		{name: "single char", input: "a", wantErr: true},
		{name: "uppercase", input: "Claude", wantErr: true},
		{name: "all uppercase", input: "CLAUDE", wantErr: true},
		{name: "underscore", input: "my_assistant", wantErr: true},
		{name: "dot", input: "my.assistant", wantErr: true},
		{name: "slash", input: "my/assistant", wantErr: true},
		{name: "backslash", input: "my\\assistant", wantErr: true},
		{name: "space", input: "my assistant", wantErr: true},
		{name: "leading hyphen", input: "-assistant", wantErr: true},
		{name: "trailing hyphen", input: "assistant-", wantErr: true},
		{name: "only hyphens", input: "--", wantErr: true},
		{name: "65 chars exceeds max", input: strings.Repeat("a", 65), wantErr: true},
		{name: "path traversal dots", input: "..", wantErr: true},
		{name: "path traversal dotdotslash", input: "../etc", wantErr: true},
		{name: "colon", input: "my:assistant", wantErr: true},
		{name: "at sign", input: "my@assistant", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestConfigPath(t *testing.T) {
	tests := []struct {
		name      string
		homeDir   string
		assistant string
		want      string
	}{
		{
			name:      "typical assistant",
			homeDir:   "/home/user",
			assistant: "claude",
			want:      "/home/user/.confine-ai/assistants/claude/devcontainer.json",
		},
		{
			name:      "assistant with hyphens",
			homeDir:   "/home/user",
			assistant: "my-assistant",
			want:      "/home/user/.confine-ai/assistants/my-assistant/devcontainer.json",
		},
		{
			name:      "different home dir",
			homeDir:   "/Users/dev",
			assistant: "copilot",
			want:      "/Users/dev/.confine-ai/assistants/copilot/devcontainer.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConfigPath(tt.homeDir, tt.assistant)
			if got != tt.want {
				t.Errorf("ConfigPath(%q, %q) = %q, want %q", tt.homeDir, tt.assistant, got, tt.want)
			}
		})
	}
}

func TestDataPath(t *testing.T) {
	tests := []struct {
		name      string
		homeDir   string
		assistant string
		want      string
	}{
		{
			name:      "typical assistant",
			homeDir:   "/home/user",
			assistant: "claude",
			want:      "/home/user/.confine-ai/data/claude",
		},
		{
			name:      "assistant with hyphens",
			homeDir:   "/home/user",
			assistant: "my-assistant",
			want:      "/home/user/.confine-ai/data/my-assistant",
		},
		{
			name:      "different home dir",
			homeDir:   "/Users/dev",
			assistant: "opencode",
			want:      "/Users/dev/.confine-ai/data/opencode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DataPath(tt.homeDir, tt.assistant)
			if got != tt.want {
				t.Errorf("DataPath(%q, %q) = %q, want %q", tt.homeDir, tt.assistant, got, tt.want)
			}
		})
	}
}

func TestBaseDockerfilePath(t *testing.T) {
	tests := []struct {
		name    string
		homeDir string
		want    string
	}{
		{
			name:    "linux home",
			homeDir: "/home/user",
			want:    "/home/user/.confine-ai/base/Dockerfile",
		},
		{
			name:    "macOS home",
			homeDir: "/Users/dev",
			want:    "/Users/dev/.confine-ai/base/Dockerfile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BaseDockerfilePath(tt.homeDir)
			if got != tt.want {
				t.Errorf("BaseDockerfilePath(%q) = %q, want %q", tt.homeDir, got, tt.want)
			}
		})
	}
}

func TestEnsureExtraFiles(t *testing.T) {
	t.Run("creates missing seed file", func(t *testing.T) {
		homeDir := t.TempDir()
		dataDir := DataPath(homeDir, "claude")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", dataDir, err)
		}

		if err := EnsureExtraFiles(homeDir, "claude"); err != nil {
			t.Fatalf("EnsureExtraFiles() error: %v", err)
		}

		seedPath := filepath.Join(dataDir, "claude.json")
		content, err := os.ReadFile(seedPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error: %v", seedPath, err)
		}
		if string(content) != "{}" {
			t.Errorf("seed file content = %q, want %q", string(content), "{}")
		}
	})

	t.Run("does not overwrite existing file", func(t *testing.T) {
		homeDir := t.TempDir()
		dataDir := DataPath(homeDir, "claude")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", dataDir, err)
		}

		existing := `{"hasCompletedOnboarding":true}`
		seedPath := filepath.Join(dataDir, "claude.json")
		if err := os.WriteFile(seedPath, []byte(existing), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error: %v", seedPath, err)
		}

		if err := EnsureExtraFiles(homeDir, "claude"); err != nil {
			t.Fatalf("EnsureExtraFiles() error: %v", err)
		}

		content, err := os.ReadFile(seedPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error: %v", seedPath, err)
		}
		if string(content) != existing {
			t.Errorf("seed file content = %q, want %q (should not be overwritten)", string(content), existing)
		}
	})

	t.Run("creates missing opencode seed file", func(t *testing.T) {
		homeDir := t.TempDir()
		dataDir := DataPath(homeDir, "opencode")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", dataDir, err)
		}

		if err := EnsureExtraFiles(homeDir, "opencode"); err != nil {
			t.Fatalf("EnsureExtraFiles() error: %v", err)
		}

		seedPath := filepath.Join(dataDir, "opencode.json")
		content, err := os.ReadFile(seedPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error: %v", seedPath, err)
		}
		want := `{"$schema":"https://opencode.ai/config.json","disabled_providers":["opencode"]}`
		if string(content) != want {
			t.Errorf("seed file content = %q, want %q", string(content), want)
		}
	})

	t.Run("no-op for assistant without extra files", func(t *testing.T) {
		homeDir := t.TempDir()
		dataDir := DataPath(homeDir, "copilot")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", dataDir, err)
		}

		if err := EnsureExtraFiles(homeDir, "copilot"); err != nil {
			t.Fatalf("EnsureExtraFiles() error: %v", err)
		}

		entries, err := os.ReadDir(dataDir)
		if err != nil {
			t.Fatalf("ReadDir(%q) error: %v", dataDir, err)
		}
		if len(entries) != 0 {
			t.Errorf("data dir has %d entries, want 0", len(entries))
		}
	})
}

func TestNeedsProviderHint(t *testing.T) {
	t.Run("true when config matches default seed", func(t *testing.T) {
		homeDir := t.TempDir()
		dataDir := DataPath(homeDir, "opencode")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", dataDir, err)
		}
		if err := EnsureExtraFiles(homeDir, "opencode"); err != nil {
			t.Fatalf("EnsureExtraFiles() error: %v", err)
		}

		if !NeedsProviderHint(homeDir, "opencode") {
			t.Error("NeedsProviderHint() = false, want true for default seed config")
		}
	})

	t.Run("false when config has been modified", func(t *testing.T) {
		homeDir := t.TempDir()
		dataDir := DataPath(homeDir, "opencode")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", dataDir, err)
		}

		customConfig := `{"$schema":"https://opencode.ai/config.json","provider":{"ollama":{"models":["llama3"]}}}`
		seedPath := filepath.Join(dataDir, "opencode.json")
		if err := os.WriteFile(seedPath, []byte(customConfig), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error: %v", seedPath, err)
		}

		if NeedsProviderHint(homeDir, "opencode") {
			t.Error("NeedsProviderHint() = true, want false for modified config")
		}
	})

	t.Run("false when seed file is missing", func(t *testing.T) {
		homeDir := t.TempDir()
		if NeedsProviderHint(homeDir, "opencode") {
			t.Error("NeedsProviderHint() = true, want false for missing seed file")
		}
	})

	t.Run("false for assistant without extra files", func(t *testing.T) {
		homeDir := t.TempDir()
		if NeedsProviderHint(homeDir, "copilot") {
			t.Error("NeedsProviderHint() = true, want false for assistant without extra files")
		}
	})
}

func TestExists(t *testing.T) {
	t.Run("existing assistant directory", func(t *testing.T) {
		homeDir := t.TempDir()
		assistantDir := Dir(homeDir, "claude")
		if err := os.MkdirAll(assistantDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) failed: %v", assistantDir, err)
		}

		if !Exists(homeDir, "claude") {
			t.Error("Exists() = false, want true for existing assistant directory")
		}
	})

	t.Run("missing assistant directory", func(t *testing.T) {
		homeDir := t.TempDir()

		if Exists(homeDir, "claude") {
			t.Error("Exists() = true, want false for missing assistant directory")
		}
	})

	t.Run("file instead of directory", func(t *testing.T) {
		homeDir := t.TempDir()
		assistantsDir := AssistantsDir(homeDir)
		if err := os.MkdirAll(assistantsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) failed: %v", assistantsDir, err)
		}
		// Create a file where a directory is expected.
		filePath := filepath.Join(assistantsDir, "claude")
		if err := os.WriteFile(filePath, []byte("not a dir"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) failed: %v", filePath, err)
		}

		if Exists(homeDir, "claude") {
			t.Error("Exists() = true, want false when path is a file not a directory")
		}
	})
}

func TestListNames(t *testing.T) {
	t.Run("returns sorted assistant names", func(t *testing.T) {
		homeDir := t.TempDir()
		assistantsDir := AssistantsDir(homeDir)

		// Create assistant directories in non-alphabetical order.
		for _, name := range []string{"opencode", "claude", "my-assistant"} {
			dir := filepath.Join(assistantsDir, name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("MkdirAll(%q) failed: %v", dir, err)
			}
		}

		got := ListNames(homeDir)
		want := []string{"claude", "my-assistant", "opencode"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("ListNames() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns nil when assistants directory does not exist", func(t *testing.T) {
		homeDir := t.TempDir()

		got := ListNames(homeDir)
		if got != nil {
			t.Errorf("ListNames() = %v, want nil", got)
		}
	})

	t.Run("skips files in assistants directory", func(t *testing.T) {
		homeDir := t.TempDir()
		assistantsDir := AssistantsDir(homeDir)
		if err := os.MkdirAll(assistantsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) failed: %v", assistantsDir, err)
		}

		// Create a valid assistant directory.
		if err := os.MkdirAll(filepath.Join(assistantsDir, "claude"), 0o755); err != nil {
			t.Fatalf("MkdirAll failed: %v", err)
		}
		// Create a file (not a directory) -- should be skipped.
		if err := os.WriteFile(filepath.Join(assistantsDir, "notadir"), []byte("file"), 0o644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		got := ListNames(homeDir)
		want := []string{"claude"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("ListNames() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("skips entries with invalid names", func(t *testing.T) {
		homeDir := t.TempDir()
		assistantsDir := AssistantsDir(homeDir)

		// Create a valid assistant and an invalid one (uppercase).
		for _, name := range []string{"claude", "INVALID"} {
			if err := os.MkdirAll(filepath.Join(assistantsDir, name), 0o755); err != nil {
				t.Fatalf("MkdirAll failed: %v", err)
			}
		}

		got := ListNames(homeDir)
		want := []string{"claude"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("ListNames() mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returns nil when assistants directory is unreadable", func(t *testing.T) {
		homeDir := t.TempDir()
		assistantsDir := AssistantsDir(homeDir)
		if err := os.MkdirAll(assistantsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) failed: %v", assistantsDir, err)
		}
		// Remove read permission.
		if err := os.Chmod(assistantsDir, 0o000); err != nil {
			t.Fatalf("Chmod failed: %v", err)
		}
		t.Cleanup(func() {
			// Restore permission for cleanup.
			os.Chmod(assistantsDir, 0o755)
		})

		got := ListNames(homeDir)
		if got != nil {
			t.Errorf("ListNames() = %v, want nil for unreadable directory", got)
		}
	})
}
