package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    Config
		wantErr string // substring in error message, empty means no error
	}{
		// AC #1: image field.
		{
			name: "image only",
			json: `{"image": "ubuntu:22.04"}`,
			want: Config{
				Image: "ubuntu:22.04",
			},
		},
		// AC #9: name field used in labels.
		{
			name: "name field",
			json: `{"image": "ubuntu", "name": "my-project"}`,
			want: Config{
				Name:  "my-project",
				Image: "ubuntu",
			},
		},
		// AC #7: remoteUser field.
		{
			name: "remoteUser field",
			json: `{"image": "ubuntu", "remoteUser": "node"}`,
			want: Config{
				Image:      "ubuntu",
				RemoteUser: "node",
			},
		},
		// AC #8: containerUser field.
		{
			name: "containerUser field",
			json: `{"image": "ubuntu", "containerUser": "vscode"}`,
			want: Config{
				Image:         "ubuntu",
				ContainerUser: "vscode",
			},
		},
		// workspaceFolder field.
		{
			name: "workspaceFolder field",
			json: `{"image": "ubuntu", "workspaceFolder": "/workspaces/project"}`,
			want: Config{
				Image:           "ubuntu",
				WorkspaceFolder: "/workspaces/project",
			},
		},
		// AC #2: build.dockerfile field.
		{
			name: "build with dockerfile only",
			json: `{"build": {"dockerfile": "Dockerfile"}}`,
			want: Config{
				Build: &Build{
					Dockerfile: "Dockerfile",
				},
			},
		},
		// build with all sub-fields.
		{
			name: "build with all fields",
			json: `{"build": {"dockerfile": "Dockerfile.dev", "context": "..", "args": {"GO_VERSION": "1.26"}}}`,
			want: Config{
				Build: &Build{
					Dockerfile: "Dockerfile.dev",
					Context:    "..",
					Args:       map[string]string{"GO_VERSION": "1.26"},
				},
			},
		},
		// AC #3: both image and build.dockerfile -> error.
		{
			name:    "both image and build",
			json:    `{"image": "ubuntu", "build": {"dockerfile": "Dockerfile"}}`,
			wantErr: `cannot specify both "image" and "build.dockerfile"`,
		},
		// AC #4: neither image nor build -> error.
		{
			name:    "neither image nor build",
			json:    `{"name": "test"}`,
			wantErr: `must specify either "image" or "build.dockerfile"`,
		},
		// build present but dockerfile empty -> error.
		{
			name:    "build without dockerfile",
			json:    `{"build": {"context": "."}}`,
			wantErr: `"build" requires "dockerfile"`,
		},
		// AC #6: containerEnv field.
		{
			name: "containerEnv field",
			json: `{"image": "ubuntu", "containerEnv": {"TZ": "UTC", "LANG": "en_US.UTF-8"}}`,
			want: Config{
				Image:        "ubuntu",
				ContainerEnv: map[string]string{"TZ": "UTC", "LANG": "en_US.UTF-8"},
			},
		},
		// AC #5: mounts as strings.
		{
			name: "mounts as strings",
			json: `{"image": "ubuntu", "mounts": ["source=/src,target=/dst,type=bind"]}`,
			want: Config{
				Image:  "ubuntu",
				Mounts: []string{"source=/src,target=/dst,type=bind"},
			},
		},
		// Mounts as objects.
		{
			name: "mounts as objects",
			json: `{"image": "ubuntu", "mounts": [{"source": "/host/path", "target": "/container/path", "type": "bind"}]}`,
			want: Config{
				Image:  "ubuntu",
				Mounts: []string{"type=bind,source=/host/path,target=/container/path"},
			},
		},
		// Mixed mount formats.
		{
			name: "mounts mixed string and object",
			json: `{"image": "ubuntu", "mounts": ["source=a,target=b,type=bind", {"source": "c", "target": "d", "type": "volume"}]}`,
			want: Config{
				Image: "ubuntu",
				Mounts: []string{
					"source=a,target=b,type=bind",
					"type=volume,source=c,target=d",
				},
			},
		},
		// Mount object without type defaults to empty type field (omitted).
		{
			name: "mount object without type",
			json: `{"image": "ubuntu", "mounts": [{"source": "s", "target": "t"}]}`,
			want: Config{
				Image:  "ubuntu",
				Mounts: []string{"source=s,target=t"},
			},
		},
		// All fields populated.
		{
			name: "all fields populated",
			json: `{
				"name": "full-config",
				"image": "ubuntu:22.04",
				"workspaceFolder": "/workspaces/project",
				"mounts": ["source=/a,target=/b,type=bind"],
				"containerEnv": {"TZ": "UTC"},
				"remoteUser": "node",
				"containerUser": "vscode"
			}`,
			want: Config{
				Name:            "full-config",
				Image:           "ubuntu:22.04",
				WorkspaceFolder: "/workspaces/project",
				Mounts:          []string{"source=/a,target=/b,type=bind"},
				ContainerEnv:    map[string]string{"TZ": "UTC"},
				RemoteUser:      "node",
				ContainerUser:   "vscode",
			},
		},
		// Invalid JSON.
		{
			name:    "invalid JSON",
			json:    `{invalid}`,
			wantErr: "load config:",
		},
		// Empty object: neither image nor build.
		{
			name:    "empty object",
			json:    `{}`,
			wantErr: `must specify either "image" or "build.dockerfile"`,
		},
		// Unknown fields are silently ignored.
		{
			name: "unknown fields ignored",
			json: `{"image": "ubuntu", "features": {}, "postCreateCommand": "echo hello"}`,
			want: Config{
				Image: "ubuntu",
			},
		},
		// Mount edge cases.
		{
			name: "mount null entry skipped",
			json: `{"image": "ubuntu", "mounts": [null]}`,
			want: Config{
				Image: "ubuntu",
			},
		},
		{
			name:    "mount unexpected JSON type",
			json:    `{"image": "ubuntu", "mounts": [42]}`,
			wantErr: "mount[0]: unexpected JSON type",
		},
		{
			name: "mount all null entries skipped",
			json: `{"image": "ubuntu", "mounts": [null, null]}`,
			want: Config{
				Image: "ubuntu",
			},
		},
		{
			name:    "mount object with comma in source",
			json:    `{"image": "ubuntu", "mounts": [{"source": "a,b", "target": "/dst", "type": "bind"}]}`,
			wantErr: "mount field \"source\" contains invalid character",
		},
		{
			name: "empty mount object skipped",
			json: `{"image": "ubuntu", "mounts": [{}]}`,
			want: Config{
				Image: "ubuntu",
			},
		},
		{
			name:    "mount object with equals in target",
			json:    `{"image": "ubuntu", "mounts": [{"source": "/src", "target": "/dst=bad", "type": "bind"}]}`,
			wantErr: "mount field \"target\" contains invalid character",
		},
		{
			name:    "mount object with equals in type",
			json:    `{"image": "ubuntu", "mounts": [{"source": "/src", "target": "/dst", "type": "bi=nd"}]}`,
			wantErr: "mount field \"type\" contains invalid character",
		},
		{
			name: "string mount with readonly key-only option",
			json: `{"image": "ubuntu", "mounts": ["type=bind,source=/src,target=/dst,readonly"]}`,
			want: Config{
				Image:  "ubuntu",
				Mounts: []string{"type=bind,source=/src,target=/dst,readonly"},
			},
		},
		{
			name:    "string mount with injection in value",
			json:    `{"image": "ubuntu", "mounts": ["type=bind,source=/src,target=/dst,extra=injected=bad"]}`,
			wantErr: "mount value for key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := RawConfig{
				Path: "/test/devcontainer.json",
				JSON: json.RawMessage(tt.json),
			}

			got, _, err := Load(raw)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Load() = %v, want error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Load() error = %q, want error containing %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Load() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-CF-003 AC #11: customizations.confine-ai.memory parsed.
// REQ-CF-003 AC #12: customizations.vscode reported as unsupported.
func TestLoad_Customizations(t *testing.T) {
	tests := []struct {
		name string
		json string
		want *Customizations
	}{
		{
			name: "customizations.confine-ai with memory and cpus",
			json: `{"image": "ubuntu", "customizations": {"confine-ai": {"memory": "8g", "cpus": "4"}}}`,
			want: &Customizations{Memory: "8g", CPUs: "4"},
		},
		{
			name: "customizations.confine-ai with memory only",
			json: `{"image": "ubuntu", "customizations": {"confine-ai": {"memory": "512m"}}}`,
			want: &Customizations{Memory: "512m"},
		},
		{
			name: "customizations.confine-ai with cpus only",
			json: `{"image": "ubuntu", "customizations": {"confine-ai": {"cpus": "2.5"}}}`,
			want: &Customizations{CPUs: "2.5"},
		},
		{
			name: "no customizations field",
			json: `{"image": "ubuntu"}`,
			want: nil,
		},
		{
			name: "customizations without confine-ai namespace",
			json: `{"image": "ubuntu", "customizations": {"vscode": {}}}`,
			want: nil,
		},
		{
			name: "customizations with confine-ai and vscode",
			json: `{"image": "ubuntu", "customizations": {"confine-ai": {"memory": "4g"}, "vscode": {}}}`,
			want: &Customizations{Memory: "4g"},
		},
		{
			name: "empty customizations.confine-ai",
			json: `{"image": "ubuntu", "customizations": {"confine-ai": {}}}`,
			want: &Customizations{},
		},
		{
			name: "empty customizations object",
			json: `{"image": "ubuntu", "customizations": {}}`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := RawConfig{
				Path: "/test/devcontainer.json",
				JSON: json.RawMessage(tt.json),
			}

			got, _, err := Load(raw)
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}

			if diff := cmp.Diff(tt.want, got.Customizations); diff != "" {
				t.Errorf("Load().Customizations mismatch (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("customizations.confine-ai is a string not an object", func(t *testing.T) {
		raw := RawConfig{
			Path: "/test/devcontainer.json",
			JSON: json.RawMessage(`{"image": "ubuntu", "customizations": {"confine-ai": "bad"}}`),
		}

		_, _, err := Load(raw)
		if err == nil {
			t.Fatal("Load() = nil error, want error for malformed customizations.confine-ai")
		}
		if !strings.Contains(err.Error(), "customizations.confine-ai") {
			t.Errorf("Load() error = %q, want containing %q", err.Error(), "customizations.confine-ai")
		}
	})
}

// REQ-CF-004 AC #5-7: UnsupportedCustomizations namespace inspection.
func TestUnsupportedCustomizations(t *testing.T) {
	tests := []struct {
		name string
		json string
		want []string
	}{
		{
			name: "confine-ai only no unsupported",
			json: `{"customizations": {"confine-ai": {"memory": "8g"}}}`,
			want: nil,
		},
		{
			name: "vscode only",
			json: `{"customizations": {"vscode": {}}}`,
			want: []string{"customizations.vscode"},
		},
		{
			name: "confine-ai and vscode",
			json: `{"customizations": {"confine-ai": {"memory": "8g"}, "vscode": {}}}`,
			want: []string{"customizations.vscode"},
		},
		{
			name: "multiple unsupported namespaces sorted",
			json: `{"customizations": {"vscode": {}, "jetbrains": {}, "confine-ai": {}}}`,
			want: []string{"customizations.jetbrains", "customizations.vscode"},
		},
		{
			name: "no customizations field",
			json: `{"image": "ubuntu"}`,
			want: nil,
		},
		{
			name: "empty customizations",
			json: `{"customizations": {}}`,
			want: nil,
		},
		{
			name: "invalid JSON",
			json: `{bad`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := RawConfig{
				Path: "/test/devcontainer.json",
				JSON: json.RawMessage(tt.json),
			}

			got := UnsupportedCustomizations(raw)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("UnsupportedCustomizations() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLoad_ErrorIncludesPath(t *testing.T) {
	tests := []struct {
		name string
		json string
		path string
	}{
		{
			name: "invalid JSON includes path",
			json: `{bad`,
			path: "/home/user/.devcontainer/devcontainer.json",
		},
		{
			name: "validation error includes path",
			json: `{"name": "test"}`,
			path: "/projects/myapp/.devcontainer/devcontainer.json",
		},
		{
			name: "both image and build includes path",
			json: `{"image": "ubuntu", "build": {"dockerfile": "Dockerfile"}}`,
			path: "/workspace/.devcontainer.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := RawConfig{
				Path: tt.path,
				JSON: json.RawMessage(tt.json),
			}

			_, _, err := Load(raw)
			if err == nil {
				t.Fatal("Load() = nil error, want error")
			}
			if !strings.Contains(err.Error(), tt.path) {
				t.Errorf("Load() error = %q, want error containing path %q", err.Error(), tt.path)
			}
		})
	}
}

// AC #10: credential pattern warning.
func TestLoad_CredentialWarning(t *testing.T) {
	tests := []struct {
		name         string
		envKeys      []string
		wantWarnings int // number of expected credential warnings
	}{
		{
			name:         "API_KEY suffix",
			envKeys:      []string{"ANTHROPIC_API_KEY"},
			wantWarnings: 1,
		},
		{
			name:         "TOKEN suffix",
			envKeys:      []string{"GITHUB_TOKEN"},
			wantWarnings: 1,
		},
		{
			name:         "SECRET suffix",
			envKeys:      []string{"MY_SECRET"},
			wantWarnings: 1,
		},
		{
			name:         "PASSWORD suffix",
			envKeys:      []string{"DB_PASSWORD"},
			wantWarnings: 1,
		},
		{
			name:         "CREDENTIAL suffix",
			envKeys:      []string{"AZURE_CREDENTIAL"},
			wantWarnings: 1,
		},
		{
			name:         "case insensitive match",
			envKeys:      []string{"Anthropic_Api_Key"},
			wantWarnings: 1,
		},
		{
			name:         "multiple credential keys",
			envKeys:      []string{"MY_API_KEY", "OTHER_TOKEN", "TZ"},
			wantWarnings: 2,
		},
		{
			name:         "no credential keys",
			envKeys:      []string{"TZ", "LANG", "NODE_OPTIONS"},
			wantWarnings: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := make(map[string]string)
			for _, k := range tt.envKeys {
				env[k] = "some-value"
			}

			jsonData, err := json.Marshal(map[string]any{
				"image":        "ubuntu",
				"containerEnv": env,
			})
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}

			raw := RawConfig{
				Path: "/test/devcontainer.json",
				JSON: json.RawMessage(jsonData),
			}

			got, warnings, err := Load(raw)
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}

			// Verify the Config is still valid.
			if got.Image != "ubuntu" {
				t.Errorf("Load().Image = %q, want %q", got.Image, "ubuntu")
			}

			// REQ-CO-007: credential values must never appear in warnings.
			for _, w := range warnings {
				if strings.Contains(w, "some-value") {
					t.Errorf("Load() warning contains credential value %q\ngot: %s", "some-value", w)
				}

				// Credential key names should appear in warnings so the user
				// can identify which key triggered it. Values must not.
			}

			// Count warnings by counting occurrences of "credential pattern".
			gotWarnings := 0
			for _, w := range warnings {
				if strings.Contains(w, "credential pattern") {
					gotWarnings++
				}
			}
			if gotWarnings != tt.wantWarnings {
				t.Errorf("Load() returned %d warnings, want %d\ngot: %v", gotWarnings, tt.wantWarnings, warnings)
			}

			// Verify no warnings when none expected.
			if tt.wantWarnings == 0 && len(warnings) != 0 {
				t.Errorf("Load() unexpected warnings: %v", warnings)
			}
		})
	}
}

// REQ-CF-004: Unsupported Field Reporting.
func TestUnsupportedFields(t *testing.T) {
	tests := []struct {
		name string
		json string
		want []string
	}{
		// AC #1: features is unsupported.
		{
			name: "single unsupported field features",
			json: `{"image": "ubuntu", "features": {"ghcr.io/devcontainers/features/go:1": {}}}`,
			want: []string{"features"},
		},
		// AC #2: postCreateCommand is unsupported.
		{
			name: "single unsupported field postCreateCommand",
			json: `{"image": "ubuntu", "postCreateCommand": "echo hello"}`,
			want: []string{"postCreateCommand"},
		},
		// Multiple unsupported fields returned sorted.
		{
			name: "multiple unsupported fields sorted",
			json: `{"image": "ubuntu", "postCreateCommand": "echo", "features": {}, "forwardPorts": [8080]}`,
			want: []string{"features", "forwardPorts", "postCreateCommand"},
		},
		// AC #3: only supported fields, no warnings.
		{
			name: "only supported fields",
			json: `{"image": "ubuntu", "name": "test"}`,
			want: nil,
		},
		// All supported fields present, no unsupported.
		{
			name: "all supported fields present",
			json: `{
				"name": "full",
				"image": "ubuntu",
				"build": {"dockerfile": "Dockerfile"},
				"workspaceFolder": "/workspaces/project",
				"mounts": [],
				"containerEnv": {},
				"remoteUser": "node",
				"containerUser": "vscode",
				"customizations": {"confine-ai": {"memory": "8g"}}
			}`,
			want: nil,
		},
		// Empty JSON object.
		{
			name: "empty object",
			json: `{}`,
			want: nil,
		},
		// Invalid JSON returns nil defensively.
		{
			name: "invalid JSON returns nil",
			json: `{invalid`,
			want: nil,
		},
		// AC #4: unsupported fields present alongside supported.
		// REQ-CF-004 AC #6: customizations.vscode is unsupported IDE config.
		{
			name: "customizations.vscode unsupported",
			json: `{"image": "ubuntu", "name": "test", "customizations": {"vscode": {}}}`,
			want: nil,
		},
		// Control characters in field names are sanitized.
		{
			name: "field name with control characters sanitized",
			json: "{\"image\": \"ubuntu\", \"evil\\nfield\": true}",
			want: []string{"evil" + string('\ufffd') + "field"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := RawConfig{
				Path: "/test/devcontainer.json",
				JSON: json.RawMessage(tt.json),
			}

			got := UnsupportedFields(raw)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("UnsupportedFields() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-CO-005: SupportedFields returns supported field names present in raw config.
func TestSupportedFields(t *testing.T) {
	tests := []struct {
		name string
		json string
		want []string
	}{
		{
			name: "single supported field",
			json: `{"image": "ubuntu"}`,
			want: []string{"image"},
		},
		{
			name: "multiple supported fields sorted",
			json: `{"image": "ubuntu", "name": "n", "build": {}}`,
			want: []string{"build", "image", "name"},
		},
		{
			name: "no supported fields",
			json: `{"features": {}}`,
			want: nil,
		},
		{
			name: "mixed supported and unsupported",
			json: `{"image": "ubuntu", "features": {}}`,
			want: []string{"image"},
		},
		{
			name: "all supported fields present",
			json: `{
				"name": "full",
				"image": "ubuntu",
				"build": {"dockerfile": "Dockerfile"},
				"workspaceFolder": "/workspaces/project",
				"mounts": [],
				"containerEnv": {},
				"remoteUser": "node",
				"containerUser": "vscode",
				"customizations": {"confine-ai": {}}
			}`,
			want: []string{"build", "containerEnv", "containerUser", "customizations", "image", "mounts", "name", "remoteUser", "workspaceFolder"},
		},
		{
			name: "empty object",
			json: `{}`,
			want: nil,
		},
		{
			name: "invalid JSON returns nil",
			json: `{bad`,
			want: nil,
		},
		{
			name: "near-match field names not treated as supported",
			json: `{"IMAGE": "ubuntu", " image": "ubuntu", "images": "ubuntu"}`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := RawConfig{
				Path: "/test/devcontainer.json",
				JSON: json.RawMessage(tt.json),
			}

			got := SupportedFields(raw)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("SupportedFields() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestSupportedFieldsMatchRawConfigTags verifies that the supportedFields map
// stays in sync with the json struct tags on configJSON. If a field is added to
// configJSON but not to supportedFields (or vice versa), this test fails.
func TestSupportedFieldsMatchRawConfigTags(t *testing.T) {
	rt := reflect.TypeFor[configJSON]()
	tagsFromStruct := make(map[string]bool)

	for field := range rt.Fields() {
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		// Strip options like ",omitempty".
		name, _, _ := strings.Cut(tag, ",")
		if name != "" {
			tagsFromStruct[name] = true
		}
	}

	// Every json tag on configJSON must be in supportedFields.
	for tag := range tagsFromStruct {
		if !supportedFields[tag] {
			t.Errorf("configJSON has json tag %q not present in supportedFields", tag)
		}
	}

	// Every entry in supportedFields must have a corresponding json tag.
	for field := range supportedFields {
		if !tagsFromStruct[field] {
			t.Errorf("supportedFields has %q not present as json tag on configJSON", field)
		}
	}
}
