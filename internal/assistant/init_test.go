package assistant

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit(t *testing.T) {
	t.Run("known assistant creates files and directories", func(t *testing.T) {
		homeDir := t.TempDir()
		dockerfile := []byte("FROM localhost/confine-ai-base:latest\nRUN echo hello\n")

		err := Init(homeDir, "claude", dockerfile)
		if err != nil {
			t.Fatalf("Init(%q, %q) unexpected error: %v", homeDir, "claude", err)
		}

		// Verify Dockerfile written.
		dfPath := DockerfilePath(homeDir, "claude")
		dfContent, err := os.ReadFile(dfPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error: %v", dfPath, err)
		}
		if string(dfContent) != string(dockerfile) {
			t.Errorf("Dockerfile content = %q, want %q", string(dfContent), string(dockerfile))
		}

		// Verify devcontainer.json written with correct mount.
		dcPath := ConfigPath(homeDir, "claude")
		dcContent, err := os.ReadFile(dcPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error: %v", dcPath, err)
		}

		// Parse as JSON to verify structure.
		var dcJSON map[string]any
		if err := json.Unmarshal(dcContent, &dcJSON); err != nil {
			t.Fatalf("json.Unmarshal(devcontainer.json) error: %v", err)
		}

		// Verify build.dockerfile field.
		build, ok := dcJSON["build"].(map[string]any)
		if !ok {
			t.Fatal("devcontainer.json missing build field")
		}
		if df, ok := build["dockerfile"].(string); !ok || df != "Dockerfile" {
			t.Errorf("build.dockerfile = %q, want %q", df, "Dockerfile")
		}

		// Verify mounts: data directory + extra file (claude.json).
		mounts, ok := dcJSON["mounts"].([]any)
		if !ok {
			t.Fatal("devcontainer.json missing mounts field")
		}
		if len(mounts) != 2 {
			t.Fatalf("mounts length = %d, want 2", len(mounts))
		}

		// First mount: data directory.
		mount0 := mounts[0].(string)
		if !strings.Contains(mount0, "target=/home/dev/.claude") {
			t.Errorf("mount[0] = %q, want containing %q", mount0, "target=/home/dev/.claude")
		}
		if !strings.Contains(mount0, "source=${localEnv:HOME}/.confine-ai/data/claude") {
			t.Errorf("mount[0] = %q, want containing %q", mount0, "source=${localEnv:HOME}/.confine-ai/data/claude")
		}

		// Second mount: extra file (claude.json).
		mount1 := mounts[1].(string)
		if !strings.Contains(mount1, "source=${localEnv:HOME}/.confine-ai/data/claude/claude.json") {
			t.Errorf("mount[1] = %q, want containing %q", mount1, "source=${localEnv:HOME}/.confine-ai/data/claude/claude.json")
		}
		if !strings.Contains(mount1, "target=/home/dev/.claude.json") {
			t.Errorf("mount[1] = %q, want containing %q", mount1, "target=/home/dev/.claude.json")
		}

		// Verify data directory created.
		dataDir := DataPath(homeDir, "claude")
		info, err := os.Stat(dataDir)
		if err != nil {
			t.Fatalf("Stat(%q) error: %v", dataDir, err)
		}
		if !info.IsDir() {
			t.Errorf("data path is not a directory")
		}

		// Verify claude.json seed file created in data directory.
		seedPath := filepath.Join(dataDir, "claude.json")
		seedContent, err := os.ReadFile(seedPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error: %v", seedPath, err)
		}
		if string(seedContent) != "{}" {
			t.Errorf("seed file content = %q, want %q", string(seedContent), "{}")
		}
	})

	t.Run("copilot assistant gets correct mount target", func(t *testing.T) {
		homeDir := t.TempDir()
		dockerfile := []byte("FROM localhost/confine-ai-base:latest\n")

		err := Init(homeDir, "copilot", dockerfile)
		if err != nil {
			t.Fatalf("Init(%q, %q) unexpected error: %v", homeDir, "copilot", err)
		}

		dcPath := ConfigPath(homeDir, "copilot")
		dcContent, err := os.ReadFile(dcPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error: %v", dcPath, err)
		}

		if !strings.Contains(string(dcContent), "target=/home/dev/.copilot") {
			t.Errorf("devcontainer.json = %q, want containing %q", string(dcContent), "target=/home/dev/.copilot")
		}

		// Copilot should have exactly 1 mount (no extra files).
		var dcJSON map[string]any
		if err := json.Unmarshal(dcContent, &dcJSON); err != nil {
			t.Fatalf("json.Unmarshal(devcontainer.json) error: %v", err)
		}
		mounts, ok := dcJSON["mounts"].([]any)
		if !ok {
			t.Fatal("devcontainer.json missing mounts field")
		}
		if len(mounts) != 1 {
			t.Fatalf("mounts length = %d, want 1", len(mounts))
		}
	})

	t.Run("opencode assistant gets data dir and host mount", func(t *testing.T) {
		homeDir := t.TempDir()
		dockerfile := []byte("FROM localhost/confine-ai-base:latest\n")

		err := Init(homeDir, "opencode", dockerfile)
		if err != nil {
			t.Fatalf("Init(%q, %q) unexpected error: %v", homeDir, "opencode", err)
		}

		dcPath := ConfigPath(homeDir, "opencode")
		dcContent, err := os.ReadFile(dcPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error: %v", dcPath, err)
		}

		// Verify mounts: data directory + host mount for XDG data dir.
		var dcJSON map[string]any
		if err := json.Unmarshal(dcContent, &dcJSON); err != nil {
			t.Fatalf("json.Unmarshal(devcontainer.json) error: %v", err)
		}
		mounts, ok := dcJSON["mounts"].([]any)
		if !ok {
			t.Fatal("devcontainer.json missing mounts field")
		}
		if len(mounts) != 2 {
			t.Fatalf("mounts length = %d, want 2", len(mounts))
		}

		// First mount: confine-ai data directory mapped to XDG config dir.
		mount0, ok := mounts[0].(string)
		if !ok {
			t.Fatalf("mounts[0] type = %T, want string", mounts[0])
		}
		if !strings.Contains(mount0, "source=${localEnv:HOME}/.confine-ai/data/opencode") {
			t.Errorf("mount[0] = %q, want containing %q", mount0, "source=${localEnv:HOME}/.confine-ai/data/opencode")
		}
		if !strings.Contains(mount0, "target=/home/dev/.config/opencode") {
			t.Errorf("mount[0] = %q, want containing %q", mount0, "target=/home/dev/.config/opencode")
		}

		// Second mount: host pass-through for opencode XDG data dir (auth state).
		// Sourced from live host state, not from ~/.confine-ai/data/.
		mount1, ok := mounts[1].(string)
		if !ok {
			t.Fatalf("mounts[1] type = %T, want string", mounts[1])
		}
		if !strings.Contains(mount1, "source=${localEnv:HOME}/.local/share/opencode") {
			t.Errorf("mount[1] = %q, want containing %q", mount1, "source=${localEnv:HOME}/.local/share/opencode")
		}
		if !strings.Contains(mount1, "target=/home/dev/.local/share/opencode") {
			t.Errorf("mount[1] = %q, want containing %q", mount1, "target=/home/dev/.local/share/opencode")
		}
		// Must be read-write so opencode can refresh auth tokens.
		if strings.Contains(mount1, "readonly") {
			t.Errorf("mount[1] = %q, want without %q", mount1, "readonly")
		}
		// Mount string format: exactly three comma-separated fields
		// (type, source, target) for the read-write case. Guards against
		// "," or "=" leaking into source/target — a malformed entry would
		// either split into too many fields or put the leak in the wrong
		// position.
		fields := strings.Split(mount1, ",")
		if len(fields) != 3 {
			t.Errorf("mount[1] = %q has %d comma-separated fields, want 3", mount1, len(fields))
		}

		// Verify opencode.json seed file created in data directory (seed-only,
		// no separate mount — the file is visible through the directory mount).
		seedPath := filepath.Join(DataPath(homeDir, "opencode"), "opencode.json")
		seedContent, err := os.ReadFile(seedPath)
		if err != nil {
			t.Fatalf("ReadFile(%q) error: %v", seedPath, err)
		}
		wantSeed := `{"$schema":"https://opencode.ai/config.json","disabled_providers":["opencode"]}`
		if string(seedContent) != wantSeed {
			t.Errorf("seed file content = %q, want %q", string(seedContent), wantSeed)
		}
	})

	t.Run("only opencode declares host mounts", func(t *testing.T) {
		// Whole-map length check: any future addition of a host mount to any
		// assistant (including a new one) trips this guard so the change is
		// reviewed consciously. Extending this list requires updating the
		// test along with the map, which is the intended friction.
		if got := len(knownAssistantHostMounts); got != 1 {
			t.Errorf("len(knownAssistantHostMounts) = %d, want 1 (opencode only)", got)
		}
		if got := len(knownAssistantHostMounts["opencode"]); got != 1 {
			t.Errorf("knownAssistantHostMounts[opencode] length = %d, want 1", got)
		}
	})

	t.Run("creates intermediate directories", func(t *testing.T) {
		homeDir := t.TempDir()
		dockerfile := []byte("FROM localhost/confine-ai-base:latest\n")

		// No pre-existing .confine-ai directory.
		err := Init(homeDir, "claude", dockerfile)
		if err != nil {
			t.Fatalf("Init(%q, %q) unexpected error: %v", homeDir, "claude", err)
		}

		// Verify the full path was created.
		if _, err := os.Stat(Dir(homeDir, "claude")); err != nil {
			t.Errorf("assistant directory not created: %v", err)
		}
	})
}

func TestInit_ExistingDir(t *testing.T) {
	homeDir := t.TempDir()
	assistantDir := Dir(homeDir, "claude")
	if err := os.MkdirAll(assistantDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error: %v", assistantDir, err)
	}

	// Write a sentinel file to verify nothing is overwritten.
	sentinel := DockerfilePath(homeDir, "claude")
	if err := os.WriteFile(sentinel, []byte("original"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error: %v", sentinel, err)
	}

	err := Init(homeDir, "claude", []byte("FROM new:latest\n"))
	if err == nil {
		t.Fatal("Init() = nil, want error for existing directory")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("Init() error = %q, want containing %q", err.Error(), "already exists")
	}

	// Verify sentinel file not overwritten.
	content, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("ReadFile(%q) error: %v", sentinel, err)
	}
	if string(content) != "original" {
		t.Errorf("sentinel file content = %q, want %q (should not be overwritten)", string(content), "original")
	}
}

func TestInit_UnknownAssistant(t *testing.T) {
	homeDir := t.TempDir()

	// Unknown assistant gets no Dockerfile (nil), uses image-based template.
	err := Init(homeDir, "my-custom-assistant", nil)
	if err != nil {
		t.Fatalf("Init(%q, %q) unexpected error: %v", homeDir, "my-custom-assistant", err)
	}

	// Verify devcontainer.json uses image reference instead of build.
	dcPath := ConfigPath(homeDir, "my-custom-assistant")
	dcContent, err := os.ReadFile(dcPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error: %v", dcPath, err)
	}

	var dcJSON map[string]any
	if err := json.Unmarshal(dcContent, &dcJSON); err != nil {
		t.Fatalf("json.Unmarshal(devcontainer.json) error: %v", err)
	}

	// Should have image, not build.
	if _, ok := dcJSON["build"]; ok {
		t.Error("unknown assistant devcontainer.json has build field, want image-only")
	}
	img, ok := dcJSON["image"].(string)
	if !ok {
		t.Fatal("devcontainer.json missing image field")
	}
	if img != "localhost/confine-ai-base:latest" {
		t.Errorf("image = %q, want %q", img, "localhost/confine-ai-base:latest")
	}

	// Mount target for unknown assistant uses /home/dev/.config/<name>.
	mounts, ok := dcJSON["mounts"].([]any)
	if !ok {
		t.Fatal("devcontainer.json missing mounts field")
	}
	if len(mounts) != 1 {
		t.Fatalf("mounts length = %d, want 1", len(mounts))
	}
	mount := mounts[0].(string)
	if !strings.Contains(mount, "target=/home/dev/.config/my-custom-assistant") {
		t.Errorf("mount = %q, want containing %q", mount, "target=/home/dev/.config/my-custom-assistant")
	}

	// Should NOT have a Dockerfile (image-based, not build-based).
	if _, err := os.Stat(DockerfilePath(homeDir, "my-custom-assistant")); !os.IsNotExist(err) {
		t.Errorf("Dockerfile should not exist for unknown assistant, got err: %v", err)
	}

	// Data directory should still be created.
	dataDir := DataPath(homeDir, "my-custom-assistant")
	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("Stat(%q) error: %v", dataDir, err)
	}
	if !info.IsDir() {
		t.Errorf("data path is not a directory")
	}
}

func TestSeedBaseDockerfile(t *testing.T) {
	seed := []byte("# seed\nFROM debian:bookworm-slim\n")

	t.Run("writes file when absent", func(t *testing.T) {
		homeDir := t.TempDir()

		wrote, err := SeedBaseDockerfile(homeDir, seed)
		if err != nil {
			t.Fatalf("SeedBaseDockerfile() unexpected error: %v", err)
		}
		if !wrote {
			t.Errorf("SeedBaseDockerfile() wrote = false, want true")
		}

		got, err := os.ReadFile(BaseDockerfilePath(homeDir))
		if err != nil {
			t.Fatalf("ReadFile() error: %v", err)
		}
		if string(got) != string(seed) {
			t.Errorf("file contents = %q, want %q", string(got), string(seed))
		}
	})

	t.Run("returns wrote=false when file exists", func(t *testing.T) {
		homeDir := t.TempDir()
		path := BaseDockerfilePath(homeDir)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() error: %v", err)
		}
		existing := []byte("FROM ubuntu:24.04\n# user edits\n")
		if err := os.WriteFile(path, existing, 0o644); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}

		wrote, err := SeedBaseDockerfile(homeDir, seed)
		if err != nil {
			t.Fatalf("SeedBaseDockerfile() unexpected error: %v", err)
		}
		if wrote {
			t.Errorf("SeedBaseDockerfile() wrote = true, want false (file exists)")
		}

		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile() error: %v", err)
		}
		if string(got) != string(existing) {
			t.Errorf("file contents = %q, want %q (should not be overwritten)", string(got), string(existing))
		}
	})

	t.Run("creates parent directory with mode 0o755", func(t *testing.T) {
		homeDir := t.TempDir()

		if _, err := SeedBaseDockerfile(homeDir, seed); err != nil {
			t.Fatalf("SeedBaseDockerfile() unexpected error: %v", err)
		}

		baseDir := filepath.Join(homeDir, ".confine-ai", "base")
		info, err := os.Stat(baseDir)
		if err != nil {
			t.Fatalf("Stat(%q) error: %v", baseDir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%q is not a directory", baseDir)
		}
		if got := info.Mode().Perm(); got != 0o755 {
			t.Errorf("base directory mode = %o, want %o", got, 0o755)
		}
	})

	t.Run("writes file with mode 0o644", func(t *testing.T) {
		homeDir := t.TempDir()

		if _, err := SeedBaseDockerfile(homeDir, seed); err != nil {
			t.Fatalf("SeedBaseDockerfile() unexpected error: %v", err)
		}

		info, err := os.Stat(BaseDockerfilePath(homeDir))
		if err != nil {
			t.Fatalf("Stat() error: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o644 {
			t.Errorf("file mode = %o, want %o", got, 0o644)
		}
	})
}

func TestRenderHostMounts(t *testing.T) {
	t.Run("empty input returns nil", func(t *testing.T) {
		if got := renderHostMounts(nil); got != nil {
			t.Errorf("renderHostMounts(nil) = %v, want nil", got)
		}
		if got := renderHostMounts([]hostMount{}); got != nil {
			t.Errorf("renderHostMounts([]) = %v, want nil", got)
		}
	})

	t.Run("read-write entry has three comma fields", func(t *testing.T) {
		got := renderHostMounts([]hostMount{
			{Source: "${localEnv:HOME}/.local/share/opencode", Target: "/home/dev/.local/share/opencode"},
		})
		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1", len(got))
		}
		want := "type=bind,source=${localEnv:HOME}/.local/share/opencode,target=/home/dev/.local/share/opencode"
		if got[0] != want {
			t.Errorf("renderHostMounts()[0] = %q, want %q", got[0], want)
		}
		if strings.HasSuffix(got[0], ",readonly") {
			t.Errorf("renderHostMounts()[0] = %q, want without %q suffix", got[0], ",readonly")
		}
		if n := len(strings.Split(got[0], ",")); n != 3 {
			t.Errorf("comma field count = %d, want 3", n)
		}
	})

	t.Run("read-only entry appends readonly", func(t *testing.T) {
		// Pins the init.go ReadOnly branch which the production map does
		// not exercise. When a future host mount sets ReadOnly: true, the
		// emission must append ",readonly" so the runtime refuses writes.
		got := renderHostMounts([]hostMount{
			{Source: "${localEnv:HOME}/.config/foo", Target: "/home/dev/.config/foo", ReadOnly: true},
		})
		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1", len(got))
		}
		if !strings.HasSuffix(got[0], ",readonly") {
			t.Errorf("renderHostMounts()[0] = %q, want suffix %q", got[0], ",readonly")
		}
		// Sanity: still contains the base three fields plus the readonly
		// suffix, so the full string has four comma-separated fields.
		if n := len(strings.Split(got[0], ",")); n != 4 {
			t.Errorf("comma field count = %d, want 4 (type, source, target, readonly)", n)
		}
	})

	t.Run("mixed slice preserves order and flags", func(t *testing.T) {
		got := renderHostMounts([]hostMount{
			{Source: "/a", Target: "/A"},
			{Source: "/b", Target: "/B", ReadOnly: true},
		})
		if len(got) != 2 {
			t.Fatalf("len(got) = %d, want 2", len(got))
		}
		if got[0] != "type=bind,source=/a,target=/A" {
			t.Errorf("got[0] = %q, want %q", got[0], "type=bind,source=/a,target=/A")
		}
		if got[1] != "type=bind,source=/b,target=/B,readonly" {
			t.Errorf("got[1] = %q, want %q", got[1], "type=bind,source=/b,target=/B,readonly")
		}
	})
}

func TestHostMountSources(t *testing.T) {
	t.Run("opencode expands home and returns single source", func(t *testing.T) {
		got := HostMountSources("/home/alice", "opencode")
		want := []string{"/home/alice/.local/share/opencode"}
		if len(got) != len(want) || got[0] != want[0] {
			t.Errorf("HostMountSources(/home/alice, opencode) = %v, want %v", got, want)
		}
	})

	t.Run("claude returns nil", func(t *testing.T) {
		if got := HostMountSources("/home/alice", "claude"); got != nil {
			t.Errorf("HostMountSources(claude) = %v, want nil", got)
		}
	})

	t.Run("copilot returns nil", func(t *testing.T) {
		if got := HostMountSources("/home/alice", "copilot"); got != nil {
			t.Errorf("HostMountSources(copilot) = %v, want nil", got)
		}
	})

	t.Run("unknown assistant returns nil", func(t *testing.T) {
		if got := HostMountSources("/home/alice", "my-tool"); got != nil {
			t.Errorf("HostMountSources(my-tool) = %v, want nil", got)
		}
	})
}

func TestInit_InvalidName(t *testing.T) {
	homeDir := t.TempDir()

	err := Init(homeDir, "A", []byte("FROM test\n"))
	if err == nil {
		t.Fatal("Init() = nil, want error for invalid name")
	}
	// Should be a validation error, not a filesystem error.
	if !strings.Contains(err.Error(), "assistant name") {
		t.Errorf("Init() error = %q, want containing %q", err.Error(), "assistant name")
	}

	// Verify no directories created.
	if _, err := os.Stat(Dir(homeDir, "A")); !os.IsNotExist(err) {
		t.Errorf("assistant directory should not exist for invalid name, got err: %v", err)
	}
}
