package runtime

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLookPath returns a LookPathFunc that resolves the given binaries.
// Binaries not in the map return an error matching exec.ErrNotFound behavior.
func fakeLookPath(binaries map[string]string) LookPathFunc {
	return func(file string) (string, error) {
		if p, ok := binaries[file]; ok {
			return p, nil
		}
		return "", errors.New("executable file not found in $PATH")
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name         string
		explicitPath string
		lookPath     LookPathFunc
		wantName     string
		wantPath     string
		wantErr      string
	}{
		// PATH-based detection (AC #1-4).
		{
			name:     "docker on PATH",
			lookPath: fakeLookPath(map[string]string{"docker": "/usr/bin/docker"}),
			wantName: "docker",
			wantPath: "/usr/bin/docker",
		},
		{
			name:     "podman on PATH but no docker",
			lookPath: fakeLookPath(map[string]string{"podman": "/usr/bin/podman"}),
			wantName: "podman",
			wantPath: "/usr/bin/podman",
		},
		{
			name: "both on PATH prefers docker",
			lookPath: fakeLookPath(map[string]string{
				"docker": "/usr/bin/docker",
				"podman": "/usr/bin/podman",
			}),
			wantName: "docker",
			wantPath: "/usr/bin/docker",
		},
		{
			name:     "no runtime on PATH",
			lookPath: fakeLookPath(map[string]string{}),
			wantErr:  "no container runtime found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Detect(tt.explicitPath, tt.lookPath)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Detect(%q, lookPath) = %v, want error containing %q", tt.explicitPath, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Detect(%q, lookPath) error = %q, want error containing %q", tt.explicitPath, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("Detect(%q, lookPath) unexpected error: %v", tt.explicitPath, err)
			}

			if got.Name != tt.wantName {
				t.Errorf("Detect(%q, lookPath).Name = %q, want %q", tt.explicitPath, got.Name, tt.wantName)
			}
			if got.Path != tt.wantPath {
				t.Errorf("Detect(%q, lookPath).Path = %q, want %q", tt.explicitPath, got.Path, tt.wantPath)
			}
		})
	}
}

// createExecutable creates a file with the given name in dir and sets the
// executable permission bit. Returns the absolute path to the file.
func createExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", p, err)
	}
	return p
}

func TestDetect_ExplicitPath(t *testing.T) {
	// lookPath should not be called when explicitPath is set.
	neverCalled := func(_ string) (string, error) {
		t.Fatal("lookPath called with explicit path set")
		return "", nil
	}

	tests := []struct {
		name     string
		setup    func(t *testing.T) string // returns explicitPath
		wantName string
		wantPath string // if set, check Path equals this instead of explicitPath
		skipPath bool   // skip exact path check (for symlink tests where resolved path is dynamic)
		wantErr  string
	}{
		{
			name: "explicit docker path",
			setup: func(t *testing.T) string {
				t.Helper()
				return createExecutable(t, t.TempDir(), "docker")
			},
			wantName: "docker",
		},
		{
			name: "explicit podman path",
			setup: func(t *testing.T) string {
				t.Helper()
				return createExecutable(t, t.TempDir(), "podman")
			},
			wantName: "podman",
		},
		{
			name: "explicit path does not exist",
			setup: func(t *testing.T) string {
				t.Helper()
				return filepath.Join(t.TempDir(), "docker")
			},
			wantErr: "no such file or directory",
		},
		{
			name: "explicit path unrecognized basename",
			setup: func(t *testing.T) string {
				t.Helper()
				return createExecutable(t, t.TempDir(), "nerdctl")
			},
			wantErr: "unrecognized runtime",
		},
		{
			name: "explicit path not executable",
			setup: func(t *testing.T) string {
				t.Helper()
				p := filepath.Join(t.TempDir(), "docker")
				if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o644); err != nil {
					t.Fatalf("WriteFile(%q): %v", p, err)
				}
				return p
			},
			wantErr: "not executable",
		},
		{
			name: "symlink named docker pointing to unrecognized binary",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				createExecutable(t, dir, "malicious")
				link := filepath.Join(dir, "docker")
				if err := os.Symlink(filepath.Join(dir, "malicious"), link); err != nil {
					t.Fatalf("Symlink: %v", err)
				}
				return link
			},
			wantErr: "unrecognized runtime",
		},
		{
			name: "symlink named docker pointing to real docker",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				createExecutable(t, dir, "docker")
				subdir := filepath.Join(dir, "links")
				if err := os.MkdirAll(subdir, 0o755); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}
				link := filepath.Join(subdir, "docker")
				if err := os.Symlink(filepath.Join(dir, "docker"), link); err != nil {
					t.Fatalf("Symlink: %v", err)
				}
				return link
			},
			wantName: "docker",
			skipPath: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			explicitPath := tt.setup(t)
			got, err := Detect(explicitPath, neverCalled)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Detect(%q, lookPath) = %v, want error containing %q", explicitPath, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Detect(%q, lookPath) error = %q, want error containing %q", explicitPath, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("Detect(%q, lookPath) unexpected error: %v", explicitPath, err)
			}

			if got.Name != tt.wantName {
				t.Errorf("Detect(%q, lookPath).Name = %q, want %q", explicitPath, got.Name, tt.wantName)
			}
			if !tt.skipPath {
				wantPath := explicitPath
				if tt.wantPath != "" {
					wantPath = tt.wantPath
				}
				if resolved, err := filepath.EvalSymlinks(wantPath); err == nil {
					wantPath = resolved
				} else if !errors.Is(err, fs.ErrNotExist) {
					t.Logf("EvalSymlinks(%q): %v (using original)", wantPath, err)
				}
				if got.Path != wantPath {
					t.Errorf("Detect(%q, lookPath).Path = %q, want %q", explicitPath, got.Path, wantPath)
				}
			}
		})
	}
}

func TestRuntime_DefaultNetwork(t *testing.T) {
	tests := []struct {
		name string
		rt   Runtime
		want string
	}{
		{name: "docker", rt: Runtime{Name: "docker"}, want: "bridge"},
		{name: "podman", rt: Runtime{Name: "podman"}, want: "podman"},
		{name: "empty defaults to bridge", rt: Runtime{}, want: "bridge"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rt.DefaultNetwork(); got != tt.want {
				t.Errorf("Runtime{Name: %q}.DefaultNetwork() = %q, want %q", tt.rt.Name, got, tt.want)
			}
		})
	}
}
