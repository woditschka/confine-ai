package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCompletion(t *testing.T) {
	t.Run("bash generates script", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := RunCompletion(&stdout, &stderr, []string{"bash"})
		if err != nil {
			t.Fatalf("RunCompletion(bash) unexpected error: %v", err)
		}
		if !strings.Contains(stdout.String(), "confine-ai") {
			t.Errorf("RunCompletion(bash) stdout = %q, want containing %q", stdout.String(), "confine-ai")
		}
	})

	t.Run("zsh generates script", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := RunCompletion(&stdout, &stderr, []string{"zsh"})
		if err != nil {
			t.Fatalf("RunCompletion(zsh) unexpected error: %v", err)
		}
		if !strings.Contains(stdout.String(), "confine-ai") {
			t.Errorf("RunCompletion(zsh) stdout = %q, want containing %q", stdout.String(), "confine-ai")
		}
	})

	t.Run("missing shell returns error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := RunCompletion(&stdout, &stderr, nil)
		if err == nil {
			t.Fatal("RunCompletion(nil) = nil, want error")
		}
		if !strings.Contains(err.Error(), "shell argument required") {
			t.Errorf("RunCompletion(nil) error = %q, want containing %q", err.Error(), "shell argument required")
		}
	})

	t.Run("unsupported shell returns error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := RunCompletion(&stdout, &stderr, []string{"fish"})
		if err == nil {
			t.Fatal("RunCompletion(fish) = nil, want error")
		}
	})

	t.Run("help flag returns nil", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := RunCompletion(&stdout, &stderr, []string{"--help"})
		if err != nil {
			t.Fatalf("RunCompletion(--help) unexpected error: %v", err)
		}
	})
}

func TestRunComplete(t *testing.T) {
	fakeDockerfiles := map[string][]byte{
		"claude":  []byte("FROM base\n"),
		"copilot": []byte("FROM base\n"),
	}

	t.Run("first argument suggests subcommands and assistants", func(t *testing.T) {
		var stdout bytes.Buffer
		err := RunComplete(&stdout, []string{"--", ""}, fakeDockerfiles)
		if err != nil {
			t.Fatalf("RunComplete() unexpected error: %v", err)
		}
		output := stdout.String()
		// Should include known subcommands.
		if !strings.Contains(output, "rm") {
			t.Errorf("RunComplete() output = %q, want containing %q", output, "rm")
		}
		if !strings.Contains(output, "status") {
			t.Errorf("RunComplete() output = %q, want containing %q", output, "status")
		}
	})

	t.Run("init argument suggests template names", func(t *testing.T) {
		var stdout bytes.Buffer
		err := RunComplete(&stdout, []string{"init", "--", ""}, fakeDockerfiles)
		if err != nil {
			t.Fatalf("RunComplete() unexpected error: %v", err)
		}
		output := stdout.String()
		if !strings.Contains(output, "claude") {
			t.Errorf("RunComplete() output = %q, want containing %q", output, "claude")
		}
		if !strings.Contains(output, "copilot") {
			t.Errorf("RunComplete() output = %q, want containing %q", output, "copilot")
		}
	})

	t.Run("no separator treats all as preceding", func(t *testing.T) {
		var stdout bytes.Buffer
		err := RunComplete(&stdout, []string{"init"}, fakeDockerfiles)
		if err != nil {
			t.Fatalf("RunComplete() unexpected error: %v", err)
		}
		// Should still produce output (template names for init).
		output := stdout.String()
		if !strings.Contains(output, "claude") {
			t.Errorf("RunComplete() output = %q, want containing %q", output, "claude")
		}
	})

	t.Run("empty args suggests subcommands", func(t *testing.T) {
		var stdout bytes.Buffer
		err := RunComplete(&stdout, nil, fakeDockerfiles)
		if err != nil {
			t.Fatalf("RunComplete() unexpected error: %v", err)
		}
		if stdout.Len() == 0 {
			t.Error("RunComplete(nil) produced no output, want suggestions")
		}
	})
}
