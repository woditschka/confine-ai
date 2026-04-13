package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/woditschka/confine-ai/internal/assistant"
)

// fakeAssistantBuilder captures calls to Run so RunAssistant tests can assert on
// the exact `build` command arguments and inject failures.
type fakeAssistantBuilder struct {
	runCalls  [][]string
	runErrors []error
	idx       int

	// outputErr controls the response of Output. When nil, Output returns
	// ("", nil) — used by EnsureBaseImage to mean "image present". When set,
	// Output returns the configured error — used to mean "image absent".
	outputErr   error
	outputCalls [][]string
}

func (f *fakeAssistantBuilder) Output(_ context.Context, args ...string) (string, error) {
	f.outputCalls = append(f.outputCalls, append([]string(nil), args...))
	return "", f.outputErr
}

func (f *fakeAssistantBuilder) Run(_ context.Context, _, _ io.Writer, args ...string) error {
	f.runCalls = append(f.runCalls, append([]string(nil), args...))
	var err error
	if f.idx < len(f.runErrors) {
		err = f.runErrors[f.idx]
	}
	f.idx++
	return err
}

// fakeAssistantExecutor implements container.Executor for RunAssistant tests. It
// records the last `ps` filter query, returns a canned container-id list, and
// reports stop/rm errors via nextRunErr.
type fakeAssistantExecutor struct {
	psOutput string
	psErr    error
	runErr   error // returned for any Run invocation (stop/rm)
	runCalls [][]string
}

func (f *fakeAssistantExecutor) Output(_ context.Context, _ ...string) (string, error) {
	return f.psOutput, f.psErr
}

func (f *fakeAssistantExecutor) Run(_ context.Context, _, _ io.Writer, args ...string) error {
	f.runCalls = append(f.runCalls, append([]string(nil), args...))
	return f.runErr
}

func (*fakeAssistantExecutor) RunInteractive(_ context.Context, _ io.Reader, _, _ io.Writer, _ ...string) error {
	return nil
}

// seedAssistantDir creates ~/.confine-ai/assistants/<name>/Dockerfile under homeDir.
// Returns the assistant directory path.
func seedAssistantDir(t *testing.T, homeDir, name string) string {
	t.Helper()
	assistantDir := assistant.Dir(homeDir, name)
	if err := os.MkdirAll(assistantDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error: %v", assistantDir, err)
	}
	if err := os.WriteFile(assistant.DockerfilePath(homeDir, name), []byte("FROM localhost/confine-ai-base:latest\n"), 0o644); err != nil {
		t.Fatalf("WriteFile Dockerfile: %v", err)
	}
	return assistantDir
}

func TestRunAssistant_HappyPath(t *testing.T) {
	homeDir := t.TempDir()
	assistantDir := seedAssistantDir(t, homeDir, "claude")

	builder := &fakeAssistantBuilder{runErrors: []error{nil}}
	executor := &fakeAssistantExecutor{psOutput: ""}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		Stdout:        &stdout,
		Stderr:        &stderr,
		Builder:       builder,
		Executor:      executor,
	})

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, stderr.String())
	}
	if result.Action != ActionUpdated {
		t.Errorf("Action = %q, want %q", result.Action, ActionUpdated)
	}
	if result.Target != "claude" {
		t.Errorf("Target = %q, want %q", result.Target, "claude")
	}

	if len(builder.runCalls) != 1 {
		t.Fatalf("builder Run calls = %d, want 1", len(builder.runCalls))
	}
	args := builder.runCalls[0]
	wantDockerfile := assistant.DockerfilePath(homeDir, "claude")
	if !containsAll(args, "build", "--no-cache", "-t", "confine-ai-assistant-claude:latest", "-f", wantDockerfile, assistantDir) {
		t.Errorf("builder Run args = %v, want build --no-cache -t confine-ai-assistant-claude:latest -f %s %s",
			args, wantDockerfile, assistantDir)
	}
	for _, a := range args {
		if a == "--pull" {
			t.Errorf("builder Run args = %v, must not contain --pull (localhost/confine-ai-base:latest is local-only)", args)
		}
	}

	// The executor must have been queried for existing containers (the
	// RemoveContainersByAssistant ps call). psOutput was empty, so no stop/rm
	// should have been issued.
	if len(executor.runCalls) != 0 {
		t.Errorf("executor Run calls = %d, want 0 (no containers to drop)", len(executor.runCalls))
	}
}

func TestRunAssistant_DryRun(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	builder := &fakeAssistantBuilder{}
	executor := &fakeAssistantExecutor{}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		DryRun:        true,
		Stdout:        &stdout,
		Stderr:        &stderr,
		Builder:       builder,
		Executor:      executor,
	})

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Action != ActionWouldUpdate {
		t.Errorf("Action = %q, want %q", result.Action, ActionWouldUpdate)
	}
	if !strings.Contains(stdout.String(), "would rebuild claude") {
		t.Errorf("stdout = %q, want containing 'would rebuild claude'", stdout.String())
	}
	if len(builder.runCalls) != 0 {
		t.Errorf("builder Run calls = %d, want 0 in dry-run", len(builder.runCalls))
	}
	if len(executor.runCalls) != 0 {
		t.Errorf("executor Run calls = %d, want 0 in dry-run", len(executor.runCalls))
	}
}

func TestRunAssistant_AssistantDirMissing(t *testing.T) {
	homeDir := t.TempDir()

	builder := &fakeAssistantBuilder{}
	executor := &fakeAssistantExecutor{}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		Stdout:        &stdout,
		Stderr:        &stderr,
		Builder:       builder,
		Executor:      executor,
	})

	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
	}
	if result.Action != ActionFailed {
		t.Errorf("Action = %q, want %q", result.Action, ActionFailed)
	}
	if !strings.Contains(result.Error, "claude") {
		t.Errorf("Error = %q, want containing assistant name 'claude'", result.Error)
	}
	if len(builder.runCalls) != 0 {
		t.Errorf("builder Run calls = %d, want 0 when assistant dir missing", len(builder.runCalls))
	}
}

func TestRunAssistant_DockerfileMissing(t *testing.T) {
	homeDir := t.TempDir()
	// Create the assistant dir but NOT Dockerfile.
	assistantDir := assistant.Dir(homeDir, "claude")
	if err := os.MkdirAll(assistantDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error: %v", assistantDir, err)
	}

	builder := &fakeAssistantBuilder{}
	executor := &fakeAssistantExecutor{}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		Stdout:        &stdout,
		Stderr:        &stderr,
		Builder:       builder,
		Executor:      executor,
	})

	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
	}
	if result.Action != ActionFailed {
		t.Errorf("Action = %q, want %q", result.Action, ActionFailed)
	}
	if !strings.Contains(result.Error, "Dockerfile") {
		t.Errorf("Error = %q, want containing 'Dockerfile'", result.Error)
	}
	if len(builder.runCalls) != 0 {
		t.Errorf("builder Run calls = %d, want 0 when Dockerfile missing", len(builder.runCalls))
	}
}

func TestRunAssistant_BuilderFailure(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	builder := &fakeAssistantBuilder{runErrors: []error{errors.New("build failed")}}
	executor := &fakeAssistantExecutor{}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		Stdout:        &stdout,
		Stderr:        &stderr,
		Builder:       builder,
		Executor:      executor,
	})

	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
	}
	if result.Action != ActionFailed {
		t.Errorf("Action = %q, want %q", result.Action, ActionFailed)
	}
	if result.Error == "" {
		t.Error("Error = empty, want error message")
	}
	// Executor.Run should never be called if build failed (no drop phase).
	if len(executor.runCalls) != 0 {
		t.Errorf("executor Run calls = %d, want 0 after builder failure", len(executor.runCalls))
	}
}

func TestRunAssistant_DropFailureIsWarning(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	builder := &fakeAssistantBuilder{runErrors: []error{nil}}
	// Force RemoveContainersByAssistant to fail by making the ps query error.
	executor := &fakeAssistantExecutor{psErr: errors.New("ps failed")}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		Stdout:        &stdout,
		Stderr:        &stderr,
		Builder:       builder,
		Executor:      executor,
	})

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (drop failure is warning only)", result.ExitCode)
	}
	if result.Action != ActionUpdated {
		t.Errorf("Action = %q, want %q", result.Action, ActionUpdated)
	}
	if !strings.Contains(stderr.String(), "warning") {
		t.Errorf("stderr = %q, want containing 'warning'", stderr.String())
	}
}

func TestRunAssistant_EnsuresBaseImageWhenMissing(t *testing.T) {
	// When BaseDockerfile is supplied and the local base image is absent
	// (Output returns an error), RunAssistant must first build the base image,
	// then rebuild the assistant image. Two Run calls are expected in that
	// order: the base build and the assistant build.
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	builder := &fakeAssistantBuilder{
		outputErr: errors.New("no such image"),
		runErrors: []error{nil, nil},
	}
	executor := &fakeAssistantExecutor{psOutput: ""}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:        homeDir,
		AssistantName:  "claude",
		Stdout:         &stdout,
		Stderr:         &stderr,
		Builder:        builder,
		Executor:       executor,
		BaseDockerfile: []byte("FROM debian:bookworm-slim\n"),
	})

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, stderr.String())
	}
	if len(builder.runCalls) != 2 {
		t.Fatalf("builder Run calls = %d, want 2 (base build + assistant build)", len(builder.runCalls))
	}
	if !containsAll(builder.runCalls[0], "build", "-t", "localhost/confine-ai-base:latest") {
		t.Errorf("first Run args = %v, want base image build", builder.runCalls[0])
	}
	if !containsAll(builder.runCalls[1], "build", "--no-cache", "-t", "confine-ai-assistant-claude:latest") {
		t.Errorf("second Run args = %v, want assistant image build", builder.runCalls[1])
	}
}

func TestRunAssistant_SkipsBaseBuildWhenPresent(t *testing.T) {
	// When BaseDockerfile is supplied and Output reports the base image
	// is already present, RunAssistant must not issue a base build. Only the
	// assistant build should run.
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	builder := &fakeAssistantBuilder{
		outputErr: nil, // image present
		runErrors: []error{nil},
	}
	executor := &fakeAssistantExecutor{psOutput: ""}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:        homeDir,
		AssistantName:  "claude",
		Stdout:         &stdout,
		Stderr:         &stderr,
		Builder:        builder,
		Executor:       executor,
		BaseDockerfile: []byte("FROM debian:bookworm-slim\n"),
	})

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, stderr.String())
	}
	if len(builder.runCalls) != 1 {
		t.Fatalf("builder Run calls = %d, want 1 (assistant build only)", len(builder.runCalls))
	}
	if !containsAll(builder.runCalls[0], "build", "--no-cache", "-t", "confine-ai-assistant-claude:latest") {
		t.Errorf("Run args = %v, want assistant image build", builder.runCalls[0])
	}
	if len(builder.outputCalls) != 1 {
		t.Errorf("builder Output calls = %d, want 1 (image inspect precheck)", len(builder.outputCalls))
	}
}

// containsAll reports whether every element of subs appears in args (order
// insensitive). Mirrors the helper in internal/assistant/assistant_build_test.go.
func containsAll(args []string, subs ...string) bool {
	for _, s := range subs {
		found := slices.Contains(args, s)
		if !found {
			return false
		}
	}
	return true
}
