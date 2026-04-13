package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readFixture reads a Dockerfile fixture from testdata/.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %q: %v", path, err)
	}
	return data
}

// findGroup returns the first managed group matching tool (and distribution if
// non-empty). Returns nil when no match is found.
func findGroup(pd *ParsedDockerfile, tool, distribution string) *ManagedGroup {
	for i := range pd.Groups {
		g := &pd.Groups[i]
		if g.Tool != tool {
			continue
		}
		if distribution != "" && g.Distribution != distribution {
			continue
		}
		return g
	}
	return nil
}

// argValueOf returns the value bytes of the managed ARG at line index idx in
// the parsed file, decoded from line.Raw via the stored offsets.
func argValueOf(pd *ParsedDockerfile, idx int) string {
	line := pd.Lines[idx]
	if line.ArgValueStart == 0 && line.ArgValueEnd == 0 {
		return ""
	}
	return string(line.Raw[line.ArgValueStart:line.ArgValueEnd])
}

func TestParseDockerfile_Valid(t *testing.T) {
	// AC-1, AC-17: parser must find every managed group in the seed-shaped
	// fixture, classify the FROM line, and not mark the file as multi-stage.
	input := readFixture(t, "valid.Dockerfile")

	pd := ParseDockerfile(input)

	if pd.MultiStage {
		t.Errorf("MultiStage = true, want false")
	}
	if !pd.HasTrailingNewline {
		t.Errorf("HasTrailingNewline = false, want true")
	}
	if len(pd.Warnings) != 0 {
		t.Errorf("Warnings = %+v, want none", pd.Warnings)
	}

	// Expect two managed groups (go + java/corretto) plus no rewritable
	// image group (image markers are observed but not turned into groups).
	if len(pd.Groups) != 2 {
		t.Fatalf("len(Groups) = %d, want 2; groups=%+v", len(pd.Groups), pd.Groups)
	}

	goGroup := findGroup(pd, "go", "")
	if goGroup == nil {
		t.Fatal("no go managed group")
		return // unreachable; satisfies staticcheck SA5011
	}
	if got, want := argValueOf(pd, goGroup.VersionLineIdx), "1.26.0"; got != want {
		t.Errorf("go version value = %q, want %q", got, want)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		idx, ok := goGroup.Sha256LinesByArch[arch]
		if !ok {
			t.Errorf("go group missing arch %q", arch)
			continue
		}
		val := argValueOf(pd, idx)
		if len(val) != 64 {
			t.Errorf("go %s sha256 value = %q (len %d), want 64 hex chars", arch, val, len(val))
		}
	}

	javaGroup := findGroup(pd, "java", "corretto")
	if javaGroup == nil {
		t.Fatal("no java/corretto managed group")
		return // unreachable; satisfies staticcheck SA5011
	}
	if got, want := argValueOf(pd, javaGroup.VersionLineIdx), "25.0.2.10.1"; got != want {
		t.Errorf("corretto version value = %q, want %q", got, want)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		idx, ok := javaGroup.Sha256LinesByArch[arch]
		if !ok {
			t.Errorf("corretto group missing arch %q", arch)
			continue
		}
		val := argValueOf(pd, idx)
		if len(val) != 64 {
			t.Errorf("corretto %s sha256 value = %q (len %d), want 64 hex chars", arch, val, len(val))
		}
	}

	// The FROM line must be classified (so runUpdateBase can detect
	// multi-stage safely).
	var fromCount int
	for _, l := range pd.Lines {
		if l.Kind == LineKindFrom {
			fromCount++
		}
	}
	if fromCount != 1 {
		t.Errorf("FROM line count = %d, want 1", fromCount)
	}
}

func TestParseDockerfile_OrphanMarker(t *testing.T) {
	// AC-16: orphan marker followed by blank line → warn, continue.
	input := readFixture(t, "orphan-marker.Dockerfile")

	pd := ParseDockerfile(input)

	if pd.MultiStage {
		t.Errorf("MultiStage = true, want false")
	}
	if len(pd.Groups) != 0 {
		t.Errorf("len(Groups) = %d, want 0 (orphan produces no group)", len(pd.Groups))
	}
	if len(pd.Warnings) == 0 {
		t.Fatal("want at least one warning for orphan marker")
	}
	found := false
	for _, w := range pd.Warnings {
		if strings.Contains(w.Message, "orphan") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no orphan warning in %+v", pd.Warnings)
	}
}

func TestParseDockerfile_UnmarkedArg(t *testing.T) {
	// AC-15: a managed-looking ARG (GO_VERSION) without a preceding marker
	// must emit a warning naming the ARG and the line.
	input := readFixture(t, "unmarked-arg.Dockerfile")

	pd := ParseDockerfile(input)

	if pd.MultiStage {
		t.Errorf("MultiStage = true, want false")
	}
	// The unmarked GO_VERSION line must NOT be classified as a managed
	// ARG: that would be a silent auto-repair.
	for _, l := range pd.Lines {
		if l.ArgName == "GO_VERSION" && l.Kind == LineKindArg {
			t.Errorf("unmarked GO_VERSION line was classified as LineKindArg")
		}
	}
	// But the sha256 line (which does have a preceding marker) should
	// anchor a group.
	found := false
	for _, w := range pd.Warnings {
		if strings.Contains(w.Message, "GO_VERSION") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no warning mentioning GO_VERSION in %+v", pd.Warnings)
	}
}

func TestParseDockerfile_MultiStage(t *testing.T) {
	// AC-13: multi-stage base Dockerfiles are rejected. The parser sets
	// MultiStage=true when it sees more than one FROM line.
	input := readFixture(t, "multi-stage.Dockerfile")

	pd := ParseDockerfile(input)

	if !pd.MultiStage {
		t.Errorf("MultiStage = false, want true")
	}
	var fromCount int
	for _, l := range pd.Lines {
		if l.Kind == LineKindFrom {
			fromCount++
		}
	}
	if fromCount != 2 {
		t.Errorf("FROM line count = %d, want 2", fromCount)
	}
}

func TestParseDockerfile_UnknownDistribution(t *testing.T) {
	// AC-11: tool=java with distribution=temurin is not a parser error; it
	// is detected by the classifier. The parser records the Distribution
	// value so the classifier can reject it.
	input := readFixture(t, "unknown-distribution.Dockerfile")

	pd := ParseDockerfile(input)

	// Find the marker; verify distribution was captured as "temurin".
	var gotDistribution string
	for _, l := range pd.Lines {
		if l.Kind == LineKindMarker && l.Marker != nil && l.Marker.Tool == "java" {
			gotDistribution = l.Marker.Distribution
			break
		}
	}
	if gotDistribution != "temurin" {
		t.Errorf("java marker distribution = %q, want %q", gotDistribution, "temurin")
	}
}

func TestParseDockerfile_MissingDistribution(t *testing.T) {
	// AC-12: tool=java kind=version with no distribution= field. The
	// parser preserves the empty string; the classifier fails the run.
	input := readFixture(t, "missing-distribution.Dockerfile")

	pd := ParseDockerfile(input)

	var found bool
	for _, l := range pd.Lines {
		if l.Kind == LineKindMarker && l.Marker != nil && l.Marker.Tool == "java" && l.Marker.Kind == "version" {
			found = true
			if l.Marker.Distribution != "" {
				t.Errorf("java version marker Distribution = %q, want empty", l.Marker.Distribution)
			}
		}
	}
	if !found {
		t.Error("no tool=java kind=version marker found")
	}
}

func TestParseDockerfile_UserEditedFrom(t *testing.T) {
	// AC-25: the parser must tolerate a user-edited FROM line and NOT warn
	// about the changed base image. The managed groups must still parse.
	input := readFixture(t, "user-edited-from.Dockerfile")

	pd := ParseDockerfile(input)

	if pd.MultiStage {
		t.Errorf("MultiStage = true, want false")
	}
	// No warnings about the FROM line.
	for _, w := range pd.Warnings {
		if strings.Contains(strings.ToLower(w.Message), "from") {
			t.Errorf("unexpected warning about FROM line: %s", w.Message)
		}
	}
	// Go group must still anchor.
	if findGroup(pd, "go", "") == nil {
		t.Error("go managed group not found in user-edited-from fixture")
	}
}

func TestParseDockerfile_CRLFPreserved(t *testing.T) {
	// AC-17: CRLF line endings must be preserved byte-identically. The
	// parser must produce Lines whose concatenation equals the original
	// input byte-for-byte.
	input := readFixture(t, "crlf.Dockerfile")

	pd := ParseDockerfile(input)

	// Verify the fixture really is CRLF.
	if !strings.Contains(string(input), "\r\n") {
		t.Fatal("crlf.Dockerfile does not contain CRLF sequences; fixture is wrong")
	}

	// Concatenate Line.Raw bytes and compare to input.
	var rebuilt []byte
	for _, l := range pd.Lines {
		rebuilt = append(rebuilt, l.Raw...)
	}
	if string(rebuilt) != string(input) {
		t.Errorf("CRLF round-trip failed\ngot:  %q\nwant: %q", string(rebuilt), string(input))
	}

	if !pd.HasTrailingNewline {
		t.Errorf("HasTrailingNewline = false, want true")
	}

	// Go group must still be found even though line endings are CRLF.
	if findGroup(pd, "go", "") == nil {
		t.Error("go managed group not found in CRLF fixture")
	}
}

func TestParseDockerfile_NoTrailingNewline(t *testing.T) {
	// AC-17: a file without a trailing newline must set
	// HasTrailingNewline=false and the last Line.Raw must not end in '\n'.
	input := readFixture(t, "no-trailing-newline.Dockerfile")

	pd := ParseDockerfile(input)

	if pd.HasTrailingNewline {
		t.Errorf("HasTrailingNewline = true, want false")
	}
	if len(pd.Lines) == 0 {
		t.Fatal("no lines parsed")
	}
	last := pd.Lines[len(pd.Lines)-1]
	if len(last.Raw) == 0 {
		t.Errorf("last line Raw is empty")
	} else if last.Raw[len(last.Raw)-1] == '\n' {
		t.Errorf("last line Raw ends in newline, want no trailing newline")
	}

	// Round-trip check.
	var rebuilt []byte
	for _, l := range pd.Lines {
		rebuilt = append(rebuilt, l.Raw...)
	}
	if string(rebuilt) != string(input) {
		t.Errorf("round-trip failed\ngot:  %q\nwant: %q", string(rebuilt), string(input))
	}
}

func TestParseDockerfile_EmbeddedSeedRoundTrip(t *testing.T) {
	// Regression guard: parsing the checked-in seed Dockerfile and
	// reassembling its lines must yield the original bytes exactly. This
	// protects AC-17's byte-preservation contract against parser drift and
	// ensures samples/base/Dockerfile remains parseable as confine-ai evolves.
	input, err := os.ReadFile(filepath.Join("..", "..", "samples", "base", "Dockerfile"))
	if err != nil {
		t.Fatalf("read seed Dockerfile: %v", err)
	}

	pd := ParseDockerfile(input)

	if pd.MultiStage {
		t.Errorf("seed MultiStage = true, want false")
	}
	if findGroup(pd, "go", "") == nil {
		t.Error("seed has no go managed group")
	}
	if findGroup(pd, "java", "corretto") == nil {
		t.Error("seed has no java/corretto managed group")
	}

	var rebuilt []byte
	for _, l := range pd.Lines {
		rebuilt = append(rebuilt, l.Raw...)
	}
	if string(rebuilt) != string(input) {
		t.Errorf("seed round-trip failed (len got %d want %d)", len(rebuilt), len(input))
	}
}

func TestParseDockerfile_MarkerSentinelExact(t *testing.T) {
	// Protects the exact-sentinel rule: ## confine-ai:managed and
	// #confine-ai:managed must NOT be classified as markers.
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"exact", "# confine-ai:managed tool=go kind=version\nARG GO_VERSION=1\n", true},
		{"double-hash", "## confine-ai:managed tool=go kind=version\nARG GO_VERSION=1\n", false},
		{"no-space", "#confine-ai:managed tool=go kind=version\nARG GO_VERSION=1\n", false},
		{"trailing-garbage-token-prefix", "# confine-ai:managedx tool=go kind=version\nARG GO_VERSION=1\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pd := ParseDockerfile([]byte(tc.input))
			var found bool
			for _, l := range pd.Lines {
				if l.Kind == LineKindMarker {
					found = true
					break
				}
			}
			if found != tc.want {
				t.Errorf("marker detected = %v, want %v", found, tc.want)
			}
		})
	}
}
