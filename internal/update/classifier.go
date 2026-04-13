package update

import (
	"errors"
	"fmt"
	"sort"
)

// Sentinel errors returned by Classify. Callers use errors.Is to branch on the
// specific failure mode so the top-level orchestrator can emit the correct
// exit code (always 1 for these two per REQ-AS-008 AC-11 and AC-12).
var (
	// ErrUnknownDistribution reports a tool=java marker whose distribution=
	// field is set to something other than corretto. REQ-AS-008 constrains
	// the set to {corretto} at launch; any other value is a hard error.
	ErrUnknownDistribution = errors.New("update: unknown java distribution")

	// ErrMissingDistribution reports a tool=java kind=version marker that
	// has no distribution= field. Java identity comes from tool=java and
	// the specific distribution comes from the marker's distribution=
	// field; a missing field is a hard error.
	ErrMissingDistribution = errors.New("update: missing java distribution")
)

// UpdateTarget is the classifier's per-group output. It carries the inputs
// every probe and rewrite call needs without exposing the raw ManagedGroup
// pointer: the group index is retained for the rewriter.
type UpdateTarget struct {
	// Tool is the marker's tool= value. Current values: "go", "java".
	Tool string
	// Distribution is the marker's distribution= value. Empty for tools
	// that do not require one (e.g., go).
	Distribution string
	// CurrentVersion is the existing ARG value on the managed ARG line.
	// Used by the orchestrator to decide "already latest" and by the Java
	// adapter to compute the current major.
	CurrentVersion string
	// Arches is the sorted list of arch= values present in the managed
	// group's sha256 markers. The probe adapter fetches a sha256 per
	// arch; the rewriter writes one per arch.
	Arches []string
	// PromptOnMajorJump reports whether the orchestrator must prompt the
	// user on a major-version bump. True only for tool=java (REQ-AS-008
	// AC-6 through AC-10); false for tool=go (AC-1 "applies silently").
	PromptOnMajorJump bool
	// Group points back into ParsedDockerfile.Groups so the rewriter can
	// locate the specific line indices to rewrite.
	Group *ManagedGroup
}

// Classify walks a ParsedDockerfile and returns the UpdateTarget slice in the
// order the groups appear. Base-image groups are observed but excluded from
// the output because REQ-AS-008 does not rewrite the FROM line.
//
// Classify returns ErrUnknownDistribution (wrapped with a message naming the
// offending value) or ErrMissingDistribution the first time it encounters
// such a condition. The orchestrator exits with code 1 on either error and
// writes nothing to disk.
func Classify(pd *ParsedDockerfile) ([]UpdateTarget, error) {
	var out []UpdateTarget
	for i := range pd.Groups {
		g := &pd.Groups[i]
		switch g.Tool {
		case "go":
			out = append(out, UpdateTarget{
				Tool:              "go",
				CurrentVersion:    argValue(pd, g.VersionLineIdx),
				Arches:            sortedArches(g.Sha256LinesByArch),
				PromptOnMajorJump: false,
				Group:             g,
			})
		case "java":
			if g.Distribution == "" {
				return nil, fmt.Errorf("tool=java marker on line %d has no distribution= field: %w",
					g.VersionMarkerLine, ErrMissingDistribution)
			}
			if g.Distribution != "corretto" {
				return nil, fmt.Errorf("%q on line %d (only corretto is supported): %w",
					g.Distribution, g.VersionMarkerLine, ErrUnknownDistribution)
			}
			out = append(out, UpdateTarget{
				Tool:              "java",
				Distribution:      g.Distribution,
				CurrentVersion:    argValue(pd, g.VersionLineIdx),
				Arches:            sortedArches(g.Sha256LinesByArch),
				PromptOnMajorJump: true,
				Group:             g,
			})
		case "base-image":
			// Observed but never rewritten. REQ-AS-008 never touches
			// the FROM line.
			continue
		default:
			// Unknown tool: the parser accepted the marker for forward
			// compatibility. The classifier silently skips it — a future
			// requirement can add handling without breaking old files.
			continue
		}
	}
	return out, nil
}

// argValue returns the value string of the managed ARG line at idx, decoded
// from line.Raw via the parser-stored offsets.
func argValue(pd *ParsedDockerfile, idx int) string {
	if idx < 0 || idx >= len(pd.Lines) {
		return ""
	}
	line := pd.Lines[idx]
	return string(line.Raw[line.ArgValueStart:line.ArgValueEnd])
}

// sortedArches returns the keys of m in lexicographic order. The order is
// fixed so tests and rewrite output are deterministic.
func sortedArches(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
