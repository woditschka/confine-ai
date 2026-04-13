package container

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestDown(t *testing.T) {
	t.Run("single container stopped and removed", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "abc123\n"}, // FindByLabels: one container
			},
			runResults: []runResult{
				{err: nil}, // docker stop
				{err: nil}, // docker rm
			},
		}

		var stderr strings.Builder
		result, err := Down(t.Context(), exec, []string{"/home/user/project"}, &stderr)
		if err != nil {
			t.Fatalf("Down() unexpected error: %v", err)
		}

		wantRemoved := []string{"abc123"}
		if diff := cmp.Diff(wantRemoved, result.Removed); diff != "" {
			t.Errorf("Down() Removed mismatch (-want +got):\n%s", diff)
		}

		// Verify stop and rm were called with the container ID.
		wantRun := [][]string{
			{"stop", "abc123"},
			{"rm", "abc123"},
		}
		if diff := cmp.Diff(wantRun, exec.runCalls); diff != "" {
			t.Errorf("Down() run calls mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("no containers returns empty result", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""}, // FindByLabels: no containers
			},
		}

		var stderr strings.Builder
		result, err := Down(t.Context(), exec, []string{"/home/user/project"}, &stderr)
		if err != nil {
			t.Fatalf("Down() unexpected error: %v", err)
		}

		if len(result.Removed) != 0 {
			t.Errorf("Down() Removed = %v, want empty", result.Removed)
		}
	})

	t.Run("multiple containers warns and removes all", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "aabb1122\nccdd3344\n"}, // FindByLabels: two containers
			},
			runResults: []runResult{
				{err: nil}, // stop first
				{err: nil}, // rm first
				{err: nil}, // stop second
				{err: nil}, // rm second
			},
		}

		var stderr strings.Builder
		result, err := Down(t.Context(), exec, []string{"/home/user/project"}, &stderr)
		if err != nil {
			t.Fatalf("Down() unexpected error: %v", err)
		}

		wantRemoved := []string{"aabb1122", "ccdd3344"}
		if diff := cmp.Diff(wantRemoved, result.Removed); diff != "" {
			t.Errorf("Down() Removed mismatch (-want +got):\n%s", diff)
		}

		// Verify warning on stderr.
		if !strings.Contains(stderr.String(), "2 containers found") {
			t.Errorf("Down() stderr = %q, want warning about multiple containers", stderr.String())
		}

		// Verify stop+rm called for each container.
		wantRun := [][]string{
			{"stop", "aabb1122"},
			{"rm", "aabb1122"},
			{"stop", "ccdd3344"},
			{"rm", "ccdd3344"},
		}
		if diff := cmp.Diff(wantRun, exec.runCalls); diff != "" {
			t.Errorf("Down() run calls mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("FindByLabels failure propagates", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{err: errFake}, // FindByLabels fails
			},
		}

		var stderr strings.Builder
		_, err := Down(t.Context(), exec, []string{"/home/user/project"}, &stderr)
		if err == nil {
			t.Fatal("Down() = nil error, want error")
		}
		if !strings.Contains(err.Error(), "down") {
			t.Errorf("Down() error = %q, want containing %q", err.Error(), "down")
		}
	})

	t.Run("stopAndRemove failure propagates", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "abc123\n"}, // FindByLabels: one container
			},
			runResults: []runResult{
				{err: errFake}, // docker stop fails
			},
		}

		var stderr strings.Builder
		_, err := Down(t.Context(), exec, []string{"/home/user/project"}, &stderr)
		if err == nil {
			t.Fatal("Down() = nil error, want error")
		}
		if !strings.Contains(err.Error(), "down") {
			t.Errorf("Down() error = %q, want containing %q", err.Error(), "down")
		}
	})

	t.Run("empty folder set returns error", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{err: nil}, // Down delegates folder-set validation to FindByLabels
			},
		}

		var stderr strings.Builder
		_, err := Down(t.Context(), exec, nil, &stderr)
		if err == nil {
			t.Fatal("Down() = nil error, want error for empty folder set")
		}
		if !strings.Contains(err.Error(), "empty folder set") {
			t.Errorf("Down() error = %q, want containing %q", err.Error(), "empty folder set")
		}
	})
}

func TestDownAssistant(t *testing.T) {
	t.Run("single assistant container stopped and removed", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "abc123\n"}, // FindByAssistant: one container
			},
			runResults: []runResult{
				{err: nil}, // docker stop
				{err: nil}, // docker rm
			},
		}

		var stderr strings.Builder
		result, err := DownAssistant(t.Context(), exec, "claude", []string{"/home/user/project"}, &stderr)
		if err != nil {
			t.Fatalf("DownAssistant() unexpected error: %v", err)
		}

		wantRemoved := []string{"abc123"}
		if diff := cmp.Diff(wantRemoved, result.Removed); diff != "" {
			t.Errorf("DownAssistant() Removed mismatch (-want +got):\n%s", diff)
		}

		// Verify stop and rm were called with the container ID.
		wantRun := [][]string{
			{"stop", "abc123"},
			{"rm", "abc123"},
		}
		if diff := cmp.Diff(wantRun, exec.runCalls); diff != "" {
			t.Errorf("DownAssistant() run calls mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("no assistant container returns empty result", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: ""}, // FindByAssistant: no containers
			},
		}

		var stderr strings.Builder
		result, err := DownAssistant(t.Context(), exec, "claude", []string{"/home/user/project"}, &stderr)
		if err != nil {
			t.Fatalf("DownAssistant() unexpected error: %v", err)
		}

		if len(result.Removed) != 0 {
			t.Errorf("DownAssistant() Removed = %v, want empty", result.Removed)
		}
	})

	t.Run("FindByAssistant failure propagates", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{err: errFake}, // FindByAssistant fails
			},
		}

		var stderr strings.Builder
		_, err := DownAssistant(t.Context(), exec, "claude", []string{"/home/user/project"}, &stderr)
		if err == nil {
			t.Fatal("DownAssistant() = nil error, want error")
		}
		if !strings.Contains(err.Error(), "down assistant") {
			t.Errorf("DownAssistant() error = %q, want containing %q", err.Error(), "down assistant")
		}
	})

	t.Run("stopAndRemove failure propagates", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "abc123\n"}, // FindByAssistant: one container
			},
			runResults: []runResult{
				{err: errFake}, // docker stop fails
			},
		}

		var stderr strings.Builder
		_, err := DownAssistant(t.Context(), exec, "claude", []string{"/home/user/project"}, &stderr)
		if err == nil {
			t.Fatal("DownAssistant() = nil error, want error")
		}
		if !strings.Contains(err.Error(), "down assistant") {
			t.Errorf("DownAssistant() error = %q, want containing %q", err.Error(), "down assistant")
		}
	})

	t.Run("empty assistant name returns error", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{{err: nil}},
		}

		var stderr strings.Builder
		_, err := DownAssistant(t.Context(), exec, "", []string{"/home/user/project"}, &stderr)
		if err == nil {
			t.Fatal("DownAssistant() = nil error, want error for empty assistant name")
		}
		if !strings.Contains(err.Error(), "empty assistant name") {
			t.Errorf("DownAssistant() error = %q, want containing %q", err.Error(), "empty assistant name")
		}
	})

	t.Run("multiple assistant containers warns and removes all", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "aabb1122\nccdd3344\n"}, // FindByAssistant: two containers
			},
			runResults: []runResult{
				{err: nil}, // stop first
				{err: nil}, // rm first
				{err: nil}, // stop second
				{err: nil}, // rm second
			},
		}

		var stderr strings.Builder
		result, err := DownAssistant(t.Context(), exec, "claude", []string{"/home/user/project"}, &stderr)
		if err != nil {
			t.Fatalf("DownAssistant() unexpected error: %v", err)
		}

		wantRemoved := []string{"aabb1122", "ccdd3344"}
		if diff := cmp.Diff(wantRemoved, result.Removed); diff != "" {
			t.Errorf("DownAssistant() Removed mismatch (-want +got):\n%s", diff)
		}

		// Verify warning on stderr.
		if !strings.Contains(stderr.String(), "2 containers found") {
			t.Errorf("DownAssistant() stderr = %q, want warning about multiple containers",
				stderr.String())
		}
	})
}
