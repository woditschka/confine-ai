package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupFixture creates the specified file paths relative to the given base directory.
// Each path is treated as a file; parent directories are created automatically.
func setupFixture(t *testing.T, base string, files []string) {
	t.Helper()
	for _, f := range files {
		full := filepath.Join(base, f)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte("{}"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", full, err)
		}
	}
}

// setupDirs creates directories (not files) relative to the base directory.
func setupDirs(t *testing.T, base string, dirs []string) {
	t.Helper()
	for _, d := range dirs {
		full := filepath.Join(base, d)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", full, err)
		}
	}
}

func TestFind(t *testing.T) {
	tests := []struct {
		name              string
		files             []string // files to create relative to workspace root
		dirs              []string // directories to create (without files inside)
		subdir            string   // optional subdirectory of TempDir to use as workspace
		want              string   // expected path suffix relative to workspace root
		wantErr           string   // substring expected in error message, empty means no error
		wantErrSubstrings []string // additional substrings required in error message
	}{
		{
			name:  "direct devcontainer.json",
			files: []string{".devcontainer/devcontainer.json"},
			want:  ".devcontainer/devcontainer.json",
		},
		{
			name:  "root dotfile",
			files: []string{".devcontainer.json"},
			want:  ".devcontainer.json",
		},
		{
			name:  "single subfolder",
			files: []string{".devcontainer/node/devcontainer.json"},
			want:  ".devcontainer/node/devcontainer.json",
		},
		{
			name: "ambiguous subfolders",
			files: []string{
				".devcontainer/node/devcontainer.json",
				".devcontainer/python/devcontainer.json",
			},
			wantErr:           "multiple .devcontainer subfolders found",
			wantErrSubstrings: []string{"node", "python"},
		},
		{
			name:    "no config",
			wantErr: "no devcontainer.json found",
		},
		{
			name: "priority: direct over dotfile",
			files: []string{
				".devcontainer/devcontainer.json",
				".devcontainer.json",
			},
			want: ".devcontainer/devcontainer.json",
		},
		{
			name: "priority: direct over subfolder",
			files: []string{
				".devcontainer/devcontainer.json",
				".devcontainer/node/devcontainer.json",
			},
			want: ".devcontainer/devcontainer.json",
		},
		{
			name: "priority: dotfile over subfolder",
			files: []string{
				".devcontainer.json",
				".devcontainer/node/devcontainer.json",
			},
			want: ".devcontainer.json",
		},
		{
			name:    "subfolder without devcontainer.json",
			dirs:    []string{".devcontainer/node"},
			files:   []string{".devcontainer/node/README.md"},
			wantErr: "no devcontainer.json found",
		},
		{
			name:    "empty .devcontainer directory",
			dirs:    []string{".devcontainer"},
			wantErr: "no devcontainer.json found",
		},
		{
			name:   "path with spaces and unicode",
			subdir: "my project üñ",
			files:  []string{".devcontainer/devcontainer.json"},
			want:   ".devcontainer/devcontainer.json",
		},
		{
			name:    "directory named devcontainer.json is not a config file",
			dirs:    []string{".devcontainer/devcontainer.json"},
			wantErr: "no devcontainer.json found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspace, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatalf("EvalSymlinks: %v", err)
			}
			if tt.subdir != "" {
				workspace = filepath.Join(workspace, tt.subdir)
				if err := os.MkdirAll(workspace, 0o755); err != nil {
					t.Fatalf("MkdirAll(%q): %v", workspace, err)
				}
			}

			if len(tt.dirs) > 0 {
				setupDirs(t, workspace, tt.dirs)
			}
			if len(tt.files) > 0 {
				setupFixture(t, workspace, tt.files)
			}

			got, err := Find(workspace)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Find(%q) = %q, want error containing %q", workspace, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Find(%q) error = %q, want error containing %q", workspace, err.Error(), tt.wantErr)
				}
				for _, sub := range tt.wantErrSubstrings {
					if !strings.Contains(err.Error(), sub) {
						t.Errorf("Find(%q) error = %q, want error containing %q", workspace, err.Error(), sub)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("Find(%q) unexpected error: %v", workspace, err)
			}

			wantPath := filepath.Join(workspace, tt.want)
			if got != wantPath {
				t.Errorf("Find(%q) = %q, want %q", workspace, got, wantPath)
			}
		})
	}
}

func TestFind_WorkspaceDoesNotExist(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "nonexistent")

	_, err := Find(workspace)
	if err == nil {
		t.Fatal("Find(nonexistent) = nil error, want error")
	}
	if !strings.Contains(err.Error(), "stat workspace folder") {
		t.Errorf("Find(nonexistent) error = %q, want error containing %q", err.Error(), "stat workspace folder")
	}
}

func TestFind_SymlinkEscape(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()

	// Create a real config file outside the workspace.
	outsideConfig := filepath.Join(outside, "devcontainer.json")
	if err := os.WriteFile(outsideConfig, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Symlink .devcontainer/devcontainer.json -> outside config.
	devcontainerDir := filepath.Join(workspace, ".devcontainer")
	if err := os.MkdirAll(devcontainerDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	link := filepath.Join(devcontainerDir, "devcontainer.json")
	if err := os.Symlink(outsideConfig, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err := Find(workspace)
	if err == nil {
		t.Fatal("Find() with symlink escape = nil error, want error")
	}
	if !strings.Contains(err.Error(), "no devcontainer.json found") {
		t.Errorf("Find() error = %q, want error containing %q", err.Error(), "no devcontainer.json found")
	}
}

// TestFind_UnreadableDevcontainerDirReportsNotFound verifies that an
// unreadable .devcontainer directory produces a "not found" error rather
// than an I/O error, since ReadDir failure is indistinguishable from absence.
func TestFind_UnreadableDevcontainerDirReportsNotFound(t *testing.T) {
	workspace := t.TempDir()

	devcontainerDir := filepath.Join(workspace, ".devcontainer")
	if err := os.MkdirAll(devcontainerDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Remove read permission so ReadDir fails.
	if err := os.Chmod(devcontainerDir, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(devcontainerDir, 0o755) //nolint:errcheck // best-effort cleanup
	})

	_, err := Find(workspace)
	if err == nil {
		t.Fatal("Find() with unreadable dir = nil error, want error")
	}
	if !strings.Contains(err.Error(), "no devcontainer.json found") {
		t.Errorf("Find() error = %q, want error containing %q", err.Error(), "no devcontainer.json found")
	}
}
