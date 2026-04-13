package container

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// ExitError carries a process exit code through the error chain.
// It is returned by ExecInteractive when the executed command exits with a
// non-zero code.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit code %d", e.Code)
}

// exitCoder is satisfied by *os/exec.ExitError and test fakes.
type exitCoder interface {
	ExitCode() int
}

// ExecInteractive executes a command inside a container with optional TTY
// allocation. It takes a container ID directly (no lookup), wires stdin for
// interactive use, and adds -i -t flags when tty is true. Used by the
// assistant shortcut handler where the container ID is already known.
func ExecInteractive(ctx context.Context, executor Executor, containerID string, command []string, tty bool, stdin io.Reader, stdout, stderr io.Writer, workdir string) error {
	args := []string{"exec"}

	if workdir != "" {
		args = append(args, "-w", workdir)
	}

	if tty {
		args = append(args, "-i", "-t")
	}

	args = append(args, containerID)
	args = append(args, command...)

	return wrapExitError(executor.RunInteractive(ctx, stdin, stdout, stderr, args...), "exec interactive")
}

// wrapExitError converts a runtime exit code into an ExitError, or wraps
// other errors with the provided context string. Returns nil for nil input.
func wrapExitError(err error, errContext string) error {
	if err == nil {
		return nil
	}
	var ec exitCoder
	if errors.As(err, &ec) {
		return &ExitError{Code: ec.ExitCode()}
	}
	return fmt.Errorf("%s: %w", errContext, err)
}
