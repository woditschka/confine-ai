package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woditschka/confine-ai/internal/update"
)

// stubProber is a test Prober that returns canned results for Go and Corretto
// without issuing real HTTP requests.
type stubProber struct {
	goResult       update.Resolved
	goErr          error
	correttoResult update.Resolved
	correttoErr    error
}

func (s *stubProber) ProbeGo(_ context.Context, _ []string) (update.Resolved, error) {
	return s.goResult, s.goErr
}

func (s *stubProber) ProbeCorretto(_ context.Context, _ string, _ []string) (update.Resolved, error) {
	return s.correttoResult, s.correttoErr
}

// sampleFixture is a minimal Dockerfile with confine-ai:managed markers matching
// the shape of samples/base/Dockerfile.
const sampleFixture = `# confine-ai:managed tool=base-image kind=image
FROM debian:bookworm-slim

# confine-ai:managed tool=go kind=version
ARG GO_VERSION=1.24.0
# confine-ai:managed tool=go kind=sha256 arch=amd64
ARG GO_SHA256_AMD64=aaaa0000000000000000000000000000000000000000000000000000000000aa
# confine-ai:managed tool=go kind=sha256 arch=arm64
ARG GO_SHA256_ARM64=bbbb0000000000000000000000000000000000000000000000000000000000bb

# confine-ai:managed tool=java kind=version distribution=corretto
ARG CORRETTO_VERSION=21.0.5.11.1
# confine-ai:managed tool=java kind=sha256 arch=amd64 distribution=corretto
ARG CORRETTO_SHA256_AMD64=cccc0000000000000000000000000000000000000000000000000000000000cc
# confine-ai:managed tool=java kind=sha256 arch=arm64 distribution=corretto
ARG CORRETTO_SHA256_ARM64=dddd0000000000000000000000000000000000000000000000000000000000dd

RUN echo hello
`

// writeFixture writes sampleFixture to a temp directory as
// samples/base/Dockerfile and returns the file path.
func writeFixture(t *testing.T, dir, content string) string {
	t.Helper()
	sampleDir := filepath.Join(dir, "samples", "base")
	if err := os.MkdirAll(sampleDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(sampleDir, "Dockerfile")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func newGoResolved(version string) update.Resolved {
	return update.Resolved{
		Version: version,
		Sha256: map[string]string{
			"amd64": "1111111111111111111111111111111111111111111111111111111111111111",
			"arm64": "2222222222222222222222222222222222222222222222222222222222222222",
		},
	}
}

func newCorrettoResolved(version string) update.Resolved {
	return update.Resolved{
		Version: version,
		Sha256: map[string]string{
			"amd64": "3333333333333333333333333333333333333333333333333333333333333333",
			"arm64": "4444444444444444444444444444444444444444444444444444444444444444",
		},
	}
}

// TestRun_HappyPath verifies that when probes return new versions, the file is
// rewritten and stdout reports the per-group deltas.
func TestRun_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, sampleFixture)

	prober := &stubProber{
		goResult:       newGoResolved("1.26.0"),
		correttoResult: newCorrettoResolved("25.0.3.11.1"),
	}

	var stdout, stderr bytes.Buffer
	code := run(t.Context(), filepath.Join(dir, "samples", "base", "Dockerfile"), prober, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, want 0; stderr=%q", code, stderr.String())
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Contains(got, []byte("ARG GO_VERSION=1.26.0")) {
		t.Errorf("GO_VERSION not rewritten: %s", got)
	}
	if !bytes.Contains(got, []byte("ARG CORRETTO_VERSION=25.0.3.11.1")) {
		t.Errorf("CORRETTO_VERSION not rewritten: %s", got)
	}

	// Stdout should report the changes.
	out := stdout.String()
	if !strings.Contains(out, "1.24.0") || !strings.Contains(out, "1.26.0") {
		t.Errorf("stdout missing Go version delta: %q", out)
	}
	if !strings.Contains(out, "21.0.5.11.1") || !strings.Contains(out, "25.0.3.11.1") {
		t.Errorf("stdout missing Corretto version delta: %q", out)
	}
}

// TestRun_Unchanged verifies that when probes return the same versions, no
// bytes change and stdout reports "unchanged".
func TestRun_Unchanged(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, sampleFixture)
	origBytes, _ := os.ReadFile(path)

	prober := &stubProber{
		goResult: update.Resolved{
			Version: "1.24.0",
			Sha256: map[string]string{
				"amd64": "aaaa0000000000000000000000000000000000000000000000000000000000aa",
				"arm64": "bbbb0000000000000000000000000000000000000000000000000000000000bb",
			},
		},
		correttoResult: update.Resolved{
			Version: "21.0.5.11.1",
			Sha256: map[string]string{
				"amd64": "cccc0000000000000000000000000000000000000000000000000000000000cc",
				"arm64": "dddd0000000000000000000000000000000000000000000000000000000000dd",
			},
		},
	}

	var stdout, stderr bytes.Buffer
	code := run(t.Context(), filepath.Join(dir, "samples", "base", "Dockerfile"), prober, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, want 0; stderr=%q", code, stderr.String())
	}

	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, origBytes) {
		t.Errorf("file was rewritten; wanted no-op")
	}

	if !strings.Contains(stdout.String(), "unchanged") {
		t.Errorf("stdout does not contain 'unchanged': %q", stdout.String())
	}
}

// TestRun_ProbeFailure verifies that a probe error causes a non-zero exit and
// no file modification.
func TestRun_ProbeFailure(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, sampleFixture)
	origBytes, _ := os.ReadFile(path)

	prober := &stubProber{
		goErr:          os.ErrNotExist, // arbitrary error
		correttoResult: newCorrettoResolved("25.0.3.11.1"),
	}

	var stdout, stderr bytes.Buffer
	code := run(t.Context(), filepath.Join(dir, "samples", "base", "Dockerfile"), prober, &stdout, &stderr)
	if code == 0 {
		t.Fatal("run() = 0, want non-zero on probe failure")
	}

	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, origBytes) {
		t.Errorf("file was rewritten despite probe failure")
	}
}

// TestRun_MissingFile verifies that a nonexistent path causes a non-zero exit.
func TestRun_MissingFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), "/nonexistent/samples/base/Dockerfile", &stubProber{}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("run() = 0, want non-zero on missing file")
	}

	if !strings.Contains(stderr.String(), "nonexistent") {
		t.Errorf("stderr does not mention the missing path: %q", stderr.String())
	}
}

// TestRun_JavaMajorJumpAutoAccepted verifies that a Java major-version jump
// (e.g., 21 -> 25) is auto-accepted without prompting.
func TestRun_JavaMajorJumpAutoAccepted(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, sampleFixture)

	// Fixture has Corretto 21.x; probe returns 25.x (major jump).
	prober := &stubProber{
		goResult:       newGoResolved("1.26.0"),
		correttoResult: newCorrettoResolved("25.0.3.11.1"),
	}

	var stdout, stderr bytes.Buffer
	code := run(t.Context(), filepath.Join(dir, "samples", "base", "Dockerfile"), prober, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, want 0; stderr=%q", code, stderr.String())
	}

	got, _ := os.ReadFile(path)
	if !bytes.Contains(got, []byte("ARG CORRETTO_VERSION=25.0.3.11.1")) {
		t.Errorf("major jump not applied: %s", got)
	}
}

// TestRun_BytePreservation verifies that non-managed lines are
// byte-identical after a rewrite.
func TestRun_BytePreservation(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, sampleFixture)

	prober := &stubProber{
		goResult:       newGoResolved("1.26.0"),
		correttoResult: newCorrettoResolved("25.0.3.11.1"),
	}

	var stdout, stderr bytes.Buffer
	code := run(t.Context(), filepath.Join(dir, "samples", "base", "Dockerfile"), prober, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}

	path := filepath.Join(dir, "samples", "base", "Dockerfile")
	got, _ := os.ReadFile(path)

	origLines := strings.Split(sampleFixture, "\n")
	gotLines := strings.Split(string(got), "\n")
	if len(origLines) != len(gotLines) {
		t.Fatalf("line count changed: orig=%d got=%d", len(origLines), len(gotLines))
	}
	for i, origLine := range origLines {
		gotLine := gotLines[i]
		isArg := strings.HasPrefix(origLine, "ARG ") &&
			(strings.Contains(origLine, "VERSION=") || strings.Contains(origLine, "SHA256"))
		if isArg {
			continue
		}
		if origLine != gotLine {
			t.Errorf("line %d differs\n  orig: %q\n  got:  %q", i+1, origLine, gotLine)
		}
	}
}
