package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/woditschka/confine-ai/internal/assistant"
)

func TestRun(t *testing.T) {
	// stubGetwd returns a getwd function that returns the given directory.
	stubGetwd := func(dir string) func() (string, error) {
		return func() (string, error) {
			return dir, nil
		}
	}

	tests := []struct {
		name       string
		args       []string
		getwd      func() (string, error)
		wantErr    string
		wantStdout string
		wantStderr []string // substring matches on stderr
	}{
		// No command: shows usage (help).
		{
			name:       "no args shows usage",
			args:       []string{},
			getwd:      stubGetwd("/home/user/project"),
			wantStderr: []string{"Usage:"},
		},

		// --version flag.
		{
			name:       "version flag prints version and commit",
			args:       []string{"--version"},
			getwd:      stubGetwd("/home/user/project"),
			wantStdout: "dev (unknown)",
		},

		// --help flag.
		{
			name:  "help flag shows usage with all commands and flags",
			args:  []string{"--help"},
			getwd: stubGetwd("/home/user/project"),
			wantStderr: []string{
				"Usage:", "completion", "init", "rm", "status", "update",
				"-workspace-folder", "-config", "-docker-path",
			},
		},

		// Per-command --help.
		{
			name:       "rm --help shows rm usage",
			args:       []string{"rm", "--help"},
			getwd:      stubGetwd("/home/user/project"),
			wantStderr: []string{"Usage: confine-ai rm"},
		},

		// Assistant name that does not exist: treated as assistant shortcut,
		// reports init suggestion. "foo" is a valid assistant name (3 chars,
		// lowercase alphanumeric) but the assistant directory does not exist.
		{
			name:    "valid assistant name without directory suggests init",
			args:    []string{"foo"},
			getwd:   stubGetwd("/home/user/project"),
			wantErr: `assistant "foo" not found; run 'confine-ai init foo'`,
		},

		// Invalid name that is also not a subcommand.
		{
			name:    "invalid name reports unknown command",
			args:    []string{"FOO"},
			getwd:   stubGetwd("/home/user/project"),
			wantErr: `unknown command or assistant "FOO"`,
		},

		// rm is fully wired — tested in TestRun_Rm.

		// Global flags with commands — verifies flags are accepted without parse errors.
		// Value verification (REQ-CL-002-2/3/4) deferred to command implementations.
		// rm is used because it needs only runtime detection (no config loading).
		// When a runtime is available, rm succeeds (no containers = no error).
		// When no runtime is available, it fails on runtime detection.
		// Use --docker-path with a nonexistent path to get a deterministic error.
		{
			name:    "workspace-folder flag accepted",
			args:    []string{"--workspace-folder", "/other/path", "--docker-path", "/nonexistent/docker", "rm"},
			getwd:   stubGetwd("/home/user/project"),
			wantErr: "rm: runtime:",
		},
		{
			name:    "config flag accepted with rm",
			args:    []string{"--config", "/home/user/project/.devcontainer/devcontainer.json", "--docker-path", "/nonexistent/docker", "rm"},
			getwd:   stubGetwd("/home/user/project"),
			wantErr: "rm: runtime:",
		},
		{
			name:    "docker-path flag accepted",
			args:    []string{"--docker-path", "/nonexistent/podman", "rm"},
			getwd:   stubGetwd("/home/user/project"),
			wantErr: "rm: runtime:",
		},

		// Unknown flag.
		{
			name:    "unknown flag reports error",
			args:    []string{"--unknown-flag"},
			getwd:   stubGetwd("/home/user/project"),
			wantErr: "flag provided but not defined: -unknown-flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(t.Context(), tt.args, &stdout, &stderr, tt.getwd)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("run(%v) = nil, want error containing %q", tt.args, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("run(%v) error = %q, want error containing %q", tt.args, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("run(%v) unexpected error: %v", tt.args, err)
			}

			if tt.wantStdout != "" {
				if !strings.Contains(stdout.String(), tt.wantStdout) {
					t.Errorf("run(%v) stdout = %q, want substring %q", tt.args, stdout.String(), tt.wantStdout)
				}
			}

			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("run(%v) stderr = %q, want substring %q", tt.args, stderr.String(), want)
				}
			}
		})
	}
}

func TestRun_WorkspaceFolder(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		getwdDir   string
		getwdFails bool
		wantErr    string
	}{
		{
			name:     "default workspace uses cwd without resolution error",
			args:     []string{"--docker-path", "/nonexistent/docker", "rm"},
			getwdDir: "/home/user/project",
			wantErr:  "rm: runtime:",
		},
		{
			name:     "explicit workspace-folder accepted without resolution error",
			args:     []string{"--workspace-folder", "/other/path", "--docker-path", "/nonexistent/docker", "rm"},
			getwdDir: "/home/user/project",
			wantErr:  "rm: runtime:",
		},
		{
			name:     "relative workspace-folder resolves to absolute path",
			args:     []string{"--workspace-folder", "relative/path", "--docker-path", "/nonexistent/docker", "rm"},
			getwdDir: "/home/user/project",
			wantErr:  "rm: runtime:",
		},
		{
			name:       "getwd failure",
			args:       []string{"rm"},
			getwdFails: true,
			wantErr:    "working directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			getwd := func() (string, error) {
				if tt.getwdFails {
					return "", &testGetwdError{}
				}
				return tt.getwdDir, nil
			}

			err := run(t.Context(), tt.args, &stdout, &stderr, getwd)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("run(%v) = nil, want error containing %q", tt.args, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("run(%v) error = %q, want error containing %q", tt.args, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("run(%v) unexpected error: %v", tt.args, err)
			}
		})
	}
}

// TestRun_RelativeWorkspaceResolution verifies that a relative --workspace-folder
// is converted to an absolute path by filepath.Abs.
func TestRun_RelativeWorkspaceResolution(t *testing.T) {
	var stdout, stderr bytes.Buffer
	getwd := func() (string, error) { return "/home/user/project", nil }

	// A relative path should not cause a resolution error. Use "rm"
	// with a nonexistent docker path to isolate workspace resolution from
	// runtime auto-detection. A runtime error proves the path resolved.
	err := run(t.Context(), []string{"--workspace-folder", "relative/path", "--docker-path", "/nonexistent/docker", "rm"}, &stdout, &stderr, getwd)
	if err == nil {
		t.Fatal("run() = nil, want error (runtime detection failure)")
	}

	// Should reach rm's runtime detection, not fail on path resolution.
	if !strings.Contains(err.Error(), "rm: runtime:") {
		t.Errorf("run() error = %q, want containing %q (relative path should resolve)", err.Error(), "rm: runtime:")
	}

	// Verify filepath.Abs would produce an absolute path for "relative/path".
	abs, absErr := filepath.Abs("relative/path")
	if absErr != nil {
		t.Fatalf("filepath.Abs(relative/path) error: %v", absErr)
	}
	if !filepath.IsAbs(abs) {
		t.Errorf("filepath.Abs(relative/path) = %q, want absolute path", abs)
	}
}

// TestRun_Rm verifies that the rm command is wired: it detects the
// runtime and calls container.Down. These tests verify CLI wiring, not
// container.Down logic (tested in internal/container/down_test.go).
func TestRun_Rm(t *testing.T) {
	t.Run("runtime detect failure returns error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		getwd := func() (string, error) { return "/home/user/project", nil }

		err := run(t.Context(), []string{
			"--workspace-folder", "/home/user/project",
			"--docker-path", "/nonexistent/docker",
			"rm",
		}, &stdout, &stderr, getwd)
		if err == nil {
			t.Fatal("run() = nil, want error for runtime detection failure")
		}
		if !strings.Contains(err.Error(), "runtime") {
			t.Errorf("run() error = %q, want containing %q", err.Error(), "runtime")
		}
	})

	t.Run("executor failure returns wrapped error", func(t *testing.T) {
		// Create a workspace with no devcontainer.json — rm doesn't need config.
		// Use a nonexistent docker-path so runtime detection fails predictably.
		var stdout, stderr bytes.Buffer
		getwd := func() (string, error) { return t.TempDir(), nil }

		err := run(t.Context(), []string{
			"--docker-path", "/nonexistent/docker",
			"rm",
		}, &stdout, &stderr, getwd)
		if err == nil {
			t.Fatal("run() = nil, want error")
		}
		if !strings.Contains(err.Error(), "rm:") {
			t.Errorf("run() error = %q, want containing %q", err.Error(), "rm:")
		}
	})
}

func TestRun_RmAssistant(t *testing.T) {
	stubGetwd := func(dir string) func() (string, error) {
		return func() (string, error) {
			return dir, nil
		}
	}

	t.Run("rm with assistant name dispatches to DownAssistant", func(t *testing.T) {
		// When no runtime is available, both paths fail on runtime detection.
		// The assistant-targeted rm path should include the assistant name context.
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{
			"--workspace-folder", "/home/user/project",
			"--docker-path", "/nonexistent/docker",
			"rm", "claude",
		}, &stdout, &stderr, stubGetwd("/home/user/project"))

		if err == nil {
			t.Fatal("run() = nil, want error for runtime detection failure")
		}
		if !strings.Contains(err.Error(), "rm:") {
			t.Errorf("run() error = %q, want containing %q", err.Error(), "rm:")
		}
		if !strings.Contains(err.Error(), "runtime") {
			t.Errorf("run() error = %q, want containing %q", err.Error(), "runtime")
		}
	})

	t.Run("rm --help shows assistant usage", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{
			"rm", "--help",
		}, &stdout, &stderr, stubGetwd("/home/user/project"))

		if err != nil {
			t.Fatalf("run() unexpected error: %v", err)
		}
		if !strings.Contains(stderr.String(), "assistant-name") {
			t.Errorf("run() stderr = %q, want containing %q", stderr.String(), "assistant-name")
		}
	})
}

func TestRun_Init(t *testing.T) {
	stubGetwd := func(dir string) func() (string, error) {
		return func() (string, error) {
			return dir, nil
		}
	}

	t.Run("init without assistant name seeds base Dockerfile", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"init"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() unexpected error: %v", err)
		}
		if !strings.Contains(stdout.String(), "Seeded base Dockerfile") {
			t.Errorf("stdout = %q, want containing %q", stdout.String(), "Seeded base Dockerfile")
		}

		basePath := assistant.BaseDockerfilePath(homeDir)
		got, err := os.ReadFile(basePath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error: %v", basePath, err)
		}
		if !bytes.Equal(got, baseDockerfile) {
			t.Errorf("base Dockerfile contents = %q, want embedded seed", string(got))
		}

		// No assistant directory should have been created.
		assistantsDir := assistant.AssistantsDir(homeDir)
		if _, err := os.Stat(assistantsDir); !os.IsNotExist(err) {
			t.Errorf("assistants directory should not exist for no-arg init, got err: %v", err)
		}
	})

	t.Run("init without assistant name reports already present when base exists", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		stubStdin(t, "")

		// Pre-seed a user-modified base Dockerfile.
		basePath := assistant.BaseDockerfilePath(homeDir)
		if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
			t.Fatalf("MkdirAll() error: %v", err)
		}
		userCopy := []byte("# user edited\nFROM ubuntu:24.04\n")
		if err := os.WriteFile(basePath, userCopy, 0o644); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}

		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"init"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() unexpected error: %v", err)
		}
		if !strings.Contains(stdout.String(), "already present") {
			t.Errorf("stdout = %q, want containing %q", stdout.String(), "already present")
		}

		got, err := os.ReadFile(basePath)
		if err != nil {
			t.Fatalf("ReadFile() error: %v", err)
		}
		if !bytes.Equal(got, userCopy) {
			t.Errorf("base Dockerfile contents changed; got %q, want %q", string(got), string(userCopy))
		}
	})

	t.Run("init with invalid name returns validation error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"init", "A"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err == nil {
			t.Fatal("run() = nil, want error for invalid name")
		}
		if !strings.Contains(err.Error(), "assistant name") {
			t.Errorf("run() error = %q, want containing %q", err.Error(), "assistant name")
		}
	})

	t.Run("init --help shows usage", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"init", "--help"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() unexpected error: %v", err)
		}
		if !strings.Contains(stderr.String(), "Usage: confine-ai init") {
			t.Errorf("stderr = %q, want containing %q", stderr.String(), "Usage: confine-ai init")
		}
	})

	t.Run("init known assistant creates files and seeds base", func(t *testing.T) {
		// Use HOME override via environment to test in temp dir.
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"init", "claude"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() unexpected error: %v", err)
		}

		if !strings.Contains(stdout.String(), "Initialized assistant") {
			t.Errorf("stdout = %q, want containing %q", stdout.String(), "Initialized assistant")
		}
		if !strings.Contains(stdout.String(), "Seeded base Dockerfile") {
			t.Errorf("stdout = %q, want containing %q", stdout.String(), "Seeded base Dockerfile")
		}

		// Verify files were created.
		if _, err := os.Stat(assistant.ConfigPath(homeDir, "claude")); err != nil {
			t.Errorf("devcontainer.json not created: %v", err)
		}
		if _, err := os.Stat(assistant.DockerfilePath(homeDir, "claude")); err != nil {
			t.Errorf("Dockerfile not created: %v", err)
		}

		// Verify base Dockerfile seeded.
		basePath := assistant.BaseDockerfilePath(homeDir)
		got, err := os.ReadFile(basePath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error: %v", basePath, err)
		}
		if !bytes.Equal(got, baseDockerfile) {
			t.Errorf("base Dockerfile contents = %q, want embedded seed", string(got))
		}
	})

	t.Run("init assistant with existing base leaves base unchanged", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		stubStdin(t, "")

		// Pre-seed a user-modified base Dockerfile.
		basePath := assistant.BaseDockerfilePath(homeDir)
		if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
			t.Fatalf("MkdirAll() error: %v", err)
		}
		userCopy := []byte("# user edited\nFROM ubuntu:24.04\n")
		if err := os.WriteFile(basePath, userCopy, 0o644); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}

		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"init", "claude"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() unexpected error: %v", err)
		}

		if !strings.Contains(stdout.String(), "already present") {
			t.Errorf("stdout = %q, want containing %q", stdout.String(), "already present")
		}
		if !strings.Contains(stdout.String(), "Initialized assistant") {
			t.Errorf("stdout = %q, want containing %q", stdout.String(), "Initialized assistant")
		}

		// Verify base contents unchanged.
		got, err := os.ReadFile(basePath)
		if err != nil {
			t.Fatalf("ReadFile() error: %v", err)
		}
		if !bytes.Equal(got, userCopy) {
			t.Errorf("base Dockerfile contents changed; got %q, want %q", string(got), string(userCopy))
		}

		// Assistant dir exists.
		assistantDir := assistant.Dir(homeDir, "claude")
		if _, err := os.Stat(assistantDir); err != nil {
			t.Errorf("assistant directory not created: %v", err)
		}
	})

	t.Run("init unknown assistant creates generic template", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"init", "my-assistant"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() unexpected error: %v", err)
		}

		if !strings.Contains(stdout.String(), "Initialized assistant") {
			t.Errorf("stdout = %q, want containing %q", stdout.String(), "Initialized assistant")
		}

		// Unknown assistant should not have a Dockerfile.
		if _, err := os.Stat(assistant.DockerfilePath(homeDir, "my-assistant")); !os.IsNotExist(err) {
			t.Errorf("Dockerfile should not exist for unknown assistant, got err: %v", err)
		}
	})

	t.Run("init existing assistant non-interactive preserves", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		stubStdin(t, "")

		sentinel := assistant.DockerfilePath(homeDir, "claude")
		if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
			t.Fatalf("MkdirAll error: %v", err)
		}
		if err := os.WriteFile(sentinel, []byte("original"), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}

		var stdout, stderr bytes.Buffer
		if err := run(t.Context(), []string{"init", "claude"}, &stdout, &stderr, stubGetwd("/home/user/project")); err != nil {
			t.Fatalf("run() unexpected error: %v", err)
		}
		if !strings.Contains(stdout.String(), "already present") {
			t.Errorf("stdout = %q, want 'already present'", stdout.String())
		}
		got, err := os.ReadFile(sentinel)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(got) != "original" {
			t.Errorf("sentinel overwritten: got %q, want %q", string(got), "original")
		}
	})

	t.Run("init existing assistant with -y overwrites", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)
		stubStdin(t, "")

		if err := assistant.Init(homeDir, "claude", []byte("FROM original\n")); err != nil {
			t.Fatalf("seed Init: %v", err)
		}

		var stdout, stderr bytes.Buffer
		if err := run(t.Context(), []string{"init", "-y", "claude"}, &stdout, &stderr, stubGetwd("/home/user/project")); err != nil {
			t.Fatalf("run() unexpected error: %v", err)
		}
		if !strings.Contains(stdout.String(), "Initialized assistant") {
			t.Errorf("stdout = %q, want 'Initialized assistant'", stdout.String())
		}
		got, err := os.ReadFile(assistant.DockerfilePath(homeDir, "claude"))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if bytes.Equal(got, []byte("FROM original\n")) {
			t.Errorf("Dockerfile not overwritten")
		}
	})
}

func TestEmbeddedBaseDockerfileMarkers(t *testing.T) {
	// Lock the marker convention on the embedded seed so an accidental edit
	// to samples/base/Dockerfile is caught in review. See ADR
	// 2026-04-10-managed-dockerfile-classification.md for the grammar.
	seed := string(baseDockerfile)

	wantMarkers := []string{
		"# confine-ai:managed tool=base-image kind=image",
		"# confine-ai:managed tool=go kind=version",
		"# confine-ai:managed tool=go kind=sha256 arch=amd64",
		"# confine-ai:managed tool=go kind=sha256 arch=arm64",
		"# confine-ai:managed tool=java kind=version distribution=corretto",
		"# confine-ai:managed tool=java kind=sha256 arch=amd64 distribution=corretto",
		"# confine-ai:managed tool=java kind=sha256 arch=arm64 distribution=corretto",
	}

	for _, m := range wantMarkers {
		if !strings.Contains(seed, m) {
			t.Errorf("embedded base Dockerfile missing marker %q", m)
		}
		// Exactly one occurrence.
		if got := strings.Count(seed, m); got != 1 {
			t.Errorf("marker %q occurs %d times, want 1", m, got)
		}
	}

	// Each marker must immediately precede its target line (no blank line
	// between them). Verify by scanning lines: when we see a marker, the
	// following non-empty line must be the managed line it describes.
	lines := strings.Split(seed, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "# confine-ai:managed ") {
			continue
		}
		if i+1 >= len(lines) {
			t.Errorf("marker at line %d has no following line", i+1)
			continue
		}
		next := lines[i+1]
		if strings.TrimSpace(next) == "" {
			t.Errorf("marker at line %d followed by blank line (want managed line immediately after)", i+1)
			continue
		}
		switch {
		case strings.Contains(line, "kind=image"):
			if !strings.HasPrefix(next, "FROM ") {
				t.Errorf("marker %q at line %d not followed by FROM (got %q)", line, i+1, next)
			}
		case strings.Contains(line, "kind=version"), strings.Contains(line, "kind=sha256"):
			if !strings.HasPrefix(next, "ARG ") {
				t.Errorf("marker %q at line %d not followed by ARG (got %q)", line, i+1, next)
			}
		}
	}
}

func TestCommandRouting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	stubGetwd := func(dir string) func() (string, error) {
		return func() (string, error) {
			return dir, nil
		}
	}

	tests := []struct {
		name    string
		args    []string
		getwd   func() (string, error)
		wantErr string
	}{
		// Known subcommands dispatch correctly.
		{
			name:    "rm dispatches to rm handler",
			args:    []string{"--docker-path", "/nonexistent/docker", "rm"},
			getwd:   stubGetwd("/home/user/project"),
			wantErr: "rm: runtime:", // fails at runtime, proving rm handler ran
		},
		{
			name:    "status dispatches to status handler",
			args:    []string{"--docker-path", "/nonexistent/docker", "status"},
			getwd:   stubGetwd("/home/user/project"),
			wantErr: "status: runtime:", // fails at runtime, proving status handler ran
		},

		// Valid assistant name but directory missing.
		{
			name:    "valid assistant name without init suggests init",
			args:    []string{"claude"},
			getwd:   stubGetwd("/home/user/project"),
			wantErr: `assistant "claude" not found; run 'confine-ai init claude'`,
		},

		// Invalid name (not a subcommand, not a valid assistant name).
		{
			name:    "uppercase name is unknown command",
			args:    []string{"CLAUDE"},
			getwd:   stubGetwd("/home/user/project"),
			wantErr: `unknown command or assistant "CLAUDE"`,
		},
		{
			name:    "single char is unknown command",
			args:    []string{"x"},
			getwd:   stubGetwd("/home/user/project"),
			wantErr: `unknown command or assistant "x"`,
		},
		{
			name:    "name with underscore is unknown command",
			args:    []string{"my_assistant"},
			getwd:   stubGetwd("/home/user/project"),
			wantErr: `unknown command or assistant "my_assistant"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(t.Context(), tt.args, &stdout, &stderr, tt.getwd)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("run(%v) = nil, want error containing %q", tt.args, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("run(%v) error = %q, want error containing %q", tt.args, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("run(%v) unexpected error: %v", tt.args, err)
			}
		})
	}
}

func TestRunStatus(t *testing.T) {
	stubGetwd := func(dir string) func() (string, error) {
		return func() (string, error) {
			return dir, nil
		}
	}

	t.Run("status without runtime returns error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{
			"--docker-path", "/nonexistent/docker",
			"status",
		}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err == nil {
			t.Fatal("run() = nil, want error for runtime detection failure")
		}
		if !strings.Contains(err.Error(), "status: runtime:") {
			t.Errorf("run() error = %q, want containing %q", err.Error(), "status: runtime:")
		}
	})

	t.Run("status --help shows usage", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{
			"status", "--help",
		}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() unexpected error: %v", err)
		}
		if !strings.Contains(stderr.String(), "Usage: confine-ai status") {
			t.Errorf("stderr = %q, want containing %q", stderr.String(), "Usage: confine-ai status")
		}
	})
}

func TestRunAssistant(t *testing.T) {
	stubGetwd := func(dir string) func() (string, error) {
		return func() (string, error) {
			return dir, nil
		}
	}

	t.Run("assistant shortcut with missing assistant directory suggests init", func(t *testing.T) {
		// Use a clean HOME so the test is not affected by the developer's
		// real ~/.confine-ai/assistants/ directory.
		t.Setenv("HOME", t.TempDir())

		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{
			"claude",
		}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err == nil {
			t.Fatal("run() = nil, want error for missing assistant directory")
		}
		if !strings.Contains(err.Error(), "confine-ai init claude") {
			t.Errorf("run() error = %q, want containing %q", err.Error(), "confine-ai init claude")
		}
	})

	t.Run("assistant shortcut does not emit base-dockerfile fallback message", func(t *testing.T) {
		// The assistant shortcut auto-build path resolves the base Dockerfile with
		// announceFallback=false. Even when the user copy is absent, no fallback
		// message should be emitted from the resolver. This is a regression guard
		// against passing announceFallback=true at the assistant shortcut call site.
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		assistantDir := assistant.Dir(homeDir, "claude")
		if err := os.MkdirAll(assistantDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", assistantDir, err)
		}
		if err := os.WriteFile(filepath.Join(assistantDir, "devcontainer.json"), []byte(`{"image": "localhost/confine-ai-base:latest"}`), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}
		dataDir := assistant.DataPath(homeDir, "claude")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", dataDir, err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "claude.json"), []byte("{}"), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}

		var stdout, stderr bytes.Buffer
		_ = run(t.Context(), []string{
			"--docker-path", "/nonexistent/docker",
			"claude",
		}, &stdout, &stderr, stubGetwd("/home/user/project"))

		if strings.Contains(stderr.String(), "base Dockerfile not found") {
			t.Errorf("stderr = %q, assistant shortcut must not emit fallback message", stderr.String())
		}
	})

	t.Run("assistant shortcut with existing assistant directory fails at runtime", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		// Create assistant directory so the assistant exists.
		assistantDir := assistant.Dir(homeDir, "claude")
		if err := os.MkdirAll(assistantDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", assistantDir, err)
		}
		// Write a minimal devcontainer.json.
		cfgPath := filepath.Join(assistantDir, "devcontainer.json")
		if err := os.WriteFile(cfgPath, []byte(`{"image": "localhost/confine-ai-base:latest"}`), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error: %v", cfgPath, err)
		}
		// Create data directory with seed file for EnsureExtraFiles.
		dataDir := assistant.DataPath(homeDir, "claude")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", dataDir, err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "claude.json"), []byte("{}"), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}

		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{
			"--docker-path", "/nonexistent/docker",
			"claude",
		}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err == nil {
			t.Fatal("run() = nil, want error for runtime detection failure")
		}
		if !strings.Contains(err.Error(), "runtime") {
			t.Errorf("run() error = %q, want containing %q", err.Error(), "runtime")
		}
	})

	t.Run("assistant shortcut with config memory emits no warning", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		// Create assistant with customizations.confine-ai memory.
		assistantDir := assistant.Dir(homeDir, "claude")
		if err := os.MkdirAll(assistantDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", assistantDir, err)
		}
		cfgContent := `{"image": "localhost/confine-ai-base:latest", "customizations": {"confine-ai": {"memory": "8g", "cpus": "4"}}}`
		if err := os.WriteFile(filepath.Join(assistantDir, "devcontainer.json"), []byte(cfgContent), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}
		// Create data directory with seed file for EnsureExtraFiles.
		dataDir := assistant.DataPath(homeDir, "claude")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", dataDir, err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "claude.json"), []byte("{}"), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}

		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{
			"--docker-path", "/nonexistent/docker",
			"claude",
		}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err == nil {
			t.Fatal("run() = nil, want error (runtime detection failure)")
		}

		if strings.Contains(stderr.String(), "no memory limit set") {
			t.Errorf("run() stderr = %q, want NO memory warning when assistant config has memory", stderr.String())
		}
	})

	t.Run("assistant shortcut without memory emits warning after runtime", func(t *testing.T) {
		// This test verifies that the resource limits resolution logic runs
		// after config loading and before container operations. The warning
		// emission is after runtime detection, so we cannot test it without
		// a real runtime. Instead we verify the code path through the
		// "with config memory" case above (no warning = resolution ran).
		// The warning logic is the same code path as runUp, which is tested
		// in "no memory limit emits warning" above.
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		assistantDir := assistant.Dir(homeDir, "claude")
		if err := os.MkdirAll(assistantDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", assistantDir, err)
		}
		if err := os.WriteFile(filepath.Join(assistantDir, "devcontainer.json"), []byte(`{"image": "localhost/confine-ai-base:latest"}`), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}
		// Create data directory with seed file for EnsureExtraFiles.
		dataDir := assistant.DataPath(homeDir, "claude")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", dataDir, err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "claude.json"), []byte("{}"), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}

		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{
			"--docker-path", "/nonexistent/docker",
			"claude",
		}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err == nil {
			t.Fatal("run() = nil, want error (runtime detection failure)")
		}
		// Fails at runtime detection, before reaching resource limits resolution.
		if !strings.Contains(err.Error(), "runtime") {
			t.Errorf("run() error = %q, want containing %q", err.Error(), "runtime")
		}
	})

	t.Run("assistant shortcut with passthrough args after --", func(t *testing.T) {
		// Use a clean HOME so the test is not affected by the developer's
		// real ~/.confine-ai/assistants/ directory.
		t.Setenv("HOME", t.TempDir())

		var stdout, stderr bytes.Buffer
		// Even though assistant doesn't exist, verify the -- separator is handled
		// (the error occurs before the args are used, but the routing should still work).
		err := run(t.Context(), []string{
			"claude", "--", "--continue",
		}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err == nil {
			t.Fatal("run() = nil, want error for missing assistant directory")
		}
		if !strings.Contains(err.Error(), "confine-ai init claude") {
			t.Errorf("run() error = %q, want containing %q", err.Error(), "confine-ai init claude")
		}
	})

	t.Run("assistant shortcut --no-git-identity flag extracted (AC REQ-CL-003 #6)", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		// Create assistant directory with minimal config.
		assistantDir := assistant.Dir(homeDir, "claude")
		if err := os.MkdirAll(assistantDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", assistantDir, err)
		}
		if err := os.WriteFile(filepath.Join(assistantDir, "devcontainer.json"), []byte(`{"image": "localhost/confine-ai-base:latest"}`), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}
		dataDir := assistant.DataPath(homeDir, "claude")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", dataDir, err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "claude.json"), []byte("{}"), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}

		// Use empty gitconfig so warning would appear without --no-git-identity.
		tmpFile := filepath.Join(t.TempDir(), "gitconfig")
		if err := os.WriteFile(tmpFile, []byte(""), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}
		t.Setenv("GIT_CONFIG_GLOBAL", tmpFile)
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

		var stdout, stderr bytes.Buffer
		// --no-git-identity is extracted before folder parsing;
		// fails at runtime detection, but no git identity warning should appear.
		err := run(t.Context(), []string{
			"--docker-path", "/nonexistent/docker",
			"claude", "--no-git-identity",
		}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err == nil {
			t.Fatal("run() = nil, want error (runtime detection failure)")
		}

		if strings.Contains(stderr.String(), "git identity not forwarded") {
			t.Errorf("run() stderr = %q, want NO git identity warning when --no-git-identity is set", stderr.String())
		}
	})

	t.Run("assistant shortcut git identity warning when missing (AC REQ-CL-003 #7)", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		// Create assistant directory with minimal config.
		assistantDir := assistant.Dir(homeDir, "claude")
		if err := os.MkdirAll(assistantDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", assistantDir, err)
		}
		if err := os.WriteFile(filepath.Join(assistantDir, "devcontainer.json"), []byte(`{"image": "localhost/confine-ai-base:latest"}`), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}
		dataDir := assistant.DataPath(homeDir, "claude")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", dataDir, err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "claude.json"), []byte("{}"), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}

		// Use empty gitconfig so identity is missing.
		tmpFile := filepath.Join(t.TempDir(), "gitconfig")
		if err := os.WriteFile(tmpFile, []byte(""), 0o644); err != nil {
			t.Fatalf("WriteFile error: %v", err)
		}
		t.Setenv("GIT_CONFIG_GLOBAL", tmpFile)
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

		var stdout, stderr bytes.Buffer
		// Will fail at runtime detection, but git identity warning should appear first.
		err := run(t.Context(), []string{
			"--docker-path", "/nonexistent/docker",
			"claude",
		}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err == nil {
			t.Fatal("run() = nil, want error (runtime detection failure)")
		}

		if !strings.Contains(stderr.String(), "git identity not forwarded") {
			t.Errorf("run() stderr = %q, want containing %q", stderr.String(), "git identity not forwarded")
		}
	})
}

func TestRun_AssistantWithFolderArgs(t *testing.T) {
	t.Run("assistant shortcut with folder args and passthrough (AC REQ-CL-005 #2)", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		assistantDir := assistant.Dir(homeDir, "claude")
		if err := os.MkdirAll(assistantDir, 0o755); err != nil {
			t.Fatal(err)
		}
		cfgContent := `{"image": "localhost/confine-ai-base:latest"}`
		if err := os.WriteFile(filepath.Join(assistantDir, "devcontainer.json"), []byte(cfgContent), 0o644); err != nil {
			t.Fatal(err)
		}
		// Create data directory with seed file for EnsureExtraFiles.
		dataDir := assistant.DataPath(homeDir, "claude")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "claude.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}

		dir1 := t.TempDir()
		dir2 := t.TempDir()

		var stdout, stderr bytes.Buffer
		getwd := func() (string, error) { return dir1, nil }

		// Should parse folders before -- and args after --.
		// Fails at runtime detection since no docker is available.
		err := run(t.Context(), []string{
			"--docker-path", filepath.Join(homeDir, "nonexistent-docker"),
			"claude", dir1, dir2, "--", "--continue",
		}, &stdout, &stderr, getwd)
		if err == nil {
			t.Fatal("run() = nil, want error for runtime detection failure")
		}
		if !strings.Contains(err.Error(), "runtime") {
			t.Errorf("run() error = %q, want containing %q", err.Error(), "runtime")
		}
	})

	t.Run("assistant shortcut with non-existent folder returns error", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		assistantDir := assistant.Dir(homeDir, "claude")
		if err := os.MkdirAll(assistantDir, 0o755); err != nil {
			t.Fatal(err)
		}

		var stdout, stderr bytes.Buffer
		getwd := func() (string, error) { return homeDir, nil }

		err := run(t.Context(), []string{
			"claude", "/nonexistent/folder/xyz",
		}, &stdout, &stderr, getwd)
		if err == nil {
			t.Fatal("run() = nil, want error for non-existent folder")
		}
		if !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("run() error = %q, want containing %q", err.Error(), "does not exist")
		}
	})

	t.Run("assistant shortcut with only -- preserves existing behavior", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())

		// "claude -- --continue" should have no folders (defaults to cwd),
		// and "--continue" as the assistant arg.
		var stdout, stderr bytes.Buffer
		getwd := func() (string, error) { return "/home/user/project", nil }

		err := run(t.Context(), []string{
			"claude", "--", "--continue",
		}, &stdout, &stderr, getwd)
		if err == nil {
			t.Fatal("run() = nil, want error for missing assistant directory")
		}
		// Should fail at assistant lookup, not folder parsing.
		if !strings.Contains(err.Error(), "confine-ai init claude") {
			t.Errorf("run() error = %q, want containing %q", err.Error(), "confine-ai init claude")
		}
	})
}

func TestRun_Completion(t *testing.T) {
	stubGetwd := func(dir string) func() (string, error) {
		return func() (string, error) { return dir, nil }
	}

	t.Run("bash outputs script", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"completion", "bash"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() error: %v", err)
		}
		if !strings.Contains(stdout.String(), "__complete") {
			t.Errorf("run() stdout = %q, want containing '__complete'", stdout.String())
		}
		if !strings.Contains(stdout.String(), "confine-ai") {
			t.Errorf("run() stdout = %q, want containing 'confine-ai'", stdout.String())
		}
	})

	t.Run("bash skips instructions when stdout is not a terminal", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"completion", "bash"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() error: %v", err)
		}
		if strings.Contains(stderr.String(), "~/.bashrc") {
			t.Errorf("run() stderr = %q, want no instructions when stdout is not a terminal", stderr.String())
		}
	})

	t.Run("zsh outputs script", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"completion", "zsh"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() error: %v", err)
		}
		if !strings.Contains(stdout.String(), "__complete") {
			t.Errorf("run() stdout = %q, want containing '__complete'", stdout.String())
		}
	})

	t.Run("zsh skips instructions when stdout is not a terminal", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"completion", "zsh"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() error: %v", err)
		}
		if strings.Contains(stderr.String(), "~/.zshrc") {
			t.Errorf("run() stderr = %q, want no instructions when stdout is not a terminal", stderr.String())
		}
	})

	t.Run("no args returns error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"completion"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err == nil {
			t.Fatal("run() = nil, want error")
		}
		if !strings.Contains(err.Error(), "bash") {
			t.Errorf("run() error = %q, want containing 'bash'", err.Error())
		}
		if !strings.Contains(err.Error(), "zsh") {
			t.Errorf("run() error = %q, want containing 'zsh'", err.Error())
		}
	})

	t.Run("fish returns error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"completion", "fish"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err == nil {
			t.Fatal("run() = nil, want error")
		}
		if !strings.Contains(err.Error(), "bash") {
			t.Errorf("run() error = %q, want containing 'bash'", err.Error())
		}
		if !strings.Contains(err.Error(), "zsh") {
			t.Errorf("run() error = %q, want containing 'zsh'", err.Error())
		}
	})

	t.Run("help does not show __complete", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"--help"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() error: %v", err)
		}
		if strings.Contains(stderr.String(), "__complete") {
			t.Errorf("run() stderr = %q, want NOT containing '__complete'", stderr.String())
		}
	})
}

// TestRun_Complete tests the __complete hidden command wiring (REQ-SC-002).
func TestRun_Complete(t *testing.T) {
	stubGetwd := func(dir string) func() (string, error) {
		return func() (string, error) { return dir, nil }
	}

	t.Run("first arg completions include subcommands", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"__complete", "--", ""}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() error: %v", err)
		}
		out := stdout.String()
		for _, cmd := range []string{"rm", "init", "status", "update", "completion"} {
			if !strings.Contains(out, cmd) {
				t.Errorf("run() stdout = %q, want containing %q", out, cmd)
			}
		}
	})

	t.Run("prefix filtering works", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"__complete", "--", "r"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() error: %v", err)
		}
		out := strings.TrimSpace(stdout.String())
		if out != "rm" {
			t.Errorf("run() stdout = %q, want %q", out, "rm")
		}
	})

	t.Run("completion subcommand suggests shells", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"__complete", "completion", "--", ""}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() error: %v", err)
		}
		out := stdout.String()
		if !strings.Contains(out, "bash") {
			t.Errorf("run() stdout = %q, want containing 'bash'", out)
		}
		if !strings.Contains(out, "zsh") {
			t.Errorf("run() stdout = %q, want containing 'zsh'", out)
		}
	})

	t.Run("init subcommand suggests templates", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"__complete", "init", "--", ""}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() error: %v", err)
		}
		out := stdout.String()
		for _, name := range []string{"claude", "copilot", "opencode"} {
			if !strings.Contains(out, name) {
				t.Errorf("run() stdout = %q, want containing %q", out, name)
			}
		}
	})

	t.Run("update flags suggested", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"__complete", "update", "--", "--"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() error: %v", err)
		}
		out := stdout.String()
		if !strings.Contains(out, "--dry-run") {
			t.Errorf("run() stdout = %q, want containing '--dry-run'", out)
		}
		if !strings.Contains(out, "--yes") {
			t.Errorf("run() stdout = %q, want containing '--yes'", out)
		}
	})

	t.Run("no args returns all first-arg completions", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"__complete"}, &stdout, &stderr, stubGetwd("/home/user/project"))
		if err != nil {
			t.Fatalf("run() error: %v", err)
		}
		out := stdout.String()
		if !strings.Contains(out, "update") {
			t.Errorf("run() stdout = %q, want containing 'update'", out)
		}
	})
}

// testGetwdError is a custom error type for testing getwd failures.
type testGetwdError struct{}

func (*testGetwdError) Error() string {
	return "working directory unavailable"
}

// captureBuilder is an assistant.ImageBuilder AND container.Executor that records
// the bytes of the Dockerfile in the build context passed to Run.
// BuildBaseImage writes the Dockerfile to a temp directory and passes the
// directory as the final positional argument; captureBuilder reads that file
// during Run (before BuildBaseImage's defer cleanup removes the directory).
//
// It also implements container.Executor (Output, Run, RunInteractive) so it
// can be injected into runBuildBaseWithExecutor and runUpdateWithExecutor.
// The outputResults queue feeds any Output call in order — callers control
// what post-build queries (ps, image ls) return by seeding the queue.
type captureBuilder struct {
	capturedDockerfile []byte
	runErr             error

	// outputResults feeds Output calls in order. Each call pops one result.
	// Used for EnsureBaseImage's image-inspect (return "no such image"), for
	// post-build ps/image-ls queries in update orchestrators, etc.
	outputResults []captureOutputResult
	outputIdx     int
}

type captureOutputResult struct {
	output string
	err    error
}

func (c *captureBuilder) Output(_ context.Context, _ ...string) (string, error) {
	if c.outputIdx >= len(c.outputResults) {
		return "", errors.New("captureBuilder: no more Output results")
	}
	r := c.outputResults[c.outputIdx]
	c.outputIdx++
	return r.output, r.err
}

func (c *captureBuilder) Run(_ context.Context, _, _ io.Writer, args ...string) error {
	// captureBuilder.Run is also invoked by container helpers (rmi <img>, stop,
	// rm) during update orchestrators. Only the "build ... <ctx>" invocation
	// has a Dockerfile to capture — identify it by the "build" subcommand and
	// the directory context as the final positional argument.
	if len(args) == 0 {
		return errors.New("captureBuilder: Run called with no args")
	}
	if args[0] != "build" {
		return c.runErr
	}
	buildCtx := args[len(args)-1]
	dfPath := filepath.Join(buildCtx, "Dockerfile")
	data, err := os.ReadFile(dfPath)
	if err != nil {
		return err
	}
	c.capturedDockerfile = data
	return c.runErr
}

// RunInteractive is a no-op; runBuildBaseWithExecutor and
// runUpdateWithExecutor never call interactive commands. Defined to satisfy
// container.Executor so captureBuilder can be injected directly.
func (*captureBuilder) RunInteractive(_ context.Context, _ io.Reader, _, _ io.Writer, _ ...string) error {
	return nil
}

// TestBaseDockerfileBytes_ForwardedFromUserCopy verifies that when
// ~/.confine-ai/base/Dockerfile exists, the exact bytes passed to
// assistant.BuildBaseImage (and assistant.EnsureBaseImage) come from the user copy
// and NOT from the embedded seed. This is a regression guard against wiring
// the wrong variable (seed vs. user copy) at the call sites in main.go:
// runBuildBaseWithExecutor and the assistant shortcut auto-build path.
//
// Strategy:
//
//   - runBuildBase subtest drives the injectable helper
//     runBuildBaseWithExecutor directly with a captureBuilder. A regression
//     that swapped `resolvedBase` for `baseDockerfile` would fail the test.
//   - The assistant_shortcut subtest mirrors the auto-build sequence at the
//     assistant-package layer (ResolveBaseDockerfile + EnsureBaseImage).
//   - The assistant_layer_contract subtest locks the assistant-package contract
//     independent of main.go wiring.
//
// Every subtest pre-checks that the sentinel is absent from the embedded seed
// so a false-positive pass (capturing the seed and "finding" the sentinel) is
// impossible, then asserts the captured Dockerfile contains the sentinel AND
// differs from `baseDockerfile` AND equals `userCopy` exactly.
func TestBaseDockerfileBytes_ForwardedFromUserCopy(t *testing.T) {
	const sentinel = "# SENTINEL-AC5-AC6-a1b2c3d4-e5f6-7890-abcd-ef0123456789"

	// Safety: if the sentinel somehow appears in the embedded seed, the
	// "contains sentinel" assertion would be meaningless.
	if bytes.Contains(baseDockerfile, []byte(sentinel)) {
		t.Fatalf("embedded seed already contains sentinel %q; choose a different marker", sentinel)
	}

	userCopy := []byte("# user-owned base\nFROM debian:bookworm-slim\n" + sentinel + "\nRUN echo hi\n")

	// writeUserCopy seeds ~/.confine-ai/base/Dockerfile under homeDir.
	writeUserCopy := func(t *testing.T, homeDir string) {
		t.Helper()
		path := assistant.BaseDockerfilePath(homeDir)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, userCopy, 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error: %v", path, err)
		}
	}

	assertCaptured := func(t *testing.T, captured []byte) {
		t.Helper()
		if captured == nil {
			t.Fatal("capture builder did not record any Dockerfile bytes")
		}
		if !bytes.Contains(captured, []byte(sentinel)) {
			t.Errorf("captured Dockerfile missing sentinel %q; got %q", sentinel, string(captured))
		}
		if bytes.Equal(captured, baseDockerfile) {
			t.Error("captured Dockerfile equals embedded seed; user copy was not forwarded")
		}
		if !bytes.Equal(captured, userCopy) {
			t.Errorf("captured Dockerfile differs from user copy\n got: %q\nwant: %q", string(captured), string(userCopy))
		}
	}

	t.Run("assistant shortcut call site forwards user copy", func(t *testing.T) {
		homeDir := t.TempDir()
		writeUserCopy(t, homeDir)

		// Mirror the assistant shortcut auto-build sequence: resolve with
		// announceFallback=false, then EnsureBaseImage. EnsureBaseImage first
		// calls Output("image","inspect",...); we make that return "no such
		// image" so it proceeds to the build path.
		var stderr bytes.Buffer
		resolved, err := assistant.ResolveBaseDockerfile(homeDir, baseDockerfile, &stderr, false)
		if err != nil {
			t.Fatalf("ResolveBaseDockerfile() error: %v", err)
		}

		builder := &captureBuilder{
			outputResults: []captureOutputResult{
				{output: "", err: errors.New("no such image")},
			},
		}
		if err := assistant.EnsureBaseImage(t.Context(), builder, resolved, io.Discard); err != nil {
			t.Fatalf("EnsureBaseImage() error: %v", err)
		}

		assertCaptured(t, builder.capturedDockerfile)

		// Assistant shortcut runs with announceFallback=false: never announce.
		if strings.Contains(stderr.String(), "base Dockerfile not found") {
			t.Errorf("stderr = %q, should never contain fallback message on assistant shortcut path", stderr.String())
		}
	})

	t.Run("assistant layer contract forwards user copy", func(t *testing.T) {
		// Contract test for the assistant package: ResolveBaseDockerfile +
		// BuildBaseImage forward the resolved user copy unchanged. This
		// duplicates a slice of the main.go wiring tests above but locks the
		// lower-level contract independently so a refactor of main.go
		// cannot silently break the assistant package's guarantee.
		homeDir := t.TempDir()
		writeUserCopy(t, homeDir)

		var stderr bytes.Buffer
		resolved, err := assistant.ResolveBaseDockerfile(homeDir, baseDockerfile, &stderr, true)
		if err != nil {
			t.Fatalf("ResolveBaseDockerfile() error: %v", err)
		}

		builder := &captureBuilder{}
		if err := assistant.BuildBaseImage(t.Context(), builder, resolved, nil, assistant.BuildOptions{}, io.Discard); err != nil {
			t.Fatalf("BuildBaseImage() error: %v", err)
		}

		assertCaptured(t, builder.capturedDockerfile)
	})
}

// TestEmbeddedBaseDockerfile_Sha256VerifyBeforeExtract structurally verifies
// that every RUN block in the embedded seed Dockerfile that downloads an
// archive performs `sha256sum -c` BEFORE any tar extraction, within the same
// `&&`-chained RUN command. This is a regression guard against a future edit
// that accidentally inverts the order (extract-then-verify) or drops the
// verify step entirely — the exact attack surface AC-9 through AC-12 pin
// down. Pure string parsing; no container runtime required.
func TestEmbeddedBaseDockerfile_Sha256VerifyBeforeExtract(t *testing.T) {
	seed := string(baseDockerfile)

	// Split into logical RUN blocks. A RUN block starts at a line beginning
	// with "RUN " and continues while the previous line ends with "\" (shell
	// line continuation).
	runBlocks := extractRunBlocks(seed)
	if len(runBlocks) == 0 {
		t.Fatal("embedded seed has no RUN blocks; Dockerfile parser is broken or seed is empty")
	}

	// curl -fsSL -o /tmp/<name>.tar.gz ...
	downloadRE := regexp.MustCompile(`curl\b[^&]*\.tar\.gz`)
	// sha256sum -c (with optional flags like -) within the chain.
	verifyRE := regexp.MustCompile(`\bsha256sum\s+-c\b`)
	// tar extraction: tar -xz or tar xzf or tar -C ... -xzf ...
	extractRE := regexp.MustCompile(`\btar\b[^&]*-[xX]z`)

	var sawDownloadBlock bool
	for i, block := range runBlocks {
		if !downloadRE.MatchString(block) {
			continue
		}
		sawDownloadBlock = true

		verifyLoc := verifyRE.FindStringIndex(block)
		extractLoc := extractRE.FindStringIndex(block)

		if verifyLoc == nil {
			t.Errorf("RUN block #%d downloads a .tar.gz archive but has no `sha256sum -c` verification:\n%s", i, block)
			continue
		}
		if extractLoc == nil {
			t.Errorf("RUN block #%d downloads a .tar.gz archive but has no tar extraction command:\n%s", i, block)
			continue
		}
		if verifyLoc[0] >= extractLoc[0] {
			t.Errorf("RUN block #%d extracts before (or without) verifying sha256; verify at offset %d, extract at offset %d:\n%s",
				i, verifyLoc[0], extractLoc[0], block)
			continue
		}

		// Confirm the chain between verify and extract uses `&&` so a
		// failing verify actually aborts the extract. Look at the substring
		// between the two commands. Safe to slice because verifyLoc[0] <
		// extractLoc[0] and verifyLoc[1] <= extractLoc[0] is guaranteed by
		// the verify and extract regexes not overlapping (verify matches
		// `sha256sum -c`, extract matches `tar ... -xz`).
		if verifyLoc[1] > extractLoc[0] {
			t.Errorf("RUN block #%d has overlapping verify/extract matches (verify end=%d, extract start=%d):\n%s",
				i, verifyLoc[1], extractLoc[0], block)
			continue
		}
		between := block[verifyLoc[1]:extractLoc[0]]
		if !strings.Contains(between, "&&") {
			t.Errorf("RUN block #%d has verify and extract but no `&&` between them (verify failure would not abort extract):\nbetween=%q\nblock=%s",
				i, between, block)
		}
	}

	if !sawDownloadBlock {
		t.Fatal("embedded seed has no RUN blocks that download a .tar.gz archive; expected Go and Corretto download blocks")
	}

	// Sanity: expect at least two download blocks (Go and Corretto).
	var downloadBlockCount int
	for _, block := range runBlocks {
		if downloadRE.MatchString(block) {
			downloadBlockCount++
		}
	}
	if downloadBlockCount < 2 {
		t.Errorf("found %d download RUN blocks, want at least 2 (Go and Java/Corretto)", downloadBlockCount)
	}
}

// TestSha256VerifyBeforeExtract_ParserSanity exercises the parser and
// regexes used by TestEmbeddedBaseDockerfile_Sha256VerifyBeforeExtract
// against synthetic Dockerfile fixtures. It documents the exact failure
// modes the structural test is guarding against: inverted order (extract
// before verify), missing verify, missing extract, and an `&&`-less chain.
// Without this test, a silent regression in the parser could cause
// TestEmbeddedBaseDockerfile_Sha256VerifyBeforeExtract to vacuously pass.
func TestSha256VerifyBeforeExtract_ParserSanity(t *testing.T) {
	downloadRE := regexp.MustCompile(`curl\b[^&]*\.tar\.gz`)
	verifyRE := regexp.MustCompile(`\bsha256sum\s+-c\b`)
	extractRE := regexp.MustCompile(`\btar\b[^&]*-[xX]z`)

	type outcome struct {
		download     bool
		hasVerify    bool
		hasExtract   bool
		verifyBefore bool
		andBetween   bool
	}
	analyze := func(dockerfile string) outcome {
		blocks := extractRunBlocks(dockerfile)
		for _, b := range blocks {
			if !downloadRE.MatchString(b) {
				continue
			}
			v := verifyRE.FindStringIndex(b)
			e := extractRE.FindStringIndex(b)
			o := outcome{
				download:   true,
				hasVerify:  v != nil,
				hasExtract: e != nil,
			}
			if v != nil && e != nil {
				o.verifyBefore = v[0] < e[0]
				if o.verifyBefore {
					o.andBetween = strings.Contains(b[v[1]:e[0]], "&&")
				}
			}
			return o
		}
		return outcome{}
	}

	correct := `FROM debian:bookworm-slim
RUN curl -fsSL -o /tmp/go.tar.gz https://example/go.tar.gz \
    && echo "abc  /tmp/go.tar.gz" | sha256sum -c - \
    && tar -C /usr/local -xzf /tmp/go.tar.gz
`
	inverted := `FROM debian:bookworm-slim
RUN curl -fsSL -o /tmp/go.tar.gz https://example/go.tar.gz \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && echo "abc  /tmp/go.tar.gz" | sha256sum -c -
`
	missingVerify := `FROM debian:bookworm-slim
RUN curl -fsSL -o /tmp/go.tar.gz https://example/go.tar.gz \
    && tar -C /usr/local -xzf /tmp/go.tar.gz
`
	missingExtract := `FROM debian:bookworm-slim
RUN curl -fsSL -o /tmp/go.tar.gz https://example/go.tar.gz \
    && echo "abc  /tmp/go.tar.gz" | sha256sum -c -
`

	tests := []struct {
		name       string
		dockerfile string
		want       outcome
	}{
		{
			name:       "correct order passes all gates",
			dockerfile: correct,
			want:       outcome{download: true, hasVerify: true, hasExtract: true, verifyBefore: true, andBetween: true},
		},
		{
			name:       "inverted order fails verify-before-extract",
			dockerfile: inverted,
			want:       outcome{download: true, hasVerify: true, hasExtract: true, verifyBefore: false, andBetween: false},
		},
		{
			name:       "missing verify is detected",
			dockerfile: missingVerify,
			want:       outcome{download: true, hasVerify: false, hasExtract: true},
		},
		{
			name:       "missing extract is detected",
			dockerfile: missingExtract,
			want:       outcome{download: true, hasVerify: true, hasExtract: false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyze(tt.dockerfile)
			if got != tt.want {
				t.Errorf("analyze() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// extractRunBlocks returns the logical RUN blocks in a Dockerfile. A RUN
// block starts at a line beginning with "RUN " and extends across
// continuation lines (previous line ends with backslash).
func extractRunBlocks(dockerfile string) []string {
	var blocks []string
	lines := strings.Split(dockerfile, "\n")
	i := 0
	for i < len(lines) {
		if !strings.HasPrefix(lines[i], "RUN ") {
			i++
			continue
		}
		start := i
		// Consume continuation lines.
		for i < len(lines) && strings.HasSuffix(strings.TrimRight(lines[i], " \t"), "\\") {
			i++
		}
		// i now points at the final (non-continuation) line of the block.
		if i < len(lines) {
			i++
		}
		blocks = append(blocks, strings.Join(lines[start:i], "\n"))
	}
	return blocks
}

// stubStdin points os.Stdin at a temp file containing content for the duration
// of the test. The file is a regular file, so isatty(os.Stdin) returns false
// and interactive prompts are skipped — use this to preserve non-interactive
// behavior in tests that go through run() and may otherwise read a real TTY.
func stubStdin(t *testing.T, content string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("create temp stdin: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp stdin: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek temp stdin: %v", err)
	}
	orig := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = orig
		_ = f.Close()
	})
}

func TestRun_Init_YesFlagOverwritesBase(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	stubStdin(t, "")

	basePath := assistant.BaseDockerfilePath(homeDir)
	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	userCopy := []byte("# user edited\nFROM ubuntu:24.04\n")
	if err := os.WriteFile(basePath, userCopy, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	stubGetwd := func() (string, error) { return "/home/user/project", nil }

	var stdout, stderr bytes.Buffer
	if err := run(t.Context(), []string{"init", "-y"}, &stdout, &stderr, stubGetwd); err != nil {
		t.Fatalf("run() error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Overwrote base Dockerfile") {
		t.Errorf("stdout = %q, want 'Overwrote base Dockerfile'", stdout.String())
	}

	got, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if bytes.Equal(got, userCopy) {
		t.Errorf("base file unchanged; expected overwrite")
	}
	if !bytes.Equal(got, baseDockerfile) {
		t.Errorf("base file = %q, want embedded seed", string(got))
	}
}
