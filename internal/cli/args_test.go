package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestResolveFolders(t *testing.T) {
	t.Run("empty rawPaths returns workspaceFolder as primary", func(t *testing.T) {
		primary, additional, err := resolveFolders(nil, "/home/user/project")
		if err != nil {
			t.Fatalf("resolveFolders(nil) unexpected error: %v", err)
		}
		if primary != "/home/user/project" {
			t.Errorf("resolveFolders(nil) primary = %q, want %q", primary, "/home/user/project")
		}
		if len(additional) != 0 {
			t.Errorf("resolveFolders(nil) additional = %v, want empty", additional)
		}
	})

	t.Run("single dot resolves to workspaceFolder", func(t *testing.T) {
		// Create a temp dir to use as cwd.
		dir := t.TempDir()
		primary, additional, err := resolveFolders([]string{dir}, "/fallback")
		if err != nil {
			t.Fatalf("resolveFolders([dir]) unexpected error: %v", err)
		}
		if primary != dir {
			t.Errorf("resolveFolders([dir]) primary = %q, want %q", primary, dir)
		}
		if len(additional) != 0 {
			t.Errorf("resolveFolders([dir]) additional = %v, want empty", additional)
		}
	})

	t.Run("multiple paths returns primary and additional", func(t *testing.T) {
		dir1 := t.TempDir()
		dir2 := t.TempDir()
		dir3 := t.TempDir()

		primary, additional, err := resolveFolders([]string{dir1, dir2, dir3}, "/fallback")
		if err != nil {
			t.Fatalf("resolveFolders() unexpected error: %v", err)
		}
		if primary != dir1 {
			t.Errorf("resolveFolders() primary = %q, want %q", primary, dir1)
		}
		if len(additional) != 2 {
			t.Fatalf("resolveFolders() additional length = %d, want 2", len(additional))
		}
		if additional[0] != dir2 {
			t.Errorf("resolveFolders() additional[0] = %q, want %q", additional[0], dir2)
		}
		if additional[1] != dir3 {
			t.Errorf("resolveFolders() additional[1] = %q, want %q", additional[1], dir3)
		}
	})

	t.Run("non-existent path returns error", func(t *testing.T) {
		_, _, err := resolveFolders([]string{"/nonexistent/path/abc123"}, "/fallback")
		if err == nil {
			t.Fatal("resolveFolders() = nil error, want error for non-existent path")
		}
		if !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("resolveFolders() error = %q, want containing %q", err.Error(), "does not exist")
		}
	})

	t.Run("file path returns not-a-directory error", func(t *testing.T) {
		dir := t.TempDir()
		filePath := filepath.Join(dir, "afile.txt")
		if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		_, _, err := resolveFolders([]string{filePath}, "/fallback")
		if err == nil {
			t.Fatal("resolveFolders() = nil error, want error for file path")
		}
		if !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("resolveFolders() error = %q, want containing %q", err.Error(), "not a directory")
		}
	})

	t.Run("basename collision returns error", func(t *testing.T) {
		// Create two directories with the same basename under different parents.
		parent1 := t.TempDir()
		parent2 := t.TempDir()
		shared1 := filepath.Join(parent1, "shared")
		shared2 := filepath.Join(parent2, "shared")
		if err := os.MkdirAll(shared1, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.MkdirAll(shared2, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}

		_, _, err := resolveFolders([]string{shared1, shared2}, "/fallback")
		if err == nil {
			t.Fatal("resolveFolders() = nil error, want error for basename collision")
		}
		if !strings.Contains(err.Error(), "basename collision") {
			t.Errorf("resolveFolders() error = %q, want containing %q", err.Error(), "basename collision")
		}
		if !strings.Contains(err.Error(), "shared") {
			t.Errorf("resolveFolders() error = %q, want containing basename %q", err.Error(), "shared")
		}
	})

	t.Run("no collision when basenames differ", func(t *testing.T) {
		dir1 := t.TempDir()
		dir2 := t.TempDir()

		_, _, err := resolveFolders([]string{dir1, dir2}, "/fallback")
		if err != nil {
			t.Fatalf("resolveFolders() unexpected error: %v", err)
		}
	})

	t.Run("single path has no collision", func(t *testing.T) {
		dir := t.TempDir()
		primary, additional, err := resolveFolders([]string{dir}, "/fallback")
		if err != nil {
			t.Fatalf("resolveFolders() unexpected error: %v", err)
		}
		if primary != dir {
			t.Errorf("resolveFolders() primary = %q, want %q", primary, dir)
		}
		if len(additional) != 0 {
			t.Errorf("resolveFolders() additional = %v, want empty", additional)
		}
	})
}

func TestParseFolderArgs(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		wantFolders       []string
		wantAssistantArgs []string
	}{
		{
			name:              "empty args",
			args:              nil,
			wantFolders:       nil,
			wantAssistantArgs: nil,
		},
		{
			name:              "folders only",
			args:              []string{".", "../A"},
			wantFolders:       []string{".", "../A"},
			wantAssistantArgs: nil,
		},
		{
			name:              "folders and assistant args",
			args:              []string{".", "../A", "--", "--continue"},
			wantFolders:       []string{".", "../A"},
			wantAssistantArgs: []string{"--continue"},
		},
		{
			name:              "just separator with assistant args",
			args:              []string{"--", "--continue"},
			wantFolders:       nil,
			wantAssistantArgs: []string{"--continue"},
		},
		{
			name:              "separator only",
			args:              []string{"--"},
			wantFolders:       nil,
			wantAssistantArgs: nil,
		},
		{
			name:              "multiple assistant args after separator",
			args:              []string{".", "--", "--continue", "--model", "opus"},
			wantFolders:       []string{"."},
			wantAssistantArgs: []string{"--continue", "--model", "opus"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			folders, assistantArgs := parseFolderArgs(tt.args)

			if diff := cmp.Diff(tt.wantFolders, folders, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("parseFolderArgs(%v) folders mismatch (-want +got):\n%s", tt.args, diff)
			}
			if diff := cmp.Diff(tt.wantAssistantArgs, assistantArgs, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("parseFolderArgs(%v) assistantArgs mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

func TestExtractShellFlag(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantShell     bool
		wantRemaining []string
	}{
		{
			name:          "no args",
			args:          nil,
			wantShell:     false,
			wantRemaining: nil,
		},
		{
			name:          "shell flag only",
			args:          []string{"--shell"},
			wantShell:     true,
			wantRemaining: nil,
		},
		{
			name:          "shell with folders",
			args:          []string{"--shell", ".", "../other"},
			wantShell:     true,
			wantRemaining: []string{".", "../other"},
		},
		{
			name:          "shell after folders",
			args:          []string{".", "--shell", "--", "--continue"},
			wantShell:     true,
			wantRemaining: []string{".", "--", "--continue"},
		},
		{
			name:          "no shell flag",
			args:          []string{".", "--", "--continue"},
			wantShell:     false,
			wantRemaining: []string{".", "--", "--continue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotShell, gotRemaining := extractBoolFlag(tt.args, "--shell")
			if gotShell != tt.wantShell {
				t.Errorf("extractBoolFlag(%v, --shell) shell = %v, want %v", tt.args, gotShell, tt.wantShell)
			}
			if diff := cmp.Diff(tt.wantRemaining, gotRemaining, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("extractBoolFlag(%v, --shell) remaining mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

func TestExtractRepeatedFlag(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantValues    []string
		wantRemaining []string
	}{
		{
			name:          "no args",
			args:          nil,
			wantValues:    nil,
			wantRemaining: nil,
		},
		{
			name:          "no flag present",
			args:          []string{".", "../other", "--", "--continue"},
			wantValues:    nil,
			wantRemaining: []string{".", "../other", "--", "--continue"},
		},
		{
			name:          "single occurrence",
			args:          []string{"--allowed-hosts", "api.anthropic.com", "."},
			wantValues:    []string{"api.anthropic.com"},
			wantRemaining: []string{"."},
		},
		{
			name:          "multiple occurrences",
			args:          []string{"--allowed-hosts", "api.anthropic.com", "--allowed-hosts", "statsig.anthropic.com", "."},
			wantValues:    []string{"api.anthropic.com", "statsig.anthropic.com"},
			wantRemaining: []string{"."},
		},
		{
			name:          "flag at end without value (dangling)",
			args:          []string{".", "--allowed-hosts"},
			wantValues:    nil,
			wantRemaining: []string{".", "--allowed-hosts"},
		},
		{
			name:          "flag mixed with other flags",
			args:          []string{"--shell", "--allowed-hosts", "api.anthropic.com", ".", "--", "--continue"},
			wantValues:    []string{"api.anthropic.com"},
			wantRemaining: []string{"--shell", ".", "--", "--continue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotValues, gotRemaining := extractRepeatedFlag(tt.args, "--allowed-hosts")
			if diff := cmp.Diff(tt.wantValues, gotValues, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("extractRepeatedFlag(%v) values mismatch (-want +got):\n%s", tt.args, diff)
			}
			if diff := cmp.Diff(tt.wantRemaining, gotRemaining, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("extractRepeatedFlag(%v) remaining mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

func TestExtractNoGitIdentityFlag(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantFlag      bool
		wantRemaining []string
	}{
		{
			name:          "no args",
			args:          nil,
			wantFlag:      false,
			wantRemaining: nil,
		},
		{
			name:          "flag only",
			args:          []string{"--no-git-identity"},
			wantFlag:      true,
			wantRemaining: nil,
		},
		{
			name:          "flag with folders",
			args:          []string{"--no-git-identity", ".", "../other"},
			wantFlag:      true,
			wantRemaining: []string{".", "../other"},
		},
		{
			name:          "flag after folders",
			args:          []string{".", "--no-git-identity", "--", "--continue"},
			wantFlag:      true,
			wantRemaining: []string{".", "--", "--continue"},
		},
		{
			name:          "no flag",
			args:          []string{".", "--", "--continue"},
			wantFlag:      false,
			wantRemaining: []string{".", "--", "--continue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFlag, gotRemaining := extractBoolFlag(tt.args, "--no-git-identity")
			if gotFlag != tt.wantFlag {
				t.Errorf("extractBoolFlag(%v, --no-git-identity) flag = %v, want %v", tt.args, gotFlag, tt.wantFlag)
			}
			if diff := cmp.Diff(tt.wantRemaining, gotRemaining, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("extractBoolFlag(%v, --no-git-identity) remaining mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}
