package assistant

// Note on test locality: the structural / marker-presence tests for the
// embedded base Dockerfile do NOT live in this package. They live in
// main_test.go (see TestEmbeddedBaseDockerfileMarkers and
// TestEmbeddedBaseDockerfile_Sha256VerifyBeforeExtract). The embed directive
// `//go:embed samples/base/Dockerfile` resolves relative to the compilation
// unit that declares it — that is the `main` package at the repo root, not
// this package. A `//go:embed ../../samples/base/Dockerfile` in this package
// would be rejected by the embed package because embed forbids paths that
// escape the package directory. Tests here cover the assistant/base.go functions
// against synthetic Dockerfile bytes; tests over the real embedded seed must
// live alongside the embed declaration.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeImageBuilder records calls to Output and Run for test verification.
type fakeImageBuilder struct {
	outputCalls   [][]string
	outputResults []outputResult
	outputIdx     int

	runCalls   [][]string
	runResults []error
	runIdx     int
}

type outputResult struct {
	output string
	err    error
}

func (f *fakeImageBuilder) Output(_ context.Context, args ...string) (string, error) {
	f.outputCalls = append(f.outputCalls, args)
	if f.outputIdx >= len(f.outputResults) {
		return "", errors.New("fakeImageBuilder: no more Output results")
	}
	r := f.outputResults[f.outputIdx]
	f.outputIdx++
	return r.output, r.err
}

func (f *fakeImageBuilder) Run(_ context.Context, _, _ io.Writer, args ...string) error {
	f.runCalls = append(f.runCalls, args)
	if f.runIdx >= len(f.runResults) {
		return errors.New("fakeImageBuilder: no more Run results")
	}
	r := f.runResults[f.runIdx]
	f.runIdx++
	return r
}

func TestEnsureBaseImage(t *testing.T) {
	baseDockerfile := []byte("FROM debian:bookworm-slim\nRUN echo base\n")

	t.Run("image exists skips build", func(t *testing.T) {
		builder := &fakeImageBuilder{
			outputResults: []outputResult{
				{output: "[{\"Id\": \"sha256:abc123\"}]", err: nil},
			},
		}

		err := EnsureBaseImage(t.Context(), builder, baseDockerfile, io.Discard)
		if err != nil {
			t.Fatalf("EnsureBaseImage() unexpected error: %v", err)
		}

		// Verify inspect was called.
		if len(builder.outputCalls) != 1 {
			t.Fatalf("Output calls = %d, want 1", len(builder.outputCalls))
		}
		args := builder.outputCalls[0]
		if !containsAll(args, "image", "inspect", "localhost/confine-ai-base:latest") {
			t.Errorf("Output args = %v, want containing image inspect localhost/confine-ai-base:latest", args)
		}

		// Verify build was NOT called.
		if len(builder.runCalls) != 0 {
			t.Errorf("Run calls = %d, want 0 (image exists, no build needed)", len(builder.runCalls))
		}
	})

	t.Run("image missing triggers build", func(t *testing.T) {
		builder := &fakeImageBuilder{
			outputResults: []outputResult{
				{output: "", err: errors.New("No such image")},
			},
			runResults: []error{nil}, // build succeeds
		}

		err := EnsureBaseImage(t.Context(), builder, baseDockerfile, io.Discard)
		if err != nil {
			t.Fatalf("EnsureBaseImage() unexpected error: %v", err)
		}

		// Verify inspect was called.
		if len(builder.outputCalls) != 1 {
			t.Fatalf("Output calls = %d, want 1", len(builder.outputCalls))
		}

		// Verify build was called.
		if len(builder.runCalls) != 1 {
			t.Fatalf("Run calls = %d, want 1", len(builder.runCalls))
		}
		args := builder.runCalls[0]
		if !containsAll(args, "build", "-t", "localhost/confine-ai-base:latest") {
			t.Errorf("Run args = %v, want containing build -t localhost/confine-ai-base:latest", args)
		}
	})

	t.Run("build failure propagates error", func(t *testing.T) {
		builder := &fakeImageBuilder{
			outputResults: []outputResult{
				{output: "", err: errors.New("No such image")},
			},
			runResults: []error{errors.New("build failed")},
		}

		err := EnsureBaseImage(t.Context(), builder, baseDockerfile, io.Discard)
		if err == nil {
			t.Fatal("EnsureBaseImage() = nil, want error for build failure")
		}
		if !strings.Contains(err.Error(), "build") {
			t.Errorf("EnsureBaseImage() error = %q, want containing %q", err.Error(), "build")
		}
	})
}

func TestBuildBaseImage(t *testing.T) {
	baseDockerfile := []byte("FROM debian:bookworm-slim\nRUN echo base\n")

	t.Run("builds with correct args", func(t *testing.T) {
		builder := &fakeImageBuilder{
			runResults: []error{nil},
		}

		err := BuildBaseImage(t.Context(), builder, baseDockerfile, nil, BuildOptions{}, io.Discard)
		if err != nil {
			t.Fatalf("BuildBaseImage() unexpected error: %v", err)
		}

		if len(builder.runCalls) != 1 {
			t.Fatalf("Run calls = %d, want 1", len(builder.runCalls))
		}
		args := builder.runCalls[0]
		if !containsAll(args, "build", "-t", "localhost/confine-ai-base:latest") {
			t.Errorf("Run args = %v, want containing build -t localhost/confine-ai-base:latest", args)
		}

		// The last arg should be the build context directory (temp dir).
		// We verify it existed during the call by checking the Dockerfile was written.
		// Since the fake builder doesn't actually read files, we verify the arg
		// looks like a path (contains os.TempDir prefix or a directory separator).
		lastArg := args[len(args)-1]
		if !filepath.IsAbs(lastArg) {
			t.Errorf("last arg = %q, want absolute path (build context)", lastArg)
		}
	})

	t.Run("passes build args", func(t *testing.T) {
		builder := &fakeImageBuilder{
			runResults: []error{nil},
		}

		buildArgs := map[string]string{
			"GO_VERSION": "1.26.0",
		}

		err := BuildBaseImage(t.Context(), builder, baseDockerfile, buildArgs, BuildOptions{}, io.Discard)
		if err != nil {
			t.Fatalf("BuildBaseImage() unexpected error: %v", err)
		}

		if len(builder.runCalls) != 1 {
			t.Fatalf("Run calls = %d, want 1", len(builder.runCalls))
		}
		args := builder.runCalls[0]
		if !containsAll(args, "--build-arg", "GO_VERSION=1.26.0") {
			t.Errorf("Run args = %v, want containing --build-arg GO_VERSION=1.26.0", args)
		}
	})

	t.Run("passes pull flag", func(t *testing.T) {
		builder := &fakeImageBuilder{
			runResults: []error{nil},
		}

		err := BuildBaseImage(t.Context(), builder, baseDockerfile, nil, BuildOptions{Pull: true}, io.Discard)
		if err != nil {
			t.Fatalf("BuildBaseImage() unexpected error: %v", err)
		}

		args := builder.runCalls[0]
		if !containsAll(args, "--pull") {
			t.Errorf("Run args = %v, want containing --pull", args)
		}
	})

	t.Run("passes no-cache flag", func(t *testing.T) {
		builder := &fakeImageBuilder{
			runResults: []error{nil},
		}

		err := BuildBaseImage(t.Context(), builder, baseDockerfile, nil, BuildOptions{NoCache: true}, io.Discard)
		if err != nil {
			t.Fatalf("BuildBaseImage() unexpected error: %v", err)
		}

		args := builder.runCalls[0]
		if !containsAll(args, "--no-cache") {
			t.Errorf("Run args = %v, want containing --no-cache", args)
		}
	})

	t.Run("cleans up temp directory", func(t *testing.T) {
		// Count temp dirs before and after to verify cleanup.
		builder := &fakeImageBuilder{
			runResults: []error{nil},
		}

		// Use a custom approach: the Run fake captures the build context path.
		err := BuildBaseImage(t.Context(), builder, baseDockerfile, nil, BuildOptions{}, io.Discard)
		if err != nil {
			t.Fatalf("BuildBaseImage() unexpected error: %v", err)
		}

		// The build context dir should be cleaned up after the call.
		args := builder.runCalls[0]
		buildContextDir := args[len(args)-1]
		if _, err := os.Stat(buildContextDir); !os.IsNotExist(err) {
			t.Errorf("temp directory %q still exists after BuildBaseImage, want cleaned up", buildContextDir)
		}
	})

	t.Run("build failure still cleans up temp directory", func(t *testing.T) {
		builder := &fakeImageBuilder{
			runResults: []error{errors.New("build failed")},
		}

		err := BuildBaseImage(t.Context(), builder, baseDockerfile, nil, BuildOptions{}, io.Discard)
		if err == nil {
			t.Fatal("BuildBaseImage() = nil, want error")
		}

		// Temp dir should still be cleaned up.
		args := builder.runCalls[0]
		buildContextDir := args[len(args)-1]
		if _, err := os.Stat(buildContextDir); !os.IsNotExist(err) {
			t.Errorf("temp directory %q still exists after failed build, want cleaned up", buildContextDir)
		}
	})
}

func TestResolveBaseDockerfile(t *testing.T) {
	seed := []byte("# embedded seed\nFROM debian:bookworm-slim\n")

	t.Run("missing user copy returns seed without message", func(t *testing.T) {
		homeDir := t.TempDir()
		var stderr bytes.Buffer

		got, err := ResolveBaseDockerfile(homeDir, seed, &stderr, false)
		if err != nil {
			t.Fatalf("ResolveBaseDockerfile() unexpected error: %v", err)
		}
		if !bytes.Equal(got, seed) {
			t.Errorf("bytes = %q, want seed %q", string(got), string(seed))
		}
		if stderr.Len() != 0 {
			t.Errorf("stderr = %q, want empty (announceFallback=false)", stderr.String())
		}
	})

	t.Run("missing user copy emits fallback message when announced", func(t *testing.T) {
		homeDir := t.TempDir()
		var stderr bytes.Buffer

		got, err := ResolveBaseDockerfile(homeDir, seed, &stderr, true)
		if err != nil {
			t.Fatalf("ResolveBaseDockerfile() unexpected error: %v", err)
		}
		if !bytes.Equal(got, seed) {
			t.Errorf("bytes = %q, want seed", string(got))
		}
		msg := stderr.String()
		if !strings.Contains(msg, "~/.confine-ai/base/Dockerfile") {
			t.Errorf("stderr = %q, want containing %q", msg, "~/.confine-ai/base/Dockerfile")
		}
		if !strings.Contains(msg, "embedded seed") {
			t.Errorf("stderr = %q, want containing %q", msg, "embedded seed")
		}
		if !strings.Contains(msg, "confine-ai init") {
			t.Errorf("stderr = %q, want containing %q", msg, "confine-ai init")
		}
	})

	t.Run("present user copy returns file contents without message", func(t *testing.T) {
		homeDir := t.TempDir()
		userCopy := []byte("# user edited\nFROM ubuntu:24.04\n")
		path := BaseDockerfilePath(homeDir)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() error: %v", err)
		}
		if err := os.WriteFile(path, userCopy, 0o644); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}

		var stderr bytes.Buffer
		got, err := ResolveBaseDockerfile(homeDir, seed, &stderr, true)
		if err != nil {
			t.Fatalf("ResolveBaseDockerfile() unexpected error: %v", err)
		}
		if !bytes.Equal(got, userCopy) {
			t.Errorf("bytes = %q, want user copy %q", string(got), string(userCopy))
		}
		if stderr.Len() != 0 {
			t.Errorf("stderr = %q, want empty (user copy present)", stderr.String())
		}
	})

	t.Run("unreadable user copy returns error", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("permission semantics differ on Windows")
		}
		if os.Geteuid() == 0 {
			t.Skip("root bypasses file permissions")
		}
		homeDir := t.TempDir()
		path := BaseDockerfilePath(homeDir)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() error: %v", err)
		}
		if err := os.WriteFile(path, []byte("FROM x\n"), 0o644); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}
		// Remove read permission.
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("Chmod() error: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

		var stderr bytes.Buffer
		_, err := ResolveBaseDockerfile(homeDir, seed, &stderr, false)
		if err == nil {
			t.Fatal("ResolveBaseDockerfile() = nil, want error for unreadable file")
		}
		if !strings.Contains(err.Error(), "base dockerfile") {
			t.Errorf("error = %q, want containing %q", err.Error(), "base dockerfile")
		}
	})
}

// containsAll returns true if all items are found (in any position) in slice.
func containsAll(slice []string, items ...string) bool {
	set := make(map[string]struct{}, len(slice))
	for _, s := range slice {
		set[s] = struct{}{}
	}
	for _, item := range items {
		if _, ok := set[item]; !ok {
			return false
		}
	}
	return true
}
