package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestDiskHintWriter_TripsOnSentinel(t *testing.T) {
	var sink bytes.Buffer
	d := newDiskHintWriter(&sink)

	msg := "copyfile failed: ENOSPC: no space left on device, copyfile '/a' -> '/b'\n"
	if _, err := io.WriteString(d, msg); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if !d.Tripped() {
		t.Error("Tripped() = false, want true after writing sentinel")
	}
	if sink.String() != msg {
		t.Errorf("forwarded = %q, want %q", sink.String(), msg)
	}
}

func TestDiskHintWriter_DetectsAcrossWrites(t *testing.T) {
	var sink bytes.Buffer
	d := newDiskHintWriter(&sink)

	// Split the sentinel across two writes to exercise the tail buffer.
	parts := []string{"error: no space left", " on device while copying"}
	for _, p := range parts {
		if _, err := io.WriteString(d, p); err != nil {
			t.Fatalf("Write(%q) error: %v", p, err)
		}
	}
	if !d.Tripped() {
		t.Error("Tripped() = false, want true for sentinel split across writes")
	}
}

func TestDiskHintWriter_IgnoresUnrelatedOutput(t *testing.T) {
	var sink bytes.Buffer
	d := newDiskHintWriter(&sink)

	if _, err := io.WriteString(d, "STEP 1/2: FROM localhost/confine-ai-base:latest\nSTEP 2/2: RUN echo ok\n"); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if d.Tripped() {
		t.Error("Tripped() = true, want false for unrelated build output")
	}
}

// stderrWritingBuilder is a fake assistant.ImageBuilder that writes a canned
// message to the stderr writer it receives and then returns an error,
// modelling podman's behavior when the build layer runs out of space.
type stderrWritingBuilder struct {
	stderrMsg string
}

func (*stderrWritingBuilder) Output(_ context.Context, _ ...string) (string, error) {
	return "", errors.New("stderrWritingBuilder: Output not expected")
}

func (b *stderrWritingBuilder) Run(_ context.Context, _, stderr io.Writer, _ ...string) error {
	_, _ = io.WriteString(stderr, b.stderrMsg)
	return errors.New("exit status 1")
}

func TestRunAssistant_EmitsDiskHintOnENOSPC(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	builder := &stderrWritingBuilder{
		stderrMsg: "ENOSPC: no space left on device, copyfile '/x' -> '/y'\n",
	}
	executor := &fakeAssistantExecutor{}

	var stdout, stderr bytes.Buffer
	result := RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		Stdout:        &stdout,
		Stderr:        &stderr,
		Builder:       builder,
		Executor:      executor,
	})

	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
	}
	got := stderr.String()
	if !strings.Contains(got, "podman image prune -af") {
		t.Errorf("stderr = %q, want containing 'podman image prune -af'", got)
	}
	if !strings.Contains(got, "podman system prune -f") {
		t.Errorf("stderr = %q, want containing 'podman system prune -f'", got)
	}
}

func TestRunAssistant_NoDiskHintOnOtherFailure(t *testing.T) {
	homeDir := t.TempDir()
	seedAssistantDir(t, homeDir, "claude")

	builder := &stderrWritingBuilder{stderrMsg: "error: Dockerfile syntax error\n"}
	executor := &fakeAssistantExecutor{}

	var stdout, stderr bytes.Buffer
	RunAssistant(context.Background(), AssistantOptions{
		HomeDir:       homeDir,
		AssistantName: "claude",
		Stdout:        &stdout,
		Stderr:        &stderr,
		Builder:       builder,
		Executor:      executor,
	})

	if strings.Contains(stderr.String(), "podman system prune") {
		t.Errorf("stderr = %q, want no disk hint for unrelated failure", stderr.String())
	}
}
