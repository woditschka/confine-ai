package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/woditschka/confine-ai/internal/container"
)

// cliFakeExecutor is a minimal canned-response executor for CLI-layer tests.
// It returns Output results in sequence and records Output/Run calls so tests
// can assert on the exact subprocess arguments that were issued.
type cliFakeExecutor struct {
	outputResults []cliOutputResult
	outputIdx     int
	outputCalls   [][]string
	runResults    []error
	runIdx        int
	runCalls      [][]string
}

type cliOutputResult struct {
	output string
	err    error
}

func (f *cliFakeExecutor) Output(_ context.Context, args ...string) (string, error) {
	f.outputCalls = append(f.outputCalls, append([]string(nil), args...))
	if f.outputIdx >= len(f.outputResults) {
		return "", errors.New("cliFakeExecutor: no more Output results")
	}
	r := f.outputResults[f.outputIdx]
	f.outputIdx++
	return r.output, r.err
}

func (f *cliFakeExecutor) Run(_ context.Context, _, _ io.Writer, args ...string) error {
	f.runCalls = append(f.runCalls, append([]string(nil), args...))
	if f.runIdx >= len(f.runResults) {
		return errors.New("cliFakeExecutor: no more Run results")
	}
	r := f.runResults[f.runIdx]
	f.runIdx++
	return r
}

func (*cliFakeExecutor) RunInteractive(_ context.Context, _ io.Reader, _, _ io.Writer, _ ...string) error {
	return nil
}

func TestRunRmWithExecutor(t *testing.T) {
	t.Run("no containers found reports empty", func(t *testing.T) {
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: ""}, // FindByLabels: no containers
			},
		}

		var stdout, stderr bytes.Buffer
		err := runRmWithExecutor(t.Context(), exec, &stdout, &stderr, "/home/user/project", nil)
		if err != nil {
			t.Fatalf("runRmWithExecutor() unexpected error: %v", err)
		}
		if !strings.Contains(stdout.String(), "No container found") {
			t.Errorf("runRmWithExecutor() stdout = %q, want containing %q", stdout.String(), "No container found")
		}
	})

	t.Run("removes workspace container", func(t *testing.T) {
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: "aabb0011\n"}, // FindByLabels: one container
			},
			runResults: []error{
				nil, // docker stop
				nil, // docker rm
			},
		}

		var stdout, stderr bytes.Buffer
		err := runRmWithExecutor(t.Context(), exec, &stdout, &stderr, "/home/user/project", nil)
		if err != nil {
			t.Fatalf("runRmWithExecutor() unexpected error: %v", err)
		}
		if !strings.Contains(stdout.String(), "Removed aabb0011") {
			t.Errorf("runRmWithExecutor() stdout = %q, want containing %q", stdout.String(), "Removed aabb0011")
		}
	})

	t.Run("removes assistant container by name", func(t *testing.T) {
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: "ccdd2233\n"}, // FindByAssistant: one container
			},
			runResults: []error{
				nil, // docker stop
				nil, // docker rm
			},
		}

		var stdout, stderr bytes.Buffer
		err := runRmWithExecutor(t.Context(), exec, &stdout, &stderr, "/home/user/project", []string{"claude"})
		if err != nil {
			t.Fatalf("runRmWithExecutor() unexpected error: %v", err)
		}
		if !strings.Contains(stdout.String(), "Removed ccdd2233") {
			t.Errorf("runRmWithExecutor() stdout = %q, want containing %q", stdout.String(), "Removed ccdd2233")
		}
	})

	t.Run("invalid assistant name returns error", func(t *testing.T) {
		exec := &cliFakeExecutor{}

		var stdout, stderr bytes.Buffer
		err := runRmWithExecutor(t.Context(), exec, &stdout, &stderr, "/home/user/project", []string{"INVALID_NAME"})
		if err == nil {
			t.Fatal("runRmWithExecutor(INVALID_NAME) = nil, want error")
		}
	})

	t.Run("down error propagated", func(t *testing.T) {
		exec := &cliFakeExecutor{
			outputResults: []cliOutputResult{
				{output: "", err: errors.New("connection refused")}, // FindByLabels: error
			},
		}

		var stdout, stderr bytes.Buffer
		err := runRmWithExecutor(t.Context(), exec, &stdout, &stderr, "/home/user/project", nil)
		if err == nil {
			t.Fatal("runRmWithExecutor() = nil, want error")
		}
		if !strings.Contains(err.Error(), "rm") {
			t.Errorf("runRmWithExecutor() error = %q, want containing %q", err.Error(), "rm")
		}
	})

	t.Run("help flag returns nil", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := RunRm(t.Context(), &stdout, &stderr, "/home/user/project", "", []string{"--help"})
		if err != nil {
			t.Fatalf("RunRm(--help) unexpected error: %v", err)
		}
	})
}

func TestWriteRmResult(t *testing.T) {
	const workspace = "/home/user/project"
	tests := []struct {
		name    string
		removed []string
		want    string
	}{
		{
			name:    "no containers reports empty",
			removed: nil,
			want:    "No container found for workspace /home/user/project\n",
		},
		{
			name:    "single container prints one line",
			removed: []string{"abc123"},
			want:    "Removed abc123\n",
		},
		{
			name:    "multiple containers print one line each in order",
			removed: []string{"abc123", "def456"},
			want:    "Removed abc123\nRemoved def456\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			writeRmResult(&buf, workspace, container.DownResult{Removed: tt.removed})
			if got := buf.String(); got != tt.want {
				t.Errorf("writeRmResult(%v) = %q, want %q", tt.removed, got, tt.want)
			}
		})
	}
}
