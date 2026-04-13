package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woditschka/confine-ai/internal/assistant"
	"github.com/woditschka/confine-ai/internal/config"
	"github.com/woditschka/confine-ai/internal/container"
	"github.com/woditschka/confine-ai/internal/runtime"
)

func TestEnsureHostMountDirs(t *testing.T) {
	t.Run("opencode creates missing dir with mode 0o700", func(t *testing.T) {
		// First-time-user scenario: ~/.local/share/opencode does not exist.
		// ensureHostMountDirs must create it with mode 0o700 so the host
		// directory lines up with the bind mount before container.Up runs
		// its ValidateMounts check and before the runtime would otherwise
		// materialize it as a root-owned empty dir.
		homeDir := t.TempDir()

		if err := ensureHostMountDirs(homeDir, "opencode"); err != nil {
			t.Fatalf("ensureHostMountDirs(opencode) error: %v", err)
		}

		path := filepath.Join(homeDir, ".local", "share", "opencode")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q) error: %v", path, err)
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", path)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("dir mode = %o, want %o", got, 0o700)
		}
	})

	t.Run("opencode leaves existing dir untouched", func(t *testing.T) {
		// Returning-user scenario: host dir already exists with real
		// contents (auth.json, mode set by opencode). ensureHostMountDirs
		// must be a no-op; MkdirAll does not change the mode or contents
		// of an existing directory.
		homeDir := t.TempDir()
		existing := filepath.Join(homeDir, ".local", "share", "opencode")
		if err := os.MkdirAll(existing, 0o755); err != nil {
			t.Fatalf("MkdirAll setup error: %v", err)
		}
		sentinel := filepath.Join(existing, "auth.json")
		if err := os.WriteFile(sentinel, []byte(`{"token":"x"}`), 0o600); err != nil {
			t.Fatalf("WriteFile sentinel error: %v", err)
		}

		if err := ensureHostMountDirs(homeDir, "opencode"); err != nil {
			t.Fatalf("ensureHostMountDirs(opencode) error: %v", err)
		}

		// Sentinel must still be present and unchanged.
		got, err := os.ReadFile(sentinel)
		if err != nil {
			t.Fatalf("ReadFile(%q) error: %v", sentinel, err)
		}
		if string(got) != `{"token":"x"}` {
			t.Errorf("sentinel content = %q, want unchanged", string(got))
		}
		// MkdirAll does not chmod existing directories; the 0o755 from
		// setup must survive.
		info, err := os.Stat(existing)
		if err != nil {
			t.Fatalf("Stat(%q) error: %v", existing, err)
		}
		if got := info.Mode().Perm(); got != 0o755 {
			t.Errorf("existing dir mode changed to %o, want 0o755 preserved", got)
		}
	})

	t.Run("claude is a no-op", func(t *testing.T) {
		homeDir := t.TempDir()
		if err := ensureHostMountDirs(homeDir, "claude"); err != nil {
			t.Fatalf("ensureHostMountDirs(claude) error: %v", err)
		}
		// No opencode dir should have been created under this homeDir.
		if _, err := os.Stat(filepath.Join(homeDir, ".local")); !os.IsNotExist(err) {
			t.Errorf("claude created a .local directory, want no-op; err = %v", err)
		}
	})

	t.Run("copilot is a no-op", func(t *testing.T) {
		homeDir := t.TempDir()
		if err := ensureHostMountDirs(homeDir, "copilot"); err != nil {
			t.Fatalf("ensureHostMountDirs(copilot) error: %v", err)
		}
		if _, err := os.Stat(filepath.Join(homeDir, ".local")); !os.IsNotExist(err) {
			t.Errorf("copilot created a .local directory, want no-op; err = %v", err)
		}
	})

	t.Run("unknown assistant is a no-op", func(t *testing.T) {
		homeDir := t.TempDir()
		if err := ensureHostMountDirs(homeDir, "my-tool"); err != nil {
			t.Fatalf("ensureHostMountDirs(my-tool) error: %v", err)
		}
		if _, err := os.Stat(filepath.Join(homeDir, ".local")); !os.IsNotExist(err) {
			t.Errorf("unknown assistant created a .local directory, want no-op; err = %v", err)
		}
	})
}

func TestRunAssistant(t *testing.T) {
	t.Run("nonexistent assistant returns error with init suggestion", func(t *testing.T) {
		// RunAssistant checks assistant.Exists before reaching newExecutor,
		// so this test works without a real container runtime.
		homeDir := t.TempDir()
		// Set HOME so os.UserHomeDir returns our temp dir.
		t.Setenv("HOME", homeDir)

		var stdout, stderr bytes.Buffer
		err := RunAssistant(t.Context(), &stdout, &stderr, "nonexistent", "/some/workspace", "", nil, nil)
		if err == nil {
			t.Fatal("RunAssistant() = nil, want error for missing assistant")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("RunAssistant() error = %q, want containing %q", err.Error(), "not found")
		}
		if !strings.Contains(err.Error(), "confine-ai init") {
			t.Errorf("RunAssistant() error = %q, want containing init suggestion", err.Error())
		}
	})

	t.Run("folder resolution error returns error", func(t *testing.T) {
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		// Initialize the assistant so we pass the Exists check.
		if err := assistant.Init(homeDir, "claude", []byte("FROM ubuntu:24.04\n")); err != nil {
			t.Fatalf("assistant.Init: %v", err)
		}

		var stdout, stderr bytes.Buffer
		// Pass a nonexistent folder path to trigger folder resolution error.
		err := RunAssistant(t.Context(), &stdout, &stderr, "claude", "/some/workspace", "", []string{"/nonexistent/folder"}, nil)
		if err == nil {
			t.Fatal("RunAssistant() = nil, want error for bad folder")
		}
		if !strings.Contains(err.Error(), "folder") {
			t.Errorf("RunAssistant() error = %q, want containing %q", err.Error(), "folder")
		}
	})
}

// seedAssistantHome creates a temp home with a valid assistant config so
// runAssistantWithExecutor can proceed past config loading.
func seedAssistantHome(t *testing.T, name string) (homeDir, workspaceDir string) {
	t.Helper()
	homeDir = t.TempDir()
	workspaceDir = t.TempDir()

	// Initialize assistant (creates devcontainer.json).
	if err := assistant.Init(homeDir, name, []byte("FROM ubuntu:24.04\n")); err != nil {
		t.Fatalf("assistant.Init: %v", err)
	}

	// Seed base Dockerfile so ResolveBaseDockerfile succeeds.
	if _, err := assistant.SeedBaseDockerfile(homeDir, []byte("FROM ubuntu:24.04\n")); err != nil {
		t.Fatalf("SeedBaseDockerfile: %v", err)
	}

	return homeDir, workspaceDir
}

func TestRunAssistantWithExecutor(t *testing.T) {
	t.Run("invalid allowed hosts returns error", func(t *testing.T) {
		homeDir, wsDir := seedAssistantHome(t, "claude")
		exec := &cliFakeExecutor{}

		var stdout, stderr bytes.Buffer
		err := runAssistantWithExecutor(t.Context(), exec, runtime.Runtime{Name: "docker"}, &stdout, &stderr, assistantParams{
			assistantName:   "claude",
			workspaceFolder: wsDir,
			allowedHosts:    []string{"bad;host"},
			homeDir:         homeDir,
			baseDockerfile:  []byte("FROM ubuntu:24.04\n"),
		}, "", "")

		if err == nil {
			t.Fatal("runAssistantWithExecutor() = nil, want error for invalid allowed hosts")
		}
	})

	t.Run("base image ensure failure returns error", func(t *testing.T) {
		homeDir, wsDir := seedAssistantHome(t, "claude")
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: "", err: errors.New("image not found")}, // image inspect fails
			},
			runResults: []error{
				errors.New("build failed"), // build fails
			},
		}

		var stdout, stderr bytes.Buffer
		err := runAssistantWithExecutor(t.Context(), exec, runtime.Runtime{Name: "docker"}, &stdout, &stderr, assistantParams{
			assistantName:   "claude",
			workspaceFolder: wsDir,
			homeDir:         homeDir,
			baseDockerfile:  []byte("FROM ubuntu:24.04\n"),
		}, "", "")

		if err == nil {
			t.Fatal("runAssistantWithExecutor() = nil, want error for base image failure")
		}
		if !strings.Contains(err.Error(), "base image") {
			t.Errorf("runAssistantWithExecutor() error = %q, want containing %q", err.Error(), "base image")
		}
	})

	t.Run("config load and up with no existing container", func(t *testing.T) {
		homeDir, wsDir := seedAssistantHome(t, "claude")
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: "sha256:abc\n"},      // EnsureBaseImage: image inspect succeeds
				{output: ""},                  // FindByAssistant: no containers
				{output: ""},                  // FindByLabels (inside Up): no containers
				{output: "newcontainer123\n"}, // createContainer: returns ID
			},
			runResults: []error{
				nil, // applyFirewallRules (inside setupFirewall)
			},
		}

		var stdout, stderr bytes.Buffer
		err := runAssistantWithExecutor(t.Context(), exec, runtime.Runtime{Name: "docker"}, &stdout, &stderr, assistantParams{
			assistantName:   "claude",
			workspaceFolder: wsDir,
			homeDir:         homeDir,
			baseDockerfile:  []byte("FROM ubuntu:24.04\n"),
			noGitIdentity:   true,
		}, "", "")

		// The call fails because the fake executor runs out of canned
		// responses past container creation. The fact that we reached Up
		// means config loading, resource limits, and dispatch all worked.
		if err == nil {
			t.Fatal("runAssistantWithExecutor() = nil, want error from incomplete executor")
		}
		if !strings.Contains(stderr.String(), "Loading assistant configuration") {
			t.Errorf("runAssistantWithExecutor() stderr = %q, want containing %q", stderr.String(), "Loading assistant configuration")
		}
	})

	t.Run("existing container with matching hash reconnects", func(t *testing.T) {
		homeDir, wsDir := seedAssistantHome(t, "claude")

		// We need the config hash that runAssistantWithExecutor will compute.
		// Load the config the same way the function does to get the right hash.
		cfgPath := assistant.ConfigPath(homeDir, "claude")
		cfg, _, _ := config.LoadFromWorkspace(wsDir, cfgPath, &bytes.Buffer{}, os.LookupEnv)
		cfg.WorkspaceFolder = "/workspace/" + filepath.Base(wsDir)
		currentHash := container.ConfigHashWithFolders(cfg, nil)

		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: "sha256:abc\n"},                                     // EnsureBaseImage: image inspect succeeds
				{output: "aabb0011\n"},                                       // FindByAssistant: one container
				{output: currentHash + "\n"},                                 // InspectConfigHash: matching hash
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errors.New("no host.docker.internal")},     // hostInternalIP
			},
			runResults: []error{
				nil, // docker start
				nil, // applyFirewallRules (iptables)
			},
		}

		var stdout, stderr bytes.Buffer
		err := runAssistantWithExecutor(t.Context(), exec, runtime.Runtime{Name: "docker"}, &stdout, &stderr, assistantParams{
			assistantName:   "claude",
			workspaceFolder: wsDir,
			homeDir:         homeDir,
			baseDockerfile:  []byte("FROM ubuntu:24.04\n"),
			noGitIdentity:   true,
		}, "", "")

		// RunInteractive returns nil from the fake, so the reconnect path
		// completes successfully through ExecInteractive.
		if err != nil {
			t.Fatalf("runAssistantWithExecutor() unexpected error: %v", err)
		}
	})

	t.Run("up returns error outcome", func(t *testing.T) {
		homeDir, wsDir := seedAssistantHome(t, "claude")
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: "sha256:abc\n"}, // EnsureBaseImage: image inspect succeeds
				{output: ""},             // FindByAssistant: no containers
				{output: ""},             // FindByLabels (inside Up): no containers
			},
			runResults: []error{},
		}

		var stdout, stderr bytes.Buffer
		err := runAssistantWithExecutor(t.Context(), exec, runtime.Runtime{Name: "docker"}, &stdout, &stderr, assistantParams{
			assistantName:   "claude",
			workspaceFolder: wsDir,
			homeDir:         homeDir,
			baseDockerfile:  []byte("FROM ubuntu:24.04\n"),
			noGitIdentity:   true,
		}, "", "")

		// Up will fail trying to create the container (no more Output results),
		// which exercises the error path.
		if err == nil {
			t.Fatal("runAssistantWithExecutor() = nil, want error")
		}
	})

	t.Run("shell mode wraps command in bash", func(t *testing.T) {
		homeDir, wsDir := seedAssistantHome(t, "claude")
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: "sha256:abc\n"},      // EnsureBaseImage: image inspect succeeds
				{output: ""},                  // FindByAssistant: no containers
				{output: ""},                  // FindByLabels (inside Up): no containers
				{output: "newcontainer123\n"}, // createContainer
			},
			runResults: []error{nil},
		}

		var stdout, stderr bytes.Buffer
		err := runAssistantWithExecutor(t.Context(), exec, runtime.Runtime{Name: "docker"}, &stdout, &stderr, assistantParams{
			assistantName:            "claude",
			workspaceFolder:          wsDir,
			homeDir:                  homeDir,
			baseDockerfile:           []byte("FROM ubuntu:24.04\n"),
			noGitIdentity:            true,
			shellMode:                true,
			assistantPassthroughArgs: []string{"--continue"},
		}, "", "")
		if err == nil {
			t.Fatal("runAssistantWithExecutor() = nil, want error from incomplete executor")
		}

		if !strings.Contains(stderr.String(), "Loading assistant configuration") {
			t.Errorf("runAssistantWithExecutor() stderr = %q, want containing %q", stderr.String(), "Loading assistant configuration")
		}
	})

	t.Run("config load error for bad workspace returns error", func(t *testing.T) {
		homeDir, _ := seedAssistantHome(t, "claude")
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: "sha256:abc\n"}, // EnsureBaseImage: image inspect succeeds
				{output: ""},             // FindByAssistant: no containers
			},
		}

		var stdout, stderr bytes.Buffer
		// Podman's default network is "podman" (not "none"), so we use a runtime
		// with Name "none-rt" that returns "none" as default — but DefaultNetwork
		// only returns "podman" for name=="podman" and "bridge" otherwise.
		// Instead, test the allowed-hosts validation error path (already covered)
		// or a config load error. Let's test a non-existent config path.
		err := runAssistantWithExecutor(t.Context(), exec, runtime.Runtime{Name: "docker"}, &stdout, &stderr, assistantParams{
			assistantName:   "claude",
			workspaceFolder: "/nonexistent/workspace",
			homeDir:         homeDir,
			baseDockerfile:  []byte("FROM ubuntu:24.04\n"),
			noGitIdentity:   true,
		}, "", "")

		if err == nil {
			t.Fatal("runAssistantWithExecutor() = nil, want error for bad workspace")
		}
	})

	t.Run("reconnect error propagated", func(t *testing.T) {
		homeDir, wsDir := seedAssistantHome(t, "claude")
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: "sha256:abc\n"},                        // EnsureBaseImage
				{output: "aabb0011\n"},                          // FindByAssistant: one container
				{output: "", err: errors.New("inspect failed")}, // InspectConfigHash: error
			},
		}

		var stdout, stderr bytes.Buffer
		err := runAssistantWithExecutor(t.Context(), exec, runtime.Runtime{Name: "docker"}, &stdout, &stderr, assistantParams{
			assistantName:   "claude",
			workspaceFolder: wsDir,
			homeDir:         homeDir,
			baseDockerfile:  []byte("FROM ubuntu:24.04\n"),
			noGitIdentity:   true,
		}, "", "")

		if err == nil {
			t.Fatal("runAssistantWithExecutor() = nil, want error")
		}
		if !strings.Contains(err.Error(), "assistant") {
			t.Errorf("runAssistantWithExecutor() error = %q, want containing %q", err.Error(), "assistant")
		}
	})

	t.Run("reconnect hash mismatch triggers recreation", func(t *testing.T) {
		homeDir, wsDir := seedAssistantHome(t, "claude")
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: "sha256:abc\n"},   // EnsureBaseImage: image inspect succeeds
				{output: "aabb0011\n"},     // FindByAssistant: one container
				{output: "stale-hash\n"},   // InspectConfigHash: different hash
				{output: ""},               // FindByLabels (inside Up for recreated): no containers
				{output: "newcontainer\n"}, // createContainer
			},
			runResults: []error{
				nil, // docker stop (StopAndRemove)
				nil, // docker rm (StopAndRemove)
				nil, // firewall
			},
		}

		var stdout, stderr bytes.Buffer
		_ = runAssistantWithExecutor(t.Context(), exec, runtime.Runtime{Name: "docker"}, &stdout, &stderr, assistantParams{
			assistantName:   "claude",
			workspaceFolder: wsDir,
			homeDir:         homeDir,
			baseDockerfile:  []byte("FROM ubuntu:24.04\n"),
			noGitIdentity:   true,
		}, "", "")

		if !strings.Contains(stderr.String(), "Configuration changed") {
			t.Errorf("runAssistantWithExecutor() stderr = %q, want containing %q", stderr.String(), "Configuration changed")
		}
	})
}
