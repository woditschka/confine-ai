package config

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// stubLookupEnv returns a lookup function that resolves the given map entries.
// Variables not in the map return ("", false).
func stubLookupEnv(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

func TestSubstituteString(t *testing.T) {
	workspace := "/home/user/project"
	ctx := &substContext{
		workspaceFolder:         workspace,
		workspaceFolderBasename: "project",
		devcontainerID:          devcontainerID(workspace),
		lookupEnv: stubLookupEnv(map[string]string{
			"HOME":      "/home/user",
			"GOPATH":    "/home/user/go",
			"EMPTY_VAR": "",
		}),
	}

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		// Cycle 1: Simple variable patterns.
		{
			name:  "localWorkspaceFolder",
			input: "${localWorkspaceFolder}",
			want:  "/home/user/project",
		},
		{
			name:  "localWorkspaceFolderBasename",
			input: "${localWorkspaceFolderBasename}",
			want:  "project",
		},
		{
			name:  "devcontainerId",
			input: "${devcontainerId}",
			want:  devcontainerID(workspace),
		},

		// Cycle 2: localEnv with set variable.
		{
			name:  "localEnv HOME set",
			input: "${localEnv:HOME}",
			want:  "/home/user",
		},
		{
			name:  "localEnv GOPATH set",
			input: "${localEnv:GOPATH}",
			want:  "/home/user/go",
		},
		{
			name:  "localEnv empty string value",
			input: "${localEnv:EMPTY_VAR}",
			want:  "",
		},

		// Cycle 3: localEnv with default (variable unset).
		{
			name:  "localEnv unset with default",
			input: "${localEnv:MISSING:fallback}",
			want:  "fallback",
		},
		{
			name:  "localEnv set ignores default",
			input: "${localEnv:HOME:/other}",
			want:  "/home/user",
		},
		{
			name:  "localEnv unset with empty default",
			input: "${localEnv:MISSING:}",
			want:  "",
		},
		{
			name:  "localEnv unset with default containing colons",
			input: "${localEnv:MISSING:/usr/bin:/usr/local/bin}",
			want:  "/usr/bin:/usr/local/bin",
		},

		// Cycle 4: localEnv error when unset and no default.
		{
			name:    "localEnv unset no default",
			input:   "${localEnv:MISSING}",
			wantErr: `environment variable "MISSING" is not set`,
		},

		// Cycle 5: Error cases.
		{
			name:    "unknown pattern",
			input:   "${unknownPattern}",
			wantErr: `unknown variable pattern "unknownPattern"`,
		},
		{
			name:    "unclosed variable reference",
			input:   "prefix ${localWorkspaceFolder",
			wantErr: "unclosed variable reference",
		},
		{
			name:    "unclosed at end",
			input:   "${",
			wantErr: "unclosed variable reference",
		},

		// Cycle 8: Edge cases.
		{
			name:  "no patterns",
			input: "just a plain string",
			want:  "just a plain string",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "text with variable in middle",
			input: "prefix-${localWorkspaceFolder}-suffix",
			want:  "prefix-/home/user/project-suffix",
		},
		{
			name:  "multiple variables in one string",
			input: "${localWorkspaceFolder}/${localWorkspaceFolderBasename}",
			want:  "/home/user/project/project",
		},
		{
			name:  "multiple localEnv in one string",
			input: "${localEnv:HOME}:${localEnv:GOPATH}",
			want:  "/home/user:/home/user/go",
		},
		{
			name:  "dollar sign without brace",
			input: "price is $5",
			want:  "price is $5",
		},
		{
			name:  "dollar sign followed by non-brace",
			input: "$HOME is not a pattern",
			want:  "$HOME is not a pattern",
		},
		{
			name:  "literal text around variable",
			input: "name=${localEnv:HOME}/.config",
			want:  "name=/home/user/.config",
		},
		{
			name:    "localEnv with empty variable name",
			input:   "${localEnv:}",
			wantErr: `environment variable "" is not set`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := substituteString(tt.input, ctx)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("substituteString(%q) = %q, want error containing %q", tt.input, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("substituteString(%q) error = %q, want error containing %q", tt.input, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("substituteString(%q) unexpected error: %v", tt.input, err)
			}

			if got != tt.want {
				t.Errorf("substituteString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDevcontainerID(t *testing.T) {
	// devcontainerID must return the lowercase hex SHA-256 of the workspace path.
	workspace := "/home/user/project"
	got := devcontainerID(workspace)

	hash := sha256.Sum256([]byte(workspace))
	want := hex.EncodeToString(hash[:])

	if got != want {
		t.Errorf("devcontainerID(%q) = %q, want %q", workspace, got, want)
	}

	// Must be 64 hex characters.
	if len(got) != 64 {
		t.Errorf("devcontainerID(%q) length = %d, want 64", workspace, len(got))
	}

	// Must be deterministic.
	got2 := devcontainerID(workspace)
	if got != got2 {
		t.Errorf("devcontainerID(%q) not deterministic: %q != %q", workspace, got, got2)
	}

	// Different paths produce different IDs.
	other := devcontainerID("/other/path")
	if got == other {
		t.Errorf("devcontainerID(%q) == devcontainerID(%q), want different", workspace, "/other/path")
	}
}

func TestSubstitute(t *testing.T) {
	workspace := "/home/user/project"
	lookup := stubLookupEnv(map[string]string{
		"HOME":   "/home/user",
		"GOPATH": "/home/user/go",
	})

	tests := []struct {
		name    string
		cfg     Config
		want    Config
		wantErr string
	}{
		// Cycle 6: All string fields are substituted.
		{
			name: "substitutes Name",
			cfg: Config{
				Image: "ubuntu",
				Name:  "${localWorkspaceFolderBasename}",
			},
			want: Config{
				Image: "ubuntu",
				Name:  "project",
			},
		},
		{
			name: "substitutes Image",
			cfg: Config{
				Image: "ubuntu:${localEnv:HOME}",
			},
			want: Config{
				Image: "ubuntu:/home/user",
			},
		},
		{
			name: "substitutes WorkspaceFolder",
			cfg: Config{
				Image:           "ubuntu",
				WorkspaceFolder: "/workspaces/${localWorkspaceFolderBasename}",
			},
			want: Config{
				Image:           "ubuntu",
				WorkspaceFolder: "/workspaces/project",
			},
		},
		{
			name: "substitutes RemoteUser",
			cfg: Config{
				Image:      "ubuntu",
				RemoteUser: "${localEnv:HOME}",
			},
			want: Config{
				Image:      "ubuntu",
				RemoteUser: "/home/user",
			},
		},
		{
			name: "substitutes ContainerUser",
			cfg: Config{
				Image:         "ubuntu",
				ContainerUser: "${localEnv:HOME}",
			},
			want: Config{
				Image:         "ubuntu",
				ContainerUser: "/home/user",
			},
		},
		{
			name: "substitutes ContainerEnv values",
			cfg: Config{
				Image: "ubuntu",
				ContainerEnv: map[string]string{
					"WORKSPACE": "${localWorkspaceFolder}",
					"STATIC":    "no-change",
				},
			},
			want: Config{
				Image: "ubuntu",
				ContainerEnv: map[string]string{
					"WORKSPACE": "/home/user/project",
					"STATIC":    "no-change",
				},
			},
		},
		{
			name: "substitutes Mounts entries",
			cfg: Config{
				Image: "ubuntu",
				Mounts: []string{
					"source=${localWorkspaceFolder},target=/workspaces/project,type=bind",
				},
			},
			want: Config{
				Image: "ubuntu",
				Mounts: []string{
					"source=/home/user/project,target=/workspaces/project,type=bind",
				},
			},
		},
		{
			name: "substitutes Build.Dockerfile",
			cfg: Config{
				Build: &Build{
					Dockerfile: "${localEnv:HOME}/Dockerfile",
				},
			},
			want: Config{
				Build: &Build{
					Dockerfile: "/home/user/Dockerfile",
				},
			},
		},
		{
			name: "substitutes Build.Context",
			cfg: Config{
				Build: &Build{
					Dockerfile: "Dockerfile",
					Context:    "${localWorkspaceFolder}",
				},
			},
			want: Config{
				Build: &Build{
					Dockerfile: "Dockerfile",
					Context:    "/home/user/project",
				},
			},
		},
		{
			name: "substitutes Build.Args values",
			cfg: Config{
				Build: &Build{
					Dockerfile: "Dockerfile",
					Args: map[string]string{
						"BASE_DIR": "${localWorkspaceFolder}",
						"VERSION":  "1.0",
					},
				},
			},
			want: Config{
				Build: &Build{
					Dockerfile: "Dockerfile",
					Args: map[string]string{
						"BASE_DIR": "/home/user/project",
						"VERSION":  "1.0",
					},
				},
			},
		},
		{
			name: "nil Build is preserved",
			cfg: Config{
				Image: "ubuntu",
				Build: nil,
			},
			want: Config{
				Image: "ubuntu",
				Build: nil,
			},
		},
		{
			name: "nil maps and slices are preserved",
			cfg: Config{
				Image: "ubuntu",
			},
			want: Config{
				Image: "ubuntu",
			},
		},
		// Customizations passthrough.
		{
			name: "customizations preserved through substitution",
			cfg: Config{
				Image: "ubuntu",
				Customizations: &Customizations{
					Memory: "8g",
					CPUs:   "4",
				},
			},
			want: Config{
				Image: "ubuntu",
				Customizations: &Customizations{
					Memory: "8g",
					CPUs:   "4",
				},
			},
		},
		{
			name: "nil customizations preserved",
			cfg: Config{
				Image:          "ubuntu",
				Customizations: nil,
			},
			want: Config{
				Image:          "ubuntu",
				Customizations: nil,
			},
		},
		{
			name: "all fields with variables",
			cfg: Config{
				Name:            "${localWorkspaceFolderBasename}-dev",
				Image:           "ubuntu",
				WorkspaceFolder: "/workspaces/${localWorkspaceFolderBasename}",
				RemoteUser:      "node",
				ContainerUser:   "node",
				ContainerEnv: map[string]string{
					"PROJECT": "${localWorkspaceFolderBasename}",
					"HOME":    "${localEnv:HOME}",
				},
				Mounts: []string{
					"source=${localWorkspaceFolder}/.claude,target=/home/node/.claude,type=bind",
				},
				Build: &Build{
					Dockerfile: "Dockerfile",
					Context:    "${localWorkspaceFolder}",
					Args: map[string]string{
						"WORKSPACE": "${localWorkspaceFolder}",
					},
				},
			},
			want: Config{
				Name:            "project-dev",
				Image:           "ubuntu",
				WorkspaceFolder: "/workspaces/project",
				RemoteUser:      "node",
				ContainerUser:   "node",
				ContainerEnv: map[string]string{
					"PROJECT": "project",
					"HOME":    "/home/user",
				},
				Mounts: []string{
					"source=/home/user/project/.claude,target=/home/node/.claude,type=bind",
				},
				Build: &Build{
					Dockerfile: "Dockerfile",
					Context:    "/home/user/project",
					Args: map[string]string{
						"WORKSPACE": "/home/user/project",
					},
				},
			},
		},
		// Error propagation.
		{
			name: "error in Name propagates",
			cfg: Config{
				Image: "ubuntu",
				Name:  "${localEnv:MISSING}",
			},
			wantErr: `environment variable "MISSING" is not set`,
		},
		{
			name: "error in ContainerEnv value propagates",
			cfg: Config{
				Image: "ubuntu",
				ContainerEnv: map[string]string{
					"KEY": "${localEnv:MISSING}",
				},
			},
			wantErr: `environment variable "MISSING" is not set`,
		},
		{
			name: "error in Mounts entry propagates",
			cfg: Config{
				Image:  "ubuntu",
				Mounts: []string{"${unknownVar}"},
			},
			wantErr: `unknown variable pattern "unknownVar"`,
		},
		{
			name: "error in Build.Args value propagates",
			cfg: Config{
				Build: &Build{
					Dockerfile: "Dockerfile",
					Args: map[string]string{
						"KEY": "${localEnv:MISSING}",
					},
				},
			},
			wantErr: `environment variable "MISSING" is not set`,
		},
		// No recursive expansion.
		{
			name: "no recursive expansion",
			cfg: Config{
				Image: "ubuntu",
				Name:  "${localEnv:HOME}",
			},
			want: Config{
				Image: "ubuntu",
				Name:  "/home/user",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Substitute(tt.cfg, workspace, lookup)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Substitute() = %v, want error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Substitute() error = %q, want error containing %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("Substitute() unexpected error: %v", err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Substitute() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Cycle 7: Substitute does not mutate input.
func TestSubstitute_NoMutation(t *testing.T) {
	workspace := "/home/user/project"
	lookup := stubLookupEnv(map[string]string{
		"HOME": "/home/user",
	})

	original := Config{
		Name:            "${localWorkspaceFolderBasename}",
		Image:           "ubuntu",
		WorkspaceFolder: "/workspaces/${localWorkspaceFolderBasename}",
		ContainerEnv: map[string]string{
			"PROJECT": "${localWorkspaceFolderBasename}",
		},
		Mounts: []string{
			"source=${localWorkspaceFolder},target=/dst,type=bind",
		},
		Build: &Build{
			Dockerfile: "Dockerfile",
			Context:    "${localWorkspaceFolder}",
			Args: map[string]string{
				"DIR": "${localWorkspaceFolder}",
			},
		},
	}

	// Deep copy the originals for comparison.
	wantName := original.Name
	wantWorkspace := original.WorkspaceFolder
	wantEnvValue := original.ContainerEnv["PROJECT"]
	wantMount := original.Mounts[0]
	wantContext := original.Build.Context
	wantArgsValue := original.Build.Args["DIR"]

	_, err := Substitute(original, workspace, lookup)
	if err != nil {
		t.Fatalf("Substitute() unexpected error: %v", err)
	}

	// Verify input was not mutated.
	if original.Name != wantName {
		t.Errorf("Substitute() mutated input Name: got %q, want %q", original.Name, wantName)
	}
	if original.WorkspaceFolder != wantWorkspace {
		t.Errorf("Substitute() mutated input WorkspaceFolder: got %q, want %q", original.WorkspaceFolder, wantWorkspace)
	}
	if original.ContainerEnv["PROJECT"] != wantEnvValue {
		t.Errorf("Substitute() mutated input ContainerEnv: got %q, want %q", original.ContainerEnv["PROJECT"], wantEnvValue)
	}
	if original.Mounts[0] != wantMount {
		t.Errorf("Substitute() mutated input Mounts: got %q, want %q", original.Mounts[0], wantMount)
	}
	if original.Build.Context != wantContext {
		t.Errorf("Substitute() mutated input Build.Context: got %q, want %q", original.Build.Context, wantContext)
	}
	if original.Build.Args["DIR"] != wantArgsValue {
		t.Errorf("Substitute() mutated input Build.Args: got %q, want %q", original.Build.Args["DIR"], wantArgsValue)
	}
}

// TestSubstitute_ErrorIncludesFieldContext verifies error messages include field context.
func TestSubstitute_ErrorIncludesFieldContext(t *testing.T) {
	workspace := "/home/user/project"
	lookup := stubLookupEnv(nil)

	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "error mentions name field",
			cfg: Config{
				Image: "ubuntu",
				Name:  "${localEnv:MISSING}",
			},
			wantErr: "name",
		},
		{
			name: "error mentions image field",
			cfg: Config{
				Image: "${localEnv:MISSING}",
			},
			wantErr: "image",
		},
		{
			name: "error mentions containerEnv",
			cfg: Config{
				Image: "ubuntu",
				ContainerEnv: map[string]string{
					"KEY": "${localEnv:MISSING}",
				},
			},
			wantErr: "containerEnv",
		},
		{
			name: "error mentions mounts",
			cfg: Config{
				Image:  "ubuntu",
				Mounts: []string{"${localEnv:MISSING}"},
			},
			wantErr: "mounts",
		},
		{
			name: "error mentions build.dockerfile",
			cfg: Config{
				Build: &Build{
					Dockerfile: "${localEnv:MISSING}",
				},
			},
			wantErr: "build.dockerfile",
		},
		{
			name: "error mentions build.context",
			cfg: Config{
				Build: &Build{
					Dockerfile: "Dockerfile",
					Context:    "${localEnv:MISSING}",
				},
			},
			wantErr: "build.context",
		},
		{
			name: "error mentions build.args",
			cfg: Config{
				Build: &Build{
					Dockerfile: "Dockerfile",
					Args: map[string]string{
						"KEY": "${localEnv:MISSING}",
					},
				},
			},
			wantErr: "build.args",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Substitute(tt.cfg, workspace, lookup)
			if err == nil {
				t.Fatal("Substitute() = nil error, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Substitute() error = %q, want error containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestSubstitute_ContainerEnvKeysNotSubstituted verifies that map keys
// are not subject to variable substitution.
func TestSubstitute_ContainerEnvKeysNotSubstituted(t *testing.T) {
	workspace := "/home/user/project"
	lookup := stubLookupEnv(nil)

	cfg := Config{
		Image: "ubuntu",
		ContainerEnv: map[string]string{
			"${localWorkspaceFolder}": "value",
		},
	}

	got, err := Substitute(cfg, workspace, lookup)
	if err != nil {
		t.Fatalf("Substitute() unexpected error: %v", err)
	}

	// The key should remain as-is (not substituted).
	if _, ok := got.ContainerEnv["${localWorkspaceFolder}"]; !ok {
		t.Errorf("Substitute() substituted map key; want key %q preserved", "${localWorkspaceFolder}")
	}
}

// TestSubstitute_BuildArgsKeysNotSubstituted verifies that build arg keys
// are not subject to variable substitution.
func TestSubstitute_BuildArgsKeysNotSubstituted(t *testing.T) {
	workspace := "/home/user/project"
	lookup := stubLookupEnv(nil)

	cfg := Config{
		Build: &Build{
			Dockerfile: "Dockerfile",
			Args: map[string]string{
				"${localWorkspaceFolder}": "value",
			},
		},
	}

	got, err := Substitute(cfg, workspace, lookup)
	if err != nil {
		t.Fatalf("Substitute() unexpected error: %v", err)
	}

	if _, ok := got.Build.Args["${localWorkspaceFolder}"]; !ok {
		t.Errorf("Substitute() substituted build arg key; want key %q preserved", "${localWorkspaceFolder}")
	}
}

// TestSubstitute_NoRecursiveExpansion verifies that resolved values containing
// ${...} patterns are not re-expanded.
func TestSubstitute_NoRecursiveExpansion(t *testing.T) {
	workspace := "/home/user/project"
	lookup := stubLookupEnv(map[string]string{
		"NESTED": "${localWorkspaceFolder}",
	})

	cfg := Config{
		Image: "ubuntu",
		Name:  "${localEnv:NESTED}",
	}

	got, err := Substitute(cfg, workspace, lookup)
	if err != nil {
		t.Fatalf("Substitute() unexpected error: %v", err)
	}

	// The resolved value should be the literal string "${localWorkspaceFolder}",
	// not the workspace path.
	want := "${localWorkspaceFolder}"
	if got.Name != want {
		t.Errorf("Substitute() Name = %q, want %q (no recursive expansion)", got.Name, want)
	}
}

// FuzzSubstituteString exercises the variable substitution parser with
// arbitrary input to catch panics and index-out-of-bounds errors.
func FuzzSubstituteString(f *testing.F) {
	f.Add("plain text")
	f.Add("${localWorkspaceFolder}")
	f.Add("${localEnv:HOME}")
	f.Add("${localEnv:MISSING:default}")
	f.Add("${devcontainerId}")
	f.Add("${unclosed")
	f.Add("${}")
	f.Add("${unknown:pattern}")
	f.Add("a]${b}c${d}e")
	f.Add("")

	ctx := &substContext{
		workspaceFolder:         "/home/user/project",
		workspaceFolderBasename: "project",
		devcontainerID:          "abc123",
		lookupEnv:               func(_ string) (string, bool) { return "", false },
	}

	f.Fuzz(func(_ *testing.T, s string) {
		// substituteString must not panic on any input.
		substituteString(s, ctx) //nolint:errcheck // fuzz target tests for panics
	})
}
