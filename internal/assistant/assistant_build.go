package assistant

import (
	"context"
	"fmt"
	"io"
)

// AssistantImageTag returns the canonical image tag used by `confine-ai update
// <assistant>` and consumed by the assistant shortcut. The tag is
// `confine-ai-assistant-<name>:latest`. The caller is responsible for
// validating `name` via ValidateName.
func AssistantImageTag(name string) string {
	return "confine-ai-assistant-" + name + ":latest"
}

// EnsureAssistantImage checks whether the canonical assistant image tag exists
// in the local container runtime. If it is present, the function returns
// without action. If it is absent, it emits a single stderr breadcrumb naming
// the image being built, then invokes BuildAssistantImage with a *cached*
// build (BuildOptions.NoCache == false).
//
// This is the shortcut's first-use auto-ensure path. Cache-busting is
// exclusively the job of `confine-ai update <assistant>`; the auto-ensure must
// not pass --no-cache. Both writers share the same fixed-Dockerfile core via
// BuildAssistantImage, so by construction they produce byte-identical images
// from the same source tree — `build.args` and `build.context` from the
// assistant's devcontainer.json are never interpreted on either path.
//
// The breadcrumb wording parallels EnsureBaseImage's "Base image %s not
// found, building..." line so first-use ergonomics stay consistent with the
// base-image path.
func EnsureAssistantImage(ctx context.Context, builder ImageBuilder, homeDir, name string, stderr io.Writer) error {
	tag := AssistantImageTag(name)
	if _, err := builder.Output(ctx, "image", "inspect", tag); err == nil {
		return nil
	}

	fmt.Fprintf(stderr, "Assistant image %s not found, building...\n", tag)
	return BuildAssistantImage(ctx, builder, homeDir, name, BuildOptions{}, stderr)
}

// BuildAssistantImage rebuilds the assistant image from the assistant's user-owned
// Dockerfile. The cache policy is controlled by opts.NoCache: `confine-ai
// update <assistant>` passes NoCache=true (cache-bust is part of update's
// contract); the shortcut's first-use auto-ensure passes NoCache=false
// (cached build, first-run ergonomics). This is the only opts field consumed
// on the assistant path — opts.Pull is intentionally ignored (see below).
//
// The build does not pass `--pull`: the FROM image is
// `localhost/confine-ai-base:latest`, a locally-built image with no remote
// source, and `--pull` would cause podman to fail trying to re-resolve it
// against registries. `confine-ai update` rebuilds the base image immediately
// before the assistant rebuild, so the local `localhost/confine-ai-base:latest`
// is already current when this function runs.
//
// The function does not probe upstreams, does not read any marker, and does
// not touch any other file. It reads no `build.*` fields from the assistant's
// devcontainer.json: both writers call this function, and this function takes
// only the fixed Dockerfile path and its directory. A user editing
// build.args/build.context/build.dockerfile cannot affect either writer —
// this is a code-structure invariant, not a runtime check.
//
// Build stdout and stderr are wired to the provided stderr writer.
func BuildAssistantImage(ctx context.Context, builder ImageBuilder, homeDir, assistantName string, opts BuildOptions, stderr io.Writer) error {
	tag := AssistantImageTag(assistantName)
	assistantDir := Dir(homeDir, assistantName)
	dockerfile := DockerfilePath(homeDir, assistantName)

	args := []string{"build"}
	if opts.NoCache {
		args = append(args, "--no-cache")
	}
	args = append(args, "-t", tag, "-f", dockerfile, assistantDir)

	fmt.Fprintf(stderr, "Rebuilding assistant image %s...\n", tag)
	if err := builder.Run(ctx, stderr, stderr, args...); err != nil {
		return fmt.Errorf("build assistant image %s: %w", assistantName, err)
	}
	return nil
}
