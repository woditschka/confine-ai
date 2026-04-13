package container

import (
	"context"
	"io"
)

// Executor runs container runtime CLI commands and returns their output.
// Defined at the consumer site per the Go style guide ("accept interfaces,
// return structs") and ADR 2026-04-04 (container executor interface).
type Executor interface {
	// Output runs the command with the given arguments and returns its
	// stdout. The args slice contains the runtime subcommand and
	// flags (e.g., "ps", "--filter", "label=key=value", "--format", "{{.ID}}").
	Output(ctx context.Context, args ...string) (string, error)

	// Run executes the command with the given arguments, wiring stdout
	// and stderr to the provided writers. Either writer may be nil to
	// discard that stream. Used for operations like docker build, docker
	// stop, and docker start where output goes to the caller's terminal.
	Run(ctx context.Context, stdout, stderr io.Writer, args ...string) error

	// RunInteractive executes the command with stdin, stdout, and stderr
	// wired to the provided streams. Used for interactive operations like
	// docker exec -it where the user's terminal input must reach the
	// container process. The stdin reader may be nil to leave stdin
	// unconnected.
	RunInteractive(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) error
}

// Container holds the identity of a container found by a label query.
type Container struct {
	// ID is the container identifier (short or full hex hash) from docker ps.
	ID string
}
