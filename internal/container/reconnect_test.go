package container

import (
	"strings"
	"testing"

	"github.com/woditschka/confine-ai/internal/config"
)

func TestReconnectOrRecreate(t *testing.T) {
	baseCfg := config.Config{
		Image:           "ubuntu:latest",
		WorkspaceFolder: "/workspace/project",
	}

	t.Run("hash matches: start and reconnect without recreation", func(t *testing.T) {
		// Simulate a stopped container whose config hash matches the current config.
		// Expected: InspectConfigHash returns the matching hash, then docker start is called.
		currentHash := ConfigHashWithFolders(baseCfg, nil)

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: currentHash + "\n"}, // InspectConfigHash: matching hash
			},
			runResults: []runResult{
				{err: nil}, // docker start: success
			},
		}

		var stderr strings.Builder
		outcome, err := ReconnectOrRecreate(t.Context(), exec, ReconnectOptions{
			ContainerID: "container-abc",
			Config:      baseCfg,
			Network:     "none",
		}, &stderr)

		if err != nil {
			t.Fatalf("ReconnectOrRecreate() unexpected error: %v", err)
		}
		if outcome != ReconnectStarted {
			t.Errorf("ReconnectOrRecreate() outcome = %v, want ReconnectStarted", outcome)
		}

		// Verify docker start was called with the container ID.
		if len(exec.runCalls) != 1 {
			t.Fatalf("ReconnectOrRecreate() run calls = %d, want 1 (start)", len(exec.runCalls))
		}
		if exec.runCalls[0][0] != "start" {
			t.Errorf("ReconnectOrRecreate() run call[0] = %v, want start command", exec.runCalls[0])
		}
		if exec.runCalls[0][1] != "container-abc" {
			t.Errorf("ReconnectOrRecreate() start container ID = %q, want %q", exec.runCalls[0][1], "container-abc")
		}

		// No stop/rm should have been called — no recreation.
		if stderr.Len() != 0 {
			t.Errorf("ReconnectOrRecreate() stderr = %q, want empty (no recreation message)", stderr.String())
		}
	})

	t.Run("hash differs: stop, remove, and signal recreation", func(t *testing.T) {
		// Simulate a container whose stored config hash differs from the current config.
		// Expected: InspectConfigHash returns a different hash, then stop + rm are called.
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "stale-hash-value\n"}, // InspectConfigHash: different hash
			},
			runResults: []runResult{
				{err: nil}, // docker stop: success
				{err: nil}, // docker rm: success
			},
		}

		var stderr strings.Builder
		outcome, err := ReconnectOrRecreate(t.Context(), exec, ReconnectOptions{
			ContainerID: "container-xyz",
			Config:      baseCfg,
		}, &stderr)

		if err != nil {
			t.Fatalf("ReconnectOrRecreate() unexpected error: %v", err)
		}
		if outcome != ReconnectRecreated {
			t.Errorf("ReconnectOrRecreate() outcome = %v, want ReconnectRecreated", outcome)
		}

		// Verify stop and rm were called.
		if len(exec.runCalls) != 2 {
			t.Fatalf("ReconnectOrRecreate() run calls = %d, want 2 (stop, rm)", len(exec.runCalls))
		}
		if exec.runCalls[0][0] != "stop" {
			t.Errorf("ReconnectOrRecreate() run call[0] = %v, want stop command", exec.runCalls[0])
		}
		if exec.runCalls[1][0] != "rm" {
			t.Errorf("ReconnectOrRecreate() run call[1] = %v, want rm command", exec.runCalls[1])
		}
	})

	t.Run("hash differs: stderr message indicates recreation", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "different-hash\n"}, // InspectConfigHash: different hash
			},
			runResults: []runResult{
				{err: nil}, // docker stop: success
				{err: nil}, // docker rm: success
			},
		}

		var stderr strings.Builder
		_, err := ReconnectOrRecreate(t.Context(), exec, ReconnectOptions{
			ContainerID: "container-123",
			Config:      baseCfg,
		}, &stderr)

		if err != nil {
			t.Fatalf("ReconnectOrRecreate() unexpected error: %v", err)
		}
		if !strings.Contains(stderr.String(), "Configuration changed, recreating container...") {
			t.Errorf("ReconnectOrRecreate() stderr = %q, want containing %q",
				stderr.String(), "Configuration changed, recreating container...")
		}
	})

	t.Run("hash matches with network bridge: start and reapply firewall", func(t *testing.T) {
		currentHash := ConfigHashWithFolders(baseCfg, nil)

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: currentHash + "\n"},                                 // InspectConfigHash: matching hash
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "192.168.65.254\n"},                                 // hostInternalIP
			},
			runResults: []runResult{
				{err: nil}, // docker start: success
				{err: nil}, // applyFirewallRules: gateway
				{err: nil}, // applyFirewallRules: host.docker.internal
			},
		}

		var stderr strings.Builder
		outcome, err := ReconnectOrRecreate(t.Context(), exec, ReconnectOptions{
			ContainerID: "container-fw",
			Config:      baseCfg,
			Network:     "bridge",
		}, &stderr)

		if err != nil {
			t.Fatalf("ReconnectOrRecreate() unexpected error: %v", err)
		}
		if outcome != ReconnectStarted {
			t.Errorf("ReconnectOrRecreate() outcome = %v, want ReconnectStarted", outcome)
		}

		// Verify docker start was called, then firewall rules were applied.
		if len(exec.runCalls) != 3 {
			t.Fatalf("ReconnectOrRecreate() run calls = %d, want 3 (start + 2 firewall rules)", len(exec.runCalls))
		}
		if exec.runCalls[0][0] != "start" {
			t.Errorf("ReconnectOrRecreate() run call[0] = %v, want start command", exec.runCalls[0])
		}
		if exec.runCalls[1][0] != "exec" {
			t.Errorf("ReconnectOrRecreate() run call[1] = %v, want exec (firewall) command", exec.runCalls[1])
		}
	})

	t.Run("hash matches with additional folders: start without recreation", func(t *testing.T) {
		additionalFolders := []string{"/home/user/lib-a", "/home/user/lib-b"}
		currentHash := ConfigHashWithFolders(baseCfg, additionalFolders)

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: currentHash + "\n"}, // InspectConfigHash: matching hash (includes additional folders)
			},
			runResults: []runResult{
				{err: nil}, // docker start: success
			},
		}

		var stderr strings.Builder
		outcome, err := ReconnectOrRecreate(t.Context(), exec, ReconnectOptions{
			ContainerID:       "container-multi",
			Config:            baseCfg,
			AdditionalFolders: additionalFolders,
			Network:           "none",
		}, &stderr)

		if err != nil {
			t.Fatalf("ReconnectOrRecreate() unexpected error: %v", err)
		}
		if outcome != ReconnectStarted {
			t.Errorf("ReconnectOrRecreate() outcome = %v, want ReconnectStarted", outcome)
		}
		if stderr.Len() != 0 {
			t.Errorf("ReconnectOrRecreate() stderr = %q, want empty", stderr.String())
		}
	})

	t.Run("additional folders changed: hash differs triggers recreation", func(t *testing.T) {
		// Container was created with [lib-a], now invoked with [lib-a, lib-b].
		oldFolders := []string{"/home/user/lib-a"}
		newFolders := []string{"/home/user/lib-a", "/home/user/lib-b"}
		storedHash := ConfigHashWithFolders(baseCfg, oldFolders)

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: storedHash + "\n"}, // InspectConfigHash: old hash
			},
			runResults: []runResult{
				{err: nil}, // docker stop
				{err: nil}, // docker rm
			},
		}

		var stderr strings.Builder
		outcome, err := ReconnectOrRecreate(t.Context(), exec, ReconnectOptions{
			ContainerID:       "container-changed",
			Config:            baseCfg,
			AdditionalFolders: newFolders,
		}, &stderr)

		if err != nil {
			t.Fatalf("ReconnectOrRecreate() unexpected error: %v", err)
		}
		if outcome != ReconnectRecreated {
			t.Errorf("ReconnectOrRecreate() outcome = %v, want ReconnectRecreated", outcome)
		}
		if !strings.Contains(stderr.String(), "Configuration changed") {
			t.Errorf("ReconnectOrRecreate() stderr = %q, want containing %q",
				stderr.String(), "Configuration changed")
		}
	})

	t.Run("inspect config hash error propagated", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "", err: errFake}, // InspectConfigHash: error
			},
		}

		var stderr strings.Builder
		_, err := ReconnectOrRecreate(t.Context(), exec, ReconnectOptions{
			ContainerID: "container-err",
			Config:      baseCfg,
		}, &stderr)

		if err == nil {
			t.Fatal("ReconnectOrRecreate() = nil, want error")
		}
		if !strings.Contains(err.Error(), "inspect config hash") {
			t.Errorf("ReconnectOrRecreate() error = %q, want containing %q", err.Error(), "inspect config hash")
		}
	})

	t.Run("stop and remove error propagated", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "different-hash\n"}, // InspectConfigHash: different hash
			},
			runResults: []runResult{
				{err: errFake}, // docker stop: error
			},
		}

		var stderr strings.Builder
		_, err := ReconnectOrRecreate(t.Context(), exec, ReconnectOptions{
			ContainerID: "container-stop-err",
			Config:      baseCfg,
		}, &stderr)

		if err == nil {
			t.Fatal("ReconnectOrRecreate() = nil, want error")
		}
		if !strings.Contains(err.Error(), "recreate container") {
			t.Errorf("ReconnectOrRecreate() error = %q, want containing %q", err.Error(), "recreate container")
		}
	})

	t.Run("start container error propagated", func(t *testing.T) {
		currentHash := ConfigHashWithFolders(baseCfg, nil)

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: currentHash + "\n"}, // InspectConfigHash: matching hash
			},
			runResults: []runResult{
				{err: errFake}, // docker start: error
			},
		}

		var stderr strings.Builder
		_, err := ReconnectOrRecreate(t.Context(), exec, ReconnectOptions{
			ContainerID: "container-start-err",
			Config:      baseCfg,
		}, &stderr)

		if err == nil {
			t.Fatal("ReconnectOrRecreate() = nil, want error")
		}
		if !strings.Contains(err.Error(), "start container") {
			t.Errorf("ReconnectOrRecreate() error = %q, want containing %q", err.Error(), "start container")
		}
	})

	t.Run("firewall setup error stops and removes container", func(t *testing.T) {
		currentHash := ConfigHashWithFolders(baseCfg, nil)

		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: currentHash + "\n"}, // InspectConfigHash: matching hash
				{output: "", err: errFake},   // gatewayIP: error triggers firewall failure
			},
			runResults: []runResult{
				{err: nil}, // docker start: success
				{err: nil}, // StopAndRemove: docker stop
				{err: nil}, // StopAndRemove: docker rm
			},
		}

		var stderr strings.Builder
		_, err := ReconnectOrRecreate(t.Context(), exec, ReconnectOptions{
			ContainerID: "container-fw-err",
			Config:      baseCfg,
			Network:     "bridge",
		}, &stderr)

		if err == nil {
			t.Fatal("ReconnectOrRecreate() = nil, want error")
		}
		if !strings.Contains(err.Error(), "firewall setup") {
			t.Errorf("ReconnectOrRecreate() error = %q, want containing %q", err.Error(), "firewall setup")
		}

		// Verify fail-secure: container was stopped and removed after firewall failure.
		stopCalled := false
		for _, call := range exec.runCalls {
			if len(call) > 0 && call[0] == "stop" {
				stopCalled = true
			}
		}
		if !stopCalled {
			t.Error("ReconnectOrRecreate() did not call stop after firewall failure (fail-secure violation)")
		}
	})
}
