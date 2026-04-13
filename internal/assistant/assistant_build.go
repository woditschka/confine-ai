package assistant

import (
	"context"
	"fmt"
	"io"
)

// AssistantImageTag returns the canonical image tag used by `confine-ai update
// <assistant>`. The tag is `confine-ai-assistant-<name>:latest`. The caller is
// responsible for validating `name` via ValidateName.
func AssistantImageTag(name string) string {
	return "confine-ai-assistant-" + name + ":latest"
}

// BuildAssistantImage rebuilds the assistant image from the assistant's user-owned
// Dockerfile. The build runs with `--no-cache` so that layer cache is ignored,
// which is the cache-bust behavior required by REQ-AS-008 for assistant updates.
//
// The build does not pass `--pull`: the FROM image is
// `localhost/confine-ai-base:latest`, a locally-built image with no remote
// source, and `--pull` would cause podman to fail trying to re-resolve it
// against registries. `confine-ai update` rebuilds the base image immediately
// before the assistant rebuild, so the local `localhost/confine-ai-base:latest`
// is already current when this function runs.
//
// The function does not probe upstreams, does not read any marker, and does
// not touch any other file. Build stdout and stderr are wired to the provided
// stderr writer.
func BuildAssistantImage(ctx context.Context, builder ImageBuilder, homeDir, assistantName string, stderr io.Writer) error {
	tag := AssistantImageTag(assistantName)
	assistantDir := Dir(homeDir, assistantName)
	dockerfile := DockerfilePath(homeDir, assistantName)

	args := []string{
		"build",
		"--no-cache",
		"-t", tag,
		"-f", dockerfile,
		assistantDir,
	}

	fmt.Fprintf(stderr, "Rebuilding assistant image %s...\n", tag)
	if err := builder.Run(ctx, stderr, stderr, args...); err != nil {
		return fmt.Errorf("build assistant image %s: %w", assistantName, err)
	}
	return nil
}
