package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRunStatusWithExecutor(t *testing.T) {
	t.Run("no containers reports empty", func(t *testing.T) {
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: ""}, // FindAllManaged: no containers
			},
		}

		var stdout bytes.Buffer
		err := runStatusWithExecutor(t.Context(), exec, &stdout)
		if err != nil {
			t.Fatalf("runStatusWithExecutor() unexpected error: %v", err)
		}
		if !strings.Contains(stdout.String(), "No confine-ai containers running") {
			t.Errorf("runStatusWithExecutor() stdout = %q, want containing %q", stdout.String(), "No confine-ai containers running")
		}
	})

	t.Run("displays container table", func(t *testing.T) {
		// FindAllManaged expects tab-separated: ID\tStatus\tLabelsJSON
		labelsJSON := `{"devcontainer.metadata_id":"abc","devcontainer.assistant_name":"claude","devcontainer.local_folder":"/home/user/project"}`
		line := "aabbccdd0011\tUp 2 hours\t" + labelsJSON
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: line}, // FindAllManaged: one container
			},
		}

		var stdout bytes.Buffer
		err := runStatusWithExecutor(t.Context(), exec, &stdout)
		if err != nil {
			t.Fatalf("runStatusWithExecutor() unexpected error: %v", err)
		}
		output := stdout.String()
		if !strings.Contains(output, "ASSISTANT") {
			t.Errorf("runStatusWithExecutor() output missing header, got %q", output)
		}
		if !strings.Contains(output, "claude") {
			t.Errorf("runStatusWithExecutor() output = %q, want containing %q", output, "claude")
		}
		if !strings.Contains(output, "aabbccdd0011") {
			t.Errorf("runStatusWithExecutor() output = %q, want containing container ID", output)
		}
	})

	t.Run("truncates long container ID", func(t *testing.T) {
		labelsJSON := `{"devcontainer.metadata_id":"abc","devcontainer.assistant_name":"claude","devcontainer.local_folder":"/ws"}`
		longID := "aabbccdd00112233"
		line := longID + "\tUp\t" + labelsJSON
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: line},
			},
		}

		var stdout bytes.Buffer
		err := runStatusWithExecutor(t.Context(), exec, &stdout)
		if err != nil {
			t.Fatalf("runStatusWithExecutor() unexpected error: %v", err)
		}
		output := stdout.String()
		// Full 16-char ID should not appear; truncated 12-char should.
		if strings.Contains(output, longID) {
			t.Errorf("runStatusWithExecutor() output contains full ID %q, want truncated to 12 chars", longID)
		}
		if !strings.Contains(output, longID[:12]) {
			t.Errorf("runStatusWithExecutor() output = %q, want containing truncated ID %q", output, longID[:12])
		}
	})

	t.Run("executor error propagated", func(t *testing.T) {
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: "", err: errors.New("connection refused")},
			},
		}

		var stdout bytes.Buffer
		err := runStatusWithExecutor(t.Context(), exec, &stdout)
		if err == nil {
			t.Fatal("runStatusWithExecutor() = nil, want error")
		}
		if !strings.Contains(err.Error(), "status") {
			t.Errorf("runStatusWithExecutor() error = %q, want containing %q", err.Error(), "status")
		}
	})

	t.Run("help flag returns nil", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := RunStatus(t.Context(), &stdout, &stderr, "", []string{"--help"})
		if err != nil {
			t.Fatalf("RunStatus(--help) unexpected error: %v", err)
		}
	})
}
