package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stubBuilder implements assistant.ImageBuilder for RunBase tests. Unlike the
// internal/assistant package's fakeImageBuilder (which is in another package),
// this stub lives alongside the update tests and supports the sequence of
// calls RunBase issues: optionally a pre-check (skipped here because RunBase
// does not call Output before rebuild), then a Run("build", ...) for the
// rebuild.
type stubBuilder struct {
	runCalls   [][]string
	runResults []error
	runIdx     int

	outputCalls   [][]string
	outputResults []stubOutputResult
	outputIdx     int
}

type stubOutputResult struct {
	out string
	err error
}

func (s *stubBuilder) Run(_ context.Context, _, _ io.Writer, args ...string) error {
	s.runCalls = append(s.runCalls, args)
	if s.runIdx >= len(s.runResults) {
		return nil
	}
	r := s.runResults[s.runIdx]
	s.runIdx++
	return r
}

func (s *stubBuilder) Output(_ context.Context, args ...string) (string, error) {
	s.outputCalls = append(s.outputCalls, args)
	if s.outputIdx >= len(s.outputResults) {
		return "", nil
	}
	r := s.outputResults[s.outputIdx]
	s.outputIdx++
	return r.out, r.err
}

// stubProber is a test Prober that returns canned results for Go and
// Corretto without issuing real HTTP requests. Each field can be set to a
// function returning either a Resolved or an error. A nil function means
// "return an unimplemented error" so the test catches unexpected calls.
type stubProber struct {
	goResult       Resolved
	goErr          error
	correttoResult Resolved
	correttoErr    error

	goCalls       int
	correttoCalls int
	lastCurrent   string
}

func (s *stubProber) ProbeGo(_ context.Context, _ []string) (Resolved, error) {
	s.goCalls++
	return s.goResult, s.goErr
}

func (s *stubProber) ProbeCorretto(_ context.Context, currentVersion string, _ []string) (Resolved, error) {
	s.correttoCalls++
	s.lastCurrent = currentVersion
	return s.correttoResult, s.correttoErr
}

// baseFixture is the canonical base Dockerfile used by the RunBase tests. It
// mirrors internal/update/testdata/valid.Dockerfile but is declared inline so
// each test can edit the current versions before writing the file.
const baseFixture = `# confine-ai:managed tool=base-image kind=image
FROM debian:bookworm-slim

# confine-ai:managed tool=go kind=version
ARG GO_VERSION=1.26.0
# confine-ai:managed tool=go kind=sha256 arch=amd64
ARG GO_SHA256_AMD64=aaaa0000000000000000000000000000000000000000000000000000000000aa
# confine-ai:managed tool=go kind=sha256 arch=arm64
ARG GO_SHA256_ARM64=bbbb0000000000000000000000000000000000000000000000000000000000bb

# confine-ai:managed tool=java kind=version distribution=corretto
ARG CORRETTO_VERSION=25.0.2.10.1
# confine-ai:managed tool=java kind=sha256 arch=amd64 distribution=corretto
ARG CORRETTO_SHA256_AMD64=cccc0000000000000000000000000000000000000000000000000000000000cc
# confine-ai:managed tool=java kind=sha256 arch=arm64 distribution=corretto
ARG CORRETTO_SHA256_ARM64=dddd0000000000000000000000000000000000000000000000000000000000dd

RUN echo hello
`

// writeBaseFixture writes the fixture to ~/.confine-ai/base/Dockerfile under
// homeDir and returns the file path. Tests mutate the default content by
// passing a replaced string.
func writeBaseFixture(t *testing.T, homeDir, content string) string {
	t.Helper()
	dir := filepath.Join(homeDir, ".confine-ai", "base")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// newBaseOptions constructs a BaseOptions with sensible defaults for tests.
func newBaseOptions(t *testing.T, homeDir string, prober Prober, builder *stubBuilder) BaseOptions {
	t.Helper()
	return BaseOptions{
		HomeDir:  homeDir,
		Stdin:    strings.NewReader(""),
		Stdout:   &bytes.Buffer{},
		Stderr:   &bytes.Buffer{},
		Prober:   prober,
		Executor: builder,
		IsTTY:    false,
	}
}

// newGoResolved returns a canned Go probe result.
func newGoResolved(version string) Resolved {
	return Resolved{
		Version: version,
		Sha256: map[string]string{
			"amd64": "1111111111111111111111111111111111111111111111111111111111111111",
			"arm64": "2222222222222222222222222222222222222222222222222222222222222222",
		},
	}
}

// newCorrettoResolved returns a canned Corretto probe result.
func newCorrettoResolved(version string) Resolved {
	return Resolved{
		Version: version,
		Sha256: map[string]string{
			"amd64": "3333333333333333333333333333333333333333333333333333333333333333",
			"arm64": "4444444444444444444444444444444444444444444444444444444444444444",
		},
	}
}

// TestRunBase_GoHappyPath — AC-1: Go probe and Corretto probe both succeed
// with new values; file is rewritten; rebuild runs with --pull; exit 0.
func TestRunBase_GoHappyPath(t *testing.T) {
	homeDir := t.TempDir()
	path := writeBaseFixture(t, homeDir, baseFixture)

	prober := &stubProber{
		goResult:       newGoResolved("1.27.1"),
		correttoResult: newCorrettoResolved("25.0.3.11.1"),
	}
	builder := &stubBuilder{}

	res := RunBase(t.Context(), newBaseOptions(t, homeDir, prober, builder))
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0, error=%q", res.ExitCode, res.Error)
	}
	if res.Action != ActionUpdated {
		t.Errorf("Action = %q, want %q", res.Action, ActionUpdated)
	}

	// File rewritten.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Contains(got, []byte("ARG GO_VERSION=1.27.1")) {
		t.Errorf("GO_VERSION not rewritten: %s", got)
	}
	if !bytes.Contains(got, []byte("ARG CORRETTO_VERSION=25.0.3.11.1")) {
		t.Errorf("CORRETTO_VERSION not rewritten: %s", got)
	}
	if !bytes.Contains(got, []byte("ARG GO_SHA256_AMD64=1111")) {
		t.Errorf("GO_SHA256_AMD64 not rewritten: %s", got)
	}

	// Rebuild invoked with --pull.
	if len(builder.runCalls) == 0 {
		t.Fatal("builder.Run never called; expected base rebuild")
	}
	found := false
	for _, args := range builder.runCalls {
		if args[0] == "build" && containsArg(args, "--pull") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no build call with --pull; runCalls=%v", builder.runCalls)
	}
}

// TestRunBase_GoAlreadyLatest — AC-2: probes succeed but report the same
// versions + shas already in the file. Action=unchanged, no write, no
// rebuild, exit 0.
func TestRunBase_GoAlreadyLatest(t *testing.T) {
	homeDir := t.TempDir()
	path := writeBaseFixture(t, homeDir, baseFixture)
	origBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) = %v", path, err)
	}

	prober := &stubProber{
		goResult: Resolved{
			Version: "1.26.0",
			Sha256: map[string]string{
				"amd64": "aaaa0000000000000000000000000000000000000000000000000000000000aa",
				"arm64": "bbbb0000000000000000000000000000000000000000000000000000000000bb",
			},
		},
		correttoResult: Resolved{
			Version: "25.0.2.10.1",
			Sha256: map[string]string{
				"amd64": "cccc0000000000000000000000000000000000000000000000000000000000cc",
				"arm64": "dddd0000000000000000000000000000000000000000000000000000000000dd",
			},
		},
	}
	builder := &stubBuilder{}
	res := RunBase(t.Context(), newBaseOptions(t, homeDir, prober, builder))
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0, error=%q", res.ExitCode, res.Error)
	}
	if res.Action != ActionUnchanged {
		t.Errorf("Action = %q, want %q", res.Action, ActionUnchanged)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, origBytes) {
		t.Errorf("file was rewritten; wanted no-op\n--- want\n%s\n--- got\n%s", origBytes, got)
	}
	if len(builder.runCalls) != 0 {
		t.Errorf("builder.Run called %d times; expected 0 (unchanged)", len(builder.runCalls))
	}
}

// TestRunBase_DryRunHappy — AC-3: probes succeed, new versions reported. In
// dry-run mode we must not write the file and must not rebuild, but exit 0
// and report "would update".
func TestRunBase_DryRunHappy(t *testing.T) {
	homeDir := t.TempDir()
	path := writeBaseFixture(t, homeDir, baseFixture)
	origBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) = %v", path, err)
	}

	prober := &stubProber{
		goResult:       newGoResolved("1.27.1"),
		correttoResult: newCorrettoResolved("25.0.3.11.1"),
	}
	builder := &stubBuilder{}
	opts := newBaseOptions(t, homeDir, prober, builder)
	opts.DryRun = true

	res := RunBase(t.Context(), opts)
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
	if res.Action != ActionWouldUpdate {
		t.Errorf("Action = %q, want %q", res.Action, ActionWouldUpdate)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, origBytes) {
		t.Error("file was rewritten in dry-run mode")
	}
	if len(builder.runCalls) != 0 {
		t.Errorf("builder.Run called in dry-run mode: %v", builder.runCalls)
	}
	// Deltas reported.
	if len(res.GroupDeltas) != 2 {
		t.Errorf("GroupDeltas = %d, want 2: %+v", len(res.GroupDeltas), res.GroupDeltas)
	}
}

// TestRunBase_DryRunShaFailure — AC-4: dry-run mode propagates real exit
// codes. Here Go probe errors with a sha256 problem → exit 3 even in dry-run.
func TestRunBase_DryRunShaFailure(t *testing.T) {
	homeDir := t.TempDir()
	path := writeBaseFixture(t, homeDir, baseFixture)
	origBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) = %v", path, err)
	}

	prober := &stubProber{
		goErr:          fmt.Errorf("wrap: %w", ErrInvalidSha256),
		correttoResult: newCorrettoResolved("25.0.3.11.1"),
	}
	builder := &stubBuilder{}
	opts := newBaseOptions(t, homeDir, prober, builder)
	opts.DryRun = true

	res := RunBase(t.Context(), opts)
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
	if res.Action != ActionFailed {
		t.Errorf("Action = %q, want %q", res.Action, ActionFailed)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, origBytes) {
		t.Error("file was rewritten after sha failure")
	}
}

// TestRunBase_Atomicity_ShaFailure — AC-5: Go probe succeeds but Corretto
// sha256 fetch fails. Nothing is written; exit 3.
func TestRunBase_Atomicity_ShaFailure(t *testing.T) {
	homeDir := t.TempDir()
	path := writeBaseFixture(t, homeDir, baseFixture)
	origBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) = %v", path, err)
	}

	prober := &stubProber{
		goResult:    newGoResolved("1.27.1"),
		correttoErr: fmt.Errorf("wrap: %w", ErrInvalidSha256),
	}
	builder := &stubBuilder{}

	res := RunBase(t.Context(), newBaseOptions(t, homeDir, prober, builder))
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3, error=%q", res.ExitCode, res.Error)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, origBytes) {
		t.Error("file was rewritten despite atomicity failure")
	}
	if len(builder.runCalls) != 0 {
		t.Error("builder.Run called despite atomicity failure")
	}
}

// TestRunBase_JavaMajorJump_Proceed — AC-6: Java major jump prompted; user
// answers "proceed"; rewrite + rebuild.
func TestRunBase_JavaMajorJump_Proceed(t *testing.T) {
	homeDir := t.TempDir()
	path := writeBaseFixture(t, homeDir, baseFixture)

	prober := &stubProber{
		goResult:       newGoResolved("1.27.1"),
		correttoResult: newCorrettoResolved("26.0.0.5.1"),
	}
	builder := &stubBuilder{}
	opts := newBaseOptions(t, homeDir, prober, builder)
	opts.Stdin = strings.NewReader("proceed\n")
	opts.IsTTY = true

	res := RunBase(t.Context(), opts)
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0, error=%q", res.ExitCode, res.Error)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Contains(got, []byte("ARG CORRETTO_VERSION=26.0.0.5.1")) {
		t.Errorf("major jump not written: %s", got)
	}
	if len(builder.runCalls) == 0 {
		t.Error("rebuild not invoked after major-jump proceed")
	}
}

// TestRunBase_JavaMajorJump_Skip — AC-7: user answers "skip"; Corretto
// unchanged, Go still applies.
func TestRunBase_JavaMajorJump_Skip(t *testing.T) {
	homeDir := t.TempDir()
	path := writeBaseFixture(t, homeDir, baseFixture)

	prober := &stubProber{
		goResult:       newGoResolved("1.27.1"),
		correttoResult: newCorrettoResolved("26.0.0.5.1"),
	}
	builder := &stubBuilder{}
	opts := newBaseOptions(t, homeDir, prober, builder)
	opts.Stdin = strings.NewReader("skip\n")
	opts.IsTTY = true

	res := RunBase(t.Context(), opts)
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0, error=%q", res.ExitCode, res.Error)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Contains(got, []byte("ARG GO_VERSION=1.27.1")) {
		t.Errorf("Go not rewritten: %s", got)
	}
	if bytes.Contains(got, []byte("ARG CORRETTO_VERSION=26.0.0.5.1")) {
		t.Errorf("Corretto rewritten despite skip: %s", got)
	}
	if !bytes.Contains(got, []byte("ARG CORRETTO_VERSION=25.0.2.10.1")) {
		t.Errorf("Original Corretto not preserved: %s", got)
	}
}

// TestRunBase_JavaMajorJump_Abort — AC-8: user answers "abort"; exit 4; no
// writes; the walk halts (tested at CLI level, here we just assert exit 4).
func TestRunBase_JavaMajorJump_Abort(t *testing.T) {
	homeDir := t.TempDir()
	path := writeBaseFixture(t, homeDir, baseFixture)
	origBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) = %v", path, err)
	}

	prober := &stubProber{
		goResult:       newGoResolved("1.27.1"),
		correttoResult: newCorrettoResolved("26.0.0.5.1"),
	}
	builder := &stubBuilder{}
	opts := newBaseOptions(t, homeDir, prober, builder)
	opts.Stdin = strings.NewReader("abort\n")
	opts.IsTTY = true

	res := RunBase(t.Context(), opts)
	if res.ExitCode != 4 {
		t.Errorf("ExitCode = %d, want 4, error=%q", res.ExitCode, res.Error)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, origBytes) {
		t.Error("file was rewritten despite abort")
	}
	if len(builder.runCalls) != 0 {
		t.Error("rebuild invoked despite abort")
	}
}

// TestRunBase_NonTerminal_ImplicitSkip — AC-9: non-terminal stdin and no
// AutoYes → Java group is implicitly skipped; Go proceeds.
func TestRunBase_NonTerminal_ImplicitSkip(t *testing.T) {
	homeDir := t.TempDir()
	path := writeBaseFixture(t, homeDir, baseFixture)

	prober := &stubProber{
		goResult:       newGoResolved("1.27.1"),
		correttoResult: newCorrettoResolved("26.0.0.5.1"),
	}
	builder := &stubBuilder{}
	opts := newBaseOptions(t, homeDir, prober, builder)
	opts.IsTTY = false
	opts.AutoYes = false

	res := RunBase(t.Context(), opts)
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Contains(got, []byte("ARG GO_VERSION=1.27.1")) {
		t.Errorf("Go not rewritten: %s", got)
	}
	if bytes.Contains(got, []byte("ARG CORRETTO_VERSION=26.0.0.5.1")) {
		t.Errorf("Corretto rewritten despite implicit skip: %s", got)
	}
}

// TestRunBase_NonTerminal_AutoYes — AC-10: non-terminal stdin but AutoYes →
// Java major jump proceeds without prompt.
func TestRunBase_NonTerminal_AutoYes(t *testing.T) {
	homeDir := t.TempDir()
	path := writeBaseFixture(t, homeDir, baseFixture)

	prober := &stubProber{
		goResult:       newGoResolved("1.27.1"),
		correttoResult: newCorrettoResolved("26.0.0.5.1"),
	}
	builder := &stubBuilder{}
	opts := newBaseOptions(t, homeDir, prober, builder)
	opts.IsTTY = false
	opts.AutoYes = true

	res := RunBase(t.Context(), opts)
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Contains(got, []byte("ARG CORRETTO_VERSION=26.0.0.5.1")) {
		t.Errorf("AutoYes did not apply Corretto major jump: %s", got)
	}
}

// TestRunBase_UnknownDistribution — AC-11: unknown distribution → exit 1,
// no write.
func TestRunBase_UnknownDistribution(t *testing.T) {
	homeDir := t.TempDir()
	bad := strings.ReplaceAll(baseFixture, "distribution=corretto", "distribution=temurin")
	path := writeBaseFixture(t, homeDir, bad)
	orig, _ := os.ReadFile(path)

	prober := &stubProber{}
	builder := &stubBuilder{}
	res := RunBase(t.Context(), newBaseOptions(t, homeDir, prober, builder))
	if res.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", res.ExitCode)
	}
	if res.Action != ActionFailed {
		t.Errorf("Action = %q, want failed", res.Action)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, orig) {
		t.Error("file rewritten on unknown distribution")
	}
	if prober.goCalls != 0 || prober.correttoCalls != 0 {
		t.Error("prober called despite classifier failure")
	}
}

// TestRunBase_MissingDistribution — AC-12: tool=java without distribution= →
// exit 1, no write.
func TestRunBase_MissingDistribution(t *testing.T) {
	homeDir := t.TempDir()
	bad := strings.ReplaceAll(baseFixture,
		"# confine-ai:managed tool=java kind=version distribution=corretto",
		"# confine-ai:managed tool=java kind=version")
	path := writeBaseFixture(t, homeDir, bad)
	orig, _ := os.ReadFile(path)

	res := RunBase(t.Context(), newBaseOptions(t, homeDir, &stubProber{}, &stubBuilder{}))
	if res.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", res.ExitCode)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, orig) {
		t.Error("file rewritten despite missing distribution")
	}
}

// TestRunBase_MultiStage — AC-13: two FROM lines → exit 1, no probe.
func TestRunBase_MultiStage(t *testing.T) {
	homeDir := t.TempDir()
	bad := baseFixture + "\nFROM debian:bookworm-slim\nRUN echo second\n"
	path := writeBaseFixture(t, homeDir, bad)
	orig, _ := os.ReadFile(path)

	prober := &stubProber{}
	res := RunBase(t.Context(), newBaseOptions(t, homeDir, prober, &stubBuilder{}))
	if res.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1, error=%q", res.ExitCode, res.Error)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, orig) {
		t.Error("file rewritten on multi-stage")
	}
	if prober.goCalls != 0 {
		t.Error("prober invoked on multi-stage")
	}
}

// TestRunBase_MissingDockerfile — AC-14: no ~/.confine-ai/base/Dockerfile →
// exit 1 with init hint.
func TestRunBase_MissingDockerfile(t *testing.T) {
	homeDir := t.TempDir()
	res := RunBase(t.Context(), newBaseOptions(t, homeDir, &stubProber{}, &stubBuilder{}))
	if res.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", res.ExitCode)
	}
	if !strings.Contains(res.Error, "confine-ai init") {
		t.Errorf("Error does not hint confine-ai init: %q", res.Error)
	}
}

// TestRunBase_UnmarkedArgWarning — AC-15: a managed-looking ARG without a
// marker surfaces as a warning on stderr; other groups proceed.
func TestRunBase_UnmarkedArgWarning(t *testing.T) {
	homeDir := t.TempDir()
	// Inject an unmarked managed-looking ARG.
	withWarn := strings.Replace(baseFixture,
		"RUN echo hello\n",
		"ARG SOMETHING_VERSION=1.0.0\nRUN echo hello\n", 1)
	path := writeBaseFixture(t, homeDir, withWarn)

	prober := &stubProber{
		goResult:       newGoResolved("1.27.1"),
		correttoResult: newCorrettoResolved("25.0.3.11.1"),
	}
	stderr := &bytes.Buffer{}
	opts := newBaseOptions(t, homeDir, prober, &stubBuilder{})
	opts.Stderr = stderr

	res := RunBase(t.Context(), opts)
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0, error=%q", res.ExitCode, res.Error)
	}
	if !strings.Contains(stderr.String(), "SOMETHING_VERSION") {
		t.Errorf("stderr did not mention SOMETHING_VERSION warning: %q", stderr.String())
	}
	got, _ := os.ReadFile(path)
	if !bytes.Contains(got, []byte("ARG GO_VERSION=1.27.1")) {
		t.Errorf("Go group did not proceed despite warning: %s", got)
	}
}

// TestRunBase_OrphanMarkerWarning — AC-16: a marker followed by blank line
// surfaces as a warning; other groups proceed.
func TestRunBase_OrphanMarkerWarning(t *testing.T) {
	homeDir := t.TempDir()
	withOrphan := strings.Replace(baseFixture,
		"RUN echo hello\n",
		"# confine-ai:managed tool=go kind=version\n\nRUN echo hello\n", 1)
	path := writeBaseFixture(t, homeDir, withOrphan)

	prober := &stubProber{
		goResult:       newGoResolved("1.27.1"),
		correttoResult: newCorrettoResolved("25.0.3.11.1"),
	}
	stderr := &bytes.Buffer{}
	opts := newBaseOptions(t, homeDir, prober, &stubBuilder{})
	opts.Stderr = stderr

	res := RunBase(t.Context(), opts)
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0, error=%q", res.ExitCode, res.Error)
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "orphan") {
		t.Errorf("stderr did not mention orphan marker: %q", stderr.String())
	}
	got, _ := os.ReadFile(path)
	if !bytes.Contains(got, []byte("ARG GO_VERSION=1.27.1")) {
		t.Errorf("Go group did not proceed despite orphan warning: %s", got)
	}
}

// TestRunBase_BytePreservationExceptDeltas — AC-17: after rewrite, every
// byte outside the rewritten ARG value spans equals the original.
func TestRunBase_BytePreservationExceptDeltas(t *testing.T) {
	homeDir := t.TempDir()
	path := writeBaseFixture(t, homeDir, baseFixture)

	prober := &stubProber{
		goResult:       newGoResolved("1.27.1"),
		correttoResult: newCorrettoResolved("25.0.3.11.1"),
	}
	res := RunBase(t.Context(), newBaseOptions(t, homeDir, prober, &stubBuilder{}))
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
	got, _ := os.ReadFile(path)

	// The only lines that should differ are the ARG *VERSION= and
	// ARG *SHA256_* lines. FROM, comments, and RUN must match byte-for-
	// byte.
	origLines := strings.Split(baseFixture, "\n")
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

// TestRunBase_PreservesEditedFrom — AC-25: a user-edited FROM line (e.g.,
// FROM ubuntu:24.04) is never touched by the rewriter and produces no
// warning.
func TestRunBase_PreservesEditedFrom(t *testing.T) {
	homeDir := t.TempDir()
	edited := strings.Replace(baseFixture, "FROM debian:bookworm-slim", "FROM ubuntu:24.04", 1)
	path := writeBaseFixture(t, homeDir, edited)

	prober := &stubProber{
		goResult:       newGoResolved("1.27.1"),
		correttoResult: newCorrettoResolved("25.0.3.11.1"),
	}
	stderr := &bytes.Buffer{}
	opts := newBaseOptions(t, homeDir, prober, &stubBuilder{})
	opts.Stderr = stderr

	res := RunBase(t.Context(), opts)
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0, error=%q", res.ExitCode, res.Error)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Contains(got, []byte("FROM ubuntu:24.04")) {
		t.Errorf("FROM line lost: %s", got)
	}
	if bytes.Contains(got, []byte("FROM debian:bookworm-slim")) {
		t.Error("FROM rewritten back to debian")
	}
	if strings.Contains(strings.ToLower(stderr.String()), "ubuntu") {
		t.Errorf("stderr mentioned ubuntu (unexpected warning): %q", stderr.String())
	}
}

// TestRunBase_RebuildUsesPullFlag — AC-27: the base rebuild call includes
// --pull.
func TestRunBase_RebuildUsesPullFlag(t *testing.T) {
	homeDir := t.TempDir()
	writeBaseFixture(t, homeDir, baseFixture)

	prober := &stubProber{
		goResult:       newGoResolved("1.27.1"),
		correttoResult: newCorrettoResolved("25.0.3.11.1"),
	}
	builder := &stubBuilder{}
	res := RunBase(t.Context(), newBaseOptions(t, homeDir, prober, builder))
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}

	foundPull := false
	for _, args := range builder.runCalls {
		if args[0] == "build" && containsArg(args, "--pull") {
			foundPull = true
			break
		}
	}
	if !foundPull {
		t.Errorf("no build call with --pull; runCalls=%v", builder.runCalls)
	}
}

// TestRunBase_RebuildFailureExitsOne — failed rebuild after successful
// rewrite returns exit 1 (file already rewritten; rollback is out of scope
// per PRD).
func TestRunBase_RebuildFailureExitsOne(t *testing.T) {
	homeDir := t.TempDir()
	path := writeBaseFixture(t, homeDir, baseFixture)

	prober := &stubProber{
		goResult:       newGoResolved("1.27.1"),
		correttoResult: newCorrettoResolved("25.0.3.11.1"),
	}
	builder := &stubBuilder{
		runResults: []error{errors.New("build failed")},
	}
	res := RunBase(t.Context(), newBaseOptions(t, homeDir, prober, builder))
	if res.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1, error=%q", res.ExitCode, res.Error)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Contains(got, []byte("ARG GO_VERSION=1.27.1")) {
		t.Error("file should remain rewritten after rebuild failure (no rollback)")
	}
}

// TestRunBase_GoUpstreamNotFound — probe failure yields exit 2 (probe
// failure class), not 3.
func TestRunBase_GoUpstreamNotFound(t *testing.T) {
	homeDir := t.TempDir()
	path := writeBaseFixture(t, homeDir, baseFixture)
	orig, _ := os.ReadFile(path)

	prober := &stubProber{
		goErr:          fmt.Errorf("wrap: %w", ErrUpstreamNotFound),
		correttoResult: newCorrettoResolved("25.0.3.11.1"),
	}
	res := RunBase(t.Context(), newBaseOptions(t, homeDir, prober, &stubBuilder{}))
	if res.ExitCode != 2 {
		t.Errorf("ExitCode = %d, want 2, error=%q", res.ExitCode, res.Error)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, orig) {
		t.Error("file rewritten despite probe failure")
	}
}

// containsArg reports whether args slice contains target.
func containsArg(args []string, target string) bool {
	return slices.Contains(args, target)
}

// Silence unused imports guard.
var _ = httptest.NewTLSServer
var _ = http.StatusOK
