package container

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFindManagedImages(t *testing.T) {
	t.Run("returns parsed image list", func(t *testing.T) {
		executor := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "confine-ai-base:latest\nconfine-ai-myproject:latest\n"},
			},
		}

		images, err := FindManagedImages(t.Context(), executor)
		if err != nil {
			t.Fatalf("FindManagedImages() error = %v", err)
		}

		want := []string{"confine-ai-base:latest", "confine-ai-myproject:latest"}
		if diff := cmp.Diff(want, images); diff != "" {
			t.Errorf("FindManagedImages() mismatch (-want +got):\n%s", diff)
		}

		// Verify correct args.
		if len(executor.outputCalls) != 1 {
			t.Fatalf("Output calls = %d, want 1", len(executor.outputCalls))
		}
		args := executor.outputCalls[0]
		if !strings.Contains(strings.Join(args, " "), "image ls") {
			t.Errorf("args = %v, want containing 'image ls'", args)
		}
	})

	t.Run("returns nil for empty output", func(t *testing.T) {
		executor := &fakeMultiExecutor{
			outputResults: []outputResult{{output: ""}},
		}

		images, err := FindManagedImages(t.Context(), executor)
		if err != nil {
			t.Fatalf("FindManagedImages() error = %v", err)
		}
		if images != nil {
			t.Errorf("FindManagedImages() = %v, want nil", images)
		}
	})

	t.Run("propagates executor error", func(t *testing.T) {
		executor := &fakeMultiExecutor{
			outputResults: []outputResult{{err: errFake}},
		}

		_, err := FindManagedImages(t.Context(), executor)
		if err == nil {
			t.Fatal("FindManagedImages() = nil, want error")
		}
	})
}

func TestRemoveImages(t *testing.T) {
	t.Run("removes non-base images", func(t *testing.T) {
		executor := &fakeMultiExecutor{
			runResults: []runResult{{}, {}},
		}

		images := []string{"localhost/confine-ai-base:latest", "confine-ai-foo:latest", "confine-ai-bar:latest"}
		err := RemoveImages(t.Context(), executor, images, io.Discard)
		if err != nil {
			t.Fatalf("RemoveImages() error = %v", err)
		}

		// Should have removed foo and bar, skipped base.
		if len(executor.runCalls) != 2 {
			t.Fatalf("Run calls = %d, want 2", len(executor.runCalls))
		}
		// Explicitly assert localhost/confine-ai-base:latest was the image
		// that was skipped. Each rmi Run call has the image in args[1]
		// (args[0] is "rmi").
		for _, call := range executor.runCalls {
			if len(call) >= 2 && call[1] == "localhost/confine-ai-base:latest" {
				t.Errorf("rmi called on localhost/confine-ai-base:latest, want skipped (args=%v)", call)
			}
		}
		if executor.runCalls[0][1] != "confine-ai-foo:latest" {
			t.Errorf("first rmi target = %q, want confine-ai-foo:latest", executor.runCalls[0][1])
		}
		if executor.runCalls[1][1] != "confine-ai-bar:latest" {
			t.Errorf("second rmi target = %q, want confine-ai-bar:latest", executor.runCalls[1][1])
		}
	})

	t.Run("skips base image only", func(t *testing.T) {
		executor := &fakeMultiExecutor{}

		images := []string{"confine-ai-base:latest"}
		err := RemoveImages(t.Context(), executor, images, io.Discard)
		if err != nil {
			t.Fatalf("RemoveImages() error = %v", err)
		}
		if len(executor.runCalls) != 0 {
			t.Errorf("Run calls = %d, want 0 (only base image)", len(executor.runCalls))
		}
	})

	t.Run("returns first error but continues", func(t *testing.T) {
		executor := &fakeMultiExecutor{
			runResults: []runResult{
				{err: errors.New("rmi failed")},
				{},
			},
		}

		images := []string{"confine-ai-foo:latest", "confine-ai-bar:latest"}
		err := RemoveImages(t.Context(), executor, images, io.Discard)
		if err == nil {
			t.Fatal("RemoveImages() = nil, want error")
		}
		// Both images should have been attempted.
		if len(executor.runCalls) != 2 {
			t.Errorf("Run calls = %d, want 2 (continues after error)", len(executor.runCalls))
		}
	})
}

func TestRemoveContainersByAssistant(t *testing.T) {
	t.Run("filters by assistant label and removes matches", func(t *testing.T) {
		executor := &fakeMultiExecutor{
			outputResults: []outputResult{
				// ps output: two containers for the same assistant across two workspaces.
				{output: "aaa111bbb222\nccc333ddd444\n"},
			},
			runResults: []runResult{
				{}, // stop 1
				{}, // rm 1
				{}, // stop 2
				{}, // rm 2
			},
		}

		err := RemoveContainersByAssistant(t.Context(), executor, "claude", io.Discard)
		if err != nil {
			t.Fatalf("RemoveContainersByAssistant() error = %v", err)
		}

		// Verify the ps call includes the assistant label filter.
		if len(executor.outputCalls) != 1 {
			t.Fatalf("Output calls = %d, want 1", len(executor.outputCalls))
		}
		psArgs := strings.Join(executor.outputCalls[0], " ")
		if !strings.Contains(psArgs, "ps") {
			t.Errorf("output args = %v, want containing 'ps'", executor.outputCalls[0])
		}
		if !strings.Contains(psArgs, "label=devcontainer.assistant_name=claude") {
			t.Errorf("output args = %v, want containing assistant label filter for claude", executor.outputCalls[0])
		}
		if !strings.Contains(psArgs, "--all") {
			t.Errorf("output args = %v, want containing --all (include stopped)", executor.outputCalls[0])
		}

		// Verify 2 containers were stopped and removed (4 Run calls: stop+rm twice).
		if len(executor.runCalls) != 4 {
			t.Fatalf("Run calls = %d, want 4 (stop+rm for 2 containers)", len(executor.runCalls))
		}
		if executor.runCalls[0][0] != "stop" || executor.runCalls[0][1] != "aaa111bbb222" {
			t.Errorf("runCalls[0] = %v, want [stop aaa111bbb222]", executor.runCalls[0])
		}
		if executor.runCalls[1][0] != "rm" || executor.runCalls[1][1] != "aaa111bbb222" {
			t.Errorf("runCalls[1] = %v, want [rm aaa111bbb222]", executor.runCalls[1])
		}
		if executor.runCalls[2][0] != "stop" || executor.runCalls[2][1] != "ccc333ddd444" {
			t.Errorf("runCalls[2] = %v, want [stop ccc333ddd444]", executor.runCalls[2])
		}
		if executor.runCalls[3][0] != "rm" || executor.runCalls[3][1] != "ccc333ddd444" {
			t.Errorf("runCalls[3] = %v, want [rm ccc333ddd444]", executor.runCalls[3])
		}
	})

	t.Run("empty list is a no-op", func(t *testing.T) {
		executor := &fakeMultiExecutor{
			outputResults: []outputResult{{output: ""}},
		}

		err := RemoveContainersByAssistant(t.Context(), executor, "claude", io.Discard)
		if err != nil {
			t.Fatalf("RemoveContainersByAssistant() error = %v", err)
		}
		if len(executor.runCalls) != 0 {
			t.Errorf("Run calls = %d, want 0 (no containers to remove)", len(executor.runCalls))
		}
	})

	t.Run("empty assistant name is an error", func(t *testing.T) {
		executor := &fakeMultiExecutor{}
		err := RemoveContainersByAssistant(t.Context(), executor, "", io.Discard)
		if err == nil {
			t.Fatal("RemoveContainersByAssistant() = nil, want error for empty assistant name")
		}
	})

	t.Run("partial failure returns error but continues", func(t *testing.T) {
		executor := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "aaa111\nbbb222\n"},
			},
			runResults: []runResult{
				{err: errors.New("stop failed")}, // stop 1 fails
				{},                               // stop 2 ok
				{},                               // rm 2 ok
			},
		}

		err := RemoveContainersByAssistant(t.Context(), executor, "claude", io.Discard)
		if err == nil {
			t.Fatal("RemoveContainersByAssistant() = nil, want error for partial failure")
		}

		// Verify the second container was still attempted (3 Run calls:
		// fail-stop-1, stop-2, rm-2).
		if len(executor.runCalls) != 3 {
			t.Errorf("Run calls = %d, want 3 (continues after partial failure)", len(executor.runCalls))
		}
	})

	t.Run("propagates ps error", func(t *testing.T) {
		executor := &fakeMultiExecutor{
			outputResults: []outputResult{{err: errFake}},
		}
		err := RemoveContainersByAssistant(t.Context(), executor, "claude", io.Discard)
		if err == nil {
			t.Fatal("RemoveContainersByAssistant() = nil, want error from ps failure")
		}
	})
}

func TestRemoveAllContainers(t *testing.T) {
	t.Run("removes all managed containers", func(t *testing.T) {
		// FindAllManaged output + stop + rm for each container.
		executor := &fakeMultiExecutor{
			outputResults: []outputResult{
				// FindAllManaged ps output: ID\tStatus\t{labels-json}.
				{output: "abc123def456\tUp 2 hours\t" +
					`{"devcontainer.assistant_name":"claude","devcontainer.local_folder":"/home/user/project"}`},
			},
			runResults: []runResult{
				{}, // stop
				{}, // rm
			},
		}

		removed, err := RemoveAllContainers(t.Context(), executor, io.Discard)
		if err != nil {
			t.Fatalf("RemoveAllContainers() error = %v", err)
		}
		if len(removed) != 1 {
			t.Fatalf("removed = %d, want 1", len(removed))
		}
		if removed[0] != "abc123def456" {
			t.Errorf("removed[0] = %q, want abc123def456", removed[0])
		}
	})

	t.Run("handles no containers", func(t *testing.T) {
		executor := &fakeMultiExecutor{
			outputResults: []outputResult{{output: ""}},
		}

		removed, err := RemoveAllContainers(t.Context(), executor, io.Discard)
		if err != nil {
			t.Fatalf("RemoveAllContainers() error = %v", err)
		}
		if len(removed) != 0 {
			t.Errorf("removed = %d, want 0", len(removed))
		}
	})
}
