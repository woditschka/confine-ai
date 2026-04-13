package config

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/woditschka/confine-ai/internal/sanitize"
)

// credentialSuffixes lists the key name suffixes that indicate credential
// values in containerEnv. Matched case-insensitively.
var credentialSuffixes = []string{
	"_API_KEY",
	"_TOKEN",
	"_SECRET",
	"_PASSWORD",
	"_CREDENTIAL",
}

// supportedFields lists the top-level JSON field names that the tool
// recognizes. These correspond to the json struct tags on configJSON.
var supportedFields = map[string]bool{
	"name":            true,
	"image":           true,
	"build":           true,
	"workspaceFolder": true,
	"mounts":          true,
	"containerEnv":    true,
	"remoteUser":      true,
	"containerUser":   true,
	"customizations":  true,
}

// topLevelKeys unmarshals the raw JSON into a map of field names to raw
// values. Returns nil if the JSON cannot be parsed.
func topLevelKeys(raw RawConfig) map[string]json.RawMessage {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw.JSON, &keys); err != nil {
		return nil // Defensive: Parse already validates JSON.
	}
	return keys
}

// SupportedFields returns the names of top-level JSON fields in raw that are
// in the supported set. Returns nil if no supported fields are present or if
// the JSON cannot be parsed.
// The returned slice is sorted alphabetically.
func SupportedFields(raw RawConfig) []string {
	keys := topLevelKeys(raw)
	var supported []string
	for field := range supportedFields {
		if _, ok := keys[field]; ok {
			supported = append(supported, field)
		}
	}
	if len(supported) == 0 {
		return nil
	}
	slices.Sort(supported)
	return supported
}

// UnsupportedFields returns the names of top-level JSON fields in raw that are
// not in the supported set. Returns nil if all fields are supported or if the
// JSON cannot be parsed.
// The returned slice is sorted alphabetically. Field names are sanitized to
// remove control characters that could enable log or terminal injection.
func UnsupportedFields(raw RawConfig) []string {
	keys := topLevelKeys(raw)
	var unsupported []string
	for key := range keys {
		if !supportedFields[key] {
			unsupported = append(unsupported, sanitize.ControlChars(key))
		}
	}
	if len(unsupported) == 0 {
		return nil
	}
	slices.Sort(unsupported)
	return unsupported
}

// Customizations holds parsed customizations.confine-ai settings from
// devcontainer.json. Nil when the customizations field is absent or when
// customizations.confine-ai is not present.
type Customizations struct {
	// Memory is the memory limit in Docker format (e.g., "8g", "512m").
	// Empty when not set.
	Memory string `json:"memory"`

	// CPUs is the CPU limit as a decimal number (e.g., "4", "0.5").
	// Empty when not set.
	CPUs string `json:"cpus"`
}

// Config holds the typed representation of a devcontainer.json file's
// supported fields. Zero values represent absent fields.
type Config struct {
	// Name is the display name for the development container.
	Name string `json:"name"`

	// Image is the base container image reference.
	Image string `json:"image"`

	// Build holds the build configuration when building a custom image.
	// Nil when the build section is absent from the configuration.
	Build *Build `json:"build"`

	// WorkspaceFolder is the working directory inside the container.
	WorkspaceFolder string `json:"workspaceFolder"`

	// Mounts holds volume and bind mount declarations as Docker CLI
	// format strings. Both string and object mount formats from the
	// devcontainer spec are normalized to strings during loading.
	Mounts []string `json:"-"`

	// ContainerEnv holds environment variables set at container creation
	// time. All processes in the container see these values.
	ContainerEnv map[string]string `json:"containerEnv"`

	// RemoteUser is the user identity for commands executed via exec.
	RemoteUser string `json:"remoteUser"`

	// ContainerUser is the user identity for the container's main process.
	ContainerUser string `json:"containerUser"`

	// Customizations holds confine-ai-specific customizations. Nil when the
	// customizations field is absent or when customizations.confine-ai is not
	// present in the configuration.
	Customizations *Customizations `json:"-"`
}

// Build holds the build configuration for building a custom container image.
type Build struct {
	// Dockerfile is the path to a Dockerfile for building the image.
	Dockerfile string `json:"dockerfile"`

	// Context is the build context directory.
	Context string `json:"context"`

	// Args holds build-time arguments passed to the image build.
	Args map[string]string `json:"args"`
}

// configJSON is an intermediate type for unmarshaling. It uses
// json.RawMessage for mounts to handle both string and object formats.
type configJSON struct {
	Name            string            `json:"name"`
	Image           string            `json:"image"`
	Build           *Build            `json:"build"`
	WorkspaceFolder string            `json:"workspaceFolder"`
	Mounts          []json.RawMessage `json:"mounts"`
	ContainerEnv    map[string]string `json:"containerEnv"`
	RemoteUser      string            `json:"remoteUser"`
	ContainerUser   string            `json:"containerUser"`
	Customizations  json.RawMessage   `json:"customizations"`
}

// mountObject represents a mount declaration in object format.
type mountObject struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

// Load unmarshals a RawConfig into a typed Config and validates the result.
// Warnings (e.g. credential pattern matches) are returned as strings for the
// caller to display. This replaces the previous slog-based approach so that
// warnings flow through the same io.Writer as all other user-facing output.
func Load(raw RawConfig) (Config, []string, error) {
	var rc configJSON
	if err := json.Unmarshal(raw.JSON, &rc); err != nil {
		return Config{}, nil, fmt.Errorf("load config: %q: %w", raw.Path, err)
	}

	cfg := Config{
		Name:            rc.Name,
		Image:           rc.Image,
		Build:           rc.Build,
		WorkspaceFolder: rc.WorkspaceFolder,
		ContainerEnv:    rc.ContainerEnv,
		RemoteUser:      rc.RemoteUser,
		ContainerUser:   rc.ContainerUser,
	}

	// Convert mounts from raw JSON to strings.
	mounts, err := parseMounts(rc.Mounts)
	if err != nil {
		return Config{}, nil, fmt.Errorf("load config: %q: %w", raw.Path, err)
	}
	cfg.Mounts = mounts

	// Parse customizations.confine-ai if present.
	cfg.Customizations, err = parseCustomizations(rc.Customizations)
	if err != nil {
		return Config{}, nil, fmt.Errorf("load config: %q: %w", raw.Path, err)
	}

	// Validate image vs build.
	if err := validateImageBuild(cfg, raw.Path); err != nil {
		return Config{}, nil, err
	}

	// Collect credential pattern warnings.
	warnings := checkCredentialPatterns(cfg.ContainerEnv, raw.Path)

	return cfg, warnings, nil
}

// validateImageBuild checks the mutual exclusivity of image and build fields.
func validateImageBuild(cfg Config, path string) error {
	hasImage := cfg.Image != ""
	hasBuild := cfg.Build != nil

	if hasImage && hasBuild {
		return fmt.Errorf("load config: %q: cannot specify both \"image\" and \"build.dockerfile\"", path)
	}
	if !hasImage && !hasBuild {
		return fmt.Errorf("load config: %q: must specify either \"image\" or \"build.dockerfile\"", path)
	}
	if hasBuild && cfg.Build.Dockerfile == "" {
		return fmt.Errorf("load config: %q: \"build\" requires \"dockerfile\"", path)
	}

	return nil
}

// parseMounts converts raw JSON mount entries (strings or objects) into
// Docker CLI mount format strings.
func parseMounts(raw []json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	mounts := make([]string, 0, len(raw))
	for i, entry := range raw {
		trimmed := strings.TrimSpace(string(entry))
		if len(trimmed) == 0 || trimmed == "null" {
			continue
		}

		switch trimmed[0] {
		case '"':
			// String mount: unmarshal to get the actual string value.
			var s string
			if err := json.Unmarshal(entry, &s); err != nil {
				return nil, fmt.Errorf("mount[%d]: %w", i, err)
			}
			if err := validateStringMount(s); err != nil {
				return nil, fmt.Errorf("mount[%d]: %w", i, err)
			}
			mounts = append(mounts, s)
		case '{':
			// Object mount: unmarshal and convert to CLI format.
			var obj mountObject
			if err := json.Unmarshal(entry, &obj); err != nil {
				return nil, fmt.Errorf("mount[%d]: %w", i, err)
			}
			s, err := mountObjectToString(obj)
			if err != nil {
				return nil, fmt.Errorf("mount[%d]: %w", i, err)
			}
			if s == "" {
				continue // Empty mount object, skip.
			}
			mounts = append(mounts, s)
		default:
			return nil, fmt.Errorf("mount[%d]: unexpected JSON type", i)
		}
	}

	if len(mounts) == 0 {
		return nil, nil
	}
	return mounts, nil
}

// validateStringMount checks that a string-format mount contains only valid
// key=value pairs separated by commas. Each value is checked for characters
// that could inject extra mount options.
func validateStringMount(s string) error {
	for part := range strings.SplitSeq(s, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue // key-only options (e.g., "readonly") are valid.
		}
		if strings.ContainsAny(kv[1], ",=") {
			return fmt.Errorf("mount value for key %q contains invalid character in %q", kv[0], kv[1])
		}
	}
	return nil
}

// mountObjectToString converts a mount object to Docker CLI mount format.
// Format: type=<type>,source=<source>,target=<target>.
// Fields with empty values are omitted. Returns an error if any value
// contains characters that would corrupt the mount argument format.
func mountObjectToString(obj mountObject) (string, error) {
	for _, v := range []struct{ name, val string }{
		{"type", obj.Type}, {"source", obj.Source}, {"target", obj.Target},
	} {
		if strings.ContainsAny(v.val, ",=") {
			return "", fmt.Errorf("mount field %q contains invalid character in value %q", v.name, v.val)
		}
	}

	var parts []string
	if obj.Type != "" {
		parts = append(parts, "type="+obj.Type)
	}
	if obj.Source != "" {
		parts = append(parts, "source="+obj.Source)
	}
	if obj.Target != "" {
		parts = append(parts, "target="+obj.Target)
	}
	return strings.Join(parts, ","), nil
}

// parseCustomizations extracts the confine-ai namespace from the raw
// customizations JSON. Returns nil if the field is absent or if the
// confine-ai namespace is not present.
func parseCustomizations(raw json.RawMessage) (*Customizations, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var namespaces map[string]json.RawMessage
	if err := json.Unmarshal(raw, &namespaces); err != nil {
		return nil, fmt.Errorf("customizations: %w", err)
	}

	confineRaw, ok := namespaces["confine-ai"]
	if !ok {
		return nil, nil
	}

	var c Customizations
	if err := json.Unmarshal(confineRaw, &c); err != nil {
		return nil, fmt.Errorf("customizations.confine-ai: %w", err)
	}

	return &c, nil
}

// UnsupportedCustomizations inspects the customizations field in raw JSON and
// returns unsupported namespace names prefixed with "customizations." (e.g.,
// "customizations.vscode"). The confine-ai namespace is recognized and excluded
// from the result. Returns nil if no unsupported namespaces exist.
func UnsupportedCustomizations(raw RawConfig) []string {
	keys := topLevelKeys(raw)
	custRaw, ok := keys["customizations"]
	if !ok {
		return nil
	}

	var namespaces map[string]json.RawMessage
	if err := json.Unmarshal(custRaw, &namespaces); err != nil {
		return nil
	}

	var unsupported []string
	for ns := range namespaces {
		if ns != "confine-ai" {
			unsupported = append(unsupported, "customizations."+ns)
		}
	}

	if len(unsupported) == 0 {
		return nil
	}

	slices.Sort(unsupported)
	return unsupported
}

// checkCredentialPatterns returns warnings for any containerEnv keys that
// match credential name patterns. The check is case-insensitive on the suffix.
func checkCredentialPatterns(env map[string]string, path string) []string {
	var warnings []string
	for key := range env {
		upper := strings.ToUpper(key)
		for _, suffix := range credentialSuffixes {
			if strings.HasSuffix(upper, suffix) {
				warnings = append(warnings,
					fmt.Sprintf("containerEnv key %q matches credential pattern in %q; value is visible via docker inspect; use OAuth authentication instead", key, path))
				break
			}
		}
	}
	return warnings
}
