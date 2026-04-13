package container

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestExitError(t *testing.T) {
	t.Run("Error returns exit code message", func(t *testing.T) {
		err := &ExitError{Code: 42}
		want := "exit code 42"
		if got := err.Error(); got != want {
			t.Errorf("ExitError{Code: 42}.Error() = %q, want %q", got, want)
		}
	})

	t.Run("zero exit code", func(t *testing.T) {
		err := &ExitError{Code: 0}
		want := "exit code 0"
		if got := err.Error(); got != want {
			t.Errorf("ExitError{Code: 0}.Error() = %q, want %q", got, want)
		}
	})

	t.Run("extractable via errors.As", func(t *testing.T) {
		original := &ExitError{Code: 7}
		wrapped := fmt.Errorf("exec: %w", original)

		var exitErr *ExitError
		if !errors.As(wrapped, &exitErr) {
			t.Fatal("errors.As failed to extract ExitError from wrapped error")
		}
		if exitErr.Code != 7 {
			t.Errorf("extracted ExitError.Code = %d, want 7", exitErr.Code)
		}
	})

	t.Run("not extractable from unrelated error", func(t *testing.T) {
		unrelated := errors.New("some other error")

		var exitErr *ExitError
		if errors.As(unrelated, &exitErr) {
			t.Error("errors.As should not extract ExitError from unrelated error")
		}
	})
}

func TestExecInteractive(t *testing.T) {
	t.Run("TTY true adds -it flags and uses RunInteractive", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "abc123\n"}, // FindRunning: one container
			},
			runResults: []runResult{
				{err: nil}, // docker exec: success
			},
		}

		var stdout, stderr strings.Builder
		err := ExecInteractive(t.Context(), exec, "abc123", []string{"claude"}, true, strings.NewReader(""), &stdout, &stderr, "")
		if err != nil {
			t.Fatalf("ExecInteractive() unexpected error: %v", err)
		}

		// Verify the docker exec args include -it.
		if len(exec.runCalls) != 1 {
			t.Fatalf("ExecInteractive() made %d Run calls, want 1", len(exec.runCalls))
		}

		wantArgs := []string{"exec", "-i", "-t", "abc123", "claude"}
		if diff := cmp.Diff(wantArgs, exec.runCalls[0]); diff != "" {
			t.Errorf("ExecInteractive() args mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("TTY false omits -it flags", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "abc123\n"}, // FindRunning: one container
			},
			runResults: []runResult{
				{err: nil}, // docker exec: success
			},
		}

		var stdout, stderr strings.Builder
		err := ExecInteractive(t.Context(), exec, "abc123", []string{"claude"}, false, strings.NewReader(""), &stdout, &stderr, "")
		if err != nil {
			t.Fatalf("ExecInteractive() unexpected error: %v", err)
		}

		wantArgs := []string{"exec", "abc123", "claude"}
		if diff := cmp.Diff(wantArgs, exec.runCalls[0]); diff != "" {
			t.Errorf("ExecInteractive() args mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("workdir adds -w flag before container ID", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			runResults: []runResult{
				{err: nil}, // docker exec: success
			},
		}

		var stdout, stderr strings.Builder
		err := ExecInteractive(t.Context(), exec, "abc123", []string{"claude"}, true, strings.NewReader(""), &stdout, &stderr, "/workspace/devcontainer-go")
		if err != nil {
			t.Fatalf("ExecInteractive() unexpected error: %v", err)
		}

		wantArgs := []string{"exec", "-w", "/workspace/devcontainer-go", "-i", "-t", "abc123", "claude"}
		if diff := cmp.Diff(wantArgs, exec.runCalls[0]); diff != "" {
			t.Errorf("ExecInteractive() args mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("empty workdir omits -w flag", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			runResults: []runResult{
				{err: nil}, // docker exec: success
			},
		}

		var stdout, stderr strings.Builder
		err := ExecInteractive(t.Context(), exec, "abc123", []string{"claude"}, false, strings.NewReader(""), &stdout, &stderr, "")
		if err != nil {
			t.Fatalf("ExecInteractive() unexpected error: %v", err)
		}

		wantArgs := []string{"exec", "abc123", "claude"}
		if diff := cmp.Diff(wantArgs, exec.runCalls[0]); diff != "" {
			t.Errorf("ExecInteractive() args mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("non-zero exit code forwarded", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			runResults: []runResult{
				{err: fakeExitError(2)}, // docker exec: exit code 2
			},
		}

		var stdout, stderr strings.Builder
		err := ExecInteractive(t.Context(), exec, "abc123", []string{"false"}, true, nil, &stdout, &stderr, "")
		if err == nil {
			t.Fatal("ExecInteractive() = nil, want ExitError")
		}

		var exitErr *ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("ExecInteractive() error type = %T, want *ExitError (error: %v)", err, err)
		}
		if exitErr.Code != 2 {
			t.Errorf("ExecInteractive() ExitError.Code = %d, want 2", exitErr.Code)
		}
	})

	t.Run("infrastructure failure wrapped", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			runResults: []runResult{
				{err: errFake},
			},
		}

		var stdout, stderr strings.Builder
		err := ExecInteractive(t.Context(), exec, "abc123", []string{"echo"}, false, nil, &stdout, &stderr, "")
		if err == nil {
			t.Fatal("ExecInteractive() = nil, want error")
		}

		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			t.Error("ExecInteractive() infrastructure error should not be ExitError")
		}
		if !strings.Contains(err.Error(), "exec interactive") {
			t.Errorf("ExecInteractive() error = %q, want containing %q", err.Error(), "exec interactive")
		}
	})
}

// fakeExitError returns an error that wraps an *os/exec.ExitError-like error
// with the given exit code. Since we cannot construct a real *exec.ExitError
// in tests (it requires a process), we use a custom type that the Exec
// function must handle. However, the CLIExecutor.Run wraps *exec.ExitError
// with fmt.Errorf("run %s: %w", ...), so the Exec function needs to extract
// the exit code. For unit tests, we simulate this by returning an error that
// carries the exit code.
//
// We use a testExitError that implements the ExitCoder interface to allow
// the Exec function to extract exit codes from both real *exec.ExitError
// (via os.ProcessState) and test fakes.
func fakeExitError(code int) error {
	return fmt.Errorf("run /usr/bin/docker: %w", &testProcessExitError{code: code})
}

// testProcessExitError simulates an *exec.ExitError for testing. The Exec
// function extracts exit codes by checking for the exitCoder interface.
type testProcessExitError struct {
	code int
}

func (e *testProcessExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.code)
}

func (e *testProcessExitError) ExitCode() int {
	return e.code
}
