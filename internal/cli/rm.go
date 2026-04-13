package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/woditschka/confine-ai/internal/assistant"
	"github.com/woditschka/confine-ai/internal/container"
)

// RunRm detects the runtime and stops/removes containers for the workspace.
// When an assistant name is provided as a positional argument, it targets only
// that assistant's container. Without an assistant name, it removes all
// workspace containers.
func RunRm(ctx context.Context, stdout, stderr io.Writer, workspaceFolder, dockerPath string, args []string) error {
	rmFlags := NewFlagSet("rm", stderr)

	rmFlags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: confine-ai rm [assistant-name]\n\n")
		fmt.Fprintf(stderr, "Stop and remove the development container for this workspace.\n")
		fmt.Fprintf(stderr, "When assistant-name is provided, targets only that assistant's container.\n")
	}

	if err := ParseFlags(rmFlags, args); err != nil {
		return IgnoreHelp(err)
	}

	executor, _, err := newExecutor(dockerPath)
	if err != nil {
		return fmt.Errorf("rm: runtime: %w", err)
	}

	return runRmWithExecutor(ctx, executor, stdout, stderr, workspaceFolder, rmFlags.Args())
}

// runRmWithExecutor is the injectable core of RunRm. Tests substitute a fake
// executor to exercise the dispatch logic without a real container runtime.
func runRmWithExecutor(ctx context.Context, executor container.Executor, stdout, stderr io.Writer, workspaceFolder string, args []string) error {
	var (
		result        container.DownResult
		err           error
		assistantName string
	)
	if len(args) > 0 {
		assistantName = args[0]
		if err := assistant.ValidateName(assistantName); err != nil {
			return fmt.Errorf("rm: %w", err)
		}
		result, err = container.DownAssistant(ctx, executor, assistantName, []string{workspaceFolder}, stderr)
	} else {
		result, err = container.Down(ctx, executor, []string{workspaceFolder}, stderr)
	}
	if err != nil {
		return fmt.Errorf("rm: %w", err)
	}

	writeRmResult(stdout, workspaceFolder, result)
	return nil
}

// writeRmResult formats the rm command output to stdout.
func writeRmResult(stdout io.Writer, workspaceFolder string, result container.DownResult) {
	if len(result.Removed) == 0 {
		fmt.Fprintf(stdout, "No container found for workspace %s\n", workspaceFolder)
		return
	}

	for _, id := range result.Removed {
		fmt.Fprintf(stdout, "Removed %s\n", id)
	}
}
