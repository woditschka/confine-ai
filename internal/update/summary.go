package update

import (
	"fmt"
	"strings"
)

// Action is the per-target outcome of a `confine-ai update` run.
type Action string

// Action values, ordered by reporting priority (not by severity):
// success actions first, then dry-run, then skip, then failure.
const (
	// ActionUpdated means the target's Dockerfile was rewritten and the
	// image rebuilt.
	ActionUpdated Action = "updated"
	// ActionUnchanged means the probe succeeded but nothing needed to
	// change (already latest on every group).
	ActionUnchanged Action = "unchanged"
	// ActionWouldUpdate means dry-run mode reported planned changes
	// without writing or rebuilding.
	ActionWouldUpdate Action = "would update"
	// ActionSkipped means the user declined (or non-terminal stdin
	// implicitly declined) a Java major-version bump for a managed group
	// that was otherwise up to date.
	ActionSkipped Action = "skipped"
	// ActionFailed means one or more groups failed during probe, sha256
	// fetch, rewrite, rebuild, or container drop.
	ActionFailed Action = "failed"
)

// GroupDelta describes one managed group's before/after version. The summary
// formatter prints one line per delta under the target's header.
type GroupDelta struct {
	// Tool is the managed group's tool= value (e.g., "go", "java").
	Tool string
	// Distribution is the group's distribution= value, if any.
	Distribution string
	// OldVersion is the version string previously pinned in the
	// Dockerfile.
	OldVersion string
	// NewVersion is the version string the upstream reported. Equal to
	// OldVersion when nothing changed (the orchestrator still records the
	// delta so the summary can show "1.27.1 (unchanged)").
	NewVersion string
	// Skipped reports whether this specific group was skipped even though
	// the target as a whole continued (used for partial Java skips).
	Skipped bool
}

// TargetResult is the per-target outcome returned by RunBase and RunAssistant.
// The orchestrator's CLI dispatch collects TargetResult values for every
// requested target and passes them to Aggregate.
type TargetResult struct {
	// Target is the human name of the target (e.g., "base", "claude").
	Target string
	// Action is the classified outcome.
	Action Action
	// ExitCode is the REQ-AS-008 exit code for this target alone:
	//   0 success, unchanged, skipped, or would update
	//   1 generic error (missing file, multi-stage, rewrite write, rebuild)
	//   2 upstream probe failure
	//   3 sha256 fetch or verification failure
	//   4 user aborted Java major-version jump
	ExitCode int
	// Error is an optional human-readable error message attached when
	// Action is Failed.
	Error string
	// GroupDeltas enumerates the per-managed-group changes for base-style
	// targets. Nil for assistant rebuild targets, which have no deltas.
	GroupDeltas []GroupDelta
}

// Aggregate computes the overall exit code and a human-readable summary for
// a slice of TargetResult values. The exit code is the highest-severity code
// observed in the slice per the precedence documented on TargetResult. The
// summary is a readable multi-line report the CLI prints to stdout.
//
// An empty slice yields exit 0 and an empty summary.
func Aggregate(results []TargetResult) (int, string) {
	exit := 0
	var b strings.Builder
	for _, r := range results {
		if r.ExitCode > exit {
			exit = r.ExitCode
		}
		writeTargetLine(&b, r)
	}
	return exit, b.String()
}

// FormatResult renders a single TargetResult as its header line plus any
// per-group delta lines. The CLI dispatch loop uses this to emit each
// target's summary immediately after it completes, so inline stdout from
// per-target probes (e.g. "claude already at X") stays adjacent to its own
// summary line instead of interleaving with an end-of-run batched report.
func FormatResult(r TargetResult) string {
	var b strings.Builder
	writeTargetLine(&b, r)
	return b.String()
}

// ExitCode returns the highest-severity exit code across results per the
// precedence documented on TargetResult. It mirrors the first return value
// of Aggregate for callers that emit per-target summaries inline and only
// need the overall exit code at the end.
func ExitCode(results []TargetResult) int {
	exit := 0
	for _, r := range results {
		if r.ExitCode > exit {
			exit = r.ExitCode
		}
	}
	return exit
}

// writeTargetLine formats a single TargetResult as a header line plus any
// per-group delta lines indented below it. The format is not a stable API;
// tests assert substrings rather than the exact line shape.
func writeTargetLine(b *strings.Builder, r TargetResult) {
	fmt.Fprintf(b, "%s: %s", r.Target, r.Action)
	if r.Action == ActionFailed && r.ExitCode != 0 {
		fmt.Fprintf(b, " (exit %d)", r.ExitCode)
	}
	b.WriteString("\n")
	if r.Error != "" {
		fmt.Fprintf(b, "  error: %s\n", r.Error)
	}
	for _, d := range r.GroupDeltas {
		name := d.Tool
		if d.Distribution != "" {
			name = fmt.Sprintf("%s/%s", d.Tool, d.Distribution)
		}
		switch {
		case d.Skipped:
			fmt.Fprintf(b, "  %s: skipped (was %s)\n", name, d.OldVersion)
		case d.OldVersion == d.NewVersion:
			fmt.Fprintf(b, "  %s: %s (unchanged)\n", name, d.NewVersion)
		default:
			fmt.Fprintf(b, "  %s: %s -> %s\n", name, d.OldVersion, d.NewVersion)
		}
	}
}
