package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woditschka/confine-ai/internal/assistant"
)

func TestRunInitWith(t *testing.T) {
	seed := []byte("# seed\nFROM ubuntu:24.04\n")
	dockerfiles := map[string][]byte{
		"claude": []byte("FROM seeded\n"),
	}

	t.Run("base only seeds dockerfile", func(t *testing.T) {
		homeDir := t.TempDir()
		var stdout, stderr bytes.Buffer
		err := runInitWith(strings.NewReader(""), &stdout, &stderr, homeDir, "", seed, dockerfiles, true, false)
		if err != nil {
			t.Fatalf("runInitWith() unexpected error: %v", err)
		}
		if !strings.Contains(stdout.String(), "Seeded base Dockerfile") {
			t.Errorf("runInitWith() stdout = %q, want containing %q", stdout.String(), "Seeded base Dockerfile")
		}
	})

	t.Run("base and assistant seeds both", func(t *testing.T) {
		homeDir := t.TempDir()
		var stdout, stderr bytes.Buffer
		err := runInitWith(strings.NewReader(""), &stdout, &stderr, homeDir, "claude", seed, dockerfiles, true, false)
		if err != nil {
			t.Fatalf("runInitWith() unexpected error: %v", err)
		}
		output := stdout.String()
		if !strings.Contains(output, "Seeded base Dockerfile") {
			t.Errorf("runInitWith() stdout = %q, want containing %q", output, "Seeded base Dockerfile")
		}
		if !strings.Contains(output, "Initialized assistant") {
			t.Errorf("runInitWith() stdout = %q, want containing %q", output, "Initialized assistant")
		}
		if !assistant.Exists(homeDir, "claude") {
			t.Error("runInitWith() did not create assistant directory")
		}
	})

	t.Run("base dockerfile error propagated", func(t *testing.T) {
		// Use a homeDir where base path parent is a file, not a directory,
		// causing SeedBaseDockerfile to fail.
		homeDir := t.TempDir()
		confineDir := filepath.Join(homeDir, ".confine-ai")
		// Create a file where a directory is expected.
		if err := os.WriteFile(confineDir, []byte("blocker"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		var stdout, stderr bytes.Buffer
		err := runInitWith(strings.NewReader(""), &stdout, &stderr, homeDir, "", seed, dockerfiles, true, false)
		if err == nil {
			t.Fatal("runInitWith() = nil, want error for base dockerfile failure")
		}
		if !strings.Contains(err.Error(), "init") {
			t.Errorf("runInitWith() error = %q, want containing %q", err.Error(), "init")
		}
	})

	t.Run("help flag returns nil", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := RunInit(&stdout, &stderr, []string{"--help"}, seed, dockerfiles)
		if err != nil {
			t.Fatalf("RunInit(--help) unexpected error: %v", err)
		}
	})
}

func TestHandleBaseDockerfile(t *testing.T) {
	seed := []byte("# seed\nFROM ubuntu:24.04\n")
	userCopy := []byte("# user edited\nFROM ubuntu:24.04\n")

	writeExistingBase := func(t *testing.T, homeDir string) string {
		t.Helper()
		basePath := assistant.BaseDockerfilePath(homeDir)
		if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(basePath, userCopy, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		return basePath
	}

	t.Run("absent seeds file", func(t *testing.T) {
		homeDir := t.TempDir()
		basePath := assistant.BaseDockerfilePath(homeDir)

		var stdout, stderr bytes.Buffer
		err := handleBaseDockerfile(strings.NewReader(""), &stdout, &stderr, homeDir, basePath, seed, false, true)
		if err != nil {
			t.Fatalf("handleBaseDockerfile() error: %v", err)
		}
		if !strings.Contains(stdout.String(), "Seeded base Dockerfile") {
			t.Errorf("stdout = %q, want 'Seeded base Dockerfile'", stdout.String())
		}
		got, err := os.ReadFile(basePath)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if !bytes.Equal(got, seed) {
			t.Errorf("base file = %q, want seed", string(got))
		}
	})

	t.Run("exists, assumeYes overwrites without prompting", func(t *testing.T) {
		homeDir := t.TempDir()
		basePath := writeExistingBase(t, homeDir)

		var stdout, stderr bytes.Buffer
		err := handleBaseDockerfile(strings.NewReader(""), &stdout, &stderr, homeDir, basePath, seed, true, false)
		if err != nil {
			t.Fatalf("handleBaseDockerfile() error: %v", err)
		}
		if !strings.Contains(stdout.String(), "Overwrote base Dockerfile") {
			t.Errorf("stdout = %q, want 'Overwrote base Dockerfile'", stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("stderr = %q, want empty (no prompt)", stderr.String())
		}
		got, err := os.ReadFile(basePath)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if !bytes.Equal(got, seed) {
			t.Errorf("base file = %q, want seed", string(got))
		}
	})

	t.Run("exists, non-interactive preserves file", func(t *testing.T) {
		homeDir := t.TempDir()
		basePath := writeExistingBase(t, homeDir)

		var stdout, stderr bytes.Buffer
		err := handleBaseDockerfile(strings.NewReader(""), &stdout, &stderr, homeDir, basePath, seed, false, false)
		if err != nil {
			t.Fatalf("handleBaseDockerfile() error: %v", err)
		}
		if !strings.Contains(stdout.String(), "already present") {
			t.Errorf("stdout = %q, want 'already present'", stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("stderr = %q, want empty (no prompt)", stderr.String())
		}
		got, err := os.ReadFile(basePath)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if !bytes.Equal(got, userCopy) {
			t.Errorf("base file = %q, want unchanged", string(got))
		}
	})

	promptCases := []struct {
		name      string
		input     string
		overwrote bool
	}{
		{"empty accepts default yes", "\n", true},
		{"y overwrites", "y\n", true},
		{"Y overwrites", "Y\n", true},
		{"yes overwrites", "yes\n", true},
		{"n preserves", "n\n", false},
		{"no preserves", "no\n", false},
		{"eof preserves", "", false},
	}
	for _, tc := range promptCases {
		t.Run("interactive prompt: "+tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			basePath := writeExistingBase(t, homeDir)

			var stdout, stderr bytes.Buffer
			err := handleBaseDockerfile(strings.NewReader(tc.input), &stdout, &stderr, homeDir, basePath, seed, false, true)
			if err != nil {
				t.Fatalf("handleBaseDockerfile() error: %v", err)
			}
			if !strings.Contains(stderr.String(), "Overwrite? [Y/n]") {
				t.Errorf("stderr = %q, want prompt", stderr.String())
			}
			got, err := os.ReadFile(basePath)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			want := userCopy
			wantMsg := "already present"
			if tc.overwrote {
				want = seed
				wantMsg = "Overwrote base Dockerfile"
			}
			if !bytes.Equal(got, want) {
				t.Errorf("base file = %q, want %q", string(got), string(want))
			}
			if !strings.Contains(stdout.String(), wantMsg) {
				t.Errorf("stdout = %q, want %q", stdout.String(), wantMsg)
			}
		})
	}
}

func TestHandleAssistantInit(t *testing.T) {
	// testDockerfiles provides a minimal Dockerfile for "claude" so
	// handleAssistantInit can scaffold a fresh assistant without needing the
	// real embedded samples.
	testDockerfiles := map[string][]byte{
		"claude": []byte("FROM seeded\n"),
	}

	// seedAssistant creates an existing assistant config directory with a
	// sentinel Dockerfile and a pre-populated data file so the test can
	// verify that overwrite rewrites config but preserves data.
	seedAssistant := func(t *testing.T, homeDir string) (assistantDir, dataFile string) {
		t.Helper()
		if err := assistant.Init(homeDir, "claude", []byte("FROM original\n")); err != nil {
			t.Fatalf("seed Init: %v", err)
		}
		assistantDir = assistant.Dir(homeDir, "claude")
		dataFile = filepath.Join(assistant.DataPath(homeDir, "claude"), "sentinel")
		if err := os.WriteFile(dataFile, []byte("keep me"), 0o644); err != nil {
			t.Fatalf("write sentinel: %v", err)
		}
		return assistantDir, dataFile
	}

	t.Run("absent creates assistant", func(t *testing.T) {
		homeDir := t.TempDir()
		var stdout, stderr bytes.Buffer
		err := handleAssistantInit(strings.NewReader(""), &stdout, &stderr, homeDir, "claude", testDockerfiles, false, true)
		if err != nil {
			t.Fatalf("handleAssistantInit() error: %v", err)
		}
		if !strings.Contains(stdout.String(), "Initialized assistant") {
			t.Errorf("stdout = %q, want 'Initialized assistant'", stdout.String())
		}
		if !assistant.Exists(homeDir, "claude") {
			t.Errorf("assistant dir not created")
		}
	})

	t.Run("exists, assumeYes overwrites without prompting", func(t *testing.T) {
		homeDir := t.TempDir()
		_, dataFile := seedAssistant(t, homeDir)

		var stdout, stderr bytes.Buffer
		err := handleAssistantInit(strings.NewReader(""), &stdout, &stderr, homeDir, "claude", testDockerfiles, true, false)
		if err != nil {
			t.Fatalf("handleAssistantInit() error: %v", err)
		}
		if !strings.Contains(stdout.String(), "Initialized assistant") {
			t.Errorf("stdout = %q, want 'Initialized assistant'", stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("stderr = %q, want empty (no prompt)", stderr.String())
		}
		df, err := os.ReadFile(assistant.DockerfilePath(homeDir, "claude"))
		if err != nil {
			t.Fatalf("ReadFile Dockerfile: %v", err)
		}
		if bytes.Equal(df, []byte("FROM original\n")) {
			t.Errorf("Dockerfile not overwritten")
		}
		// Data dir must survive overwrite.
		got, err := os.ReadFile(dataFile)
		if err != nil {
			t.Fatalf("ReadFile sentinel: %v", err)
		}
		if string(got) != "keep me" {
			t.Errorf("sentinel = %q, want 'keep me' (data dir must survive)", string(got))
		}
	})

	t.Run("exists, non-interactive preserves assistant", func(t *testing.T) {
		homeDir := t.TempDir()
		seedAssistant(t, homeDir)

		var stdout, stderr bytes.Buffer
		err := handleAssistantInit(strings.NewReader(""), &stdout, &stderr, homeDir, "claude", testDockerfiles, false, false)
		if err != nil {
			t.Fatalf("handleAssistantInit() error: %v", err)
		}
		if !strings.Contains(stdout.String(), "already present") {
			t.Errorf("stdout = %q, want 'already present'", stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("stderr = %q, want empty (no prompt)", stderr.String())
		}
		df, err := os.ReadFile(assistant.DockerfilePath(homeDir, "claude"))
		if err != nil {
			t.Fatalf("ReadFile Dockerfile: %v", err)
		}
		if !bytes.Equal(df, []byte("FROM original\n")) {
			t.Errorf("Dockerfile = %q, want unchanged", string(df))
		}
	})

	promptCases := []struct {
		name      string
		input     string
		overwrote bool
	}{
		{"empty accepts default yes", "\n", true},
		{"y overwrites", "y\n", true},
		{"Y overwrites", "Y\n", true},
		{"yes overwrites", "yes\n", true},
		{"n preserves", "n\n", false},
		{"no preserves", "no\n", false},
		{"eof preserves", "", false},
	}
	for _, tc := range promptCases {
		t.Run("interactive prompt: "+tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			seedAssistant(t, homeDir)

			var stdout, stderr bytes.Buffer
			err := handleAssistantInit(strings.NewReader(tc.input), &stdout, &stderr, homeDir, "claude", testDockerfiles, false, true)
			if err != nil {
				t.Fatalf("handleAssistantInit() error: %v", err)
			}
			if !strings.Contains(stderr.String(), "Overwrite? [Y/n]") {
				t.Errorf("stderr = %q, want prompt", stderr.String())
			}
			df, err := os.ReadFile(assistant.DockerfilePath(homeDir, "claude"))
			if err != nil {
				t.Fatalf("ReadFile Dockerfile: %v", err)
			}
			wasOriginal := bytes.Equal(df, []byte("FROM original\n"))
			if tc.overwrote && wasOriginal {
				t.Errorf("Dockerfile not overwritten; stdout=%q", stdout.String())
			}
			if !tc.overwrote && !wasOriginal {
				t.Errorf("Dockerfile changed but should be preserved; stdout=%q", stdout.String())
			}
			wantMsg := "already present"
			if tc.overwrote {
				wantMsg = "Initialized assistant"
			}
			if !strings.Contains(stdout.String(), wantMsg) {
				t.Errorf("stdout = %q, want %q", stdout.String(), wantMsg)
			}
		})
	}
}
