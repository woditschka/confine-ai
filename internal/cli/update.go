package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/woditschka/confine-ai/internal/assistant"
	"github.com/woditschka/confine-ai/internal/container"
	"github.com/woditschka/confine-ai/internal/update"
)

// executorFactory constructs a container.Executor on demand. Used by
// runUpdateWithExecutor so flag parsing can happen before (and independently
// of) runtime detection. The production wrapper passes a factory that calls
// newExecutor; tests inject a factory that returns a capturing fake.
type executorFactory func() (container.Executor, error)

// proberFactory constructs an update.Prober on demand. Production code wires
// this up with update.NewRealProber(update.NewClient(version)); tests inject
// a fake Prober so CLI routing can be exercised without real network calls.
type proberFactory func() update.Prober

// RunUpdate is the production wrapper for the `confine-ai update` subcommand.
// It probes the host runtime and constructs a real update.Prober backed by
// the outbound HTTP client, then delegates to runUpdateWithExecutor which
// holds all the dispatch logic. version is the compiled-in confine-ai version
// forwarded from main.go's ldflags. baseDockerfile is the embedded base
// Dockerfile seed.
func RunUpdate(ctx context.Context, stdout, stderr io.Writer, dockerPath string, args []string, version string, baseDockerfile []byte) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("update: home directory: %w", err)
	}
	execFactory := func() (container.Executor, error) {
		e, _, err := newExecutor(dockerPath)
		return e, err
	}
	probeFactory := func() update.Prober {
		return update.NewRealProber(update.NewClient(version))
	}
	return runUpdateWithExecutor(ctx, stdout, stderr, execFactory, probeFactory, homeDir, args, version, baseDockerfile)
}

// runUpdateWithExecutor is the injectable core of RunUpdate. It parses flags,
// expands the target list (walk mode when no positional arguments are given),
// dispatches each target to update.RunBase or update.RunAssistant, and
// aggregates the per-target results into a final exit code via
// update.Aggregate. Tests substitute both factories to avoid real network
// and runtime calls.
//
// Target walk semantics (REQ-AS-008 AC-22 through AC-26):
//   - With no positional arguments: walk base first, then every assistant
//     directory under ~/.confine-ai/assistants/ that contains a Dockerfile in
//     alphabetical order.
//   - Base failure halts the walk; subsequent assistants are not attempted.
//   - An individual assistant failure does NOT halt the walk.
//   - Broken assistants (dir exists without Dockerfile) are skipped with a
//     warning and never dispatched.
//
// Explicit targets are dispatched in argument order without the broken
// assistant filter: an explicit "confine-ai update broken" fails via
// RunAssistant's Dockerfile-missing path with exit 1, which is the documented
// behavior.
func runUpdateWithExecutor(
	ctx context.Context,
	stdout, stderr io.Writer,
	newExec executorFactory,
	newProber proberFactory,
	homeDir string,
	args []string,
	version string,
	baseDockerfile []byte,
) error {
	fs := NewFlagSet("update", stderr)

	var (
		dryRun  bool
		autoYes bool
	)
	fs.BoolVar(&dryRun, "dry-run", false, "Probe upstreams and report planned changes without writing or rebuilding")
	fs.BoolVar(&autoYes, "yes", false, "Accept Java major-version bumps without prompting (base target only)")

	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: confine-ai update [flags] [target...]\n\n")
		fmt.Fprintf(stderr, "Update the base image and/or assistant images to the latest pinned versions.\n")
		fmt.Fprintf(stderr, "With no target, updates base first then every assistant in alphabetical order.\n")
		fmt.Fprintf(stderr, "Targets:\n")
		fmt.Fprintf(stderr, "  base       Update ~/.confine-ai/base/Dockerfile and rebuild localhost/confine-ai-base:latest\n")
		fmt.Fprintf(stderr, "  <assistant>    Rebuild the assistant image with --no-cache --pull\n\n")
		fmt.Fprintf(stderr, "Flags:\n")
		fs.PrintDefaults()
	}

	if err := ParseFlags(fs, args); err != nil {
		return IgnoreHelp(err)
	}

	targets, walkMode, err := resolveUpdateTargets(fs.Args(), homeDir, stderr)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if len(targets) == 0 {
		// Nothing to do (walk mode found no assistants, base is handled too).
		return nil
	}

	// Lazily construct the executor and prober so --help and target
	// resolution work even when the runtime is unavailable. The first
	// non-dry-run target that needs them triggers construction.
	var (
		executor    container.Executor
		execBuildEr error
		execOnce    bool
		prober      update.Prober
	)
	needRuntime := func() (container.Executor, error) {
		if !execOnce {
			executor, execBuildEr = newExec()
			execOnce = true
		}
		return executor, execBuildEr
	}
	needProber := func() update.Prober {
		if prober == nil {
			prober = newProber()
		}
		return prober
	}

	results := make([]update.TargetResult, 0, len(targets))
	for _, tgt := range targets {
		if tgt == "base" {
			opts := update.BaseOptions{
				HomeDir: homeDir,
				DryRun:  dryRun,
				AutoYes: autoYes,
				IsTTY:   isatty(os.Stdin),
				Stdin:   os.Stdin,
				Stdout:  stdout,
				Stderr:  stderr,
				Prober:  needProber(),
			}
			if !dryRun {
				exec, err := needRuntime()
				if err != nil {
					return fmt.Errorf("update: runtime: %w", err)
				}
				opts.Executor = exec.(assistant.ImageBuilder)
			}
			res := update.RunBase(ctx, opts)
			if _, err := fmt.Fprint(stdout, update.FormatResult(res)); err != nil {
				return fmt.Errorf("update: write summary: %w", err)
			}
			results = append(results, res)
			// Halt the walk on base failure. Explicit multi-target runs
			// also halt after a base failure by the same rule.
			if walkMode && res.ExitCode != 0 {
				break
			}
			continue
		}

		opts := update.AssistantOptions{
			HomeDir:       homeDir,
			AssistantName: tgt,
			DryRun:        dryRun,
			Stdout:        stdout,
			Stderr:        stderr,
		}
		if !dryRun {
			exec, err := needRuntime()
			if err != nil {
				return fmt.Errorf("update: runtime: %w", err)
			}
			opts.Executor = exec
			opts.Builder = exec.(assistant.ImageBuilder)
			// Resolve base Dockerfile bytes so RunAssistant can ensure the
			// local base image exists before the assistant rebuild. Falls
			// back to the embedded seed if the user copy is absent.
			resolvedBase, err := assistant.ResolveBaseDockerfile(homeDir, baseDockerfile, stderr, false)
			if err != nil {
				return fmt.Errorf("update: base dockerfile: %w", err)
			}
			opts.BaseDockerfile = resolvedBase
		} else if update.HasAssistantProbe(tgt) {
			// Dry-run with a gated assistant still needs an Executor (for
			// the read-only installed-version probe) and a Client (for
			// the npm upstream probe) so the gate can report the delta.
			// Best-effort: if runtime construction fails (no podman on
			// this host), warn and skip the gate — the assistant-update
			// path falls through to the existing "would rebuild" line.
			if exec, err := needRuntime(); err == nil {
				opts.Executor = exec
			} else {
				fmt.Fprintf(stderr, "confine-ai update: warning: version gate skipped for %s: runtime unavailable: %v\n", tgt, err)
			}
		}
		if update.HasAssistantProbe(tgt) && opts.Client == nil {
			opts.Client = update.NewClient(version)
		}
		res := update.RunAssistant(ctx, opts)
		if _, err := fmt.Fprint(stdout, update.FormatResult(res)); err != nil {
			return fmt.Errorf("update: write summary: %w", err)
		}
		results = append(results, res)
	}

	if exit := update.ExitCode(results); exit != 0 {
		return &container.ExitError{Code: exit}
	}
	return nil
}

// resolveUpdateTargets expands the user-provided positional arguments into a
// concrete ordered list of target names to run. When no arguments are given,
// it returns the walk order: "base" followed by every alphabetical assistant
// directory under ~/.confine-ai/assistants/ that contains a Dockerfile.
// Broken assistant directories (no Dockerfile) are warned about on stderr
// and skipped. The walkMode return value reports whether this expansion was
// the no-arg walk (true) or an explicit list (false) so the caller can halt
// on base failure only in walk mode.
func resolveUpdateTargets(args []string, homeDir string, stderr io.Writer) ([]string, bool, error) {
	if len(args) > 0 {
		// Validate explicit targets up-front so an attacker-supplied name
		// like "../etc" cannot escape ~/.confine-ai/assistants/ via
		// filepath.Join inside RunAssistant. "base" is the only
		// non-assistant literal accepted.
		for _, a := range args {
			if a == "base" {
				continue
			}
			if err := assistant.ValidateName(a); err != nil {
				return nil, false, fmt.Errorf("invalid target %q: %w", a, err)
			}
		}
		return slices.Clone(args), false, nil
	}

	targets := []string{"base"}
	names := assistant.ListNames(homeDir)
	for _, name := range names {
		if _, err := os.Stat(assistant.DockerfilePath(homeDir, name)); err != nil {
			fmt.Fprintf(stderr, "confine-ai update: warning: skipping assistant %q: no Dockerfile\n", name)
			continue
		}
		targets = append(targets, name)
	}
	return targets, true, nil
}
