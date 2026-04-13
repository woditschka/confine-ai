package assistant

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// knownAssistantMountTargets maps known assistant names to their container-side mount
// target paths. These correspond to where each assistant stores its configuration
// and credentials inside the container.
var knownAssistantMountTargets = map[string]string{
	"claude":   "/home/dev/.claude",
	"copilot":  "/home/dev/.copilot",
	"opencode": "/home/dev/.config/opencode",
}

// extraFileMount describes an additional file seeded into the data directory.
// When Target is non-empty, a separate bind mount is generated so the file
// appears at a path outside the data-directory mount. When Target is empty,
// the file is seed-only: it is visible through the existing directory mount.
type extraFileMount struct {
	Source  string // Filename within the data directory.
	Target  string // Container path for a separate mount; empty = seed-only.
	Content string // Initial content when seeding the file.
}

// knownAssistantExtraFiles maps assistant names to additional files that must be
// seeded into the data directory and optionally bind-mounted into the container.
// Source paths are relative to ~/.confine-ai/data/<assistant>/. When Target is
// non-empty, an extra bind mount is generated; when empty, the file is seeded
// into the data directory but no separate mount is needed (the file is already
// visible through the directory mount).
var knownAssistantExtraFiles = map[string][]extraFileMount{
	"claude": {
		{Source: "claude.json", Target: "/home/dev/.claude.json", Content: "{}"},
	},
	"opencode": {
		{Source: "opencode.json", Target: "", Content: `{"$schema":"https://opencode.ai/config.json","disabled_providers":["opencode"]}`},
	},
}

// hostMount describes a bind mount whose source is a live host path outside
// ~/.confine-ai/data/. Unlike extraFileMount, confine-ai never seeds or writes the
// source; it is the user's existing host filesystem. Source is rendered into
// the devcontainer.json verbatim and is expected to contain a ${localEnv:HOME}
// expansion so the runtime resolves it at container start. Source values are
// trusted package-level constants and must not contain the "," or "="
// characters that delimit mount-string fields.
type hostMount struct {
	Source   string // e.g. "${localEnv:HOME}/.local/share/opencode"
	Target   string // e.g. "/home/dev/.local/share/opencode"
	ReadOnly bool   // adds ",readonly" to the mount string when true
}

// knownAssistantHostMounts maps assistant names to host-path bind mounts that
// originate outside ~/.confine-ai/data/. These mounts pass live host state
// through to the container and are never touched by EnsureExtraFiles.
//
// opencode: XDG data dir (~/.local/share/opencode) holds auth.json with OAuth
// refresh tokens. The mount is read-write so opencode can rotate tokens.
// RunAssistant pre-creates the host directory with mode 0o700 before container
// start so first-time users (who have not yet run `opencode auth login`) do
// not hit ValidateMounts or end up with a root-owned empty directory created
// by the runtime.
//
// Note: the path assumes the default XDG layout; XDG_DATA_HOME overrides are
// not honored. If a user sets XDG_DATA_HOME to a non-default location the
// mount still points at ~/.local/share/opencode and will be empty in the
// container.
var knownAssistantHostMounts = map[string][]hostMount{
	"opencode": {
		{
			Source: "${localEnv:HOME}/.local/share/opencode",
			Target: "/home/dev/.local/share/opencode",
		},
	},
}

// HostMountSources returns the resolved host source paths for an assistant's
// declared host mounts, with ${localEnv:HOME} expanded against homeDir. It
// returns nil for assistants that declare no host mounts. Callers use this to
// pre-create host directories before container launch so the runtime does not
// materialize them as root-owned empty dirs.
func HostMountSources(homeDir, name string) []string {
	mounts := knownAssistantHostMounts[name]
	if len(mounts) == 0 {
		return nil
	}
	sources := make([]string, 0, len(mounts))
	for _, hm := range mounts {
		sources = append(sources, expandHome(hm.Source, homeDir))
	}
	return sources
}

// expandHome replaces ${localEnv:HOME} in s with homeDir. It is intentionally
// minimal: the only placeholder used in knownAssistantHostMounts is
// ${localEnv:HOME}, so no general template engine is needed.
func expandHome(s, homeDir string) string {
	return strings.ReplaceAll(s, "${localEnv:HOME}", homeDir)
}

// SeedBaseDockerfile writes seed to ~/.confine-ai/base/Dockerfile if the file
// does not exist. It creates ~/.confine-ai/base/ with mode 0o755 if missing and
// writes the file with mode 0o644. The function never overwrites an existing
// file: when the file is already present it returns wrote=false and a nil
// error. Returns wrote=true when the file was newly written.
//
// The existence check uses os.Stat (not O_EXCL on open) so the "already
// present" path is distinguishable from an I/O error. TOCTOU between the stat
// and the write is acceptable here because the home directory is user-owned.
func SeedBaseDockerfile(homeDir string, seed []byte) (wrote bool, err error) {
	path := BaseDockerfilePath(homeDir)

	if _, statErr := os.Stat(path); statErr == nil {
		// File already exists; do not overwrite.
		return false, nil
	} else if !os.IsNotExist(statErr) {
		return false, fmt.Errorf("seed base dockerfile: stat: %w", statErr)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("seed base dockerfile: create directory: %w", err)
	}

	if err := os.WriteFile(path, seed, 0o644); err != nil {
		return false, fmt.Errorf("seed base dockerfile: write: %w", err)
	}

	return true, nil
}

// Init scaffolds assistant configuration from embedded templates. It creates the
// assistant config directory (see Dir) with a Dockerfile (for known assistants, via
// DockerfilePath) and a generated devcontainer.json (via ConfigPath), plus
// the persistent data directory (via DataPath).
//
// For known assistants (claude, copilot, opencode), the provided dockerfile bytes
// are written and the devcontainer.json uses a build directive. For unknown
// assistants, dockerfile should be nil; the devcontainer.json uses an image
// reference to localhost/confine-ai-base:latest.
//
// Init returns an error if the assistant directory already exists or if the name
// is invalid. It does not overwrite existing configurations.
func Init(homeDir, name string, dockerfile []byte) error {
	if err := ValidateName(name); err != nil {
		return err
	}

	assistantDir := Dir(homeDir, name)

	// Check for existing directory before creating anything.
	if _, err := os.Stat(assistantDir); err == nil {
		return fmt.Errorf("init: assistant %q already exists at %s", name, assistantDir)
	}

	// Create assistant config directory.
	if err := os.MkdirAll(assistantDir, 0o755); err != nil {
		return fmt.Errorf("init: create assistant directory: %w", err)
	}

	// Create persistent data directory.
	dataDir := DataPath(homeDir, name)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("init: create data directory: %w", err)
	}

	// Seed extra files that don't exist yet.
	if err := EnsureExtraFiles(homeDir, name); err != nil {
		return fmt.Errorf("init: %w", err)
	}

	// Determine mount target path.
	mountTarget, known := knownAssistantMountTargets[name]
	if !known {
		mountTarget = "/home/dev/.config/" + name
	}

	// Generate devcontainer.json.
	dcJSON, err := generateDevcontainerJSON(name, mountTarget, known)
	if err != nil {
		return fmt.Errorf("init: generate devcontainer.json: %w", err)
	}

	if err := os.WriteFile(ConfigPath(homeDir, name), dcJSON, 0o644); err != nil {
		return fmt.Errorf("init: write devcontainer.json: %w", err)
	}

	// Write Dockerfile for known assistants.
	if known && dockerfile != nil {
		if err := os.WriteFile(DockerfilePath(homeDir, name), dockerfile, 0o644); err != nil {
			return fmt.Errorf("init: write Dockerfile: %w", err)
		}
	}

	return nil
}

// devcontainerConfig represents the generated devcontainer.json structure.
type devcontainerConfig struct {
	Build          *buildConfig          `json:"build,omitempty"`
	Image          string                `json:"image,omitempty"`
	WorkspaceDir   string                `json:"workspaceFolder"`
	Mounts         []string              `json:"mounts"`
	RemoteUser     string                `json:"remoteUser"`
	Customizations *customizationsConfig `json:"customizations,omitempty"`
}

// customizationsConfig represents the customizations section.
type customizationsConfig struct {
	Confine *confineConfig `json:"confine-ai,omitempty"`
}

// confineConfig represents the confine namespace settings.
type confineConfig struct {
	Memory string `json:"memory"`
	CPUs   string `json:"cpus"`
}

// buildConfig represents the build section of devcontainer.json.
type buildConfig struct {
	Dockerfile string `json:"dockerfile"`
}

// generateDevcontainerJSON produces the devcontainer.json content for an assistant.
// Known assistants use a build directive referencing the local Dockerfile. Unknown
// assistants use an image reference to localhost/confine-ai-base:latest.
func generateDevcontainerJSON(name, mountTarget string, known bool) ([]byte, error) {
	mount := fmt.Sprintf("type=bind,source=${localEnv:HOME}/.confine-ai/data/%s,target=%s", name, mountTarget)

	mounts := []string{mount}

	// Add extra file mounts for known assistants. Entries with an empty
	// Target are seed-only (the file is visible through the directory mount).
	for _, ef := range knownAssistantExtraFiles[name] {
		if ef.Target == "" {
			continue
		}
		extraMount := fmt.Sprintf("type=bind,source=${localEnv:HOME}/.confine-ai/data/%s/%s,target=%s", name, ef.Source, ef.Target)
		mounts = append(mounts, extraMount)
	}

	// Add host-path pass-through mounts for known assistants.
	mounts = append(mounts, renderHostMounts(knownAssistantHostMounts[name])...)

	cfg := devcontainerConfig{
		WorkspaceDir: "/workspace",
		Mounts:       mounts,
		RemoteUser:   "dev",
		Customizations: &customizationsConfig{
			Confine: &confineConfig{
				Memory: "8g",
				CPUs:   "4",
			},
		},
	}

	if known {
		cfg.Build = &buildConfig{Dockerfile: "Dockerfile"}
	} else {
		cfg.Image = baseImageTag
	}

	return json.MarshalIndent(cfg, "", "  ")
}

// renderHostMounts turns a slice of hostMount values into devcontainer.json
// mount strings. Source is rendered verbatim (including ${localEnv:HOME});
// the runtime resolves it at container creation. ReadOnly entries append
// ",readonly" to the mount string. Extracted from generateDevcontainerJSON so
// the ReadOnly branch can be pinned by a direct unit test without threading
// synthetic entries through the production map.
func renderHostMounts(hostMounts []hostMount) []string {
	if len(hostMounts) == 0 {
		return nil
	}
	out := make([]string, 0, len(hostMounts))
	for _, hm := range hostMounts {
		mountStr := fmt.Sprintf("type=bind,source=%s,target=%s", hm.Source, hm.Target)
		if hm.ReadOnly {
			mountStr += ",readonly"
		}
		out = append(out, mountStr)
	}
	return out
}
