package update

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/woditschka/confine-ai/internal/assistant"
	"github.com/woditschka/confine-ai/internal/container"
)

// AssistantOptions is the input set for RunAssistant. Production code constructs one
// with a real container.Executor and assistant.ImageBuilder (the same underlying
// CLIExecutor in practice); tests supply fakes.
type AssistantOptions struct {
	// HomeDir is the user's home directory. The assistant is expected to live at
	// <HomeDir>/.confine-ai/assistants/<AssistantName>/Dockerfile.
	HomeDir string
	// AssistantName is the assistant directory name (e.g., "claude").
	AssistantName string
	// DryRun, when true, emits a "would rebuild ..." line to Stdout and
	// returns ActionWouldUpdate without invoking the runtime.
	DryRun bool
	// Stdout receives the per-target summary output (and the dry-run line).
	Stdout io.Writer
	// Stderr receives warnings and rebuild progress.
	Stderr io.Writer
	// Executor is the container runtime driver used to drop stale containers
	// after a successful rebuild. Required unless DryRun is true.
	Executor container.Executor
	// Builder rebuilds the assistant image. Required unless DryRun is true. In
	// production this is the same underlying CLIExecutor as Executor.
	Builder assistant.ImageBuilder
	// BaseDockerfile, when non-nil, triggers an EnsureBaseImage precheck
	// before the assistant rebuild: if `localhost/confine-ai-base:latest` is not
	// present in the local image store, it is built from these bytes. This
	// prevents podman's strict short-name resolution from trying to pull
	// the local-only base image from a literal `localhost` registry.
	// Production wires the bytes from ResolveBaseDockerfile; tests that do
	// not exercise the precheck may leave the field nil.
	BaseDockerfile []byte
	// Client is the shared outbound HTTP client used by the REQ-AS-008
	// version gate's upstream probes. When nil, the gate is skipped for
	// every assistant (even claude) and the existing REQ-AS-008 rebuild
	// path runs unchanged — this preserves byte-identical behaviour for
	// tests that do not exercise the gate. Production wires a single
	// Client shared with the base-update path.
	Client *Client
}

// RunAssistant orchestrates a full `confine-ai update <assistant>` run: resolve the
// assistant's user-owned Dockerfile, rebuild the image with `--no-cache`, then
// drop stale containers for this assistant so subsequent `confine-ai <assistant>`
// invocations pick up the fresh image. It returns a TargetResult that
// carries the exit code and any error message. The rebuild deliberately
// omits `--pull` because the assistant's FROM image is
// `localhost/confine-ai-base:latest`, which has no remote source.
//
// Like RunBase, RunAssistant never returns an error. All failure modes are
// carried through TargetResult.ExitCode and TargetResult.Error so the CLI
// layer can uniformly aggregate results across multiple targets.
func RunAssistant(ctx context.Context, opts AssistantOptions) TargetResult {
	result := TargetResult{Target: opts.AssistantName}

	assistantDir := assistant.Dir(opts.HomeDir, opts.AssistantName)
	info, err := os.Stat(assistantDir)
	if err != nil || !info.IsDir() {
		return failed(result, 1,
			fmt.Sprintf("assistant %q not found at %s; run 'confine-ai init %s' to create it",
				opts.AssistantName, assistantDir, opts.AssistantName))
	}

	dockerfile := assistant.DockerfilePath(opts.HomeDir, opts.AssistantName)
	if _, err := os.Stat(dockerfile); err != nil {
		return failed(result, 1,
			fmt.Sprintf("assistant %q missing Dockerfile at %s", opts.AssistantName, dockerfile))
	}

	// REQ-AS-008: assistant version gate. Consult the per-assistant probe
	// spec registry; assistants without a registered spec fall through
	// unchanged and rebuild unconditionally.
	// The gate requires both an Executor (for the installed-version read)
	// and a Client (for the upstream probe); when either is absent, the
	// gate is skipped entirely. The helper catches every probe error
	// internally and emits a single stderr warning; RunAssistant's "never
	// return error" contract is preserved.
	if spec := lookupProbeSpec(opts.AssistantName); spec != nil && opts.Executor != nil && opts.Client != nil {
		probe := buildProbe(opts.AssistantName, spec, opts.Client)
		if gated, handled := runAssistantGate(ctx, probe, gateInputs{
			AssistantName: opts.AssistantName,
			Executor:      opts.Executor,
			DryRun:        opts.DryRun,
			Stdout:        opts.Stdout,
			Stderr:        opts.Stderr,
		}); handled {
			return gated
		}
	}

	if opts.DryRun {
		fmt.Fprintf(opts.Stdout, "would rebuild %s without cache\n", opts.AssistantName)
		result.Action = ActionWouldUpdate
		result.ExitCode = 0
		return result
	}

	// Ensure the base image exists locally before rebuilding the assistant.
	// The assistant's FROM line references `localhost/confine-ai-base:latest`; if
	// that image is missing, podman would otherwise try to pull it from a
	// literal `localhost` registry and fail. EnsureBaseImage emits its own
	// "not found, building..." line when it builds.
	if opts.BaseDockerfile != nil {
		if err := assistant.EnsureBaseImage(ctx, opts.Builder, opts.BaseDockerfile, opts.Stderr); err != nil {
			return failed(result, 1, fmt.Sprintf("ensure base image: %v", err))
		}
	}

	hint := newDiskHintWriter(opts.Stderr)
	if err := assistant.BuildAssistantImage(ctx, opts.Builder, opts.HomeDir, opts.AssistantName, assistant.BuildOptions{NoCache: true}, hint); err != nil {
		if hint.Tripped() {
			emitDiskHint(opts.Stderr)
		}
		return failed(result, 1, err.Error())
	}

	if err := container.RemoveContainersByAssistant(ctx, opts.Executor, opts.AssistantName, opts.Stderr); err != nil {
		fmt.Fprintf(opts.Stderr, "confine-ai update: warning: drop stale containers for %s: %v\n",
			opts.AssistantName, err)
	}

	result.Action = ActionUpdated
	result.ExitCode = 0
	return result
}
