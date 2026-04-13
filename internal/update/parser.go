// Package update implements the `confine-ai update` command: a marker-driven
// rewrite of the user-owned base Dockerfile and a cache-bust rebuild path for
// scaffolded assistants. See docs/system-design.md#update-command.
package update

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// markerSentinel is the exact prefix that identifies a confine-ai managed-line
// marker. Any comment that does not start with this exact byte sequence
// (including the single space between `#` and `confine-ai:managed`) is treated as
// an ordinary Dockerfile comment.
const markerSentinel = "# confine-ai:managed"

// LineKind classifies a parsed line for the rewrite layer.
type LineKind int

const (
	// LineKindPlain is an ordinary Dockerfile line (comment, blank, RUN,
	// ENV, etc.) that the rewriter copies byte-identically.
	LineKindPlain LineKind = iota
	// LineKindMarker is a `# confine-ai:managed ...` comment line. Copied
	// byte-identically by the rewriter.
	LineKindMarker
	// LineKindFrom is a `FROM <image>` line. Copied byte-identically by
	// the rewriter regardless of user edits.
	LineKindFrom
	// LineKindArg is an `ARG NAME=value` line classified as managed (has
	// a preceding marker). Only managed ARG lines are candidates for
	// rewrite; unmarked ARG lines are LineKindPlain.
	LineKindArg
)

// Line is one line from the input Dockerfile together with its classification
// and, for managed ARG lines, the byte offsets needed to rewrite only the
// value portion. Raw includes the line-ending bytes exactly as they appeared
// in the source (CRLF preserved, or none for an unterminated final line).
type Line struct {
	// Raw holds the line's bytes including any trailing `\n` or `\r\n`.
	// A final line without a terminator has no trailing bytes.
	Raw []byte
	// Kind is the line's classification.
	Kind LineKind
	// Number is the 1-indexed line number for diagnostics.
	Number int
	// Marker points to the parsed marker for LineKindMarker lines. Nil
	// otherwise.
	Marker *Marker
	// ArgName is the ARG name for LineKindArg lines. Empty otherwise.
	ArgName string
	// ArgValueStart is the byte offset (within Raw) where the ARG value
	// begins — the byte immediately after the first `=`. ArgValueEnd is
	// the exclusive end of the value, positioned before any trailing
	// whitespace, comment, or line-ending bytes. Set only for
	// LineKindArg.
	ArgValueStart int
	ArgValueEnd   int
}

// Marker is a parsed `# confine-ai:managed` marker.
type Marker struct {
	// Tool is the `tool=` field value (required): `go`, `java`, `base-image`.
	Tool string
	// Kind is the `kind=` field value (required): `version`, `sha256`, `image`.
	Kind string
	// Arch is the `arch=` field value (required on `kind=sha256`).
	Arch string
	// Distribution is the `distribution=` field value (required on
	// `tool=java`).
	Distribution string
	// ExtraTokens holds unrecognized `key=value` tokens preserved for
	// forward compatibility per the classification ADR.
	ExtraTokens []string
	// LineNumber is the 1-indexed source line number of the marker.
	LineNumber int
}

// ManagedGroup is a `kind=version` marker paired with its `kind=sha256`
// markers that share the same `tool` (and `distribution` for Java) value.
// Indices refer to ParsedDockerfile.Lines.
type ManagedGroup struct {
	// Tool is the group's `tool=` value.
	Tool string
	// Distribution is the group's `distribution=` value, or empty for
	// tools that do not require one (e.g., `go`).
	Distribution string
	// VersionLineIdx is the index in Lines of the managed `ARG` that
	// carries the version. The preceding marker is at VersionLineIdx-1.
	VersionLineIdx int
	// Sha256LinesByArch maps each `arch=` value to the index in Lines of
	// its managed `ARG` sha256 line.
	Sha256LinesByArch map[string]int
	// VersionMarkerLine is the 1-indexed source line number of the
	// version marker (for diagnostics).
	VersionMarkerLine int
}

// Warning is a recoverable lint finding emitted by the parser.
type Warning struct {
	// LineNumber is the 1-indexed source line number the warning refers
	// to.
	LineNumber int
	// Message is a human-readable description of the warning.
	Message string
}

// ParsedDockerfile is the pure output of ParseDockerfile. It holds every
// line in input order, the managed groups, any warnings, and the
// multi-stage flag.
type ParsedDockerfile struct {
	// Lines is every line in source order.
	Lines []Line
	// Groups are the managed groups anchored by `kind=version` markers.
	Groups []ManagedGroup
	// Warnings are recoverable lint findings.
	Warnings []Warning
	// MultiStage is true when the file contains more than one `FROM`
	// line.
	MultiStage bool
	// HasTrailingNewline reports whether the input ended with a newline
	// byte. The rewriter preserves this state.
	HasTrailingNewline bool
	// Raw holds the complete input bytes. Used by the rewriter as the
	// source buffer.
	Raw []byte
}

// managedLookingArgPattern matches ARG names that look like they should be
// managed (VERSION or SHA256 suffix). Used for advisory warnings only; the
// parser does not classify unmarked ARGs as managed.
var managedLookingArgPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*(VERSION|SHA256(_[A-Z0-9]+)?)$`)

// ParseDockerfile parses the byte slice as a Dockerfile, extracts markers,
// resolves managed groups, and records warnings. It never returns an error;
// every recoverable condition surfaces as a Warning. Hard errors (multi-
// stage, unknown distribution, missing distribution on java) are reported by
// the classifier via Classify.
func ParseDockerfile(input []byte) *ParsedDockerfile {
	pd := &ParsedDockerfile{Raw: input}

	// Walk the byte slice, emitting one Line per `\n`-terminated segment.
	// A final segment without a terminating newline emits a Line with no
	// newline in Raw and HasTrailingNewline=false.
	lineNo := 0
	start := 0
	for i := range input {
		if input[i] != '\n' {
			continue
		}
		lineNo++
		pd.Lines = append(pd.Lines, Line{
			Raw:    input[start : i+1],
			Number: lineNo,
		})
		start = i + 1
	}
	if start < len(input) {
		lineNo++
		pd.Lines = append(pd.Lines, Line{
			Raw:    input[start:],
			Number: lineNo,
		})
		pd.HasTrailingNewline = false
	} else {
		// Either empty input or input ended with '\n'.
		pd.HasTrailingNewline = len(input) > 0
	}

	// Classify each line: FROM, marker, ARG, or plain.
	fromCount := 0
	for i := range pd.Lines {
		line := &pd.Lines[i]
		trimmed := trimLineEnding(line.Raw)
		// Trim only leading spaces/tabs; Dockerfile directives live at
		// column zero in practice and the parser is permissive of
		// indentation.
		left := bytes.TrimLeft(trimmed, " \t")
		switch {
		case bytes.HasPrefix(left, []byte("FROM ")) || bytes.Equal(left, []byte("FROM")):
			line.Kind = LineKindFrom
			fromCount++
		case bytes.HasPrefix(left, []byte("ARG ")):
			// Parse ARG name and value offsets. If there is no `=`
			// the line is treated as plain.
			if name, valStart, valEnd, ok := parseArgLine(line.Raw); ok {
				line.ArgName = name
				line.ArgValueStart = valStart
				line.ArgValueEnd = valEnd
				// Kind stays LineKindPlain until marker
				// adjacency promotes it to LineKindArg below.
				line.Kind = LineKindPlain
			}
		default:
			if isMarkerLine(left) {
				m, warn := parseMarker(left, line.Number)
				line.Kind = LineKindMarker
				line.Marker = m
				if warn != "" {
					pd.Warnings = append(pd.Warnings, Warning{
						LineNumber: line.Number,
						Message:    warn,
					})
				}
			}
		}
	}
	pd.MultiStage = fromCount > 1

	// Resolve marker → following-line adjacency.
	// A marker classifies the immediately-next non-blank line iff that
	// next line is non-blank, not a marker, and matches ARG (for
	// kind=version / kind=sha256) or FROM (for kind=image). A blank line
	// between the marker and the next line is an orphan.
	for i := 0; i < len(pd.Lines); i++ {
		line := &pd.Lines[i]
		if line.Kind != LineKindMarker || line.Marker == nil {
			continue
		}
		// Duplicate marker detection: if the very next line is also a
		// marker, warn on the second and skip the first group anchor.
		if i+1 < len(pd.Lines) && pd.Lines[i+1].Kind == LineKindMarker {
			pd.Warnings = append(pd.Warnings, Warning{
				LineNumber: pd.Lines[i+1].Number,
				Message: fmt.Sprintf(
					"duplicate confine-ai:managed marker on line %d; previous marker on line %d will be ignored",
					pd.Lines[i+1].Number, line.Number),
			})
			continue
		}

		// Check adjacency: the next line must exist and be non-blank.
		if i+1 >= len(pd.Lines) {
			pd.Warnings = append(pd.Warnings, Warning{
				LineNumber: line.Number,
				Message: fmt.Sprintf(
					"orphan confine-ai:managed marker on line %d: no managed line follows",
					line.Number),
			})
			continue
		}
		next := &pd.Lines[i+1]
		if isBlankLine(next.Raw) {
			pd.Warnings = append(pd.Warnings, Warning{
				LineNumber: line.Number,
				Message: fmt.Sprintf(
					"orphan confine-ai:managed marker on line %d: blank line follows",
					line.Number),
			})
			continue
		}
		// Promote next line to LineKindArg for ARG kinds. For FROM
		// (kind=image) the next line is already classified as
		// LineKindFrom and no promotion is needed.
		switch line.Marker.Kind {
		case "version", "sha256":
			if next.ArgName == "" {
				pd.Warnings = append(pd.Warnings, Warning{
					LineNumber: line.Number,
					Message: fmt.Sprintf(
						"orphan confine-ai:managed marker on line %d: next line is not an ARG",
						line.Number),
				})
				continue
			}
			next.Kind = LineKindArg
		case "image":
			if next.Kind != LineKindFrom {
				pd.Warnings = append(pd.Warnings, Warning{
					LineNumber: line.Number,
					Message: fmt.Sprintf(
						"orphan confine-ai:managed marker on line %d: next line is not a FROM",
						line.Number),
				})
				continue
			}
		}
	}

	// Build ManagedGroups. A group is anchored by a kind=version marker
	// and absorbs all adjacent kind=sha256 markers that share the same
	// tool (and distribution for java). The seed layout pairs them in
	// order, so we iterate markers left-to-right and attach sha256
	// entries to the most recent in-scope version group.
	activeIdx := -1
	for i := range pd.Lines {
		line := &pd.Lines[i]
		if line.Kind != LineKindMarker || line.Marker == nil {
			continue
		}
		// Only consider markers whose following line was promoted to
		// LineKindArg (i.e., not orphan / not duplicate).
		if i+1 >= len(pd.Lines) {
			continue
		}
		next := &pd.Lines[i+1]
		m := line.Marker
		switch m.Kind {
		case "version":
			if next.Kind != LineKindArg {
				continue
			}
			pd.Groups = append(pd.Groups, ManagedGroup{
				Tool:              m.Tool,
				Distribution:      m.Distribution,
				VersionLineIdx:    i + 1,
				Sha256LinesByArch: map[string]int{},
				VersionMarkerLine: line.Number,
			})
			activeIdx = len(pd.Groups) - 1
		case "sha256":
			if next.Kind != LineKindArg {
				continue
			}
			if activeIdx < 0 {
				pd.Warnings = append(pd.Warnings, Warning{
					LineNumber: line.Number,
					Message: fmt.Sprintf(
						"sha256 marker on line %d has no preceding version marker",
						line.Number),
				})
				continue
			}
			g := &pd.Groups[activeIdx]
			if g.Tool != m.Tool || g.Distribution != m.Distribution {
				pd.Warnings = append(pd.Warnings, Warning{
					LineNumber: line.Number,
					Message: fmt.Sprintf(
						"sha256 marker on line %d does not match tool/distribution of active group",
						line.Number),
				})
				continue
			}
			if m.Arch == "" {
				pd.Warnings = append(pd.Warnings, Warning{
					LineNumber: line.Number,
					Message: fmt.Sprintf(
						"sha256 marker on line %d missing arch= field",
						line.Number),
				})
				continue
			}
			g.Sha256LinesByArch[m.Arch] = i + 1
		case "image":
			// Image markers are observed but not rewritten.
			continue
		}
	}

	// Warn on managed-looking ARGs that have no preceding marker.
	for i := range pd.Lines {
		line := &pd.Lines[i]
		if line.Kind == LineKindArg {
			// Already classified as managed; skip.
			continue
		}
		if line.ArgName == "" {
			continue
		}
		if !managedLookingArgPattern.MatchString(line.ArgName) {
			continue
		}
		// Check whether preceding line is a marker; if so, the adjacency
		// pass above either promoted or warned already.
		if i > 0 && pd.Lines[i-1].Kind == LineKindMarker {
			continue
		}
		pd.Warnings = append(pd.Warnings, Warning{
			LineNumber: line.Number,
			Message: fmt.Sprintf(
				"managed-looking ARG %q on line %d has no preceding # confine-ai:managed marker",
				line.ArgName, line.Number),
		})
	}

	return pd
}

// trimLineEnding returns the bytes of raw with any trailing "\r\n" or "\n"
// removed.
func trimLineEnding(raw []byte) []byte {
	n := len(raw)
	if n > 0 && raw[n-1] == '\n' {
		n--
		if n > 0 && raw[n-1] == '\r' {
			n--
		}
	}
	return raw[:n]
}

// isBlankLine reports whether raw (including any line-ending bytes) is
// entirely whitespace.
func isBlankLine(raw []byte) bool {
	return len(bytes.TrimSpace(raw)) == 0
}

// isMarkerLine reports whether trimmed (leading whitespace already removed
// and trailing line-ending bytes already removed) begins with the marker
// sentinel followed by EOL or whitespace. Exact-sentinel enforcement: a
// stricter prefix like `## confine-ai:managed` or `#confine-ai:managed` is not a
// marker.
func isMarkerLine(trimmed []byte) bool {
	if !bytes.HasPrefix(trimmed, []byte(markerSentinel)) {
		return false
	}
	// Sentinel must be followed by end-of-line or whitespace so we do
	// not match `# confine-ai:managedx=...`.
	if len(trimmed) == len(markerSentinel) {
		return true
	}
	c := trimmed[len(markerSentinel)]
	return c == ' ' || c == '\t'
}

// parseMarker extracts a Marker from a line whose sentinel has already been
// confirmed. The input is the trimmed line (no leading whitespace, no
// line-ending bytes). Returns a warning string if required fields are
// malformed; the marker is still returned with whatever fields were parsed so
// the rest of the parser can reason about it.
func parseMarker(trimmed []byte, lineNumber int) (*Marker, string) {
	m := &Marker{LineNumber: lineNumber}
	rest := bytes.TrimSpace(trimmed[len(markerSentinel):])
	if len(rest) == 0 {
		return m, fmt.Sprintf("empty confine-ai:managed marker on line %d", lineNumber)
	}
	// Split on ASCII whitespace.
	for tok := range strings.FieldsSeq(string(rest)) {
		key, value, ok := strings.Cut(tok, "=")
		if !ok {
			m.ExtraTokens = append(m.ExtraTokens, tok)
			continue
		}
		switch key {
		case "tool":
			m.Tool = value
		case "kind":
			m.Kind = value
		case "arch":
			m.Arch = value
		case "distribution":
			m.Distribution = value
		default:
			// Unknown tokens preserved for forward compatibility per
			// the classification ADR.
			m.ExtraTokens = append(m.ExtraTokens, tok)
		}
	}
	return m, ""
}

// parseArgLine parses an ARG line and returns the ARG name plus the value
// offsets within raw. Returns ok=false if the line is not a syntactically
// valid `ARG NAME=value` directive. Offsets point into raw including any
// leading whitespace; the value span excludes any trailing whitespace and
// line-ending bytes.
func parseArgLine(raw []byte) (name string, valueStart, valueEnd int, ok bool) {
	// Locate the ARG keyword.
	trimmed := bytes.TrimLeft(raw, " \t")
	if !bytes.HasPrefix(trimmed, []byte("ARG ")) {
		return "", 0, 0, false
	}
	// Offset of first byte after "ARG " in raw.
	keywordOffset := (len(raw) - len(trimmed)) + len("ARG ")
	after := raw[keywordOffset:]
	// Skip any extra whitespace after ARG.
	spaceCount := 0
	for spaceCount < len(after) && (after[spaceCount] == ' ' || after[spaceCount] == '\t') {
		spaceCount++
	}
	after = after[spaceCount:]
	keywordOffset += spaceCount
	// Locate `=`.
	eqRel := bytes.IndexByte(after, '=')
	if eqRel < 0 {
		return "", 0, 0, false
	}
	nameBytes := after[:eqRel]
	// Validate name: non-empty, no whitespace.
	if len(nameBytes) == 0 || bytes.ContainsAny(nameBytes, " \t") {
		return "", 0, 0, false
	}
	name = string(nameBytes)
	valueStart = keywordOffset + eqRel + 1
	// valueEnd excludes any trailing line-ending bytes and trailing
	// whitespace within the line.
	end := len(raw)
	// Strip trailing `\r\n` or `\n`.
	if end > 0 && raw[end-1] == '\n' {
		end--
		if end > 0 && raw[end-1] == '\r' {
			end--
		}
	}
	// Strip any trailing whitespace before the EOL.
	for end > valueStart && (raw[end-1] == ' ' || raw[end-1] == '\t') {
		end--
	}
	valueEnd = end
	return name, valueStart, valueEnd, true
}
