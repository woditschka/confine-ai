package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/woditschka/confine-ai/internal/assistant"
	"github.com/woditschka/confine-ai/internal/config"
	"github.com/woditschka/confine-ai/internal/container"
	"github.com/woditschka/confine-ai/internal/gitenv"
	"github.com/woditschka/confine-ai/internal/runtime"
)

// assistantParams groups the resolved parameters for the assistant shortcut
// after flag parsing and folder resolution but before runtime detection.
type assistantParams struct {
	assistantName            string
	workspaceFolder          string
	additionalFolders        []string
	assistantPassthroughArgs []string
	allowedHosts             []string
	noGitIdentity            bool
	shellMode                bool
	homeDir                  string
	baseDockerfile           []byte
}

// RunAssistant handles the assistant shortcut:
// confine-ai <assistant-name> [folders...] [-- args...]. It starts or reconnects
// to an assistant container, then execs into it. baseDockerfile is the
// embedded base Dockerfile seed flowed in from main.go.
func RunAssistant(ctx context.Context, stdout, stderr io.Writer, assistantName, workspaceFolder, dockerPath string, commandArgs []string, baseDockerfile []byte) error {
	// Defense-in-depth: validate the assistant name even though main.go
	// already calls ValidateName before reaching this function. This
	// protects against future callers that bypass main's dispatch.
	if err := assistant.ValidateName(assistantName); err != nil {
		return fmt.Errorf("assistant: %w", err)
	}

	// Extract known flags before parsing folder/assistant args.
	noGitIdentity, commandArgs := extractBoolFlag(commandArgs, "--no-git-identity")
	shellMode, commandArgs := extractBoolFlag(commandArgs, "--shell")
	allowedHosts, commandArgs := extractRepeatedFlag(commandArgs, "--allowed-hosts")

	// Parse folder arguments and assistant passthrough args (REQ-MF-001, REQ-CL-005).
	folderPaths, assistantPassthroughArgs := parseFolderArgs(commandArgs)

	// Resolve folders before any other work.
	var additionalFolders []string
	if len(folderPaths) > 0 {
		primary, additional, err := resolveFolders(folderPaths, workspaceFolder)
		if err != nil {
			return fmt.Errorf("assistant: folder arguments: %w", err)
		}
		workspaceFolder = primary
		additionalFolders = additional
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("assistant: home directory: %w", err)
	}

	// Check assistant directory exists.
	if !assistant.Exists(homeDir, assistantName) {
		return fmt.Errorf("assistant %q not found; run 'confine-ai init %s'", assistantName, assistantName)
	}

	// Ensure extra files exist for assistants initialized before this feature.
	if err := assistant.EnsureExtraFiles(homeDir, assistantName); err != nil {
		return fmt.Errorf("assistant: %w", err)
	}

	// Pre-create host-mount source directories so the container runtime does
	// not materialize them as root-owned empty dirs on first launch, and so
	// ValidateMounts in container.Up does not block a first-time opencode
	// user whose ~/.local/share/opencode does not yet exist. MkdirAll is a
	// no-op when the directory already exists.
	if err := ensureHostMountDirs(homeDir, assistantName); err != nil {
		return fmt.Errorf("assistant: %w", err)
	}

	// Read host git identity early so the warning appears even if runtime
	// detection fails (REQ-CL-003).
	var gitName, gitEmail string
	if !noGitIdentity {
		var gitErr error
		gitName, gitEmail, gitErr = gitenv.ReadIdentity(ctx)
		if gitErr != nil {
			fmt.Fprintf(stderr, "warning: git identity not forwarded: %v\n", gitErr)
		}
	}

	// Detect runtime.
	fmt.Fprintf(stderr, "Detecting container runtime...\n")
	executor, rt, err := newExecutor(dockerPath)
	if err != nil {
		return fmt.Errorf("assistant: runtime: %w", err)
	}

	return runAssistantWithExecutor(ctx, executor, rt, stdout, stderr, assistantParams{
		assistantName:            assistantName,
		workspaceFolder:          workspaceFolder,
		additionalFolders:        additionalFolders,
		assistantPassthroughArgs: assistantPassthroughArgs,
		allowedHosts:             allowedHosts,
		noGitIdentity:            noGitIdentity,
		shellMode:                shellMode,
		homeDir:                  homeDir,
		baseDockerfile:           baseDockerfile,
	}, gitName, gitEmail)
}

// runAssistantWithExecutor is the injectable core of RunAssistant. Tests
// substitute a fake executor to exercise config loading, container lookup,
// and the Up/reconnect dispatch logic without a real container runtime.
func runAssistantWithExecutor(ctx context.Context, executor container.Executor, rt runtime.Runtime, stdout, stderr io.Writer, p assistantParams, gitName, gitEmail string) error {
	// Validate allowed hosts early, before any container operations (REQ-NR-001).
	if len(p.allowedHosts) > 0 {
		if err := container.ValidateAllowedHosts(p.allowedHosts); err != nil {
			return fmt.Errorf("assistant: %w", err)
		}
	}

	// Auto-build base image if missing. Resolve the base Dockerfile from the
	// user copy (~/.confine-ai/base/Dockerfile) if present; otherwise fall back
	// to the embedded seed silently (this path is not explicit-build intent).
	resolvedBase, err := assistant.ResolveBaseDockerfile(p.homeDir, p.baseDockerfile, stderr, false)
	if err != nil {
		return fmt.Errorf("assistant: base image: %w", err)
	}
	if err := assistant.EnsureBaseImage(ctx, executor, resolvedBase, stderr); err != nil {
		return fmt.Errorf("assistant: base image: %w", err)
	}

	// Auto-ensure the canonical assistant image (REQ-AS-002 ACs 16-19). This is
	// a cached build on first use only; cache-busting is exclusively the job of
	// `confine-ai update <assistant>` (REQ-AS-008 AC 42). The helper emits a
	// single stderr breadcrumb when it actually builds.
	if err := assistant.EnsureAssistantImage(ctx, executor, p.homeDir, p.assistantName, stderr); err != nil {
		return fmt.Errorf("assistant: assistant image: %w", err)
	}

	// Load assistant config through the standard pipeline.
	fmt.Fprintf(stderr, "Loading assistant configuration...\n")
	cfgPath := assistant.ConfigPath(p.homeDir, p.assistantName)

	cfg, _, err := config.LoadFromWorkspace(p.workspaceFolder, cfgPath, stderr, os.LookupEnv)
	if err != nil {
		return fmt.Errorf("assistant: %w", err)
	}

	// REQ-AS-002 ACs 12-15 (single-owner image model): the shortcut is a pure
	// consumer of the canonical tag. Override the image explicitly and clear
	// the build block so container.Up takes the no-build branch — the shortcut
	// must never invoke buildImage, which would derive a workspace-basename tag
	// and silently diverge from `confine-ai update`. The override happens after
	// LoadFromWorkspace because the loader populates these fields from the
	// assistant's devcontainer.json. This placement is load-bearing: any
	// build.args / build.context / build.dockerfile edits in the user's
	// devcontainer.json are thereby ignored on the shortcut path, matching the
	// fixed-Dockerfile invariant enforced by BuildAssistantImage.
	cfg.Image = assistant.AssistantImageTag(p.assistantName)
	cfg.Build = nil

	// Merge git identity into container env after config is loaded (REQ-CL-003).
	if !p.noGitIdentity && gitName != "" && gitEmail != "" {
		cfg.ContainerEnv = gitenv.MergeInto(cfg.ContainerEnv, gitName, gitEmail)
	}

	// Override workspace folder to /workspace/<basename> for assistant containers.
	// This ensures the primary workspace is mounted at a basename-specific path
	// rather than the flat /workspace from the devcontainer.json template.
	cfg.WorkspaceFolder = "/workspace/" + filepath.Base(p.workspaceFolder)

	// Resolve resource limits from assistant config (no CLI flags for assistant shortcut).
	limits, err := config.ResolveResourceLimits("", "", cfg.Customizations)
	if err != nil {
		return fmt.Errorf("assistant: resource limits: %w", err)
	}

	// Emit warning when no memory limit is set from any source (REQ-RL-001).
	if limits.Memory == "" {
		fmt.Fprintf(stderr, "warning: no memory limit set; container can consume all host memory\n")
	}

	// Print provider setup hint when the config still has only the default
	// seed (no providers configured). This catches users who missed the
	// post-init hint or were initialized before hint support was added.
	if hint := assistant.PostInitHint(p.assistantName); hint != "" && assistant.NeedsProviderHint(p.homeDir, p.assistantName) {
		fmt.Fprintf(stderr, "\n%s\n\n", hint)
	}

	// Determine the command to exec.
	var execCommand []string
	if p.shellMode {
		// Wrap assistant in bash so the user drops into a shell after exit.
		parts := []string{shellQuote(p.assistantName)}
		for _, arg := range p.assistantPassthroughArgs {
			parts = append(parts, shellQuote(arg))
		}
		execCommand = []string{"bash", "-c", strings.Join(parts, " ") + "; exec bash"}
	} else {
		execCommand = append([]string{p.assistantName}, p.assistantPassthroughArgs...)
	}

	// TTY detection: check if stdin is a terminal.
	isTTY := isatty(os.Stdin)

	var stdinReader io.Reader
	if isTTY {
		stdinReader = os.Stdin
	}

	// Build folder set from primary workspace + additional folders (REQ-CO-001).
	folderSet := slices.Concat([]string{p.workspaceFolder}, p.additionalFolders)

	// Find existing container for this (assistant, folder-set) pair.
	containers, err := container.FindByAssistant(ctx, executor, p.assistantName, folderSet)
	if err != nil {
		return fmt.Errorf("assistant: %w", err)
	}

	if len(containers) > 0 {
		containerID := containers[0].ID

		// Config-hash validation: compare stored hash against current config
		// (REQ-AS-002 AC 10, 11).
		outcome, err := container.ReconnectOrRecreate(ctx, executor, container.ReconnectOptions{
			ContainerID:       containerID,
			Config:            cfg,
			AdditionalFolders: p.additionalFolders,
			Network:           rt.DefaultNetwork(),
			AllowedHosts:      p.allowedHosts,
		}, stderr)
		if err != nil {
			return fmt.Errorf("assistant: %w", err)
		}

		if outcome == container.ReconnectStarted {
			// Config matches: container started, exec into it.
			return container.ExecInteractive(ctx, executor, containerID, execCommand, isTTY, stdinReader, stdout, stderr, cfg.WorkspaceFolder)
		}
		// ReconnectRecreated: old container removed, fall through to Up.
	}

	// No container exists (or old one was removed): run Up with assistant labels, then exec.
	labels := container.NewAssistantLabels(p.assistantName, folderSet)

	upOpts := container.UpOptions{
		WorkspaceFolder:      p.workspaceFolder,
		Config:               cfg,
		ConfigPath:           cfgPath,
		AdditionalFolders:    p.additionalFolders,
		AdditionalFolderBase: "/workspace",
		HomeDir:              p.homeDir,
		Network:              rt.DefaultNetwork(),
		AllowedHosts:         p.allowedHosts,
		ResourceLimits:       limits,
		RuntimeName:          rt.Name,
		Labels:               labels,
	}

	result, err := container.Up(ctx, executor, upOpts, stderr)
	if err != nil {
		return fmt.Errorf("assistant: up: %w", err)
	}

	if result.Outcome == container.OutcomeError {
		return fmt.Errorf("assistant: up: %s", result.Message)
	}

	return container.ExecInteractive(ctx, executor, result.ContainerID, execCommand, isTTY, stdinReader, stdout, stderr, cfg.WorkspaceFolder)
}

// ensureHostMountDirs pre-creates the host source directories declared by the
// assistant's host mounts. The directories are created with mode 0o700 (user
// only) because they hold live host state such as OAuth refresh tokens.
// MkdirAll is a no-op for existing directories, so returning users are
// unaffected. Assistants that declare no host mounts are a no-op entirely.
func ensureHostMountDirs(homeDir, name string) error {
	for _, source := range assistant.HostMountSources(homeDir, name) {
		if err := os.MkdirAll(source, 0o700); err != nil {
			return fmt.Errorf("pre-create host mount dir %s: %w", source, err)
		}
	}
	return nil
}
