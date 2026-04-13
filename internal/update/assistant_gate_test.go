package update

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// routingExecutor is a container.Executor fake tailored to the RunAssistant
// gate integration tests. It dispatches Output calls by the first argv
// token so image inspect, podman run (the gate's version probe), and ps
// (the REQ-AS-008 container drop) can be individually programmed.
type routingExecutor struct {
	outputs map[string]probeOutput // keyed by first argv token
	calls   [][]string
	runErr  error
}

func (r *routingExecutor) Output(_ context.Context, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if len(args) == 0 {
		return "", errors.New("routingExecutor: empty args")
	}
	if o, ok := r.outputs[args[0]]; ok {
		return o.stdout, o.err
	}
	return "", nil
}

func (r *routingExecutor) Run(_ context.Context, _, _ io.Writer, _ ...string) error {
	return r.runErr
}

func (*routingExecutor) RunInteractive(_ context.Context, _ io.Reader, _, _ io.Writer, _ ...string) error {
	return nil
}

// newTestTransportClient builds an update.Client whose transport trusts the
// supplied httptest TLS server.
func newTestTransportClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	c := NewClient("confine-ai/test")
	tr, ok := server.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("test server client transport is %T, want *http.Transport", server.Client().Transport)
	}
	tr.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    tr.TLSClientConfig.RootCAs,
	}
	c.httpClient.Transport = tr
	return c
}

// swapBaseURLs replaces all assistant probe base URLs with u for the
// duration of the test and restores them via t.Cleanup.
func swapBaseURLs(t *testing.T, u string) {
	t.Helper()
	prevClaude := claudeCodeNpmBaseURL
	prevCopilot := copilotNpmBaseURL
	prevOpencode := opencodeGitHubBaseURL
	claudeCodeNpmBaseURL = u
	copilotNpmBaseURL = u
	opencodeGitHubBaseURL = u
	t.Cleanup(func() {
		claudeCodeNpmBaseURL = prevClaude
		copilotNpmBaseURL = prevCopilot
		opencodeGitHubBaseURL = prevOpencode
	})
}

// newGateServer returns an httptest TLS server answering both npm and Go
// proxy upstream requests with the supplied version string.
func newGateServer(t *testing.T, version string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GitHub releases API returns tag_name; npm returns version.
		if strings.Contains(r.URL.Path, "/repos/") {
			_, _ = w.Write([]byte(`{"tag_name":"v` + version + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"version":"` + version + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ---- AC-1: real-run match skips rebuild and reports ActionUnchanged ----

func TestRunAssistant_GateMatch_RealRunSkipsRebuild(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	srv := newGateServer(t, "1.2.3")
	swapBaseURLs(t, srv.URL)
	client := newTestTransportClient(t, srv)

	exec := &routingExecutor{
		outputs: map[string]probeOutput{
			"image": {stdout: "sha256:abc\n"},
			"run":   {stdout: "1.2.3\n"},
			"ps":    {stdout: ""}, // no stale containers
		},
	}
	builder := &fakeAssistantBuilder{runErrors: []error{nil}}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		Stdout:        &stdout,
		Stderr:        &stderr,
		Executor:      exec,
		Builder:       builder,
		Client:        client,
	})

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, stderr.String())
	}
	if result.Action != ActionUnchanged {
		t.Errorf("Action = %q, want %q", result.Action, ActionUnchanged)
	}
	if !strings.Contains(stdout.String(), "claude already at 1.2.3") {
		t.Errorf("stdout = %q, want 'claude already at 1.2.3'", stdout.String())
	}
	if len(builder.runCalls) != 0 {
		t.Errorf("builder Run calls = %d, want 0 (rebuild must be skipped)", len(builder.runCalls))
	}
	// Assert the exact executor argv sequence for the probe: inspect + run.
	// The drop-stale-containers ps call must NOT happen because the gate
	// short-circuits before it.
	if len(exec.calls) < 2 {
		t.Fatalf("exec calls = %d, want at least 2 (inspect + run)", len(exec.calls))
	}
	inspect := exec.calls[0]
	if !sliceEqual(inspect, []string{"image", "inspect", "--format", "{{.Id}}", "confine-ai-assistant-claude:latest"}) {
		t.Errorf("inspect argv = %v, want [image inspect --format {{.Id}} confine-ai-assistant-claude:latest]", inspect)
	}
	run := exec.calls[1]
	want := []string{"run", "--rm", "--network=none", "--entrypoint", "", "confine-ai-assistant-claude:latest", "claude", "--version"}
	if !sliceEqual(run, want) {
		t.Errorf("run argv = %v, want %v", run, want)
	}
	for _, c := range exec.calls {
		if len(c) > 0 && c[0] == "ps" {
			t.Errorf("ps call issued after gate match; exec calls = %v", exec.calls)
		}
	}
}

// ---- AC-2: real-run mismatch falls through to rebuild ----

func TestRunAssistant_GateMismatch_RealRunRebuilds(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	srv := newGateServer(t, "1.2.4")
	swapBaseURLs(t, srv.URL)
	client := newTestTransportClient(t, srv)

	exec := &routingExecutor{
		outputs: map[string]probeOutput{
			"image": {stdout: "sha256:abc\n"},
			"run":   {stdout: "1.2.3\n"},
			"ps":    {stdout: ""},
		},
	}
	builder := &fakeAssistantBuilder{runErrors: []error{nil}}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		Stdout:        &stdout,
		Stderr:        &stderr,
		Executor:      exec,
		Builder:       builder,
		Client:        client,
	})

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, stderr.String())
	}
	if result.Action != ActionUpdated {
		t.Errorf("Action = %q, want %q", result.Action, ActionUpdated)
	}
	if !strings.Contains(stdout.String(), "rebuilding claude (1.2.3 -> 1.2.4)") {
		t.Errorf("stdout = %q, want 'rebuilding claude (1.2.3 -> 1.2.4)'", stdout.String())
	}
	if len(builder.runCalls) != 1 {
		t.Errorf("builder Run calls = %d, want 1 (rebuild must run on mismatch)", len(builder.runCalls))
	}
}

// ---- AC-3: dry-run match ----

func TestRunAssistant_GateMatch_DryRun(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	srv := newGateServer(t, "1.2.3")
	swapBaseURLs(t, srv.URL)
	client := newTestTransportClient(t, srv)

	exec := &routingExecutor{
		outputs: map[string]probeOutput{
			"image": {stdout: "sha256:abc\n"},
			"run":   {stdout: "1.2.3\n"},
		},
	}
	builder := &fakeAssistantBuilder{}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		DryRun:        true,
		Stdout:        &stdout,
		Stderr:        &stderr,
		Executor:      exec,
		Builder:       builder,
		Client:        client,
	})

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Action != ActionUnchanged {
		t.Errorf("Action = %q, want %q", result.Action, ActionUnchanged)
	}
	if !strings.Contains(stdout.String(), "claude already at 1.2.3") {
		t.Errorf("stdout = %q, want 'claude already at 1.2.3'", stdout.String())
	}
	if strings.Contains(stdout.String(), "would rebuild claude without cache") {
		t.Errorf("stdout = %q, must NOT include 'would rebuild claude without cache' on match", stdout.String())
	}
	if len(builder.runCalls) != 0 {
		t.Errorf("builder Run calls = %d, want 0 in dry-run", len(builder.runCalls))
	}
}

// ---- AC-4: dry-run mismatch ----

func TestRunAssistant_GateMismatch_DryRun(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	srv := newGateServer(t, "1.2.4")
	swapBaseURLs(t, srv.URL)
	client := newTestTransportClient(t, srv)

	exec := &routingExecutor{
		outputs: map[string]probeOutput{
			"image": {stdout: "sha256:abc\n"},
			"run":   {stdout: "1.2.3\n"},
		},
	}
	builder := &fakeAssistantBuilder{}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		DryRun:        true,
		Stdout:        &stdout,
		Stderr:        &stderr,
		Executor:      exec,
		Builder:       builder,
		Client:        client,
	})

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Action != ActionWouldUpdate {
		t.Errorf("Action = %q, want %q", result.Action, ActionWouldUpdate)
	}
	if !strings.Contains(stdout.String(), "would rebuild claude (1.2.3 -> 1.2.4)") {
		t.Errorf("stdout = %q, want 'would rebuild claude (1.2.3 -> 1.2.4)'", stdout.String())
	}
	// The existing "would rebuild <assistant> without cache" line must NOT be
	// emitted when the gate reports a dry-run mismatch (that line is for
	// the unregistered / probe-failure path).
	if strings.Contains(stdout.String(), "would rebuild claude without cache") {
		t.Errorf("stdout = %q, must NOT include 'would rebuild claude without cache' on gate mismatch", stdout.String())
	}
	if len(builder.runCalls) != 0 {
		t.Errorf("builder Run calls = %d, want 0 in dry-run", len(builder.runCalls))
	}
}

// ---- AC-5: installed image missing → warn + rebuild ----

func TestRunAssistant_GateImageMissing_Rebuilds(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	srv := newGateServer(t, "1.2.3")
	swapBaseURLs(t, srv.URL)
	client := newTestTransportClient(t, srv)

	exec := &routingExecutor{
		outputs: map[string]probeOutput{
			"image": {err: errors.New("no such image")},
			"run":   {stdout: "should-not-run"},
			"ps":    {stdout: ""},
		},
	}
	builder := &fakeAssistantBuilder{runErrors: []error{nil}}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		Stdout:        &stdout,
		Stderr:        &stderr,
		Executor:      exec,
		Builder:       builder,
		Client:        client,
	})

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Action != ActionUpdated {
		t.Errorf("Action = %q, want %q", result.Action, ActionUpdated)
	}
	if !strings.Contains(stderr.String(), "warning") || !strings.Contains(stderr.String(), "claude") {
		t.Errorf("stderr = %q, want warning naming claude", stderr.String())
	}
	if len(builder.runCalls) != 1 {
		t.Errorf("builder Run calls = %d, want 1 (rebuild must run after probe skip)", len(builder.runCalls))
	}
}

// ---- AC-6: --version unparseable → warn + rebuild ----

func TestRunAssistant_GateVersionUnparseable_Rebuilds(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	srv := newGateServer(t, "1.2.3")
	swapBaseURLs(t, srv.URL)
	client := newTestTransportClient(t, srv)

	exec := &routingExecutor{
		outputs: map[string]probeOutput{
			"image": {stdout: "sha256:abc\n"},
			"run":   {stdout: "hello world"},
			"ps":    {stdout: ""},
		},
	}
	builder := &fakeAssistantBuilder{runErrors: []error{nil}}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		Stdout:        &stdout,
		Stderr:        &stderr,
		Executor:      exec,
		Builder:       builder,
		Client:        client,
	})

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Action != ActionUpdated {
		t.Errorf("Action = %q, want %q", result.Action, ActionUpdated)
	}
	if !strings.Contains(stderr.String(), "warning") {
		t.Errorf("stderr = %q, want warning", stderr.String())
	}
	if len(builder.runCalls) != 1 {
		t.Errorf("builder Run calls = %d, want 1", len(builder.runCalls))
	}
}

// ---- AC-7: upstream failure → warn + rebuild ----

func TestRunAssistant_GateUpstreamFailure_Rebuilds(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	swapBaseURLs(t, srv.URL)
	client := newTestTransportClient(t, srv)

	exec := &routingExecutor{
		outputs: map[string]probeOutput{
			"image": {stdout: "sha256:abc\n"},
			"run":   {stdout: "1.2.3\n"},
			"ps":    {stdout: ""},
		},
	}
	builder := &fakeAssistantBuilder{runErrors: []error{nil}}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		Stdout:        &stdout,
		Stderr:        &stderr,
		Executor:      exec,
		Builder:       builder,
		Client:        client,
	})

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Action != ActionUpdated {
		t.Errorf("Action = %q, want %q", result.Action, ActionUpdated)
	}
	if !strings.Contains(stderr.String(), "warning") {
		t.Errorf("stderr = %q, want warning", stderr.String())
	}
	if len(builder.runCalls) != 1 {
		t.Errorf("builder Run calls = %d, want 1", len(builder.runCalls))
	}
}

// ---- AC-8: copilot and opencode now have probes and exercise the gate. ----

func TestRunAssistant_AllAssistants_GateMatch(t *testing.T) {
	for _, name := range []string{"claude", "copilot", "opencode"} {
		t.Run(name, func(t *testing.T) {
			homeDir := t.TempDir()
			seedAssistantDir(t, homeDir, name)

			srv := newGateServer(t, "2.0.0")
			swapBaseURLs(t, srv.URL)
			client := newTestTransportClient(t, srv)

			exec := &routingExecutor{
				outputs: map[string]probeOutput{
					"image": {stdout: "sha256:abc\n"},
					"run":   {stdout: "2.0.0\n"},
					"ps":    {stdout: ""},
				},
			}
			builder := &fakeAssistantBuilder{runErrors: []error{nil}}

			var stdout, stderr bytes.Buffer
			result := RunAssistant(context.Background(), AssistantOptions{
				HomeDir:       homeDir,
				AssistantName: name,
				Stdout:        &stdout,
				Stderr:        &stderr,
				Executor:      exec,
				Builder:       builder,
				Client:        client,
			})

			if result.ExitCode != 0 {
				t.Errorf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, stderr.String())
			}
			if result.Action != ActionUnchanged {
				t.Errorf("Action = %q, want %q", result.Action, ActionUnchanged)
			}
			if !strings.Contains(stdout.String(), name+" already at 2.0.0") {
				t.Errorf("stdout = %q, want '%s already at 2.0.0'", stdout.String(), name)
			}
			if len(builder.runCalls) != 0 {
				t.Errorf("builder Run calls = %d, want 0 (rebuild must be skipped on match)", len(builder.runCalls))
			}
			// Verify the gate issued the image inspect and run calls with
			// the correct image tag and version command.
			wantTag := "confine-ai-assistant-" + name + ":latest"
			if len(exec.calls) < 2 {
				t.Fatalf("exec calls = %d, want at least 2", len(exec.calls))
			}
			inspect := exec.calls[0]
			if !sliceEqual(inspect, []string{"image", "inspect", "--format", "{{.Id}}", wantTag}) {
				t.Errorf("inspect argv = %v, want tag %s", inspect, wantTag)
			}
			run := exec.calls[1]
			spec := lookupProbeSpec(name)
			wantRun := append([]string{"run", "--rm", "--network=none", "--entrypoint", "", wantTag}, spec.versionCmd...)
			if !sliceEqual(run, wantRun) {
				t.Errorf("run argv = %v, want %v", run, wantRun)
			}
		})
	}
}

func TestRunAssistant_AllAssistants_GateMismatchRebuilds(t *testing.T) {
	for _, name := range []string{"claude", "copilot", "opencode"} {
		t.Run(name, func(t *testing.T) {
			homeDir := t.TempDir()
			seedAssistantDir(t, homeDir, name)

			srv := newGateServer(t, "2.0.1")
			swapBaseURLs(t, srv.URL)
			client := newTestTransportClient(t, srv)

			exec := &routingExecutor{
				outputs: map[string]probeOutput{
					"image": {stdout: "sha256:abc\n"},
					"run":   {stdout: "2.0.0\n"},
					"ps":    {stdout: ""},
				},
			}
			builder := &fakeAssistantBuilder{runErrors: []error{nil}}

			var stdout, stderr bytes.Buffer
			result := RunAssistant(context.Background(), AssistantOptions{
				HomeDir:       homeDir,
				AssistantName: name,
				Stdout:        &stdout,
				Stderr:        &stderr,
				Executor:      exec,
				Builder:       builder,
				Client:        client,
			})

			if result.ExitCode != 0 {
				t.Errorf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, stderr.String())
			}
			if result.Action != ActionUpdated {
				t.Errorf("Action = %q, want %q", result.Action, ActionUpdated)
			}
			if !strings.Contains(stdout.String(), "rebuilding "+name+" (2.0.0 -> 2.0.1)") {
				t.Errorf("stdout = %q, want 'rebuilding %s (2.0.0 -> 2.0.1)'", stdout.String(), name)
			}
			if len(builder.runCalls) != 1 {
				t.Errorf("builder Run calls = %d, want 1", len(builder.runCalls))
			}
		})
	}
}

// ---- AC-9: unregistered assistants still bypass the gate. ----

func TestRunAssistant_UnregisteredAssistant_BypassesGate(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "my-custom-tool")

	srv := newGateServer(t, "9.9.9")
	swapBaseURLs(t, srv.URL)
	client := newTestTransportClient(t, srv)

	exec := &routingExecutor{
		outputs: map[string]probeOutput{
			"ps": {stdout: ""},
		},
	}
	builder := &fakeAssistantBuilder{runErrors: []error{nil}}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "my-custom-tool",
		Stdout:        &stdout,
		Stderr:        &stderr,
		Executor:      exec,
		Builder:       builder,
		Client:        client,
	})

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, stderr.String())
	}
	if result.Action != ActionUpdated {
		t.Errorf("Action = %q, want %q", result.Action, ActionUpdated)
	}
	if len(builder.runCalls) != 1 {
		t.Errorf("builder Run calls = %d, want 1", len(builder.runCalls))
	}
	if strings.Contains(stderr.String(), "version gate skipped") {
		t.Errorf("stderr = %q, must not mention gate for unregistered assistant", stderr.String())
	}
	// The executor must only have been used for the ps (container
	// drop) call; no image inspect / run for the gate.
	for _, c := range exec.calls {
		if len(c) > 0 && (c[0] == "image" || c[0] == "run") {
			t.Errorf("unregistered assistant issued gate call %v", c)
		}
	}
}

// ---- Dry-run: unregistered assistants print "would rebuild ... without cache". ----

func TestRunAssistant_UnregisteredAssistant_DryRunUnchanged(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "my-custom-tool")

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "my-custom-tool",
		DryRun:        true,
		Stdout:        &stdout,
		Stderr:        &stderr,
		Builder:       &fakeAssistantBuilder{},
		Executor:      &fakeAssistantExecutor{},
	})
	if result.Action != ActionWouldUpdate {
		t.Errorf("Action = %q, want %q", result.Action, ActionWouldUpdate)
	}
	if !strings.Contains(stdout.String(), "would rebuild my-custom-tool without cache") {
		t.Errorf("stdout = %q, want existing dry-run line", stdout.String())
	}
}

// ---- AC-10: gate probe failures never produce an error return. ----
// (Implied by every failure test above always checking ExitCode==0.)

// ---- AC-11: no-client path still works (gate is bypassed). ----

func TestRunAssistant_NilClient_BypassesGate(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	exec := &routingExecutor{
		outputs: map[string]probeOutput{
			"ps": {stdout: ""},
		},
	}
	builder := &fakeAssistantBuilder{runErrors: []error{nil}}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		Stdout:        &stdout,
		Stderr:        &stderr,
		Executor:      exec,
		Builder:       builder,
		// Client: nil — the gate must be skipped entirely.
	})

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, stderr.String())
	}
	if result.Action != ActionUpdated {
		t.Errorf("Action = %q, want %q", result.Action, ActionUpdated)
	}
	if len(builder.runCalls) != 1 {
		t.Errorf("builder Run calls = %d, want 1", len(builder.runCalls))
	}
	// No image/run calls issued when gate is bypassed.
	for _, c := range exec.calls {
		if len(c) > 0 && (c[0] == "image" || c[0] == "run") {
			t.Errorf("nil-Client gate still issued call %v", c)
		}
	}
}
