package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDevcontainer writes a minimal devcontainer.json at the canonical
// discovery location under workspaceFolder and returns the path. If
// relativePath is empty, uses .devcontainer/devcontainer.json.
func writeDevcontainer(t *testing.T, workspaceFolder, relativePath, content string) string {
	t.Helper()
	if relativePath == "" {
		relativePath = filepath.Join(".devcontainer", "devcontainer.json")
	}
	path := filepath.Join(workspaceFolder, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestLoadFromWorkspace(t *testing.T) {
	noEnv := func(string) (string, bool) { return "", false }

	t.Run("discovery honours default location", func(t *testing.T) {
		ws := t.TempDir()
		path := writeDevcontainer(t, ws, "", `{"image": "localhost/confine-ai-base:latest"}`)

		var stderr bytes.Buffer
		cfg, gotPath, err := LoadFromWorkspace(ws, "", &stderr, noEnv)
		if err != nil {
			t.Fatalf("LoadFromWorkspace() error: %v", err)
		}
		if gotPath != path {
			t.Errorf("path = %q, want %q", gotPath, path)
		}
		if cfg.Image != "localhost/confine-ai-base:latest" {
			t.Errorf("cfg.Image = %q, want %q", cfg.Image, "localhost/confine-ai-base:latest")
		}
	})

	t.Run("explicit override path bypasses discovery", func(t *testing.T) {
		ws := t.TempDir()
		// Put the config outside the default discovery location.
		override := filepath.Join(ws, "custom.json")
		if err := os.WriteFile(override, []byte(`{"image": "custom:tag"}`), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		var stderr bytes.Buffer
		cfg, gotPath, err := LoadFromWorkspace(ws, override, &stderr, noEnv)
		if err != nil {
			t.Fatalf("LoadFromWorkspace() error: %v", err)
		}
		if gotPath != override {
			t.Errorf("path = %q, want %q", gotPath, override)
		}
		if cfg.Image != "custom:tag" {
			t.Errorf("cfg.Image = %q, want %q", cfg.Image, "custom:tag")
		}
	})

	t.Run("discovery failure propagated as config discovery error", func(t *testing.T) {
		ws := t.TempDir() // empty workspace

		var stderr bytes.Buffer
		_, _, err := LoadFromWorkspace(ws, "", &stderr, noEnv)
		if err == nil {
			t.Fatal("LoadFromWorkspace() = nil, want discovery error")
		}
		if !strings.Contains(err.Error(), "config discovery") {
			t.Errorf("error = %q, want containing 'config discovery'", err.Error())
		}
	})

	t.Run("parse error propagated as config parse error", func(t *testing.T) {
		ws := t.TempDir()
		writeDevcontainer(t, ws, "", `{"image": "unterminated`)

		var stderr bytes.Buffer
		_, _, err := LoadFromWorkspace(ws, "", &stderr, noEnv)
		if err == nil {
			t.Fatal("LoadFromWorkspace() = nil, want parse error")
		}
		if !strings.Contains(err.Error(), "config parse") {
			t.Errorf("error = %q, want containing 'config parse'", err.Error())
		}
	})

	t.Run("substitute error propagated as config substitute error", func(t *testing.T) {
		ws := t.TempDir()
		// Unknown variable reference ${localEnv:MISSING_VAR} with no lookup provider
		// triggers a substitute error only if the value is required; the
		// substitution is permissive for unknown envs, returning empty string.
		// Instead, force an error by using a malformed variable pattern that
		// Substitute rejects — ${localEnv} with no name, for example.
		writeDevcontainer(t, ws, "",
			`{"image": "localhost/base:latest", "workspaceFolder": "${invalidVar}"}`)

		var stderr bytes.Buffer
		_, _, err := LoadFromWorkspace(ws, "", &stderr, noEnv)
		if err == nil {
			t.Fatal("LoadFromWorkspace() = nil, want substitute error")
		}
		if !strings.Contains(err.Error(), "config substitute") {
			t.Errorf("error = %q, want containing 'config substitute'", err.Error())
		}
	})

	t.Run("load warnings written to stderr", func(t *testing.T) {
		ws := t.TempDir()
		// A containerEnv key matching a credential pattern produces a Load warning.
		writeDevcontainer(t, ws, "",
			`{"image": "localhost/base:latest", "containerEnv": {"API_TOKEN": "secret"}}`)

		var stderr bytes.Buffer
		_, _, err := LoadFromWorkspace(ws, "", &stderr, noEnv)
		if err != nil {
			t.Fatalf("LoadFromWorkspace() unexpected error: %v", err)
		}
		if !strings.Contains(stderr.String(), "warning:") {
			t.Errorf("stderr = %q, want containing 'warning:'", stderr.String())
		}
	})
}
