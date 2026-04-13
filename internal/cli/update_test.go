package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woditschka/confine-ai/internal/assistant"
	"github.com/woditschka/confine-ai/internal/container"
	"github.com/woditschka/confine-ai/internal/update"
)

// testBaseDockerfile holds the bytes of samples/base/Dockerfile loaded from
// the repository root. runUpdateWithExecutor takes the base Dockerfile bytes
// as an explicit parameter (no package-level embed in internal/cli). Tests
// load it from the source tree once at package init and pass it into every
// call. The pinnedSeedProber values below are keyed to this file's contents.
var testBaseDockerfile = func() []byte {
	// cwd during `go test` is the package directory: internal/cli/
	data, err := os.ReadFile(filepath.Join("..", "..", "samples", "base", "Dockerfile"))
	if err != nil {
		panic("load samples/base/Dockerfile for tests: " + err.Error())
	}
	return data
}()

// ---------------------------------------------------------------------------
// runUpdateWithExecutor CLI dispatch tests.
//
// These tests exercise the dispatch, walk expansion, --dry-run, --yes, and
// exit-code aggregation logic of runUpdateWithExecutor by injecting fake
// executorFactory and proberFactory values. They deliberately bypass RunUpdate
// (which requires a real runtime) and talk to runUpdateWithExecutor directly.
// ---------------------------------------------------------------------------

// fakeProber is an update.Prober that returns canned Resolved values for Go
// and Corretto probes. Callers populate goResp/correttoResp or goErr/correttoErr
// to steer the orchestrator through the happy path, probe failure, or sha256
// failure branches without issuing real HTTP.
type fakeProber struct {
	goResp       update.Resolved
	goErr        error
	correttoResp update.Resolved
	correttoErr  error

	goCalls       int
	correttoCalls int
}

func (f *fakeProber) ProbeGo(_ context.Context, _ []string) (update.Resolved, error) {
	f.goCalls++
	if f.goErr != nil {
		return update.Resolved{}, f.goErr
	}
	return f.goResp, nil
}

func (f *fakeProber) ProbeCorretto(_ context.Context, _ string, _ []string) (update.Resolved, error) {
	f.correttoCalls++
	if f.correttoErr != nil {
		return update.Resolved{}, f.correttoErr
	}
	return f.correttoResp, nil
}

// pinnedSeedProber returns a fakeProber whose responses exactly match the
// pinned values in samples/base/Dockerfile (the embedded seed). Using it means
// RunBase observes "already latest" and reports ActionUnchanged with no
// rewrite. This is the steady-state "base: unchanged" posture most dispatch
// tests want: the assertions focus on walk ordering, target filtering, and
// exit-code aggregation — not probe semantics (those are covered by the
// unit tests in internal/update/base_test.go).
func pinnedSeedProber() *fakeProber {
	return &fakeProber{
		goResp: update.Resolved{
			Version: "1.26.2",
			Sha256: map[string]string{
				"amd64": "990e6b4bbba816dc3ee129eaeaf4b42f17c2800b88a2166c265ac1a200262282",
				"arm64": "c958a1fe1b361391db163a485e21f5f228142d6f8b584f6bef89b26f66dc5b23",
			},
		},
		correttoResp: update.Resolved{
			Version: "25.0.2.10.1",
			Sha256: map[string]string{
				"amd64": "313e9921e573cf28a4876ab039d56b3a142e7b1b1e847b0dddd170b8dee80387",
				"arm64": "6e966b3c3609c25f40e29d6cdb81f83f52a3723c8196a4c38e0d5d03e463c4e5",
			},
		},
	}
}

// seedUpdateHome wires up a temp HOME with ~/.confine-ai/base/Dockerfile
// populated from the embedded seed. Pass assistant names in `assistantsWithDF`
// to also create ~/.confine-ai/assistants/<name>/Dockerfile for each. Pass names
// in `assistantsNoDF` to create the assistant directory without the Dockerfile
// (broken assistants).
func seedUpdateHome(t *testing.T, assistantsWithDF, assistantsNoDF []string) string {
	t.Helper()
	homeDir := t.TempDir()

	basePath := assistant.BaseDockerfilePath(homeDir)
	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error: %v", filepath.Dir(basePath), err)
	}
	if err := os.WriteFile(basePath, testBaseDockerfile, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error: %v", basePath, err)
	}

	for _, name := range assistantsWithDF {
		if err := os.MkdirAll(assistant.Dir(homeDir, name), 0o755); err != nil {
			t.Fatalf("MkdirAll assistant dir: %v", err)
		}
		if err := os.WriteFile(assistant.DockerfilePath(homeDir, name),
			[]byte("FROM localhost/confine-ai-base:latest\n"), 0o644); err != nil {
			t.Fatalf("WriteFile assistant Dockerfile: %v", err)
		}
	}
	for _, name := range assistantsNoDF {
		if err := os.MkdirAll(assistant.Dir(homeDir, name), 0o755); err != nil {
			t.Fatalf("MkdirAll assistant dir: %v", err)
		}
	}
	return homeDir
}

// buildTagCounter returns the number of "build -t <tag>" invocations observed
// on builder.buildCalls for a specific tag. Used to assert whether a given
// target's rebuild actually fired.
func buildTagCounter(builder *updateCaptureBuilder, tag string) int {
	n := 0
	for _, call := range builder.buildCalls {
		if call == tag {
			n++
		}
	}
	return n
}

// captureOutputResult is a canned result for an Output invocation.
type captureOutputResult struct {
	output string
	err    error
}

// updateCaptureBuilder is a fake that records the -t <tag> value of every
// "build" invocation so tests can assert which targets were rebuilt. It does
// NOT read the Dockerfile from the build context (update tests don't need to
// verify bytes — the unit tests in internal/update already cover that). It
// satisfies both container.Executor and assistant.ImageBuilder.
type updateCaptureBuilder struct {
	// buildCalls collects the -t tag argument from every "build ..." Run
	// invocation in the order they occurred.
	buildCalls []string
	// runErr, when non-nil, is returned from any Run call. Set failOnTag
	// for per-target failure injection.
	runErr error
	// failOnTag, when non-empty, causes Run to return runErrFailOnTag when
	// a "build -t <failOnTag> ..." invocation is observed. Other builds
	// succeed.
	failOnTag       string
	runErrFailOnTag error

	// outputResults feeds Output calls (ps --filter label=... during
	// RemoveContainersByAssistant, etc.). If empty, Output returns "".
	outputResults []captureOutputResult
	outputIdx     int
}

func (u *updateCaptureBuilder) Output(_ context.Context, _ ...string) (string, error) {
	if u.outputIdx >= len(u.outputResults) {
		return "", nil
	}
	r := u.outputResults[u.outputIdx]
	u.outputIdx++
	return r.output, r.err
}

func (u *updateCaptureBuilder) Run(_ context.Context, _, _ io.Writer, args ...string) error {
	if len(args) == 0 {
		return errors.New("updateCaptureBuilder: Run called with no args")
	}
	if args[0] != "build" {
		// Non-build invocation (e.g., stop/rm inside
		// RemoveContainersByAssistant). With empty Output results this path
		// is not exercised; if it is, just succeed.
		return nil
	}
	// Extract the -t <tag> argument.
	var tag string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-t" {
			tag = args[i+1]
			break
		}
	}
	u.buildCalls = append(u.buildCalls, tag)
	if u.failOnTag != "" && tag == u.failOnTag {
		return u.runErrFailOnTag
	}
	return u.runErr
}

func (*updateCaptureBuilder) RunInteractive(_ context.Context, _ io.Reader, _, _ io.Writer, _ ...string) error {
	return nil
}

// exitCodeFromErr unwraps an *container.ExitError and returns its code. If err
// is nil it returns 0. If err is non-nil but not an ExitError, the test fails.
func exitCodeFromErr(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exitErr *container.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	t.Fatalf("expected *container.ExitError, got %T: %v", err, err)
	return -1
}

func TestRunUpdate_Dispatch(t *testing.T) {
	// AC-26: walk order is base, then assistants alphabetically.
	t.Run("walk order base then assistants alphabetically", func(t *testing.T) {
		homeDir := seedUpdateHome(t, []string{"zeta", "alpha", "mike"}, nil)

		var stdout, stderr bytes.Buffer
		builder := &updateCaptureBuilder{}
		execFactory := func() (container.Executor, error) { return builder, nil }
		probeFactory := func() update.Prober { return pinnedSeedProber() }

		err := runUpdateWithExecutor(t.Context(), &stdout, &stderr, execFactory, probeFactory, homeDir, nil, "dev", testBaseDockerfile)
		if code := exitCodeFromErr(t, err); code != 0 {
			t.Fatalf("runUpdateWithExecutor() exit = %d, want 0; stderr=%q", code, stderr.String())
		}

		// The summary should list base first, then alpha, mike, zeta in
		// that order. We assert on the order of header line positions.
		out := stdout.String()
		positions := make([]int, 4)
		for i, name := range []string{"base:", "alpha:", "mike:", "zeta:"} {
			pos := strings.Index(out, name)
			if pos < 0 {
				t.Fatalf("summary missing %q; stdout=%q", name, out)
			}
			positions[i] = pos
		}
		for i := 1; i < len(positions); i++ {
			if positions[i] <= positions[i-1] {
				t.Errorf("walk order violation: positions=%v; stdout=%q", positions, out)
			}
		}
	})

	// AC-22: no-arg walk with a broken assistant. Broken assistants are warned
	// about and skipped; healthy assistants still dispatch.
	t.Run("no-arg walk skips broken assistant with warning", func(t *testing.T) {
		homeDir := seedUpdateHome(t, []string{"claude"}, []string{"broken"})

		var stdout, stderr bytes.Buffer
		builder := &updateCaptureBuilder{}
		execFactory := func() (container.Executor, error) { return builder, nil }
		probeFactory := func() update.Prober { return pinnedSeedProber() }

		err := runUpdateWithExecutor(t.Context(), &stdout, &stderr, execFactory, probeFactory, homeDir, nil, "dev", testBaseDockerfile)
		if code := exitCodeFromErr(t, err); code != 0 {
			t.Fatalf("runUpdateWithExecutor() exit = %d, want 0; stderr=%q", code, stderr.String())
		}

		if !strings.Contains(stderr.String(), "skipping assistant \"broken\"") {
			t.Errorf("stderr = %q, want containing broken-assistant warning", stderr.String())
		}
		// broken was never dispatched: summary shows no "broken:" header.
		if strings.Contains(stdout.String(), "broken:") {
			t.Errorf("stdout = %q, broken assistant should never be dispatched", stdout.String())
		}
		// claude was dispatched and rebuilt.
		if got := buildTagCounter(builder, "confine-ai-assistant-claude:latest"); got != 1 {
			t.Errorf("claude build count = %d, want 1; buildCalls=%v", got, builder.buildCalls)
		}
		// base is unchanged with pinnedSeedProber: no base rebuild
		// invocation.
		if got := buildTagCounter(builder, "localhost/confine-ai-base:latest"); got != 0 {
			t.Errorf("base rebuild count = %d, want 0 (pinned probes should report unchanged)", got)
		}
	})

	// AC-23: walk halts on base probe failure. The Go probe errors, the
	// base target fails with exit 2, and NO assistants are dispatched.
	t.Run("walk halts on base probe failure", func(t *testing.T) {
		homeDir := seedUpdateHome(t, []string{"claude", "opencode"}, nil)

		var stdout, stderr bytes.Buffer
		builder := &updateCaptureBuilder{}
		execFactory := func() (container.Executor, error) { return builder, nil }
		probeFactory := func() update.Prober {
			p := pinnedSeedProber()
			p.goErr = errors.New("simulated upstream outage")
			return p
		}

		err := runUpdateWithExecutor(t.Context(), &stdout, &stderr, execFactory, probeFactory, homeDir, nil, "dev", testBaseDockerfile)
		if code := exitCodeFromErr(t, err); code != 2 {
			t.Fatalf("runUpdateWithExecutor() exit = %d, want 2; stderr=%q stdout=%q",
				code, stderr.String(), stdout.String())
		}

		// No assistant should have been rebuilt.
		for _, tag := range []string{"confine-ai-assistant-claude:latest", "confine-ai-assistant-opencode:latest"} {
			if got := buildTagCounter(builder, tag); got != 0 {
				t.Errorf("unexpected assistant rebuild %q (count=%d); walk should halt on base failure", tag, got)
			}
		}
		// Summary should mention base failure.
		if !strings.Contains(stdout.String(), "base: failed") {
			t.Errorf("stdout = %q, want containing 'base: failed'", stdout.String())
		}
	})

	// AC-24: walk continues across assistant rebuild failure. When one assistant
	// fails to rebuild, other assistants still run and the aggregate exit code
	// is 1.
	t.Run("walk continues across assistant rebuild failure", func(t *testing.T) {
		homeDir := seedUpdateHome(t, []string{"alpha", "bravo"}, nil)

		var stdout, stderr bytes.Buffer
		builder := &updateCaptureBuilder{
			failOnTag:       "confine-ai-assistant-alpha:latest",
			runErrFailOnTag: errors.New("simulated build failure"),
		}
		execFactory := func() (container.Executor, error) { return builder, nil }
		probeFactory := func() update.Prober { return pinnedSeedProber() }

		err := runUpdateWithExecutor(t.Context(), &stdout, &stderr, execFactory, probeFactory, homeDir, nil, "dev", testBaseDockerfile)
		if code := exitCodeFromErr(t, err); code != 1 {
			t.Fatalf("runUpdateWithExecutor() exit = %d, want 1; stdout=%q", code, stdout.String())
		}

		// Both assistants were attempted (alpha and bravo).
		if got := buildTagCounter(builder, "confine-ai-assistant-alpha:latest"); got != 1 {
			t.Errorf("alpha build count = %d, want 1", got)
		}
		if got := buildTagCounter(builder, "confine-ai-assistant-bravo:latest"); got != 1 {
			t.Errorf("bravo build count = %d, want 1 (walk should not halt on assistant failure)", got)
		}
	})

	// Explicit target "base" skips assistant walk entirely.
	t.Run("explicit base target skips assistant walk", func(t *testing.T) {
		homeDir := seedUpdateHome(t, []string{"claude"}, nil)

		var stdout, stderr bytes.Buffer
		builder := &updateCaptureBuilder{}
		execFactory := func() (container.Executor, error) { return builder, nil }
		probeFactory := func() update.Prober { return pinnedSeedProber() }

		err := runUpdateWithExecutor(t.Context(), &stdout, &stderr, execFactory, probeFactory, homeDir, []string{"base"}, "dev", testBaseDockerfile)
		if code := exitCodeFromErr(t, err); code != 0 {
			t.Fatalf("runUpdateWithExecutor() exit = %d, want 0; stderr=%q", code, stderr.String())
		}

		if strings.Contains(stdout.String(), "claude:") {
			t.Errorf("stdout = %q, explicit 'base' target must not dispatch assistants", stdout.String())
		}
		if !strings.Contains(stdout.String(), "base: unchanged") {
			t.Errorf("stdout = %q, want 'base: unchanged'", stdout.String())
		}
	})

	// Explicit target "<assistant>" dispatches RunAssistant.
	t.Run("explicit assistant target dispatches to RunAssistant", func(t *testing.T) {
		homeDir := seedUpdateHome(t, []string{"claude", "copilot"}, nil)

		var stdout, stderr bytes.Buffer
		builder := &updateCaptureBuilder{}
		execFactory := func() (container.Executor, error) { return builder, nil }
		probeFactory := func() update.Prober { return pinnedSeedProber() }

		err := runUpdateWithExecutor(t.Context(), &stdout, &stderr, execFactory, probeFactory, homeDir, []string{"claude"}, "dev", testBaseDockerfile)
		if code := exitCodeFromErr(t, err); code != 0 {
			t.Fatalf("runUpdateWithExecutor() exit = %d, want 0; stderr=%q", code, stderr.String())
		}

		if got := buildTagCounter(builder, "confine-ai-assistant-claude:latest"); got != 1 {
			t.Errorf("claude build count = %d, want 1", got)
		}
		// copilot was not requested: no copilot build.
		if got := buildTagCounter(builder, "confine-ai-assistant-copilot:latest"); got != 0 {
			t.Errorf("copilot build count = %d, want 0 (not requested)", got)
		}
		// base was not requested: no base rebuild.
		if got := buildTagCounter(builder, "localhost/confine-ai-base:latest"); got != 0 {
			t.Errorf("base rebuild count = %d, want 0 (not requested)", got)
		}
	})

	// Multiple explicit targets are processed in the order given.
	t.Run("multiple explicit targets run in argument order", func(t *testing.T) {
		homeDir := seedUpdateHome(t, []string{"claude"}, nil)

		var stdout, stderr bytes.Buffer
		builder := &updateCaptureBuilder{}
		execFactory := func() (container.Executor, error) { return builder, nil }
		probeFactory := func() update.Prober { return pinnedSeedProber() }

		err := runUpdateWithExecutor(t.Context(), &stdout, &stderr, execFactory, probeFactory, homeDir, []string{"base", "claude"}, "dev", testBaseDockerfile)
		if code := exitCodeFromErr(t, err); code != 0 {
			t.Fatalf("runUpdateWithExecutor() exit = %d, want 0; stderr=%q", code, stderr.String())
		}

		out := stdout.String()
		basePos := strings.Index(out, "base:")
		claudePos := strings.Index(out, "claude:")
		if basePos < 0 || claudePos < 0 {
			t.Fatalf("summary missing targets; stdout=%q", out)
		}
		if basePos > claudePos {
			t.Errorf("order: base at %d, claude at %d; want base before claude", basePos, claudePos)
		}
	})

	// --dry-run: no builder call for base or assistant targets.
	t.Run("dry-run does not invoke builder for any target", func(t *testing.T) {
		homeDir := seedUpdateHome(t, []string{"claude"}, nil)

		var stdout, stderr bytes.Buffer
		builder := &updateCaptureBuilder{}
		execFactory := func() (container.Executor, error) { return builder, nil }
		probeFactory := func() update.Prober { return pinnedSeedProber() }

		err := runUpdateWithExecutor(t.Context(), &stdout, &stderr, execFactory, probeFactory, homeDir, []string{"--dry-run"}, "dev", testBaseDockerfile)
		if code := exitCodeFromErr(t, err); code != 0 {
			t.Fatalf("runUpdateWithExecutor() exit = %d, want 0; stderr=%q", code, stderr.String())
		}

		if len(builder.buildCalls) != 0 {
			t.Errorf("builder received %d build calls in dry-run mode: %v", len(builder.buildCalls), builder.buildCalls)
		}
		if !strings.Contains(stdout.String(), "claude: would update") {
			t.Errorf("stdout = %q, want containing 'claude: would update'", stdout.String())
		}
	})

	// --yes: Java major-jump accepted without prompting. We use a probe
	// that reports a major-version bump from 25 to 26 and assert the
	// rewrite proceeds (summary shows "updated" and builder was called).
	t.Run("autoYes accepts Java major-version jump", func(t *testing.T) {
		homeDir := seedUpdateHome(t, nil, nil)

		var stdout, stderr bytes.Buffer
		builder := &updateCaptureBuilder{}
		execFactory := func() (container.Executor, error) { return builder, nil }
		probeFactory := func() update.Prober {
			p := pinnedSeedProber()
			// Report a major-version jump for Corretto: 25.x.x.x.x -> 26.0.0.0.1.
			p.correttoResp = update.Resolved{
				Version: "26.0.0.0.1",
				Sha256: map[string]string{
					"amd64": "1111111111111111111111111111111111111111111111111111111111111111",
					"arm64": "2222222222222222222222222222222222222222222222222222222222222222",
				},
			}
			return p
		}

		err := runUpdateWithExecutor(t.Context(), &stdout, &stderr, execFactory, probeFactory, homeDir, []string{"--yes", "base"}, "dev", testBaseDockerfile)
		if code := exitCodeFromErr(t, err); code != 0 {
			t.Fatalf("runUpdateWithExecutor() exit = %d, want 0; stderr=%q stdout=%q",
				code, stderr.String(), stdout.String())
		}

		// With autoYes, the major-version jump proceeds: base is rebuilt.
		if got := buildTagCounter(builder, "localhost/confine-ai-base:latest"); got != 1 {
			t.Errorf("base rebuild count = %d, want 1 (autoYes should accept major-version jump)", got)
		}
		if !strings.Contains(stdout.String(), "base: updated") {
			t.Errorf("stdout = %q, want containing 'base: updated'", stdout.String())
		}
	})

	// Unknown target fails via RunAssistant with exit 1.
	t.Run("unknown target fails via RunAssistant", func(t *testing.T) {
		homeDir := seedUpdateHome(t, nil, nil)

		var stdout, stderr bytes.Buffer
		builder := &updateCaptureBuilder{}
		execFactory := func() (container.Executor, error) { return builder, nil }
		probeFactory := func() update.Prober { return pinnedSeedProber() }

		err := runUpdateWithExecutor(t.Context(), &stdout, &stderr, execFactory, probeFactory, homeDir, []string{"nosuchthing"}, "dev", testBaseDockerfile)
		if code := exitCodeFromErr(t, err); code != 1 {
			t.Fatalf("runUpdateWithExecutor() exit = %d, want 1; stderr=%q stdout=%q",
				code, stderr.String(), stdout.String())
		}
		if !strings.Contains(stdout.String(), "nosuchthing: failed") {
			t.Errorf("stdout = %q, want containing 'nosuchthing: failed'", stdout.String())
		}
		if !strings.Contains(stdout.String(), "not found") {
			t.Errorf("stdout = %q, want containing 'not found'", stdout.String())
		}
	})
}
