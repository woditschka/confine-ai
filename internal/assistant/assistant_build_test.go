package assistant

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestBuildAssistantImage(t *testing.T) {
	t.Run("builds with correct args", func(t *testing.T) {
		builder := &fakeImageBuilder{
			runResults: []error{nil},
		}

		homeDir := "/home/user"
		err := BuildAssistantImage(t.Context(), builder, homeDir, "claude", io.Discard)
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

	t.Run("builder failure propagates", func(t *testing.T) {
		builder := &fakeImageBuilder{
			runResults: []error{errors.New("build failed")},
		}

		err := BuildAssistantImage(t.Context(), builder, "/tmp", "claude", io.Discard)
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

		if err := BuildAssistantImage(t.Context(), builder, "/tmp", "copilot", io.Discard); err != nil {
			t.Fatalf("BuildAssistantImage() unexpected error: %v", err)
		}
		args := builder.runCalls[0]
		if !containsAll(args, "-t", "confine-ai-assistant-copilot:latest") {
			t.Errorf("Run args = %v, want containing -t confine-ai-assistant-copilot:latest", args)
		}
	})
}

// Compile-time interface assertion: fakeImageBuilder (defined in base_test.go)
// must satisfy ImageBuilder for BuildAssistantImage to accept it.
var _ ImageBuilder = (*fakeImageBuilder)(nil)
