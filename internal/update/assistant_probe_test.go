package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeProbeExecutor is a narrow container.Executor fake dedicated to the
// assistant-probe tests. It records the full argv sequence of every Output call
// and returns canned stdout / errors by index so a test can assert both the
// exact commands issued and the gate's reaction to failures.
type fakeProbeExecutor struct {
	outputCalls [][]string
	// outputs is a FIFO queue of stdout / error pairs, one per Output call.
	outputs []probeOutput
	idx     int
}

type probeOutput struct {
	stdout string
	err    error
}

func (f *fakeProbeExecutor) Output(_ context.Context, args ...string) (string, error) {
	f.outputCalls = append(f.outputCalls, append([]string(nil), args...))
	if f.idx >= len(f.outputs) {
		return "", errors.New("fakeProbeExecutor: no more outputs queued")
	}
	o := f.outputs[f.idx]
	f.idx++
	return o.stdout, o.err
}

func (*fakeProbeExecutor) Run(_ context.Context, _, _ io.Writer, _ ...string) error {
	return nil
}

func (*fakeProbeExecutor) RunInteractive(_ context.Context, _ io.Reader, _, _ io.Writer, _ ...string) error {
	return nil
}

func TestParseInstalledVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{name: "bare semver", in: "1.2.3", want: "1.2.3", ok: true},
		{name: "semver with newline", in: "1.2.3\n", want: "1.2.3", ok: true},
		{name: "leading v", in: "v1.2.3\n", want: "1.2.3", ok: true},
		{name: "three-part with patch", in: "0.0.17", want: "0.0.17", ok: true},
		{name: "embedded in phrase", in: "claude 1.2.3 (build abcdef)", want: "1.2.3", ok: true},
		{name: "prerelease", in: "1.2.3-beta.4", want: "1.2.3-beta.4", ok: true},
		{name: "leading whitespace", in: "   1.2.3\n", want: "1.2.3", ok: true},
		{name: "empty", in: "", want: "", ok: false},
		{name: "whitespace only", in: "   \n", want: "", ok: false},
		{name: "banner before version", in: "Welcome to opencode\n0.0.17\n", want: "0.0.17", ok: true},
		{name: "unparseable", in: "hello world", want: "", ok: false},
		{name: "no dots", in: "42", want: "", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseInstalledVersion(tc.in)
			if got != tc.want || ok != tc.ok {
				t.Errorf("parseInstalledVersion(%q) = (%q, %v), want (%q, %v)",
					tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestProbeInstalledVersion_HappyPath(t *testing.T) {
	exec := &fakeProbeExecutor{
		outputs: []probeOutput{
			{stdout: "sha256:abc\n"}, // image inspect → present
			{stdout: "1.2.3\n"},      // podman run → version
		},
	}
	tag := "confine-ai-assistant-claude:latest"
	got, err := probeInstalledVersion(t.Context(), exec, tag, []string{"claude", "--version"})
	if err != nil {
		t.Fatalf("probeInstalledVersion unexpected error: %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("probeInstalledVersion = %q, want 1.2.3", got)
	}
	if len(exec.outputCalls) != 2 {
		t.Fatalf("outputCalls = %d, want 2", len(exec.outputCalls))
	}
	// First call: image inspect with the computed tag
	inspect := exec.outputCalls[0]
	if !sliceEqual(inspect, []string{"image", "inspect", "--format", "{{.Id}}", tag}) {
		t.Errorf("inspect argv = %v, want [image inspect --format {{.Id}} %s]", inspect, tag)
	}
	// Second call: podman run --rm --network=none --entrypoint "" <tag> claude --version
	run := exec.outputCalls[1]
	want := []string{"run", "--rm", "--network=none", "--entrypoint", "", tag, "claude", "--version"}
	if !sliceEqual(run, want) {
		t.Errorf("run argv = %v, want %v", run, want)
	}
}

func TestProbeInstalledVersion_ImageMissing(t *testing.T) {
	exec := &fakeProbeExecutor{
		outputs: []probeOutput{
			{err: errors.New("no such image")},
		},
	}
	_, err := probeInstalledVersion(t.Context(), exec, "confine-ai-assistant-claude:latest", []string{"claude", "--version"})
	if err == nil {
		t.Fatal("probeInstalledVersion(image missing) = nil, want error")
	}
	if len(exec.outputCalls) != 1 {
		t.Errorf("outputCalls = %d, want 1 (run must not be attempted when inspect fails)", len(exec.outputCalls))
	}
}

func TestProbeInstalledVersion_RunFailure(t *testing.T) {
	exec := &fakeProbeExecutor{
		outputs: []probeOutput{
			{stdout: "sha256:abc\n"},
			{err: errors.New("exit 1")},
		},
	}
	if _, err := probeInstalledVersion(t.Context(), exec, "confine-ai-assistant-claude:latest", []string{"claude", "--version"}); err == nil {
		t.Fatal("probeInstalledVersion(run fail) = nil, want error")
	}
}

func TestProbeInstalledVersion_UnparseableOutput(t *testing.T) {
	exec := &fakeProbeExecutor{
		outputs: []probeOutput{
			{stdout: "sha256:abc\n"},
			{stdout: "hello world\n"},
		},
	}
	if _, err := probeInstalledVersion(t.Context(), exec, "confine-ai-assistant-claude:latest", []string{"claude", "--version"}); err == nil {
		t.Fatal("probeInstalledVersion(unparseable) = nil, want error")
	}
}

// --------- Gate helper tests ----------

// npmTestServer returns an httptest TLS server that answers the npm `/latest`
// request with the supplied version string. A version of "" triggers a missing-field
// response so the gate sees an upstream failure.
func npmTestServer(t *testing.T, version string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if version == "" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`{"version":"` + version + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newTestConfiguredProbe wires a configuredProbe whose upstream adapter
// points at the given httptest server instead of the real registry.
func newTestConfiguredProbe(t *testing.T, name string, versionCmd []string, srv *httptest.Server) *configuredProbe {
	t.Helper()
	client := newTestClient(t, srv)
	return &configuredProbe{
		name:       name,
		versionCmd: versionCmd,
		upstream:   NewNpmLatestUpstream(client, claudeCodePackage, srv.URL),
	}
}

func TestRunAssistantGate_MatchRealRun(t *testing.T) {
	srv := npmTestServer(t, "1.2.3")
	probe := newTestConfiguredProbe(t, "claude", []string{"claude", "--version"}, srv)

	exec := &fakeProbeExecutor{
		outputs: []probeOutput{
			{stdout: "sha256:abc\n"},
			{stdout: "1.2.3\n"},
		},
	}
	var stdout, stderr bytes.Buffer
	result, handled := runAssistantGate(t.Context(), probe, gateInputs{
		AssistantName: "claude",
		Executor:      exec,
		DryRun:        false,
		Stdout:        &stdout,
		Stderr:        &stderr,
	})
	if !handled {
		t.Fatalf("handled = false, want true (match should short-circuit)")
	}
	if result.Action != ActionUnchanged {
		t.Errorf("Action = %q, want %q", result.Action, ActionUnchanged)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if !strings.Contains(stdout.String(), "claude already at 1.2.3") {
		t.Errorf("stdout = %q, want containing 'claude already at 1.2.3'", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty on match", stderr.String())
	}
}

func TestRunAssistantGate_MatchDryRun(t *testing.T) {
	srv := npmTestServer(t, "1.2.3")
	probe := newTestConfiguredProbe(t, "claude", []string{"claude", "--version"}, srv)

	exec := &fakeProbeExecutor{
		outputs: []probeOutput{
			{stdout: "sha256:abc\n"},
			{stdout: "1.2.3\n"},
		},
	}
	var stdout, stderr bytes.Buffer
	result, handled := runAssistantGate(t.Context(), probe, gateInputs{
		AssistantName: "claude",
		Executor:      exec,
		DryRun:        true,
		Stdout:        &stdout,
		Stderr:        &stderr,
	})
	if !handled {
		t.Fatal("handled = false, want true for dry-run match")
	}
	if result.Action != ActionUnchanged {
		t.Errorf("Action = %q, want %q", result.Action, ActionUnchanged)
	}
	if !strings.Contains(stdout.String(), "claude already at 1.2.3") {
		t.Errorf("stdout = %q, want 'claude already at 1.2.3'", stdout.String())
	}
}

func TestRunAssistantGate_MismatchRealRun(t *testing.T) {
	srv := npmTestServer(t, "1.2.4")
	probe := newTestConfiguredProbe(t, "claude", []string{"claude", "--version"}, srv)

	exec := &fakeProbeExecutor{
		outputs: []probeOutput{
			{stdout: "sha256:abc\n"},
			{stdout: "1.2.3\n"},
		},
	}
	var stdout, stderr bytes.Buffer
	_, handled := runAssistantGate(t.Context(), probe, gateInputs{
		AssistantName: "claude",
		Executor:      exec,
		DryRun:        false,
		Stdout:        &stdout,
		Stderr:        &stderr,
	})
	if handled {
		t.Fatal("handled = true, want false (mismatch should fall through to rebuild)")
	}
	if !strings.Contains(stdout.String(), "rebuilding claude (1.2.3 -> 1.2.4)") {
		t.Errorf("stdout = %q, want 'rebuilding claude (1.2.3 -> 1.2.4)'", stdout.String())
	}
}

func TestRunAssistantGate_MismatchDryRun(t *testing.T) {
	srv := npmTestServer(t, "1.2.4")
	probe := newTestConfiguredProbe(t, "claude", []string{"claude", "--version"}, srv)

	exec := &fakeProbeExecutor{
		outputs: []probeOutput{
			{stdout: "sha256:abc\n"},
			{stdout: "1.2.3\n"},
		},
	}
	var stdout, stderr bytes.Buffer
	result, handled := runAssistantGate(t.Context(), probe, gateInputs{
		AssistantName: "claude",
		Executor:      exec,
		DryRun:        true,
		Stdout:        &stdout,
		Stderr:        &stderr,
	})
	if !handled {
		t.Fatal("handled = false, want true for dry-run mismatch (it must short-circuit the rebuild)")
	}
	if result.Action != ActionWouldUpdate {
		t.Errorf("Action = %q, want %q", result.Action, ActionWouldUpdate)
	}
	if !strings.Contains(stdout.String(), "would rebuild claude (1.2.3 -> 1.2.4)") {
		t.Errorf("stdout = %q, want 'would rebuild claude (1.2.3 -> 1.2.4)'", stdout.String())
	}
}

func TestRunAssistantGate_InstalledProbeFailure(t *testing.T) {
	srv := npmTestServer(t, "1.2.3")
	probe := newTestConfiguredProbe(t, "claude", []string{"claude", "--version"}, srv)

	exec := &fakeProbeExecutor{
		outputs: []probeOutput{
			{err: errors.New("no such image")},
		},
	}
	var stdout, stderr bytes.Buffer
	_, handled := runAssistantGate(t.Context(), probe, gateInputs{
		AssistantName: "claude",
		Executor:      exec,
		DryRun:        false,
		Stdout:        &stdout,
		Stderr:        &stderr,
	})
	if handled {
		t.Error("handled = true, want false (probe failure must fall through)")
	}
	if !strings.Contains(stderr.String(), "warning") || !strings.Contains(stderr.String(), "claude") {
		t.Errorf("stderr = %q, want a warning naming claude", stderr.String())
	}
}

func TestRunAssistantGate_UpstreamProbeFailure(t *testing.T) {
	// Server that always errors — any Client.Get returns an error.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	probe := newTestConfiguredProbe(t, "claude", []string{"claude", "--version"}, srv)

	exec := &fakeProbeExecutor{
		outputs: []probeOutput{
			{stdout: "sha256:abc\n"},
			{stdout: "1.2.3\n"},
		},
	}
	var stdout, stderr bytes.Buffer
	_, handled := runAssistantGate(t.Context(), probe, gateInputs{
		AssistantName: "claude",
		Executor:      exec,
		DryRun:        false,
		Stdout:        &stdout,
		Stderr:        &stderr,
	})
	if handled {
		t.Error("handled = true, want false (upstream failure must fall through)")
	}
	if !strings.Contains(stderr.String(), "warning") {
		t.Errorf("stderr = %q, want a warning", stderr.String())
	}
}

// --------- Spec registry tests ----------

func TestProbeSpecRegistry_AllKnownAssistants(t *testing.T) {
	for _, name := range []string{"claude", "copilot", "opencode"} {
		if spec := lookupProbeSpec(name); spec == nil {
			t.Errorf("lookupProbeSpec(%q) = nil, want non-nil", name)
		}
		if !HasAssistantProbe(name) {
			t.Errorf("HasAssistantProbe(%q) = false, want true", name)
		}
	}
}

func TestProbeSpecRegistry_UnknownAssistant(t *testing.T) {
	if spec := lookupProbeSpec("unknown"); spec != nil {
		t.Errorf("lookupProbeSpec(unknown) = %v, want nil", spec)
	}
	if HasAssistantProbe("unknown") {
		t.Error("HasAssistantProbe(unknown) = true, want false")
	}
}

func TestProbeSpecRegistry_VersionCommands(t *testing.T) {
	cases := []struct {
		name    string
		wantCmd []string
	}{
		{"claude", []string{"claude", "--version"}},
		{"copilot", []string{"copilot", "--version"}},
		{"opencode", []string{"opencode", "--version"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := lookupProbeSpec(tc.name)
			if spec == nil {
				t.Fatalf("lookupProbeSpec(%q) = nil", tc.name)
				return // unreachable; satisfies staticcheck SA5011
			}
			if !sliceEqual(spec.versionCmd, tc.wantCmd) {
				t.Errorf("versionCmd = %v, want %v", spec.versionCmd, tc.wantCmd)
			}
		})
	}
}

func TestConfiguredProbe_UsesCorrectImageTag(t *testing.T) {
	// Verify the configuredProbe computes the right image tag and version
	// command for each registered assistant.
	for _, name := range []string{"claude", "copilot", "opencode"} {
		t.Run(name, func(t *testing.T) {
			srv := npmTestServer(t, "1.0.0")
			spec := lookupProbeSpec(name)
			client := newTestClient(t, srv)
			// Override the upstream to npm for simplicity; we're testing
			// the image tag and version command wiring, not the upstream.
			probe := &configuredProbe{
				name:       name,
				versionCmd: spec.versionCmd,
				upstream:   NewNpmLatestUpstream(client, "test-pkg", srv.URL),
			}
			exec := &fakeProbeExecutor{
				outputs: []probeOutput{
					{stdout: "sha256:abc\n"},
					{stdout: "1.0.0\n"},
				},
			}
			installed, upstream, err := probe.Probe(t.Context(), exec)
			if err != nil {
				t.Fatalf("Probe unexpected error: %v", err)
			}
			if installed != "1.0.0" || upstream != "1.0.0" {
				t.Errorf("Probe = (%q, %q), want (1.0.0, 1.0.0)", installed, upstream)
			}
			// Verify the image inspect used the correct tag.
			wantTag := "confine-ai-assistant-" + name + ":latest"
			inspect := exec.outputCalls[0]
			if !sliceEqual(inspect, []string{"image", "inspect", "--format", "{{.Id}}", wantTag}) {
				t.Errorf("inspect argv = %v, want tag %s", inspect, wantTag)
			}
			// Verify the run command includes the correct version command.
			run := exec.outputCalls[1]
			wantRun := append([]string{"run", "--rm", "--network=none", "--entrypoint", "", wantTag}, spec.versionCmd...)
			if !sliceEqual(run, wantRun) {
				t.Errorf("run argv = %v, want %v", run, wantRun)
			}
		})
	}
}

// sliceEqual reports whether a and b have identical content.
func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
