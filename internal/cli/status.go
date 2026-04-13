package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/woditschka/confine-ai/internal/container"
)

// RunStatus lists all confine-ai-managed containers.
func RunStatus(ctx context.Context, stdout, stderr io.Writer, dockerPath string, args []string) error {
	statusFlags := NewFlagSet("status", stderr)

	statusFlags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: confine-ai status\n\n")
		fmt.Fprintf(stderr, "List all confine-ai-managed containers.\n")
	}

	if err := ParseFlags(statusFlags, args); err != nil {
		return IgnoreHelp(err)
	}

	executor, _, err := newExecutor(dockerPath)
	if err != nil {
		return fmt.Errorf("status: runtime: %w", err)
	}

	return runStatusWithExecutor(ctx, executor, stdout)
}

// runStatusWithExecutor is the injectable core of RunStatus. Tests substitute
// a fake executor to exercise the display logic without a real container runtime.
func runStatusWithExecutor(ctx context.Context, executor container.Executor, stdout io.Writer) error {
	infos, err := container.FindAllManaged(ctx, executor)
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}

	if len(infos) == 0 {
		fmt.Fprintln(stdout, "No confine-ai containers running")
		return nil
	}

	// Print table header and rows.
	fmt.Fprintf(stdout, "%-16s %-40s %-14s %s\n", "ASSISTANT", "WORKSPACE", "CONTAINER ID", "STATUS")
	for _, info := range infos {
		// Truncate container ID to 12 characters for display.
		containerID := info.ID
		if len(containerID) > 12 {
			containerID = containerID[:12]
		}
		fmt.Fprintf(stdout, "%-16s %-40s %-14s %s\n", info.Assistant, info.Workspace, containerID, info.Status)
	}

	return nil
}
