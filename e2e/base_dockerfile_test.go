//go:build integration

package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// REQ-AS-007: user-owned base Dockerfile with checksum-verified downloads.
//
// These tests exercise the seed-write behavior of `confine-ai init`.

func TestInitSeedsBaseDockerfile(t *testing.T) {
	home := t.TempDir()

	stdout, stderr, code := runConfineEnv(t, []string{"HOME=" + home}, "init")
	if code != 0 {
		t.Fatalf("confine-ai init exited %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	basePath := filepath.Join(home, ".confine-ai", "base", "Dockerfile")
	if !strings.Contains(stdout, "Seeded base Dockerfile at "+basePath) {
		t.Errorf("stdout = %q, want to contain seeded announcement for %s", stdout, basePath)
	}

	info, err := os.Stat(basePath)
	if err != nil {
		t.Fatalf("stat base dockerfile: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o644 {
		t.Errorf("base dockerfile mode = %o, want 0o644", mode)
	}
	dirInfo, err := os.Stat(filepath.Dir(basePath))
	if err != nil {
		t.Fatalf("stat base dir: %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o755 {
		t.Errorf("base dir mode = %o, want 0o755", mode)
	}

	// The seed must ship with the classification markers and sha256
	// verification wiring required by REQ-AS-007 AC9-14.
	contents, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatalf("read base dockerfile: %v", err)
	}
	body := string(contents)
	wants := []string{
		"# confine-ai:managed tool=base-image",
		"# confine-ai:managed tool=go kind=version",
		"# confine-ai:managed tool=go kind=sha256",
		"# confine-ai:managed tool=java kind=version",
		"# confine-ai:managed tool=java kind=sha256",
		"sha256sum -c -",
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("seed missing %q", want)
		}
	}

	// Second invocation must be idempotent and must not overwrite user edits.
	marker := "\n# e2e-user-edit-marker\n"
	edited := append(contents, []byte(marker)...)
	if err := os.WriteFile(basePath, edited, 0o644); err != nil {
		t.Fatalf("edit base dockerfile: %v", err)
	}

	stdout, stderr, code = runConfineEnv(t, []string{"HOME=" + home}, "init")
	if code != 0 {
		t.Fatalf("second init exited %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Base Dockerfile already present") {
		t.Errorf("stdout = %q, want already-present announcement", stdout)
	}
	got, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatalf("re-read base dockerfile: %v", err)
	}
	if !strings.HasSuffix(string(got), marker) {
		t.Errorf("user edit was overwritten; last bytes = %q", tail(string(got), len(marker)+16))
	}
}

func TestInitAssistantAlsoSeedsBase(t *testing.T) {
	home := t.TempDir()

	stdout, stderr, code := runConfineEnv(t, []string{"HOME=" + home}, "init", "claude")
	if code != 0 {
		t.Fatalf("confine-ai init claude exited %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	if !strings.Contains(stdout, "Seeded base Dockerfile") {
		t.Errorf("stdout = %q, want seeded announcement", stdout)
	}
	if !strings.Contains(stdout, `Initialized assistant "claude"`) {
		t.Errorf("stdout = %q, want assistant init announcement", stdout)
	}

	if _, err := os.Stat(filepath.Join(home, ".confine-ai", "base", "Dockerfile")); err != nil {
		t.Errorf("base dockerfile not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".confine-ai", "assistants", "claude", "devcontainer.json")); err != nil {
		t.Errorf("assistant devcontainer.json not created: %v", err)
	}
}

// tail returns the last n bytes of s, or all of s if shorter.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
