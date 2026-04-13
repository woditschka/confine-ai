package assistant

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// baseImageTag is the tag used for the shared base image. The
// `localhost/` prefix is required for Podman's strict short-name resolution
// mode and is harmless on Docker Engine, Docker Desktop, BuildKit, and
// containerd, all of which treat `localhost` as a registry hostname that
// resolves against the local image store first.
const baseImageTag = "localhost/confine-ai-base:latest"

// ResolveBaseDockerfile returns the bytes of the base Dockerfile to build
// from. It reads ~/.confine-ai/base/Dockerfile when the file exists and returns
// the embedded seed when the file is absent. A present-but-unreadable user
// copy is a hard error (no silent fallback).
//
// When announceFallback is true and the fallback to the embedded seed is
// taken, it writes a single informational line to stderr. When
// announceFallback is false, the fallback is silent. The function never
// writes to disk; seeding the user copy is the job of SeedBaseDockerfile.
func ResolveBaseDockerfile(homeDir string, seed []byte, stderr io.Writer, announceFallback bool) ([]byte, error) {
	path := BaseDockerfilePath(homeDir)

	_, statErr := os.Stat(path)
	if statErr == nil {
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("base dockerfile: read %s: %w", path, err)
		}
		return contents, nil
	}
	if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("base dockerfile: stat %s: %w", path, statErr)
	}

	if announceFallback && stderr != nil {
		fmt.Fprintf(stderr, "confine-ai: base Dockerfile not found at ~/.confine-ai/base/Dockerfile; using embedded seed. Run 'confine-ai init' to persist a user copy.\n")
	}
	return seed, nil
}

// ImageBuilder runs container image operations. Defined at the consumer site
// to avoid import coupling with internal/container. The CLIExecutor from
// internal/container/cli.go satisfies this interface.
type ImageBuilder interface {
	// Output runs the command with the given arguments and returns its stdout.
	Output(ctx context.Context, args ...string) (string, error)

	// Run executes the command with the given arguments, wiring stdout and
	// stderr to the provided writers.
	Run(ctx context.Context, stdout, stderr io.Writer, args ...string) error
}

// EnsureBaseImage checks whether the base image exists in the local container
// runtime. If missing, it builds the image from the provided Dockerfile bytes.
// If present, it returns without action.
func EnsureBaseImage(ctx context.Context, builder ImageBuilder, baseDockerfile []byte, stderr io.Writer) error {
	_, err := builder.Output(ctx, "image", "inspect", baseImageTag)
	if err == nil {
		return nil
	}

	// Image not found; build it.
	fmt.Fprintf(stderr, "Base image %s not found, building...\n", baseImageTag)
	return BuildBaseImage(ctx, builder, baseDockerfile, nil, BuildOptions{}, stderr)
}

// BuildOptions controls optional flags passed to the container build command.
type BuildOptions struct {
	Pull    bool // Pass --pull to fetch the latest base image.
	NoCache bool // Pass --no-cache to disable layer caching.
}

// BuildBaseImage builds the base image unconditionally. It writes the provided
// Dockerfile bytes to a temporary directory, runs the build with the given tag,
// and cleans up the temporary directory. Optional buildArgs are passed as
// --build-arg flags to the container runtime.
func BuildBaseImage(ctx context.Context, builder ImageBuilder, baseDockerfile []byte, buildArgs map[string]string, opts BuildOptions, stderr io.Writer) error {
	// Create temporary build context directory.
	tmpDir, err := os.MkdirTemp("", "confine-ai-base-build-*")
	if err != nil {
		return fmt.Errorf("build base image: create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Write Dockerfile to the build context.
	dfPath := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(dfPath, baseDockerfile, 0o644); err != nil {
		return fmt.Errorf("build base image: write Dockerfile: %w", err)
	}

	// Assemble build command arguments.
	args := []string{"build", "-t", baseImageTag}

	if opts.Pull {
		args = append(args, "--pull")
	}
	if opts.NoCache {
		args = append(args, "--no-cache")
	}

	// Sort build arg keys for deterministic output in tests.
	for _, k := range slices.Sorted(maps.Keys(buildArgs)) {
		args = append(args, "--build-arg", k+"="+buildArgs[k])
	}

	args = append(args, tmpDir)

	fmt.Fprintf(stderr, "Building %s...\n", baseImageTag)
	if err := builder.Run(ctx, stderr, stderr, args...); err != nil {
		return fmt.Errorf("build base image: %w", err)
	}

	return nil
}
