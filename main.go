package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/woditschka/confine-ai/internal/assistant"
	"github.com/woditschka/confine-ai/internal/cli"
	"github.com/woditschka/confine-ai/internal/container"
)

// version and commit are set at build time via -ldflags.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	os.Exit(main1())
}

func main1() int {
	// Cancel container operations on SIGINT/SIGTERM so docker build, stop,
	// etc. are interrupted cleanly instead of hanging.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getwd); err != nil {
		var exitErr *container.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.Code
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getwd func() (string, error)) error {
	fs := flag.NewFlagSet("confine-ai", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		showVersion     bool
		workspaceFolder string
		configPath      string
		dockerPath      string
	)

	fs.BoolVar(&showVersion, "version", false, "Display version information")
	fs.StringVar(&workspaceFolder, "workspace-folder", "", "Path to the project root (default: current directory)")
	fs.StringVar(&configPath, "config", "", "Path to devcontainer.json (skips auto-discovery)")
	fs.StringVar(&dockerPath, "docker-path", "", "Path to docker or podman binary (skips auto-detection)")

	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: confine-ai [flags] <command> [args]\n\n")
		fmt.Fprintf(stderr, "Commands:\n")
		fmt.Fprintf(stderr, "  completion   Generate shell completion script (bash, zsh)\n")
		fmt.Fprintf(stderr, "  init         Scaffold assistant configuration from built-in templates\n")
		fmt.Fprintf(stderr, "  rm           Stop and remove container\n")
		fmt.Fprintf(stderr, "  status       List all confine-ai-managed containers\n")
		fmt.Fprintf(stderr, "  update       Update base tool versions and/or rebuild assistant images\n")
		fmt.Fprintf(stderr, "\nAssistant shortcut: confine-ai <assistant-name> [--shell] [-- args...]\n")
		fmt.Fprintf(stderr, "\nRun 'confine-ai <command> --help' for command-specific flags.\n")
		fmt.Fprintf(stderr, "\nFlags:\n")
		fs.PrintDefaults()
	}

	if err := cli.ParseFlags(fs, args); err != nil {
		return cli.IgnoreHelp(err)
	}

	if showVersion {
		fmt.Fprintf(stdout, "%s (%s)\n", version, commit)
		return nil
	}

	// No command provided: show usage.
	remaining := fs.Args()
	if len(remaining) == 0 {
		fs.Usage()
		return nil
	}

	// Resolve workspace folder.
	if workspaceFolder == "" {
		cwd, err := getwd()
		if err != nil {
			return fmt.Errorf("working directory: %w", err)
		}
		workspaceFolder = cwd
	}

	absWorkspace, err := filepath.Abs(workspaceFolder)
	if err != nil {
		return fmt.Errorf("resolve workspace folder: %w", err)
	}
	workspaceFolder = absWorkspace

	// Verify explicit --config is within the workspace.
	if configPath != "" {
		absCfg, err := filepath.Abs(configPath)
		if err != nil {
			return fmt.Errorf("resolve config path: %w", err)
		}
		configPath = absCfg

		rel, err := filepath.Rel(workspaceFolder, configPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("config path %q is outside workspace %q", configPath, workspaceFolder)
		}
	}

	command := remaining[0]
	commandArgs := remaining[1:]

	// Known subcommands dispatch directly. Embedded Dockerfile bytes flow
	// into the CLI layer as explicit parameters so //go:embed can stay at
	// the repository root in embed.go.
	switch command {
	case "rm":
		return cli.RunRm(ctx, stdout, stderr, workspaceFolder, dockerPath, commandArgs)
	case "init":
		return cli.RunInit(stdout, stderr, commandArgs, baseDockerfile, assistantDockerfiles)
	case "update":
		return cli.RunUpdate(ctx, stdout, stderr, dockerPath, commandArgs, version, baseDockerfile)
	case "status":
		return cli.RunStatus(ctx, stdout, stderr, dockerPath, commandArgs)
	case "completion":
		return cli.RunCompletion(stdout, stderr, commandArgs)
	case "__complete":
		return cli.RunComplete(stdout, commandArgs, assistantDockerfiles)
	}

	// Not a known subcommand. Check if it is a valid assistant name.
	if err := assistant.ValidateName(command); err != nil {
		return fmt.Errorf("unknown command or assistant %q; run 'confine-ai init <assistant-name>' to create assistant configuration, or 'confine-ai --help' for available commands", command)
	}

	// Valid assistant name: dispatch to assistant shortcut handler.
	return cli.RunAssistant(ctx, stdout, stderr, command, workspaceFolder, dockerPath, commandArgs, baseDockerfile)
}
