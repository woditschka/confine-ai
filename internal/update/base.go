package update

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/woditschka/confine-ai/internal/assistant"
)

// Prober abstracts the upstream probe layer so RunBase can be tested without
// real network calls. Production wires it up with Go and Corretto adapters
// backed by a single Client; tests provide a canned implementation.
type Prober interface {
	// ProbeGo returns the current stable Go version and per-arch sha256
	// values for the requested arches.
	ProbeGo(ctx context.Context, arches []string) (Resolved, error)
	// ProbeCorretto returns the latest Corretto version for the major
	// implied by currentVersion and the per-arch sha256 values for the
	// requested arches.
	ProbeCorretto(ctx context.Context, currentVersion string, arches []string) (Resolved, error)
}

// RealProber wires GoUpstream and CorrettoUpstream behind the Prober
// interface. Production code constructs one per RunBase invocation sharing a
// single Client.
type RealProber struct {
	Go       *GoUpstream
	Corretto *CorrettoUpstream
}

// ProbeGo delegates to the underlying GoUpstream.
func (r *RealProber) ProbeGo(ctx context.Context, arches []string) (Resolved, error) {
	return r.Go.Probe(ctx, arches)
}

// ProbeCorretto delegates to the underlying CorrettoUpstream.
func (r *RealProber) ProbeCorretto(ctx context.Context, currentVersion string, arches []string) (Resolved, error) {
	return r.Corretto.Probe(ctx, currentVersion, arches)
}

// NewRealProber constructs a RealProber whose adapters point at the
// canonical upstream URLs documented in docs/adr/2026-04-12-outbound-http-trust-boundary.md.
func NewRealProber(client *Client) *RealProber {
	return &RealProber{
		Go:       NewGoUpstream(client, GoDLURL),
		Corretto: NewCorrettoUpstream(client, CorrettoBaseURL),
	}
}

// BaseOptions is the input set for RunBase. Test code constructs one with a
// stub Prober and stub Executor; production code constructs one with
// NewRealProber(NewClient(version)) and the real CLI executor.
type BaseOptions struct {
	// HomeDir is the user's home directory. ~/.confine-ai/base/Dockerfile is
	// resolved relative to it.
	HomeDir string
	// DryRun, when true, runs parse/classify/probe but never writes the
	// Dockerfile and never rebuilds the image. The return value still
	// reports planned deltas and the exit code still reflects probe /
	// sha256 failures per REQ-AS-008.
	DryRun bool
	// AutoYes, when true, accepts Java major-version bumps without
	// prompting. Required when IsTTY is false to distinguish "intentional
	// automation" from "accidental non-interactive pipeline".
	AutoYes bool
	// IsTTY reports whether stdin is a terminal. When false and AutoYes
	// is also false, Java major-version bumps are implicitly skipped.
	IsTTY bool
	// Stdin is the reader used to prompt for Java major-version
	// confirmations.
	Stdin io.Reader
	// Stdout receives the per-group summary output.
	Stdout io.Writer
	// Stderr receives warnings (unmarked ARG, orphan marker) and
	// rebuild progress.
	Stderr io.Writer
	// Prober probes upstream metadata. Required.
	Prober Prober
	// Executor is the container runtime driver used for the base rebuild.
	// Required unless DryRun is true.
	Executor assistant.ImageBuilder
	// Now is an optional clock for deterministic timestamps in output. If
	// nil, time.Now is used.
	Now func() time.Time
}

// RunBase orchestrates a full `confine-ai update base` run: parse the user's
// base Dockerfile, classify managed groups, probe upstreams, prompt on Java
// major jumps, rewrite atomically, and rebuild the base image. It returns a
// TargetResult that carries the exit code, per-group deltas, and any error
// message.
//
// RunBase never returns an error. All failure modes are carried through
// TargetResult.ExitCode and TargetResult.Error so the CLI layer can uniformly
// aggregate results across multiple targets.
func RunBase(ctx context.Context, opts BaseOptions) TargetResult {
	result := TargetResult{Target: "base"}

	// 1. Read the user's base Dockerfile.
	path := assistant.BaseDockerfilePath(opts.HomeDir)
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return failed(result, 1,
				fmt.Sprintf("base Dockerfile not found at %s; run 'confine-ai init' to create it", path))
		}
		return failed(result, 1, fmt.Sprintf("read %s: %v", path, err))
	}

	// 2. Parse.
	pd := ParseDockerfile(content)
	if pd.MultiStage {
		return failed(result, 1,
			"base Dockerfile has multiple FROM stages; confine-ai update only supports single-stage files")
	}
	// Emit warnings to stderr; warnings do not fail the run.
	for _, w := range pd.Warnings {
		fmt.Fprintf(opts.Stderr, "confine-ai update: warning: line %d: %s\n", w.LineNumber, w.Message)
	}

	// 3. Classify. Unknown / missing distribution is a hard error.
	targets, err := Classify(pd)
	if err != nil {
		return failed(result, 1, err.Error())
	}
	if len(targets) == 0 {
		// No managed groups means nothing to update. This is success: the
		// file is already in its desired state by definition.
		result.Action = ActionUnchanged
		result.ExitCode = 0
		return result
	}

	// 4. Probe upstream for each classified target. Track resolved values
	// and per-target status. Any non-skipped probe failure halts the run
	// atomically. probeOutcome is declared at package scope so helper
	// functions can accept it by value.
	outcomes := make([]probeOutcome, 0, len(targets))

	for _, tgt := range targets {
		var resolved Resolved
		var perr error
		switch tgt.Tool {
		case "go":
			resolved, perr = opts.Prober.ProbeGo(ctx, tgt.Arches)
		case "java":
			resolved, perr = opts.Prober.ProbeCorretto(ctx, tgt.CurrentVersion, tgt.Arches)
		default:
			// Classifier filtered unknown tools; defensive skip.
			continue
		}
		if perr != nil {
			outcomes = append(outcomes, probeOutcome{
				target:   tgt,
				probeErr: perr,
				exitCode: classifyProbeError(perr),
			})
			continue
		}
		outcomes = append(outcomes, probeOutcome{target: tgt, resolved: resolved})
	}

	// 5. For Java targets, detect major-version jumps and prompt.
	for i := range outcomes {
		o := &outcomes[i]
		if o.probeErr != nil || o.target.Tool != "java" {
			continue
		}
		oldMajor, newMajor, err := MajorVersions(o.target.CurrentVersion, o.resolved.Version)
		if err != nil {
			// Treat as a probe failure: the version strings didn't parse.
			o.probeErr = fmt.Errorf("parse versions: %w", err)
			o.exitCode = 2
			continue
		}
		if newMajor <= oldMajor {
			continue // no jump
		}
		decision := decideJavaMajorJump(opts, o.target, oldMajor, newMajor)
		switch decision {
		case jumpDecisionProceed:
			// Keep resolved as-is.
		case jumpDecisionSkip:
			o.skipped = true
		case jumpDecisionAbort:
			// Abort the entire run. Return exit 4 with no writes.
			return failed(result, 4,
				fmt.Sprintf("user aborted Java major-version jump %d -> %d", oldMajor, newMajor))
		}
	}

	// 6. If ANY non-skipped target failed, halt atomically.
	worstExit := 0
	for _, o := range outcomes {
		if o.skipped || o.probeErr == nil {
			continue
		}
		if o.exitCode > worstExit {
			worstExit = o.exitCode
		}
	}
	if worstExit > 0 {
		// Compose a combined error message.
		var errs []string
		for _, o := range outcomes {
			if o.probeErr != nil && !o.skipped {
				errs = append(errs, fmt.Sprintf("%s: %v", o.target.Tool, o.probeErr))
			}
		}
		return failed(result, worstExit, strings.Join(errs, "; "))
	}

	// 7. Build Delta and GroupDeltas reporting.
	delta := Delta{}
	anyChange := false
	for _, o := range outcomes {
		if o.skipped || o.probeErr != nil {
			// Record the skipped state in GroupDeltas so the summary
			// shows the user what was not touched.
			if o.skipped {
				result.GroupDeltas = append(result.GroupDeltas, GroupDelta{
					Tool:         o.target.Tool,
					Distribution: o.target.Distribution,
					OldVersion:   o.target.CurrentVersion,
					NewVersion:   o.target.CurrentVersion,
					Skipped:      true,
				})
			}
			continue
		}
		result.GroupDeltas = append(result.GroupDeltas, GroupDelta{
			Tool:         o.target.Tool,
			Distribution: o.target.Distribution,
			OldVersion:   o.target.CurrentVersion,
			NewVersion:   o.resolved.Version,
		})
		if versionOrShasChanged(o, pd) {
			delta[o.target.Group] = o.resolved
			anyChange = true
		}
	}

	// 8. Dry-run short-circuit: never write, never rebuild. Exit code is
	// driven by the probe outcomes (0 if all succeeded or were skipped).
	if opts.DryRun {
		if anyChange {
			result.Action = ActionWouldUpdate
		} else {
			result.Action = ActionUnchanged
		}
		result.ExitCode = 0
		return result
	}

	// 9. If nothing to change upstream, still ensure the base image exists
	// in the local store. This covers the case where the user deleted the
	// image (or never built it) but the Dockerfile is already at the
	// latest pinned versions: without this fallback, a subsequent
	// `confine-ai update <assistant>` or `confine-ai <assistant>` would fail because
	// podman can't pull `localhost/confine-ai-base:latest` from a remote.
	if !anyChange {
		if opts.Executor != nil {
			if err := assistant.EnsureBaseImage(ctx, opts.Executor, content, opts.Stderr); err != nil {
				return failed(result, 1, fmt.Sprintf("ensure base image: %v", err))
			}
		}
		result.Action = ActionUnchanged
		result.ExitCode = 0
		return result
	}

	// 10. Rewrite and write atomically.
	newBytes := Rewrite(pd, delta)
	if err := WriteAtomic(path, newBytes, 0); err != nil {
		return failed(result, 1, fmt.Sprintf("write %s: %v", path, err))
	}

	// 11. Rebuild the base image with --pull.
	if opts.Executor != nil {
		hint := newDiskHintWriter(opts.Stderr)
		if err := assistant.BuildBaseImage(ctx, opts.Executor, newBytes, nil, assistant.BuildOptions{Pull: true}, hint); err != nil {
			if hint.Tripped() {
				emitDiskHint(opts.Stderr)
			}
			return failed(result, 1, fmt.Sprintf("rebuild base image: %v", err))
		}
	}

	result.Action = ActionUpdated
	result.ExitCode = 0
	return result
}

// versionOrShasChanged reports whether the probe-returned version or any of
// its per-arch sha256 values differ from what pd currently holds for this
// group. Used to compute "already latest" without issuing a write.
func versionOrShasChanged(o probeOutcome, pd *ParsedDockerfile) bool {
	g := o.target.Group
	if g == nil {
		return false
	}
	currentVersion := argValue(pd, g.VersionLineIdx)
	if currentVersion != o.resolved.Version {
		return true
	}
	for arch, idx := range g.Sha256LinesByArch {
		want, ok := o.resolved.Sha256[arch]
		if !ok {
			continue
		}
		if argValue(pd, idx) != want {
			return true
		}
	}
	return false
}

// probeOutcome is re-declared at package scope so versionOrShasChanged can
// accept it. It mirrors the inline declaration in RunBase.
type probeOutcome struct {
	target   UpdateTarget
	resolved Resolved
	skipped  bool
	probeErr error
	exitCode int
}

// classifyProbeError maps a probe error to a REQ-AS-008 exit code:
//
//	2 = probe failure (upstream not found, HTTP failure, parse failure)
//	3 = sha256 verification or fetch failure
func classifyProbeError(err error) int {
	if errors.Is(err, ErrInvalidSha256) {
		return 3
	}
	// Treat upstream-not-found and every other transport/parse error as
	// probe failures (exit 2).
	return 2
}

// jumpDecision enumerates the three valid responses to a Java major-version
// jump prompt.
type jumpDecision int

const (
	jumpDecisionProceed jumpDecision = iota
	jumpDecisionSkip
	jumpDecisionAbort
)

// decideJavaMajorJump resolves the user's choice for a Java major-version
// jump. When AutoYes is set it is always "proceed". When IsTTY is false and
// AutoYes is not set it is implicitly "skip" (AC-9). Otherwise it prompts on
// opts.Stderr and reads a single line from opts.Stdin.
func decideJavaMajorJump(opts BaseOptions, tgt UpdateTarget, oldMajor, newMajor int) jumpDecision {
	if opts.AutoYes {
		return jumpDecisionProceed
	}
	if !opts.IsTTY {
		fmt.Fprintf(opts.Stderr,
			"confine-ai update: Java %d -> %d is a major-version jump; skipping (stdin is not a terminal and --yes was not supplied)\n",
			oldMajor, newMajor)
		return jumpDecisionSkip
	}
	fmt.Fprintf(opts.Stderr,
		"confine-ai update: Java %d -> %d is a major-version jump for %s.\n"+
			"  Type 'proceed' to apply, 'skip' to leave %s unchanged, or 'abort' to halt the update: ",
		oldMajor, newMajor, tgt.Distribution, tgt.Distribution)
	if opts.Stdin == nil {
		return jumpDecisionSkip
	}
	scanner := bufio.NewScanner(opts.Stdin)
	if !scanner.Scan() {
		return jumpDecisionSkip
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	switch answer {
	case "proceed", "yes", "y":
		return jumpDecisionProceed
	case "skip", "n", "no":
		return jumpDecisionSkip
	case "abort":
		return jumpDecisionAbort
	default:
		// Unknown answer is treated as skip to minimize surprise.
		fmt.Fprintf(opts.Stderr, "confine-ai update: unrecognized answer %q; treating as skip\n", answer)
		return jumpDecisionSkip
	}
}

// failed is a small helper that fills in the failure fields of a
// TargetResult so RunBase's return sites stay concise.
func failed(r TargetResult, code int, msg string) TargetResult {
	r.Action = ActionFailed
	r.ExitCode = code
	r.Error = msg
	return r
}
