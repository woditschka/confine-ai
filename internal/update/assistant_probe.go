// Assistant update version gate (REQ-AS-008).
//
// This file implements a pure optimization layer on top of REQ-AS-008's
// assistant rebuild path. For assistants that have a registered probe spec,
// RunAssistant consults the probe before dispatching the unconditional
// `--no-cache` rebuild. When the installed version already matches the
// upstream release, the rebuild is skipped and the target's outcome is
// recorded as ActionUnchanged. Every probe failure degrades to a single
// stderr warning and falls through to the REQ-AS-008 rebuild path,
// preserving RunAssistant's "never return error" contract.
//
// Design summary:
//
//   - Installed version comes from the image itself: a one-shot
//     `podman run --rm --network=none --entrypoint "" <tag> <cmd>` via the
//     existing container.Executor. No sidecar file, no Dockerfile ARG.
//     `--network=none` is a hard requirement — the installed-version probe
//     must not issue outbound traffic from inside the image.
//   - Upstream version comes from the package source (npm registry, Go
//     module proxy, etc.) via the shared *update.Client under the outbound
//     HTTP trust boundary
//     (docs/adr/2026-04-12-outbound-http-trust-boundary.md).
//   - Comparison is a strict string equality check. No semantic version
//     ordering: "installed newer than upstream" is a mismatch and triggers
//     a rebuild. This matches the PRD explicitly.
//   - Per-assistant registry is a package-level map of probeSpec values,
//     seeded in init() and read-only at runtime. Adding a new assistant
//     is a single registry entry — no new types required.
//     Assistants without a registered spec rebuild unconditionally.

package update

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/woditschka/confine-ai/internal/assistant"
	"github.com/woditschka/confine-ai/internal/container"
)

// upstreamProber reads the latest version from a package source. Both
// NpmLatestUpstream and GoProxyUpstream satisfy this interface.
type upstreamProber interface {
	Probe(ctx context.Context) (string, error)
}

// probeSpec describes how to probe a specific assistant's version. The
// registry stores one spec per gated assistant; assistants without a spec
// rebuild unconditionally. Adding a new assistant is a single registry
// entry: a version command and an upstream factory.
type probeSpec struct {
	// versionCmd is the command run inside the image to read the installed
	// version (e.g., []string{"claude", "--version"}).
	versionCmd []string
	// newUpstream constructs the upstream version adapter backed by the
	// shared Client. Called once per RunAssistant invocation via
	// buildProbe.
	newUpstream func(client *Client) upstreamProber
}

// Package-level constants for each assistant's upstream identity. Keeping
// them together mirrors GoDLURL / CorrettoBaseURL so a reviewer can audit
// every authoritative upstream in one place.
const (
	claudeCodePackage = "@anthropic-ai/claude-code"
	copilotPackage    = "@github/copilot"
	opencodeRepo      = "opencode-ai/opencode"
)

// Package-level base URL variables, one per upstream adapter. They default
// to the canonical registry URLs and are package-level variables (not
// consts) so the RunAssistant integration tests can temporarily swap in an
// httptest server URL without exposing new AssistantOptions fields. Every
// override is always restored in a t.Cleanup to keep tests hermetic.
var (
	claudeCodeNpmBaseURL  = NpmRegistryURL
	copilotNpmBaseURL     = NpmRegistryURL
	opencodeGitHubBaseURL = GitHubAPIURL
)

// assistantProbeSpecs is the per-assistant registry consulted by the
// assistant update orchestrator. It is seeded in init() and is read-only
// at runtime, making concurrent reads safe without synchronization.
// Adding a probe for a new assistant is a single map entry.
var assistantProbeSpecs = map[string]probeSpec{}

func init() {
	assistantProbeSpecs["claude"] = probeSpec{
		versionCmd: []string{"claude", "--version"},
		newUpstream: func(c *Client) upstreamProber {
			return NewNpmLatestUpstream(c, claudeCodePackage, claudeCodeNpmBaseURL)
		},
	}
	assistantProbeSpecs["copilot"] = probeSpec{
		versionCmd: []string{"copilot", "--version"},
		newUpstream: func(c *Client) upstreamProber {
			return NewNpmLatestUpstream(c, copilotPackage, copilotNpmBaseURL)
		},
	}
	assistantProbeSpecs["opencode"] = probeSpec{
		versionCmd: []string{"opencode", "--version"},
		newUpstream: func(c *Client) upstreamProber {
			return NewGitHubReleaseUpstream(c, opencodeRepo, opencodeGitHubBaseURL)
		},
	}
}

// lookupProbeSpec returns the spec registered for name, or nil when the
// assistant has no registered spec.
func lookupProbeSpec(name string) *probeSpec {
	spec, ok := assistantProbeSpecs[name]
	if !ok {
		return nil
	}
	return &spec
}

// HasAssistantProbe reports whether the named assistant has a registered version
// probe. The CLI wiring uses this to decide whether a dry-run invocation
// needs to construct an Executor and Client for the gate: assistants without a
// probe keep the pre-REQ-AS-008 dry-run code path and skip runtime
// construction entirely.
func HasAssistantProbe(name string) bool {
	_, ok := assistantProbeSpecs[name]
	return ok
}

// configuredProbe is the single AssistantVersionProbe implementation. It
// composes the installed-version probe (executor-backed, same for every
// assistant) with a per-assistant upstream adapter constructed from the
// spec's factory.
type configuredProbe struct {
	name       string
	versionCmd []string
	upstream   upstreamProber
}

// buildProbe constructs a ready-to-use configuredProbe from a spec and
// the shared Client. Called by RunAssistant after looking up the spec.
func buildProbe(name string, spec *probeSpec, client *Client) *configuredProbe {
	return &configuredProbe{
		name:       name,
		versionCmd: spec.versionCmd,
		upstream:   spec.newUpstream(client),
	}
}

// Probe reads the installed version from the local image via a one-shot
// `podman run`, then reads the upstream latest version from the package
// source. The returned tuple is ("installed", "upstream", nil) on success
// or ("", "", err) on any failure. The caller treats every error
// identically: emit a stderr warning and fall through to rebuild.
func (p *configuredProbe) Probe(ctx context.Context, exec container.Executor) (string, string, error) {
	tag := assistant.AssistantImageTag(p.name)
	installed, err := probeInstalledVersion(ctx, exec, tag, p.versionCmd)
	if err != nil {
		return "", "", fmt.Errorf("installed version: %w", err)
	}
	upstream, err := p.upstream.Probe(ctx)
	if err != nil {
		return "", "", fmt.Errorf("upstream version: %w", err)
	}
	return installed, upstream, nil
}

// AssistantVersionProbe is the narrow interface the gate helper accepts.
// configuredProbe is the only production implementation; tests may supply
// fakes.
type AssistantVersionProbe interface {
	Probe(ctx context.Context, exec container.Executor) (installed string, upstream string, err error)
}

// probeInstalledVersion runs `image inspect` to verify the image is present
// locally, then runs a one-shot `podman run --rm --network=none
// --entrypoint "" <tag> <cmd...>` to capture the version output. The image
// inspect step exists only to distinguish "image missing" from "exec
// failed" in warning messages; the gate helper does not branch on the
// difference.
//
// The `--network=none` flag is a hard requirement per the design review:
// the installed-version read is a local-state operation and any outbound
// traffic from inside the image would be a bug. `--entrypoint ""` sidesteps
// any ENTRYPOINT the image defines and runs the bare command under the
// image's default user.
func probeInstalledVersion(ctx context.Context, exec container.Executor, tag string, cmd []string) (string, error) {
	if _, err := exec.Output(ctx, "image", "inspect", "--format", "{{.Id}}", tag); err != nil {
		return "", fmt.Errorf("image %s not present locally: %w", tag, err)
	}
	args := append([]string{"run", "--rm", "--network=none", "--entrypoint", "", tag}, cmd...)
	stdout, err := exec.Output(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("exec %v in %s: %w", cmd, tag, err)
	}
	version, ok := parseInstalledVersion(stdout)
	if !ok {
		return "", fmt.Errorf("unparseable version output from %s: %q", tag, strings.TrimSpace(stdout))
	}
	return version, nil
}

// versionPattern matches a semver-ish token: an optional leading `v`, at
// least two dotted integer components, and an optional `-prerelease` /
// `+build` suffix. The first match in the first non-blank line of stdout
// is returned verbatim (minus the leading `v`).
var versionPattern = regexp.MustCompile(`v?[0-9]+(?:\.[0-9]+)+(?:[-+][0-9A-Za-z.]+)?`)

// parseInstalledVersion extracts the first version-shaped token from the
// first non-blank line of s, strips an optional leading `v`, and returns
// the trimmed string. ok=false indicates no version token was found.
func parseInstalledVersion(s string) (string, bool) {
	for line := range strings.SplitSeq(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		match := versionPattern.FindString(trimmed)
		if match == "" {
			// First non-blank line had no token; try the next line in
			// case the CLI prints a banner before the version. If all
			// lines fail the regex, return false below.
			continue
		}
		// Require at least one dot to reject `42` matching the
		// "one or more components" path — we need a real version.
		if !strings.Contains(match, ".") {
			continue
		}
		return strings.TrimPrefix(match, "v"), true
	}
	return "", false
}

// gateInputs bundles the context the gate helper needs. It intentionally
// does not take an AssistantOptions value directly so the helper can be unit-
// tested without constructing a full AssistantOptions (which includes an
// ImageBuilder and paths the helper never uses).
type gateInputs struct {
	AssistantName string
	Executor      container.Executor
	DryRun        bool
	Stdout        io.Writer
	Stderr        io.Writer
}

// runAssistantGate executes the version-equality gate for the target assistant.
// It returns (result, true) when the caller should short-circuit the
// rebuild and use the returned result directly — this covers match
// (real and dry-run) and dry-run mismatch. It returns (_, false) when
// the caller should fall through to the REQ-AS-008 rebuild path — this
// covers mismatch in a real run and every probe failure.
//
// Every error surfaced by the probe is caught inside this helper and
// formatted into a single stderr warning line. The returned result is
// never an ActionFailed: RunAssistant's contract is "never return error",
// and the gate extends that contract unchanged.
func runAssistantGate(ctx context.Context, probe AssistantVersionProbe, in gateInputs) (TargetResult, bool) {
	result := TargetResult{Target: in.AssistantName}
	installed, upstream, err := probe.Probe(ctx, in.Executor)
	if err != nil {
		fmt.Fprintf(in.Stderr,
			"confine-ai update: warning: %s version gate skipped: %v\n",
			in.AssistantName, err)
		return result, false
	}

	if installed == upstream {
		fmt.Fprintf(in.Stdout, "%s already at %s\n", in.AssistantName, installed)
		result.Action = ActionUnchanged
		result.ExitCode = 0
		return result, true
	}

	// Mismatch.
	if in.DryRun {
		fmt.Fprintf(in.Stdout, "would rebuild %s (%s -> %s)\n", in.AssistantName, installed, upstream)
		result.Action = ActionWouldUpdate
		result.ExitCode = 0
		return result, true
	}
	// Real run: emit the rebuild notice and fall through so RunAssistant
	// invokes BuildAssistantImage (REQ-AS-008).
	fmt.Fprintf(in.Stdout, "rebuilding %s (%s -> %s)\n", in.AssistantName, installed, upstream)
	return result, false
}
