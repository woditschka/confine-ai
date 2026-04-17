package assistant

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestBuildAssistantImage(t *testing.T) {
	t.Run("no-cache build emits --no-cache with correct args", func(t *testing.T) {
		builder := &fakeImageBuilder{
			runResults: []error{nil},
		}

		homeDir := "/home/user"
		err := BuildAssistantImage(t.Context(), builder, homeDir, "claude", BuildOptions{NoCache: true}, io.Discard)
		if err != nil {
			t.Fatalf("BuildAssistantImage() unexpected error: %v", err)
		}

		if len(builder.runCalls) != 1 {
			t.Fatalf("Run calls = %d, want 1", len(builder.runCalls))
		}
		args := builder.runCalls[0]

		wantTag := "confine-ai-assistant-claude:latest"
		wantAssistantDir := Dir(homeDir, "claude")
		wantDockerfile := DockerfilePath(homeDir, "claude")

		if !containsAll(args, "build", "--no-cache", "-t", wantTag, "-f", wantDockerfile, wantAssistantDir) {
			t.Errorf("Run args = %v, want containing build --no-cache -t %s -f %s %s",
				args, wantTag, wantDockerfile, wantAssistantDir)
		}

		// --pull must not be present: localhost/confine-ai-base:latest is local-only
		// and passing --pull makes podman fail re-resolving it against registries.
		for _, a := range args {
			if a == "--pull" {
				t.Errorf("Run args = %v, must not contain --pull", args)
			}
		}

		// The first arg must be "build"; the context (last positional) must be the assistant dir.
		if args[0] != "build" {
			t.Errorf("args[0] = %q, want %q", args[0], "build")
		}
		if args[len(args)-1] != wantAssistantDir {
			t.Errorf("last arg = %q, want %q", args[len(args)-1], wantAssistantDir)
		}
	})

	t.Run("cached build does not emit --no-cache", func(t *testing.T) {
		// The shortcut's first-use auto-ensure invokes BuildAssistantImage with
		// BuildOptions{} (cached). Cache-busting is exclusively update's job.
		builder := &fakeImageBuilder{
			runResults: []error{nil},
		}

		err := BuildAssistantImage(t.Context(), builder, "/home/user", "claude", BuildOptions{}, io.Discard)
		if err != nil {
			t.Fatalf("BuildAssistantImage() unexpected error: %v", err)
		}

		if len(builder.runCalls) != 1 {
			t.Fatalf("Run calls = %d, want 1", len(builder.runCalls))
		}
		args := builder.runCalls[0]

		if slices.Contains(args, "--no-cache") {
			t.Errorf("Run args = %v, must not contain --no-cache when BuildOptions.NoCache is false", args)
		}
		if !containsAll(args, "build", "-t", "confine-ai-assistant-claude:latest") {
			t.Errorf("Run args = %v, want build -t confine-ai-assistant-claude:latest", args)
		}
	})

	t.Run("builder failure propagates", func(t *testing.T) {
		builder := &fakeImageBuilder{
			runResults: []error{errors.New("build failed")},
		}

		err := BuildAssistantImage(t.Context(), builder, t.TempDir(), "claude", BuildOptions{NoCache: true}, io.Discard)
		if err == nil {
			t.Fatal("BuildAssistantImage() = nil, want error")
		}
		if !strings.Contains(err.Error(), "build assistant image") {
			t.Errorf("error = %q, want containing %q", err.Error(), "build assistant image")
		}
		if !strings.Contains(err.Error(), "claude") {
			t.Errorf("error = %q, want containing assistant name %q", err.Error(), "claude")
		}
	})

	t.Run("tag derives from assistant name", func(t *testing.T) {
		builder := &fakeImageBuilder{
			runResults: []error{nil},
		}

		if err := BuildAssistantImage(t.Context(), builder, t.TempDir(), "copilot", BuildOptions{NoCache: true}, io.Discard); err != nil {
			t.Fatalf("BuildAssistantImage() unexpected error: %v", err)
		}
		args := builder.runCalls[0]
		if !containsAll(args, "-t", "confine-ai-assistant-copilot:latest") {
			t.Errorf("Run args = %v, want containing -t confine-ai-assistant-copilot:latest", args)
		}
	})
}

func TestEnsureAssistantImage(t *testing.T) {
	t.Run("image exists skips build", func(t *testing.T) {
		builder := &fakeImageBuilder{
			outputResults: []outputResult{
				{output: "[{\"Id\": \"sha256:abc123\"}]", err: nil},
			},
		}

		var stderr bytes.Buffer
		err := EnsureAssistantImage(t.Context(), builder, "/home/user", "claude", &stderr)
		if err != nil {
			t.Fatalf("EnsureAssistantImage() unexpected error: %v", err)
		}

		if len(builder.outputCalls) != 1 {
			t.Fatalf("Output calls = %d, want 1", len(builder.outputCalls))
		}
		args := builder.outputCalls[0]
		if !containsAll(args, "image", "inspect", "confine-ai-assistant-claude:latest") {
			t.Errorf("Output args = %v, want containing image inspect confine-ai-assistant-claude:latest", args)
		}

		if len(builder.runCalls) != 0 {
			t.Errorf("Run calls = %d, want 0 (image exists, no build needed)", len(builder.runCalls))
		}

		// No breadcrumb when the image already exists.
		if stderr.Len() != 0 {
			t.Errorf("stderr = %q, want empty when image exists", stderr.String())
		}
	})

	t.Run("image missing triggers cached build with breadcrumb", func(t *testing.T) {
		// Seed a real Dockerfile so BuildAssistantImage's context arg points at
		// a valid directory. BuildAssistantImage itself reads nothing from the
		// filesystem; the assertion is purely on the subprocess args.
		builder := &fakeImageBuilder{
			outputResults: []outputResult{
				{output: "", err: errors.New("No such image")},
			},
			runResults: []error{nil},
		}

		var stderr bytes.Buffer
		err := EnsureAssistantImage(t.Context(), builder, "/home/user", "claude", &stderr)
		if err != nil {
			t.Fatalf("EnsureAssistantImage() unexpected error: %v", err)
		}

		// inspect was called first.
		if len(builder.outputCalls) != 1 {
			t.Fatalf("Output calls = %d, want 1", len(builder.outputCalls))
		}

		// build was called exactly once.
		if len(builder.runCalls) != 1 {
			t.Fatalf("Run calls = %d, want 1", len(builder.runCalls))
		}
		args := builder.runCalls[0]
		if !containsAll(args, "build", "-t", "confine-ai-assistant-claude:latest") {
			t.Errorf("Run args = %v, want containing build -t confine-ai-assistant-claude:latest", args)
		}

		// Cached build (REQ-AS-002 AC 18): must NOT pass --no-cache.
		if slices.Contains(args, "--no-cache") {
			t.Errorf("Run args = %v, must not contain --no-cache on auto-ensure path (cached build)", args)
		}

		// Breadcrumb (REQ-AS-002 AC 17).
		msg := stderr.String()
		if !strings.Contains(msg, "Assistant image") {
			t.Errorf("stderr = %q, want containing %q", msg, "Assistant image")
		}
		if !strings.Contains(msg, "confine-ai-assistant-claude:latest") {
			t.Errorf("stderr = %q, want containing tag %q", msg, "confine-ai-assistant-claude:latest")
		}
		if !strings.Contains(msg, "not found, building") {
			t.Errorf("stderr = %q, want containing %q", msg, "not found, building")
		}
	})

	t.Run("build failure propagates error", func(t *testing.T) {
		builder := &fakeImageBuilder{
			outputResults: []outputResult{
				{output: "", err: errors.New("No such image")},
			},
			runResults: []error{errors.New("build failed")},
		}

		err := EnsureAssistantImage(t.Context(), builder, "/home/user", "claude", io.Discard)
		if err == nil {
			t.Fatal("EnsureAssistantImage() = nil, want error for build failure")
		}
		if !strings.Contains(err.Error(), "build") {
			t.Errorf("EnsureAssistantImage() error = %q, want containing %q", err.Error(), "build")
		}
	})
}

// Compile-time interface assertion: fakeImageBuilder (defined in base_test.go)
// must satisfy ImageBuilder for BuildAssistantImage to accept it.
var _ ImageBuilder = (*fakeImageBuilder)(nil)
