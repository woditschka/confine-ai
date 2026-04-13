package container

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/woditschka/confine-ai/internal/config"
)

// configHash is a test helper that computes the config hash without
// additional folders. Equivalent to ConfigHashWithFolders(cfg, nil).
func configHash(cfg config.Config) string {
	return ConfigHashWithFolders(cfg, nil)
}

// errFake is a sentinel error for tests.
var errFake = errors.New("fake error")

// outputResult holds a canned response for an Output call.
type outputResult struct {
	output string
	err    error
}

// runResult holds a canned response for a Run call.
type runResult struct {
	err error
}

// fakeMultiExecutor records multiple calls and returns canned responses
// in sequence. It supports both Output and Run calls.
type fakeMultiExecutor struct {
	outputResults []outputResult
	outputCalls   [][]string
	outputIdx     int

	runResults []runResult
	runCalls   [][]string
	runIdx     int
}

func (f *fakeMultiExecutor) Output(_ context.Context, args ...string) (string, error) {
	f.outputCalls = append(f.outputCalls, args)
	if f.outputIdx >= len(f.outputResults) {
		return "", errors.New("fakeMultiExecutor: no more Output results")
	}
	r := f.outputResults[f.outputIdx]
	f.outputIdx++
	return r.output, r.err
}

func (f *fakeMultiExecutor) Run(_ context.Context, _, _ io.Writer, args ...string) error {
	f.runCalls = append(f.runCalls, args)
	if f.runIdx >= len(f.runResults) {
		return errors.New("fakeMultiExecutor: no more Run results")
	}
	r := f.runResults[f.runIdx]
	f.runIdx++
	return r.err
}

func (f *fakeMultiExecutor) RunInteractive(_ context.Context, _ io.Reader, _, _ io.Writer, args ...string) error {
	f.runCalls = append(f.runCalls, args)
	if f.runIdx >= len(f.runResults) {
		return errors.New("fakeMultiExecutor: no more RunInteractive results")
	}
	r := f.runResults[f.runIdx]
	f.runIdx++
	return r.err
}

func TestWorkspaceMount(t *testing.T) {
	tests := []struct {
		name          string
		hostPath      string
		containerPath string
		want          string
	}{
		{
			name:          "explicit container path",
			hostPath:      "/home/user/project",
			containerPath: "/workspaces/project",
			want:          "type=bind,source=/home/user/project,target=/workspaces/project",
		},
		{
			name:          "default container path from basename",
			hostPath:      "/home/user/my-app",
			containerPath: "",
			want:          "type=bind,source=/home/user/my-app,target=/workspaces/my-app",
		},
		{
			name:          "custom container path",
			hostPath:      "/home/user/project",
			containerPath: "/workspace",
			want:          "type=bind,source=/home/user/project,target=/workspace",
		},
		{
			name:          "deeply nested host path defaults to basename",
			hostPath:      "/home/user/code/go/projects/myapp",
			containerPath: "",
			want:          "type=bind,source=/home/user/code/go/projects/myapp,target=/workspaces/myapp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := workspaceMount(tt.hostPath, tt.containerPath)
			if err != nil {
				t.Fatalf("workspaceMount(%q, %q) unexpected error: %v",
					tt.hostPath, tt.containerPath, err)
			}
			if got != tt.want {
				t.Errorf("workspaceMount(%q, %q) = %q, want %q",
					tt.hostPath, tt.containerPath, got, tt.want)
			}
		})
	}
}

func TestWorkspaceMountInjection(t *testing.T) {
	tests := []struct {
		name          string
		hostPath      string
		containerPath string
		wantErr       string
	}{
		{
			name:     "comma in host path",
			hostPath: "/home/user/proj,readonly",
			wantErr:  "invalid characters",
		},
		{
			name:          "equals in container path",
			hostPath:      "/home/user/project",
			containerPath: "/workspace=foo",
			wantErr:       "invalid characters",
		},
		{
			name:     "comma in derived basename",
			hostPath: "/home/user/proj,bad",
			wantErr:  "invalid characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := workspaceMount(tt.hostPath, tt.containerPath)
			if err == nil {
				t.Fatal("workspaceMount() = nil error, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("workspaceMount() error = %q, want containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestSanitizeImageTag(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple name", input: "my-project", want: "my-project"},
		{name: "spaces replaced", input: "my project", want: "my-project"},
		{name: "special chars replaced", input: "proj@v1.0!rc", want: "proj-v1.0-rc"},
		{name: "empty string", input: "", want: "unnamed"},
		{name: "dots and underscores kept", input: "my_proj.v2", want: "my_proj.v2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeImageTag(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeImageTag(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestInspectConfigHash(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		err      error
		wantHash string
		wantErr  string
		wantArgs []string
	}{
		{
			name:     "returns stored hash",
			output:   "abc123def456\n",
			wantHash: "abc123def456",
			wantArgs: []string{"inspect", "--format", "{{index .Config.Labels \"devcontainer.config_hash\"}}", "container-id-1"},
		},
		{
			name:     "empty hash for unlabeled container",
			output:   "\n",
			wantHash: "",
		},
		{
			name:    "executor error propagated",
			err:     errFake,
			wantErr: "inspect config hash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeMultiExecutor{
				outputResults: []outputResult{{output: tt.output, err: tt.err}},
			}

			got, err := InspectConfigHash(t.Context(), exec, "container-id-1")

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("InspectConfigHash() = %q, want error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("InspectConfigHash() error = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("InspectConfigHash() unexpected error: %v", err)
			}

			if got != tt.wantHash {
				t.Errorf("InspectConfigHash() = %q, want %q", got, tt.wantHash)
			}

			if tt.wantArgs != nil {
				if diff := cmp.Diff(tt.wantArgs, exec.outputCalls[0]); diff != "" {
					t.Errorf("inspectConfigHash args mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestUp(t *testing.T) {
	baseConfig := config.Config{
		Image: "ubuntu:latest",
	}

	baseOpts := UpOptions{
		WorkspaceFolder: "/home/user/project",
		Config:          baseConfig,
		ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
		HomeDir:         "/home/user",
		Network:         "bridge",
		ResolveSymlinks: cleanResolve,
	}

	t.Run("new container with image (AC REQ-CO-002 #1, #8)", func(t *testing.T) {
		// FindByLabels returns no containers, then docker run returns ID.
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},           // FindByLabels: no containers
				{output: "deadbeef\n"}, // docker run: container ID
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // applyFirewallRules
			},
		}

		result, err := Up(t.Context(), exec, baseOpts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "success")
		}
		if result.ContainerID != "deadbeef" {
			t.Errorf("Up() containerId = %q, want %q", result.ContainerID, "deadbeef")
		}
		if result.RemoteWorkspaceFolder != "/workspaces/project" {
			t.Errorf("Up() remoteWorkspaceFolder = %q, want %q", result.RemoteWorkspaceFolder, "/workspaces/project")
		}
	})

	t.Run("new container with build (AC REQ-CO-002 #2)", func(t *testing.T) {
		buildCfg := config.Config{
			Build: &config.Build{
				Dockerfile: "Dockerfile",
			},
		}
		opts := UpOptions{
			WorkspaceFolder: "/home/user/project",
			Config:          buildCfg,
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},           // FindByLabels: no containers
				{output: "cafebabe\n"}, // docker run: container ID
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // docker build: success
				{err: nil}, // applyFirewallRules
			},
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "success")
		}

		// Verify build was called (first run call) and firewall applied (second run call).
		if len(exec.runCalls) != 2 {
			t.Fatalf("Up() run calls = %d, want 2 (build, firewall)", len(exec.runCalls))
		}
		if exec.runCalls[0][0] != "build" {
			t.Errorf("Up() first run call = %v, want build command", exec.runCalls[0])
		}
	})

	t.Run("reuse existing container with same config (AC REQ-CO-002 #6)", func(t *testing.T) {
		hash := configHash(baseConfig)
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "aabb0011\n"}, // FindByLabels: one container
				{output: hash + "\n"},  // inspectConfigHash: same hash
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // docker start
				{err: nil}, // applyFirewallRules
			},
		}

		result, err := Up(t.Context(), exec, baseOpts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "success")
		}
		if result.ContainerID != "aabb0011" {
			t.Errorf("Up() containerId = %q, want %q", result.ContainerID, "aabb0011")
		}

		// Should have called docker start then firewall rules.
		if len(exec.runCalls) != 2 {
			t.Fatalf("Up() run calls = %d, want 2 (start, firewall)", len(exec.runCalls))
		}
		if exec.runCalls[0][0] != "start" {
			t.Errorf("Up() first run call = %v, want start command", exec.runCalls[0])
		}
		if exec.runCalls[1][0] != "exec" {
			t.Errorf("Up() second run call = %v, want exec (firewall) command", exec.runCalls[1])
		}
	})

	t.Run("replace existing container with changed config (AC REQ-CO-002 #7)", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "aabb0011\n"}, // FindByLabels: one container
				{output: "0000aaaa\n"}, // inspectConfigHash: different hash
				{output: "ccdd0022\n"}, // docker run: new container ID
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // docker stop
				{err: nil}, // docker rm
				{err: nil}, // applyFirewallRules
			},
		}

		result, err := Up(t.Context(), exec, baseOpts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "success")
		}
		if result.ContainerID != "ccdd0022" {
			t.Errorf("Up() containerId = %q, want %q", result.ContainerID, "ccdd0022")
		}

		// Should have called stop, rm, then firewall (3 run calls).
		if len(exec.runCalls) != 3 {
			t.Fatalf("Up() run calls = %d, want 3 (stop, rm, firewall)", len(exec.runCalls))
		}
		if exec.runCalls[0][0] != "stop" {
			t.Errorf("Up() first run call = %v, want stop", exec.runCalls[0])
		}
	})

	t.Run("remove-existing-container flag (AC REQ-CL-003 #1)", func(t *testing.T) {
		opts := baseOpts
		opts.RemoveExistingContainer = true

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "aabb0011\n"}, // FindByLabels: one container
				{output: "ccdd0022\n"}, // docker run: new container ID
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // docker stop
				{err: nil}, // docker rm
				{err: nil}, // applyFirewallRules
			},
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "success")
		}

		// Should have stopped, removed, then applied firewall.
		if len(exec.runCalls) != 3 {
			t.Fatalf("Up() run calls = %d, want 3 (stop, rm, firewall)", len(exec.runCalls))
		}
		// Should not have inspected config hash (find + run + gatewayIP + hostInternalIP = 4 Output calls).
		if len(exec.outputCalls) != 4 {
			t.Errorf("Up() output calls = %d, want 4 (find, run, gateway, host-internal)", len(exec.outputCalls))
		}
	})

	t.Run("host network blocked (REQ-CO-009)", func(t *testing.T) {
		opts := baseOpts
		opts.Network = "host"

		exec := &fakeMultiExecutor{}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "host networking is blocked") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "host networking is blocked")
		}
	})

	t.Run("build error produces error result (AC REQ-CO-002 #9)", func(t *testing.T) {
		buildCfg := config.Config{
			Build: &config.Build{
				Dockerfile: "Dockerfile",
			},
		}
		opts := UpOptions{
			WorkspaceFolder: "/home/user/project",
			Config:          buildCfg,
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""}, // FindByLabels: no containers
			},
			runResults: []runResult{
				{err: errFake}, // docker build: fail
			},
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if result.Message == "" {
			t.Error("Up() error result has empty message")
		}
	})

	t.Run("FindByLabels error produces error result", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{err: errFake}, // FindByLabels fails
			},
		}

		result, err := Up(t.Context(), exec, baseOpts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "find containers") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "find containers")
		}
	})

	t.Run("docker start failure produces error result", func(t *testing.T) {
		hash := configHash(baseConfig)
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "aabb0011\n"}, // FindByLabels: one container
				{output: hash + "\n"},  // inspectConfigHash: same hash
			},
			runResults: []runResult{
				{err: errFake}, // docker start fails
			},
		}

		result, err := Up(t.Context(), exec, baseOpts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "start container") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "start container")
		}
		if result.ContainerID != "aabb0011" {
			t.Errorf("Up() containerId = %q, want %q", result.ContainerID, "aabb0011")
		}
	})

	t.Run("docker rm failure on replace produces error result", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "aabb0011\n"}, // FindByLabels: one container
				{output: "0000aaaa\n"}, // inspectConfigHash: different hash
			},
			runResults: []runResult{
				{err: nil},     // docker stop succeeds
				{err: errFake}, // docker rm fails
			},
		}

		result, err := Up(t.Context(), exec, baseOpts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "replace container") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "replace container")
		}
	})

	t.Run("workspace mount injection blocked", func(t *testing.T) {
		opts := UpOptions{
			WorkspaceFolder: "/home/user/proj,readonly",
			Config:          baseConfig,
			ConfigPath:      "/home/user/proj,readonly/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		exec := &fakeMultiExecutor{}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "invalid characters") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "invalid characters")
		}
	})

	t.Run("remoteUser from config", func(t *testing.T) {
		cfg := config.Config{
			Image:      "ubuntu:latest",
			RemoteUser: "node",
		}
		opts := UpOptions{
			WorkspaceFolder: "/home/user/project",
			Config:          cfg,
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},         // FindByLabels: no containers
				{output: "abc123\n"}, // docker run
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // applyFirewallRules
			},
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.RemoteUser != "node" {
			t.Errorf("Up() remoteUser = %q, want %q", result.RemoteUser, "node")
		}
	})

	t.Run("remoteUser falls back to containerUser", func(t *testing.T) {
		cfg := config.Config{
			Image:         "ubuntu:latest",
			ContainerUser: "vscode",
		}
		opts := UpOptions{
			WorkspaceFolder: "/home/user/project",
			Config:          cfg,
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},         // FindByLabels
				{output: "abc123\n"}, // docker run
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // applyFirewallRules
			},
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.RemoteUser != "vscode" {
			t.Errorf("Up() remoteUser = %q, want %q", result.RemoteUser, "vscode")
		}
	})

	t.Run("workspace folder from config", func(t *testing.T) {
		cfg := config.Config{
			Image:           "ubuntu:latest",
			WorkspaceFolder: "/custom/workspace",
		}
		opts := UpOptions{
			WorkspaceFolder: "/home/user/project",
			Config:          cfg,
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},         // FindByLabels
				{output: "abc123\n"}, // docker run
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // applyFirewallRules
			},
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.RemoteWorkspaceFolder != "/custom/workspace" {
			t.Errorf("Up() remoteWorkspaceFolder = %q, want %q", result.RemoteWorkspaceFolder, "/custom/workspace")
		}
	})

	t.Run("blocked mount returns error (AC REQ-CO-008 #1)", func(t *testing.T) {
		opts := UpOptions{
			WorkspaceFolder: "/home/user/project",
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=/,target=/host"},
			},
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		exec := &fakeMultiExecutor{}
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}
		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "mount blocked") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "mount blocked")
		}
	})

	t.Run("docker socket blocked (AC REQ-CO-008 #2)", func(t *testing.T) {
		opts := UpOptions{
			WorkspaceFolder: "/home/user/project",
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock"},
			},
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		exec := &fakeMultiExecutor{}
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}
		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "mount blocked") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "mount blocked")
		}
	})

	t.Run("home dir mount blocked (AC REQ-CO-008 #3)", func(t *testing.T) {
		opts := UpOptions{
			WorkspaceFolder: "/home/user/project",
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=/home/user,target=/home"},
			},
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		exec := &fakeMultiExecutor{}
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}
		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
	})

	t.Run("home subdirectory allowed (AC REQ-CO-008 #4)", func(t *testing.T) {
		opts := UpOptions{
			WorkspaceFolder: "/home/user/project",
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=/home/user/.config/git,target=/git"},
			},
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},           // FindByLabels: no containers
				{output: "deadbeef\n"}, // docker run: container ID
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // applyFirewallRules
			},
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}
		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q; message = %q", result.Outcome, "success", result.Message)
		}
	})

	t.Run("parent of home blocked (AC REQ-CO-008 #5)", func(t *testing.T) {
		opts := UpOptions{
			WorkspaceFolder: "/home/user/project",
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=/home,target=/mnt"},
			},
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		exec := &fakeMultiExecutor{}
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}
		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
	})

	t.Run("risky mount blocked without flag (AC REQ-CO-008 #9)", func(t *testing.T) {
		opts := UpOptions{
			WorkspaceFolder: "/home/user/project",
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=/home/user/.ssh,target=/ssh"},
			},
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		exec := &fakeMultiExecutor{}
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}
		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "risky mounts detected") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "risky mounts detected")
		}
		if !strings.Contains(result.Message, "--allow-risky-mounts") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "--allow-risky-mounts")
		}
	})

	t.Run("risky mount allowed with flag", func(t *testing.T) {
		opts := UpOptions{
			WorkspaceFolder: "/home/user/project",
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=/home/user/.ssh,target=/ssh"},
			},
			ConfigPath:       "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:          "/home/user",
			AllowRiskyMounts: true,
			Network:          "bridge",
			ResolveSymlinks:  cleanResolve,
		}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},           // FindByLabels: no containers
				{output: "deadbeef\n"}, // docker run: container ID
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // applyFirewallRules
			},
		}

		var stderr strings.Builder
		result, err := Up(t.Context(), exec, opts, &stderr)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}
		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q; message = %q", result.Outcome, "success", result.Message)
		}
		if !strings.Contains(stderr.String(), "warning") {
			t.Errorf("Up() stderr = %q, want containing %q", stderr.String(), "warning")
		}
	})

	t.Run("blocked mount not overridden by allow-risky flag (AC REQ-CO-008 #10)", func(t *testing.T) {
		opts := UpOptions{
			WorkspaceFolder: "/home/user/project",
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=/,target=/host"},
			},
			ConfigPath:       "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:          "/home/user",
			AllowRiskyMounts: true,
			Network:          "bridge",
			ResolveSymlinks:  cleanResolve,
		}

		exec := &fakeMultiExecutor{}
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}
		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "mount blocked") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "mount blocked")
		}
	})

	t.Run("firewall failure removes container (AC REQ-CO-009 #5)", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},               // FindByLabels: no containers
				{output: "deadbeef\n"},     // docker run: container ID
				{output: "", err: errFake}, // gatewayIP: fails
			},
			runResults: []runResult{
				{err: nil}, // docker stop (cleanup)
				{err: nil}, // docker rm (cleanup)
			},
		}

		result, err := Up(t.Context(), exec, baseOpts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "firewall setup") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "firewall setup")
		}

		// Verify container was cleaned up (stop + rm).
		if len(exec.runCalls) != 2 {
			t.Fatalf("Up() run calls = %d, want 2 (stop, rm for cleanup)", len(exec.runCalls))
		}
		if exec.runCalls[0][0] != "stop" {
			t.Errorf("Up() first cleanup call = %v, want stop", exec.runCalls[0])
		}
		if exec.runCalls[1][0] != "rm" {
			t.Errorf("Up() second cleanup call = %v, want rm", exec.runCalls[1])
		}
	})

	t.Run("network none skips firewall (AC REQ-CO-009)", func(t *testing.T) {
		opts := baseOpts
		opts.Network = "none"

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},           // FindByLabels: no containers
				{output: "deadbeef\n"}, // docker run: container ID
			},
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q; message = %q", result.Outcome, "success", result.Message)
		}

		// No firewall calls (no Run calls, no extra Output calls beyond find + run).
		if len(exec.outputCalls) != 2 {
			t.Errorf("Up() output calls = %d, want 2 (find, run only)", len(exec.outputCalls))
		}
		if len(exec.runCalls) != 0 {
			t.Errorf("Up() run calls = %d, want 0 (no firewall for none)", len(exec.runCalls))
		}
	})

	t.Run("reuse path firewall failure removes container", func(t *testing.T) {
		hash := configHash(baseConfig)
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "aabb0011\n"},     // FindByLabels: one container
				{output: hash + "\n"},      // inspectConfigHash: same hash
				{output: "", err: errFake}, // gatewayIP: fails
			},
			runResults: []runResult{
				{err: nil}, // docker start
				{err: nil}, // docker stop (cleanup)
				{err: nil}, // docker rm (cleanup)
			},
		}

		result, err := Up(t.Context(), exec, baseOpts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "firewall setup") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "firewall setup")
		}

		// Verify: start, then cleanup (stop + rm).
		if len(exec.runCalls) != 3 {
			t.Fatalf("Up() run calls = %d, want 3 (start, stop, rm)", len(exec.runCalls))
		}
		if exec.runCalls[0][0] != "start" {
			t.Errorf("Up() first run call = %v, want start", exec.runCalls[0])
		}
		if exec.runCalls[1][0] != "stop" {
			t.Errorf("Up() second run call = %v, want stop", exec.runCalls[1])
		}
	})

	t.Run("named network gets firewall rules (AC REQ-CO-009 #5)", func(t *testing.T) {
		opts := baseOpts
		opts.Network = "my-custom-net"

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},           // FindByLabels: no containers
				{output: "deadbeef\n"}, // docker run: container ID
				{output: `[{"IPAM":{"Config":[{"Gateway":"10.0.0.1"}]}}]`}, // gatewayIP for named network
				{output: "", err: errFake},                                 // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // applyFirewallRules
			},
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q; message = %q", result.Outcome, "success", result.Message)
		}

		// Verify gatewayIP was called with the named network.
		if len(exec.outputCalls) < 3 {
			t.Fatalf("Up() output calls = %d, want at least 3", len(exec.outputCalls))
		}
		gwCall := exec.outputCalls[2]
		if gwCall[2] != "my-custom-net" {
			t.Errorf("gatewayIP network arg = %q, want %q", gwCall[2], "my-custom-net")
		}
	})

	t.Run("allowed-hosts with network none returns error (AC REQ-NR-001 #3)", func(t *testing.T) {
		opts := baseOpts
		opts.Network = "none"
		opts.AllowedHosts = []string{"api.anthropic.com"}

		exec := &fakeMultiExecutor{}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "--allowed-hosts cannot be used with --network none") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "--allowed-hosts cannot be used with --network none")
		}
	})

	t.Run("allowed-hosts full flow resolve allowlist gateway (AC REQ-NR-001 #1)", func(t *testing.T) {
		opts := baseOpts
		opts.AllowedHosts = []string{"api.anthropic.com"}

		// Allowlist: 2 policy + 4 base + 1 IP = 7 calls. Gateway: 1 call. Total: 8.
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},           // FindByLabels: no containers
				{output: "deadbeef\n"}, // docker run: container ID
				// setupFirewall: resolveAllowedHosts
				{output: "93.184.216.34   STREAM api.anthropic.com\n"},
				// setupFirewall: gatewayIP
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`},
				// setupFirewall: hostInternalIP
				{output: "", err: errFake},
			},
			runResults: make([]runResult, 8),
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q; message = %q", result.Outcome, "success", result.Message)
		}
		if result.ContainerID != "deadbeef" {
			t.Errorf("Up() containerId = %q, want %q", result.ContainerID, "deadbeef")
		}

		// Verify allowlist policy rules come first, then ACCEPT rules, then gateway blocking.
		if len(exec.runCalls) != 8 {
			t.Fatalf("Up() run calls = %d, want 8 (2 policy + 4 base + 1 allowed + 1 gateway)", len(exec.runCalls))
		}
		// First call: iptables -P OUTPUT DROP.
		if exec.runCalls[0][4] != "iptables" || exec.runCalls[0][6] != "OUTPUT" || exec.runCalls[0][7] != "DROP" {
			t.Errorf("Up() first run call = %v, want iptables DROP policy", exec.runCalls[0])
		}
		// Last call: gateway blocking INSERT.
		last := exec.runCalls[len(exec.runCalls)-1]
		if last[4] != "iptables" || last[5] != "-I" || last[8] != "172.17.0.1" {
			t.Errorf("Up() last run call = %v, want gateway INSERT rule", last)
		}
	})

	t.Run("allowed-hosts resolution failure removes container", func(t *testing.T) {
		opts := baseOpts
		opts.AllowedHosts = []string{"unreachable.invalid"}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},           // FindByLabels: no containers
				{output: "deadbeef\n"}, // docker run: container ID
				// setupFirewall: resolveAllowedHosts fails
				{output: "", err: errFake},
			},
			runResults: []runResult{
				{err: nil}, // docker stop (cleanup)
				{err: nil}, // docker rm (cleanup)
			},
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "firewall setup") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "firewall setup")
		}

		// Verify container was cleaned up.
		if len(exec.runCalls) != 2 {
			t.Fatalf("Up() run calls = %d, want 2 (stop, rm for cleanup)", len(exec.runCalls))
		}
		if exec.runCalls[0][0] != "stop" {
			t.Errorf("Up() first cleanup call = %v, want stop", exec.runCalls[0])
		}
	})

	t.Run("allowed-hosts allowlist failure removes container", func(t *testing.T) {
		opts := baseOpts
		opts.AllowedHosts = []string{"api.anthropic.com"}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},           // FindByLabels: no containers
				{output: "deadbeef\n"}, // docker run: container ID
				// setupFirewall: resolveAllowedHosts
				{output: "93.184.216.34   STREAM api.anthropic.com\n"},
			},
			runResults: []runResult{
				{err: errFake}, // first iptables policy call fails
				{err: nil},     // docker stop (cleanup)
				{err: nil},     // docker rm (cleanup)
			},
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "firewall setup") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "firewall setup")
		}
	})

	t.Run("reuse path with allowed-hosts reapplies rules (AC REQ-NR-001 #1)", func(t *testing.T) {
		hash := configHash(baseConfig)
		opts := baseOpts
		opts.AllowedHosts = []string{"api.anthropic.com"}

		// 1 start + 7 allowlist + 1 gateway = 9 run calls.
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "aabb0011\n"}, // FindByLabels: one container
				{output: hash + "\n"},  // inspectConfigHash: same hash
				// setupFirewall: resolveAllowedHosts
				{output: "93.184.216.34   STREAM api.anthropic.com\n"},
				// setupFirewall: gatewayIP
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`},
				// setupFirewall: hostInternalIP
				{output: "", err: errFake},
			},
			runResults: make([]runResult, 9),
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q; message = %q", result.Outcome, "success", result.Message)
		}
		if result.ContainerID != "aabb0011" {
			t.Errorf("Up() containerId = %q, want %q", result.ContainerID, "aabb0011")
		}

		// Verify: start first, then allowlist + gateway rules.
		if len(exec.runCalls) != 9 {
			t.Fatalf("Up() run calls = %d, want 9 (1 start + 7 allowlist + 1 gateway)", len(exec.runCalls))
		}
		if exec.runCalls[0][0] != "start" {
			t.Errorf("Up() first run call = %v, want start", exec.runCalls[0])
		}
	})
}

func TestAutoCreateMissingDirs(t *testing.T) {
	// successExecutor returns a fakeMultiExecutor wired for a successful Up()
	// flow: FindByLabels (no containers), docker run, gateway detection, and
	// firewall setup.
	successExecutor := func() *fakeMultiExecutor {
		return &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},           // FindByLabels: no containers
				{output: "deadbeef\n"}, // docker run: container ID
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // applyFirewallRules
			},
		}
	}

	t.Run("AC1: missing dir created when user confirms", func(t *testing.T) {
		workspace := t.TempDir()
		parent := filepath.Join(workspace, "volumes")
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		missingDir := filepath.Join(parent, "data")

		var promptMsg string
		opts := UpOptions{
			WorkspaceFolder: workspace,
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=" + missingDir + ",target=/data"},
			},
			ConfigPath: filepath.Join(workspace, ".devcontainer/devcontainer.json"),
			HomeDir:    "/home/testuser",
			Network:    "bridge",
			ConfirmFunc: func(msg string) bool {
				promptMsg = msg
				return true
			},
		}

		exec := successExecutor()
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q; message = %q", result.Outcome, "success", result.Message)
		}

		// Directory should have been created.
		info, statErr := os.Stat(missingDir)
		if statErr != nil {
			t.Fatalf("missing dir was not created: %v", statErr)
		}
		if !info.IsDir() {
			t.Error("created path is not a directory")
		}

		// Prompt message should mention the path.
		if !strings.Contains(promptMsg, missingDir) {
			t.Errorf("prompt message = %q, want containing %q", promptMsg, missingDir)
		}
	})

	t.Run("AC2: user declines causes mount blocked error", func(t *testing.T) {
		workspace := t.TempDir()
		parent := filepath.Join(workspace, "volumes")
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		missingDir := filepath.Join(parent, "data")

		opts := UpOptions{
			WorkspaceFolder: workspace,
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=" + missingDir + ",target=/data"},
			},
			ConfigPath: filepath.Join(workspace, ".devcontainer/devcontainer.json"),
			HomeDir:    "/home/testuser",
			Network:    "bridge",
			ConfirmFunc: func(_ string) bool {
				return false
			},
		}

		exec := &fakeMultiExecutor{}
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "mount blocked") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "mount blocked")
		}

		// Directory should NOT have been created.
		if _, statErr := os.Stat(missingDir); statErr == nil {
			t.Error("missing dir was created despite user declining")
		}
	})

	t.Run("AC3: nil ConfirmFunc causes mount blocked error without prompting", func(t *testing.T) {
		workspace := t.TempDir()
		parent := filepath.Join(workspace, "volumes")
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		missingDir := filepath.Join(parent, "data")

		opts := UpOptions{
			WorkspaceFolder: workspace,
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=" + missingDir + ",target=/data"},
			},
			ConfigPath:  filepath.Join(workspace, ".devcontainer/devcontainer.json"),
			HomeDir:     "/home/testuser",
			Network:     "bridge",
			ConfirmFunc: nil, // Non-interactive.
		}

		exec := &fakeMultiExecutor{}
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "mount blocked") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "mount blocked")
		}
	})

	t.Run("AC4: risky path not offered for creation", func(t *testing.T) {
		workspace := t.TempDir()
		parent := filepath.Join(workspace, "config")
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		// .ssh is a risky segment (tier 2).
		missingDir := filepath.Join(parent, ".ssh")

		promptCalled := false
		opts := UpOptions{
			WorkspaceFolder: workspace,
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=" + missingDir + ",target=/ssh"},
			},
			ConfigPath: filepath.Join(workspace, ".devcontainer/devcontainer.json"),
			HomeDir:    "/home/testuser",
			Network:    "bridge",
			ConfirmFunc: func(_ string) bool {
				promptCalled = true
				return true
			},
		}

		exec := &fakeMultiExecutor{}
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		// Should produce a risky mount error, not offer creation.
		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if promptCalled {
			t.Error("ConfirmFunc was called for risky path; want no prompt")
		}
	})

	t.Run("AC5: blocked path not offered for creation", func(t *testing.T) {
		workspace := t.TempDir()
		// Create a parent "fake" that resolves to "/", making the leaf
		// resolve to "/etc" which is a tier 1 blocked path.
		parent := filepath.Join(workspace, "fake")
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		missingDir := filepath.Join(parent, "etc")

		promptCalled := false
		opts := UpOptions{
			WorkspaceFolder: workspace,
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=" + missingDir + ",target=/custom"},
			},
			ConfigPath: filepath.Join(workspace, ".devcontainer/devcontainer.json"),
			HomeDir:    "/home/testuser",
			Network:    "bridge",
			ResolveSymlinks: func(path string) (string, error) {
				// Map the fake parent to "/" so that the full resolved
				// path becomes "/etc" (tier 1 blocked).
				if path == parent {
					return "/", nil
				}
				return filepath.Clean(path), nil
			},
			ConfirmFunc: func(_ string) bool {
				promptCalled = true
				return true
			},
		}

		exec := &fakeMultiExecutor{}
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if promptCalled {
			t.Error("ConfirmFunc was called for blocked path; want no prompt")
		}
	})

	t.Run("AC6: two missing dirs listed in single prompt", func(t *testing.T) {
		workspace := t.TempDir()
		parent := filepath.Join(workspace, "volumes")
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		missingDir1 := filepath.Join(parent, "data1")
		missingDir2 := filepath.Join(parent, "data2")

		promptCount := 0
		var promptMsg string
		opts := UpOptions{
			WorkspaceFolder: workspace,
			Config: config.Config{
				Image: "ubuntu:latest",
				Mounts: []string{
					"type=bind,source=" + missingDir1 + ",target=/data1",
					"type=bind,source=" + missingDir2 + ",target=/data2",
				},
			},
			ConfigPath: filepath.Join(workspace, ".devcontainer/devcontainer.json"),
			HomeDir:    "/home/testuser",
			Network:    "bridge",
			ConfirmFunc: func(msg string) bool {
				promptCount++
				promptMsg = msg
				return true
			},
		}

		exec := successExecutor()
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q; message = %q", result.Outcome, "success", result.Message)
		}

		if promptCount != 1 {
			t.Errorf("ConfirmFunc called %d times, want 1", promptCount)
		}

		if !strings.Contains(promptMsg, missingDir1) {
			t.Errorf("prompt message missing first dir: %q", promptMsg)
		}
		if !strings.Contains(promptMsg, missingDir2) {
			t.Errorf("prompt message missing second dir: %q", promptMsg)
		}

		// Both directories should have been created.
		if _, err := os.Stat(missingDir1); err != nil {
			t.Errorf("first dir not created: %v", err)
		}
		if _, err := os.Stat(missingDir2); err != nil {
			t.Errorf("second dir not created: %v", err)
		}
	})

	t.Run("AC7: parent does not exist skips creation offer", func(t *testing.T) {
		workspace := t.TempDir()
		// Parent "nonexistent" does not exist, so child is not creatable.
		missingDir := filepath.Join(workspace, "nonexistent", "data")

		promptCalled := false
		opts := UpOptions{
			WorkspaceFolder: workspace,
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=" + missingDir + ",target=/data"},
			},
			ConfigPath: filepath.Join(workspace, ".devcontainer/devcontainer.json"),
			HomeDir:    "/home/testuser",
			Network:    "bridge",
			ConfirmFunc: func(_ string) bool {
				promptCalled = true
				return true
			},
		}

		exec := &fakeMultiExecutor{}
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		// Should fall through to ValidateMounts, which blocks unresolvable paths.
		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if promptCalled {
			t.Error("ConfirmFunc was called when parent does not exist; want no prompt")
		}
	})

	t.Run("AC8: all dirs exist means no prompt", func(t *testing.T) {
		workspace := t.TempDir()
		existingDir := filepath.Join(workspace, "volumes", "data")
		if err := os.MkdirAll(existingDir, 0o755); err != nil {
			t.Fatal(err)
		}

		promptCalled := false
		opts := UpOptions{
			WorkspaceFolder: workspace,
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=" + existingDir + ",target=/data"},
			},
			ConfigPath: filepath.Join(workspace, ".devcontainer/devcontainer.json"),
			HomeDir:    "/home/testuser",
			Network:    "bridge",
			ConfirmFunc: func(_ string) bool {
				promptCalled = true
				return true
			},
		}

		exec := successExecutor()
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q; message = %q", result.Outcome, "success", result.Message)
		}
		if promptCalled {
			t.Error("ConfirmFunc was called when all dirs exist; want no prompt")
		}
	})

	t.Run("child of blocked parent not offered for creation", func(t *testing.T) {
		workspace := t.TempDir()
		// /etc exists and resolves, but is tier 1 blocked. A child
		// /etc/new-subdir must not be offered for creation.
		missingDir := "/etc/confine-test-nonexistent"

		promptCalled := false
		opts := UpOptions{
			WorkspaceFolder: workspace,
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=" + missingDir + ",target=/data"},
			},
			ConfigPath: filepath.Join(workspace, ".devcontainer/devcontainer.json"),
			HomeDir:    "/home/testuser",
			Network:    "bridge",
			ConfirmFunc: func(_ string) bool {
				promptCalled = true
				return true
			},
		}

		exec := &fakeMultiExecutor{}
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if promptCalled {
			t.Error("ConfirmFunc was called for child of blocked path; want no prompt")
		}
	})

	t.Run("mkdir failure returns error result", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("skipping permission test when running as root")
		}

		workspace := t.TempDir()
		parent := filepath.Join(workspace, "volumes")
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatal(err)
		}

		// Make parent read-only so MkdirAll fails with permission error.
		missingDir := filepath.Join(parent, "data")
		if err := os.Chmod(parent, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			// Restore permissions so TempDir cleanup works.
			os.Chmod(parent, 0o755)
		})

		opts := UpOptions{
			WorkspaceFolder: workspace,
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=" + missingDir + ",target=/data"},
			},
			ConfigPath: filepath.Join(workspace, ".devcontainer/devcontainer.json"),
			HomeDir:    "/home/testuser",
			Network:    "bridge",
			ConfirmFunc: func(_ string) bool {
				return true
			},
		}

		exec := &fakeMultiExecutor{}
		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}

		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "create directory") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "create directory")
		}
	})
}

func TestUpAdditionalFolders(t *testing.T) {
	t.Run("additional folders appear as bind mounts (AC REQ-MF-001 #3)", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},           // FindByLabels: no containers
				{output: "deadbeef\n"}, // docker run: container ID
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // applyFirewallRules
			},
		}

		opts := UpOptions{
			WorkspaceFolder:   "/home/user/project",
			AdditionalFolders: []string{"/home/user/project-a", "/home/user/project-b"},
			Config: config.Config{
				Image: "ubuntu:latest",
			},
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}
		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q; message = %q", result.Outcome, "success", result.Message)
		}

		// Verify additional mounts appear in the docker run call.
		if len(exec.outputCalls) < 2 {
			t.Fatalf("Up() output calls = %d, want at least 2", len(exec.outputCalls))
		}
		runArgs := exec.outputCalls[1] // docker run call
		wantMount1 := "type=bind,source=/home/user/project-a,target=/workspaces/project-a"
		wantMount2 := "type=bind,source=/home/user/project-b,target=/workspaces/project-b"
		foundMount1 := false
		foundMount2 := false
		for i, arg := range runArgs {
			if arg == "--mount" && i+1 < len(runArgs) {
				if runArgs[i+1] == wantMount1 {
					foundMount1 = true
				}
				if runArgs[i+1] == wantMount2 {
					foundMount2 = true
				}
			}
		}
		if !foundMount1 {
			t.Errorf("Up() docker run args missing mount %q; got %v", wantMount1, runArgs)
		}
		if !foundMount2 {
			t.Errorf("Up() docker run args missing mount %q; got %v", wantMount2, runArgs)
		}
	})

	t.Run("additional folder at blocked path returns error (AC REQ-MF-001 #7)", func(t *testing.T) {
		exec := &fakeMultiExecutor{}

		opts := UpOptions{
			WorkspaceFolder:   "/home/user/project",
			AdditionalFolders: []string{"/etc"},
			Config: config.Config{
				Image: "ubuntu:latest",
			},
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}
		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "mount blocked") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "mount blocked")
		}
	})

	t.Run("additional folder with risky content blocked without flag", func(t *testing.T) {
		// Use a real temp dir with a .env file for probeWorkspaceRisks.
		workspace := t.TempDir()
		additionalDir := t.TempDir()
		envFile := filepath.Join(additionalDir, ".env")
		if err := os.WriteFile(envFile, []byte("SECRET=value"), 0o644); err != nil {
			t.Fatal(err)
		}

		exec := &fakeMultiExecutor{}

		opts := UpOptions{
			WorkspaceFolder:   workspace,
			AdditionalFolders: []string{additionalDir},
			Config: config.Config{
				Image: "ubuntu:latest",
			},
			ConfigPath: filepath.Join(workspace, ".devcontainer/devcontainer.json"),
			HomeDir:    "/home/testuser",
			Network:    "bridge",
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}
		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "risky mounts detected") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "risky mounts detected")
		}
	})

	t.Run("additional folder mount injection blocked", func(t *testing.T) {
		exec := &fakeMultiExecutor{}

		opts := UpOptions{
			WorkspaceFolder:   "/home/user/project",
			AdditionalFolders: []string{"/home/user/proj,hack"},
			Config: config.Config{
				Image: "ubuntu:latest",
			},
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}
		if result.Outcome != "error" {
			t.Errorf("Up() outcome = %q, want %q", result.Outcome, "error")
		}
		if !strings.Contains(result.Message, "invalid characters") {
			t.Errorf("Up() message = %q, want containing %q", result.Message, "invalid characters")
		}
	})

	t.Run("no additional folders preserves existing behavior", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},           // FindByLabels: no containers
				{output: "deadbeef\n"}, // docker run: container ID
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // applyFirewallRules
			},
		}

		opts := UpOptions{
			WorkspaceFolder: "/home/user/project",
			Config: config.Config{
				Image: "ubuntu:latest",
			},
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}
		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q; message = %q", result.Outcome, "success", result.Message)
		}
	})

	t.Run("AdditionalFolderBase overrides default /workspaces base path", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""},           // FindByLabels: no containers
				{output: "deadbeef\n"}, // docker run: container ID
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: not found
			},
			runResults: []runResult{
				{err: nil}, // applyFirewallRules
			},
		}

		opts := UpOptions{
			WorkspaceFolder:      "/home/user/project",
			AdditionalFolders:    []string{"/home/user/project-a"},
			AdditionalFolderBase: "/workspace",
			Config: config.Config{
				Image: "ubuntu:latest",
			},
			ConfigPath:      "/home/user/project/.devcontainer/devcontainer.json",
			HomeDir:         "/home/user",
			Network:         "bridge",
			ResolveSymlinks: cleanResolve,
		}

		result, err := Up(t.Context(), exec, opts, nil)
		if err != nil {
			t.Fatalf("Up() unexpected error: %v", err)
		}
		if result.Outcome != "success" {
			t.Errorf("Up() outcome = %q, want %q; message = %q", result.Outcome, "success", result.Message)
		}

		// Verify additional mount uses /workspace base instead of /workspaces.
		if len(exec.outputCalls) < 2 {
			t.Fatalf("Up() output calls = %d, want at least 2", len(exec.outputCalls))
		}
		runArgs := exec.outputCalls[1] // docker run call
		wantMount := "type=bind,source=/home/user/project-a,target=/workspace/project-a"
		found := false
		for i, arg := range runArgs {
			if arg == "--mount" && i+1 < len(runArgs) && runArgs[i+1] == wantMount {
				found = true
			}
		}
		if !found {
			t.Errorf("Up() docker run args missing mount %q; got %v", wantMount, runArgs)
		}
	})
}

func TestConfigHashAdditionalFolders(t *testing.T) {
	cfg := config.Config{Image: "ubuntu:latest"}

	t.Run("empty additional folders matches no-additional hash", func(t *testing.T) {
		h1 := ConfigHashWithFolders(cfg, nil)
		h2 := ConfigHashWithFolders(cfg, []string{})
		h3 := configHash(cfg)

		if h1 != h2 {
			t.Errorf("ConfigHashWithFolders(nil) != ConfigHashWithFolders([]), want equal")
		}
		if h1 != h3 {
			t.Errorf("ConfigHashWithFolders(nil) != configHash(), want equal for backward compat")
		}
	})

	t.Run("different additional folders produce different hash", func(t *testing.T) {
		h1 := ConfigHashWithFolders(cfg, []string{"/home/user/A"})
		h2 := ConfigHashWithFolders(cfg, []string{"/home/user/B"})

		if h1 == h2 {
			t.Errorf("ConfigHashWithFolders(A) = ConfigHashWithFolders(B) = %q, want different", h1)
		}
	})

	t.Run("additional folders change hash from base config", func(t *testing.T) {
		h1 := ConfigHashWithFolders(cfg, nil)
		h2 := ConfigHashWithFolders(cfg, []string{"/home/user/A"})

		if h1 == h2 {
			t.Errorf("ConfigHashWithFolders(nil) = ConfigHashWithFolders(A) = %q, want different", h1)
		}
	})

	t.Run("order of additional folders matters", func(t *testing.T) {
		h1 := ConfigHashWithFolders(cfg, []string{"/home/user/A", "/home/user/B"})
		h2 := ConfigHashWithFolders(cfg, []string{"/home/user/B", "/home/user/A"})

		if h1 == h2 {
			t.Errorf("ConfigHashWithFolders([A,B]) = ConfigHashWithFolders([B,A]) = %q, want different", h1)
		}
	})
}

func TestCreateContainer(t *testing.T) {
	tests := []struct {
		name       string
		params     createParams
		wantArgs   []string
		outputResp string
		outputErr  error
		wantID     string
		wantErr    string
	}{
		{
			name: "simple image with defaults",
			params: createParams{
				Image:   "ubuntu:latest",
				Labels:  NewLabels([]string{"/home/user/project"}),
				Hash:    "abc123",
				WSMount: "type=bind,source=/home/user/project,target=/workspaces/project",
				Config:  config.Config{Image: "ubuntu:latest"},
				Network: "bridge",
			},
			outputResp: "deadbeef1234\n",
			wantID:     "deadbeef1234",
			wantArgs: append(append([]string{"run", "-d"},
				NewLabels([]string{"/home/user/project"}).ForArgs()...),
				"--label", "devcontainer.config_hash=abc123",
				"--mount", "type=bind,source=/home/user/project,target=/workspaces/project",
				"--cap-add=NET_ADMIN",
				"--network", "bridge",
				"ubuntu:latest", "sleep", "infinity",
			),
		},
		{
			name: "with env, user, mounts, network",
			params: createParams{
				Image:   "alpine:latest",
				Labels:  NewLabels([]string{"/home/user/project"}),
				Hash:    "def456",
				WSMount: "type=bind,source=/home/user/project,target=/workspaces/project",
				Config: config.Config{
					Image:         "alpine:latest",
					ContainerEnv:  map[string]string{"TZ": "UTC"},
					ContainerUser: "node",
					Mounts:        []string{"type=bind,source=/host/data,target=/data"},
				},
				Network: "none",
			},
			outputResp: "cafebabe5678\n",
			wantID:     "cafebabe5678",
			wantArgs: []string{
				"run", "-d",
				"--label", "devcontainer.local_folder=/home/user/project",
				"--label", "devcontainer.metadata_id=" + NewLabels([]string{"/home/user/project"}).Values()[labelMetadataID],
				"--label", "devcontainer.config_hash=def456",
				"--mount", "type=bind,source=/home/user/project,target=/workspaces/project",
				"--mount", "type=bind,source=/host/data,target=/data",
				"-e", "TZ=UTC",
				"--user", "node",
				"--network", "none",
				"alpine:latest", "sleep", "infinity",
			},
		},
		{
			name: "docker run error propagated",
			params: createParams{
				Image:   "ubuntu:latest",
				Labels:  NewLabels([]string{"/home/user/project"}),
				Hash:    "abc",
				WSMount: "type=bind,source=/home/user/project,target=/workspaces/project",
				Config:  config.Config{Image: "ubuntu:latest"},
				Network: "bridge",
			},
			outputErr: errFake,
			wantErr:   "create container",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeMultiExecutor{
				outputResults: []outputResult{{output: tt.outputResp, err: tt.outputErr}},
			}

			id, err := createContainer(t.Context(), exec, tt.params)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("createContainer() = %q, want error containing %q", id, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("createContainer() error = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("createContainer() unexpected error: %v", err)
			}

			if id != tt.wantID {
				t.Errorf("createContainer() id = %q, want %q", id, tt.wantID)
			}

			// Verify key arguments are present in the call.
			if len(exec.outputCalls) != 1 {
				t.Fatalf("createContainer() made %d Output calls, want 1", len(exec.outputCalls))
			}
			callArgs := exec.outputCalls[0]

			if diff := cmp.Diff(tt.wantArgs, callArgs); diff != "" {
				t.Errorf("createContainer() args mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCreateContainerAdditionalMounts(t *testing.T) {
	t.Run("additional mounts appear between workspace and config mounts", func(t *testing.T) {
		params := createParams{
			Image:   "ubuntu:latest",
			Labels:  NewLabels([]string{"/home/user/project"}),
			Hash:    "abc123",
			WSMount: "type=bind,source=/home/user/project,target=/workspaces/project",
			Config: config.Config{
				Image:  "ubuntu:latest",
				Mounts: []string{"type=bind,source=/host/data,target=/data"},
			},
			AdditionalMounts: []string{
				"type=bind,source=/home/user/project-a,target=/workspaces/project-a",
			},
			Network: "bridge",
		}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{{output: "deadbeef\n"}},
		}

		id, err := createContainer(t.Context(), exec, params)
		if err != nil {
			t.Fatalf("createContainer() unexpected error: %v", err)
		}
		if id != "deadbeef" {
			t.Errorf("createContainer() = %q, want %q", id, "deadbeef")
		}

		args := exec.outputCalls[0]

		// Find positions of the three mount types.
		wsIdx := -1
		addlIdx := -1
		cfgIdx := -1
		for i, arg := range args {
			if arg == "--mount" && i+1 < len(args) {
				mount := args[i+1]
				switch {
				case strings.Contains(mount, "target=/workspaces/project,") || strings.HasSuffix(mount, "target=/workspaces/project"):
					wsIdx = i
				case strings.Contains(mount, "target=/workspaces/project-a"):
					addlIdx = i
				case strings.Contains(mount, "target=/data"):
					cfgIdx = i
				}
			}
		}

		if wsIdx == -1 {
			t.Error("createContainer() missing workspace mount")
		}
		if addlIdx == -1 {
			t.Error("createContainer() missing additional mount")
		}
		if cfgIdx == -1 {
			t.Error("createContainer() missing config mount")
		}
		if wsIdx != -1 && addlIdx != -1 && addlIdx <= wsIdx {
			t.Errorf("additional mount at %d should come after workspace mount at %d", addlIdx, wsIdx)
		}
		if addlIdx != -1 && cfgIdx != -1 && cfgIdx <= addlIdx {
			t.Errorf("config mount at %d should come after additional mount at %d", cfgIdx, addlIdx)
		}
	})
}

func TestCreateContainerResourceLimits(t *testing.T) {
	t.Run("memory and cpus args added (AC REQ-RL-001 #1, #2)", func(t *testing.T) {
		params := createParams{
			Image:   "ubuntu:latest",
			Labels:  NewLabels([]string{"/home/user/project"}),
			Hash:    "abc123",
			WSMount: "type=bind,source=/home/user/project,target=/workspaces/project",
			Config:  config.Config{Image: "ubuntu:latest"},
			Network: "bridge",
			ResourceLimits: config.ResourceLimits{
				Memory: "8g",
				CPUs:   "4",
			},
		}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{{output: "deadbeef\n"}},
		}

		id, err := createContainer(t.Context(), exec, params)
		if err != nil {
			t.Fatalf("createContainer() unexpected error: %v", err)
		}
		if id != "deadbeef" {
			t.Errorf("createContainer() = %q, want %q", id, "deadbeef")
		}

		args := exec.outputCalls[0]
		// Verify --memory and --cpus appear before --network.
		memoryIdx := -1
		cpusIdx := -1
		networkIdx := -1
		for i, arg := range args {
			switch arg {
			case "--memory":
				memoryIdx = i
			case "--cpus":
				cpusIdx = i
			case "--network":
				networkIdx = i
			}
		}

		if memoryIdx == -1 {
			t.Error("createContainer() args missing --memory")
		} else if args[memoryIdx+1] != "8g" {
			t.Errorf("createContainer() --memory value = %q, want %q", args[memoryIdx+1], "8g")
		}

		if cpusIdx == -1 {
			t.Error("createContainer() args missing --cpus")
		} else if args[cpusIdx+1] != "4" {
			t.Errorf("createContainer() --cpus value = %q, want %q", args[cpusIdx+1], "4")
		}

		if memoryIdx > networkIdx {
			t.Errorf("--memory at index %d appears after --network at index %d", memoryIdx, networkIdx)
		}
		if cpusIdx > networkIdx {
			t.Errorf("--cpus at index %d appears after --network at index %d", cpusIdx, networkIdx)
		}
	})

	t.Run("no limits omits args (AC REQ-RL-001 #6)", func(t *testing.T) {
		params := createParams{
			Image:   "ubuntu:latest",
			Labels:  NewLabels([]string{"/home/user/project"}),
			Hash:    "abc123",
			WSMount: "type=bind,source=/home/user/project,target=/workspaces/project",
			Config:  config.Config{Image: "ubuntu:latest"},
			Network: "bridge",
		}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{{output: "deadbeef\n"}},
		}

		_, err := createContainer(t.Context(), exec, params)
		if err != nil {
			t.Fatalf("createContainer() unexpected error: %v", err)
		}

		args := exec.outputCalls[0]
		for _, arg := range args {
			if arg == "--memory" || arg == "--cpus" {
				t.Errorf("createContainer() args contain %q, want neither --memory nor --cpus when no limits set", arg)
			}
		}
	})

	t.Run("podman adds --userns=keep-id", func(t *testing.T) {
		params := createParams{
			Image:       "ubuntu:latest",
			Labels:      NewLabels([]string{"/home/user/project"}),
			Hash:        "abc123",
			WSMount:     "type=bind,source=/home/user/project,target=/workspaces/project",
			Config:      config.Config{Image: "ubuntu:latest"},
			Network:     "podman",
			RuntimeName: "podman",
		}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{{output: "deadbeef\n"}},
		}

		_, err := createContainer(t.Context(), exec, params)
		if err != nil {
			t.Fatalf("createContainer() unexpected error: %v", err)
		}

		args := exec.outputCalls[0]
		if !slices.Contains(args, "--userns=keep-id") {
			t.Errorf("createContainer() args = %v, want --userns=keep-id for podman runtime", args)
		}
	})

	t.Run("docker omits --userns=keep-id", func(t *testing.T) {
		params := createParams{
			Image:       "ubuntu:latest",
			Labels:      NewLabels([]string{"/home/user/project"}),
			Hash:        "abc123",
			WSMount:     "type=bind,source=/home/user/project,target=/workspaces/project",
			Config:      config.Config{Image: "ubuntu:latest"},
			Network:     "bridge",
			RuntimeName: "docker",
		}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{{output: "deadbeef\n"}},
		}

		_, err := createContainer(t.Context(), exec, params)
		if err != nil {
			t.Fatalf("createContainer() unexpected error: %v", err)
		}

		args := exec.outputCalls[0]
		if slices.Contains(args, "--userns=keep-id") {
			t.Errorf("createContainer() args contain --userns=keep-id for docker runtime")
		}
	})

	t.Run("memory only adds --memory without --cpus", func(t *testing.T) {
		params := createParams{
			Image:   "ubuntu:latest",
			Labels:  NewLabels([]string{"/home/user/project"}),
			Hash:    "abc123",
			WSMount: "type=bind,source=/home/user/project,target=/workspaces/project",
			Config:  config.Config{Image: "ubuntu:latest"},
			Network: "bridge",
			ResourceLimits: config.ResourceLimits{
				Memory: "4g",
			},
		}

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{{output: "deadbeef\n"}},
		}

		_, err := createContainer(t.Context(), exec, params)
		if err != nil {
			t.Fatalf("createContainer() unexpected error: %v", err)
		}

		args := exec.outputCalls[0]
		hasMemory := false
		hasCPUs := false
		for _, arg := range args {
			if arg == "--memory" {
				hasMemory = true
			}
			if arg == "--cpus" {
				hasCPUs = true
			}
		}
		if !hasMemory {
			t.Error("createContainer() args missing --memory")
		}
		if hasCPUs {
			t.Error("createContainer() args contain --cpus when only memory is set")
		}
	})
}

func TestConfigHashCustomizations(t *testing.T) {
	t.Run("customizations change produces different hash", func(t *testing.T) {
		cfg1 := config.Config{
			Image: "ubuntu:latest",
			Customizations: &config.Customizations{
				Memory: "4g",
				CPUs:   "2",
			},
		}
		cfg2 := config.Config{
			Image: "ubuntu:latest",
			Customizations: &config.Customizations{
				Memory: "8g",
				CPUs:   "4",
			},
		}
		cfg3 := config.Config{
			Image: "ubuntu:latest",
		}

		h1 := configHash(cfg1)
		h2 := configHash(cfg2)
		h3 := configHash(cfg3)

		if h1 == h2 {
			t.Errorf("configHash(4g/2) = configHash(8g/4) = %q, want different", h1)
		}
		if h1 == h3 {
			t.Errorf("configHash(with-customizations) = configHash(without) = %q, want different", h1)
		}
	})

	t.Run("same customizations produces same hash", func(t *testing.T) {
		cfg1 := config.Config{
			Image: "ubuntu:latest",
			Customizations: &config.Customizations{
				Memory: "8g",
				CPUs:   "4",
			},
		}
		cfg2 := config.Config{
			Image: "ubuntu:latest",
			Customizations: &config.Customizations{
				Memory: "8g",
				CPUs:   "4",
			},
		}

		if configHash(cfg1) != configHash(cfg2) {
			t.Error("configHash for identical configs with same customizations should be equal")
		}
	})
}

func TestBuildImage(t *testing.T) {
	tests := []struct {
		name       string
		build      config.Build
		configPath string
		workspace  string
		wantArgs   []string
		runErr     error
		wantErr    string
	}{
		{
			name: "simple dockerfile",
			build: config.Build{
				Dockerfile: "Dockerfile",
			},
			configPath: "/home/user/project/.devcontainer/devcontainer.json",
			workspace:  "/home/user/project",
			wantArgs: []string{
				"build",
				"-f", "/home/user/project/.devcontainer/Dockerfile",
				"-t", "confine-ai-project:latest",
				"/home/user/project/.devcontainer",
			},
		},
		{
			name: "with context and args",
			build: config.Build{
				Dockerfile: "Dockerfile.dev",
				Context:    "..",
				Args: map[string]string{
					"GO_VERSION": "1.22",
				},
			},
			configPath: "/home/user/project/.devcontainer/devcontainer.json",
			workspace:  "/home/user/project",
			wantArgs: []string{
				"build",
				"-f", "/home/user/project/.devcontainer/Dockerfile.dev",
				"-t", "confine-ai-project:latest",
				"--build-arg", "GO_VERSION=1.22",
				"/home/user/project",
			},
		},
		{
			name: "multiple build args sorted",
			build: config.Build{
				Dockerfile: "Dockerfile",
				Args: map[string]string{
					"B_ARG": "2",
					"A_ARG": "1",
				},
			},
			configPath: "/home/user/project/.devcontainer/devcontainer.json",
			workspace:  "/home/user/project",
			wantArgs: []string{
				"build",
				"-f", "/home/user/project/.devcontainer/Dockerfile",
				"-t", "confine-ai-project:latest",
				"--build-arg", "A_ARG=1",
				"--build-arg", "B_ARG=2",
				"/home/user/project/.devcontainer",
			},
		},
		{
			name: "build error propagated",
			build: config.Build{
				Dockerfile: "Dockerfile",
			},
			configPath: "/home/user/project/.devcontainer/devcontainer.json",
			workspace:  "/home/user/project",
			runErr:     errFake,
			wantErr:    "build image",
		},
		{
			name: "dockerfile path escapes workspace",
			build: config.Build{
				Dockerfile: "../../outside/Dockerfile",
			},
			configPath: "/home/user/project/.devcontainer/devcontainer.json",
			workspace:  "/home/user/project",
			wantErr:    "dockerfile path escapes workspace",
		},
		{
			name: "build context escapes workspace",
			build: config.Build{
				Dockerfile: "Dockerfile",
				Context:    "../../../",
			},
			configPath: "/home/user/project/.devcontainer/devcontainer.json",
			workspace:  "/home/user/project",
			wantErr:    "build context escapes workspace",
		},
		{
			name: "assistant config outside workspace succeeds",
			build: config.Build{
				Dockerfile: "Dockerfile",
			},
			configPath: "/home/user/.confine-ai/assistants/claude/devcontainer.json",
			workspace:  "/home/user/project",
			wantArgs: []string{
				"build",
				"-f", "/home/user/.confine-ai/assistants/claude/Dockerfile",
				"-t", "confine-ai-project:latest",
				"/home/user/.confine-ai/assistants/claude",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeMultiExecutor{
				runResults: []runResult{{err: tt.runErr}},
			}

			tag, err := buildImage(t.Context(), exec, buildParams{
				Build:           &tt.build,
				ConfigPath:      tt.configPath,
				WorkspaceFolder: tt.workspace,
			})

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("buildImage() = %q, want error containing %q", tag, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("buildImage() error = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("buildImage() unexpected error: %v", err)
			}

			wantTag := "confine-ai-project:latest"
			if tag != wantTag {
				t.Errorf("buildImage() tag = %q, want %q", tag, wantTag)
			}

			if len(exec.runCalls) != 1 {
				t.Fatalf("buildImage() made %d run calls, want 1", len(exec.runCalls))
			}

			if diff := cmp.Diff(tt.wantArgs, exec.runCalls[0]); diff != "" {
				t.Errorf("buildImage args mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStopAndRemove(t *testing.T) {
	t.Run("stop then remove", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			runResults: []runResult{{err: nil}, {err: nil}},
		}

		err := StopAndRemove(t.Context(), exec, "abc123")
		if err != nil {
			t.Fatalf("StopAndRemove() unexpected error: %v", err)
		}

		wantRun := [][]string{
			{"stop", "abc123"},
			{"rm", "abc123"},
		}
		if diff := cmp.Diff(wantRun, exec.runCalls); diff != "" {
			t.Errorf("stopAndRemove run calls mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("stop error propagated", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			runResults: []runResult{{err: errFake}},
		}

		err := StopAndRemove(t.Context(), exec, "abc123")
		if err == nil {
			t.Fatal("StopAndRemove() = nil, want error")
		}
		if !strings.Contains(err.Error(), "stop container") {
			t.Errorf("StopAndRemove() error = %q, want containing %q", err.Error(), "stop container")
		}
	})

	t.Run("rm error propagated", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			runResults: []runResult{{err: nil}, {err: errFake}},
		}

		err := StopAndRemove(t.Context(), exec, "abc123")
		if err == nil {
			t.Fatal("StopAndRemove() = nil, want error")
		}
		if !strings.Contains(err.Error(), "remove container") {
			t.Errorf("StopAndRemove() error = %q, want containing %q", err.Error(), "remove container")
		}
	})
}

func TestConfigHash(t *testing.T) {
	cfg1 := config.Config{
		Image: "ubuntu:latest",
		ContainerEnv: map[string]string{
			"FOO": "bar",
			"BAZ": "qux",
		},
	}

	cfg2 := config.Config{
		Image: "ubuntu:latest",
		ContainerEnv: map[string]string{
			"FOO": "bar",
			"BAZ": "qux",
		},
	}

	cfg3 := config.Config{
		Image: "alpine:latest",
	}

	t.Run("same config produces same hash", func(t *testing.T) {
		h1 := configHash(cfg1)
		h2 := configHash(cfg2)
		if h1 != h2 {
			t.Errorf("configHash(cfg1) = %q, configHash(cfg2) = %q, want equal", h1, h2)
		}
	})

	t.Run("different config produces different hash", func(t *testing.T) {
		h1 := configHash(cfg1)
		h3 := configHash(cfg3)
		if h1 == h3 {
			t.Errorf("configHash(cfg1) = configHash(cfg3) = %q, want different", h1)
		}
	})

	t.Run("hash is 64 hex characters", func(t *testing.T) {
		h := configHash(cfg1)
		if len(h) != 64 {
			t.Errorf("configHash(cfg1) length = %d, want 64", len(h))
		}
	})

	t.Run("deterministic across calls", func(t *testing.T) {
		h1 := configHash(cfg1)
		h2 := configHash(cfg1)
		if h1 != h2 {
			t.Errorf("configHash not deterministic: %q != %q", h1, h2)
		}
	})

	t.Run("build config produces different hash from image config", func(t *testing.T) {
		buildCfg := config.Config{
			Build: &config.Build{
				Dockerfile: "Dockerfile",
				Context:    ".",
				Args:       map[string]string{"GO_VERSION": "1.26"},
			},
		}
		hImg := configHash(cfg1)
		hBuild := configHash(buildCfg)
		if hImg == hBuild {
			t.Errorf("configHash(image) = configHash(build) = %q, want different", hImg)
		}
	})
}
