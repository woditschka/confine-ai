package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// CLIExecutor runs commands via the container runtime CLI.
// It wraps exec.CommandContext using the runtime binary path.
type CLIExecutor struct {
	path string // absolute path to docker/podman binary
}

// NewCLIExecutor creates an Executor that shells out to the runtime binary at
// the given absolute path. The path typically comes from runtime.Detect().Path.
func NewCLIExecutor(path string) *CLIExecutor {
	return &CLIExecutor{path: path}
}

// Output runs the runtime CLI with the given arguments and returns its stdout.
func (e *CLIExecutor) Output(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, e.path, args...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("run %s: %s: %w", e.path, exitErr.Stderr, err)
		}
		return "", fmt.Errorf("run %s: %w", e.path, err)
	}
	return string(out), nil
}

// Run executes the runtime CLI with the given arguments, wiring stdout and
// stderr to the provided writers. Either writer may be nil to discard output.
func (e *CLIExecutor) Run(ctx context.Context, stdout, stderr io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, e.path, args...)
	cmd.Stdout = stdout

	// Capture stderr into a buffer when the caller does not provide a writer,
	// so error messages from the runtime or container appear in the returned error.
	var stderrBuf *bytes.Buffer
	if stderr != nil {
		cmd.Stderr = stderr
	} else {
		stderrBuf = &bytes.Buffer{}
		cmd.Stderr = stderrBuf
	}

	if err := cmd.Run(); err != nil {
		if stderrBuf != nil {
			if msg := stderrBuf.String(); msg != "" {
				return fmt.Errorf("run %s: %s: %w", e.path, strings.TrimSpace(msg), err)
			}
		}
		return fmt.Errorf("run %s: %w", e.path, err)
	}
	return nil
}

// RunInteractive executes the runtime CLI with stdin, stdout, and stderr wired
// to the provided streams. Used for interactive operations like docker exec -it
// where the user's terminal input must reach the container process. The stdin
// reader may be nil to leave stdin unconnected.
func (e *CLIExecutor) RunInteractive(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, e.path, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", e.path, err)
	}
	return nil
}
