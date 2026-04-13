// Package assistant provides assistant name validation, configuration path resolution,
// and assistant directory existence checks.
package assistant

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

// Assistant name constraints.
const (
	assistantNameMinLen = 2
	assistantNameMaxLen = 64
)

// assistantNamePattern matches valid assistant names: lowercase alphanumeric with
// hyphens, starting and ending with an alphanumeric character.
var assistantNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$`)

// ValidateName checks whether name is a valid assistant name. Valid names contain
// only lowercase alphanumeric characters and hyphens, must start and end with
// an alphanumeric character, and must be between 2 and 64 characters long.
// The allowed character set prevents path traversal (no dots or slashes).
func ValidateName(name string) error {
	if len(name) < assistantNameMinLen {
		return fmt.Errorf("assistant name %q is too short (minimum %d characters)", name, assistantNameMinLen)
	}
	if len(name) > assistantNameMaxLen {
		return fmt.Errorf("assistant name %q is too long (maximum %d characters)", name, assistantNameMaxLen)
	}
	if !assistantNamePattern.MatchString(name) {
		return fmt.Errorf("assistant name %q is invalid: must contain only lowercase letters, digits, and hyphens, starting and ending with a letter or digit", name)
	}
	return nil
}

// Layout helpers. These are the single source of truth for where confine-ai
// stores files on disk. Callers must validate `name` with ValidateName before
// calling any function that takes a name. All helpers are pure path joins and
// perform no I/O.
//
// Directory layout:
//
//	~/.confine-ai/
//	├── base/Dockerfile            ← BaseDockerfilePath
//	├── assistants/                    ← AssistantsDir
//	│   └── <name>/                ← Dir
//	│       ├── Dockerfile         ← DockerfilePath
//	│       └── devcontainer.json  ← ConfigPath
//	└── data/<name>/               ← DataPath

// AssistantsDir returns the parent directory that holds all assistant config dirs.
func AssistantsDir(homeDir string) string {
	return filepath.Join(homeDir, ".confine-ai", "assistants")
}

// Dir returns the absolute path to an assistant's config directory.
func Dir(homeDir, name string) string {
	return filepath.Join(AssistantsDir(homeDir), name)
}

// DockerfilePath returns the absolute path to an assistant's Dockerfile.
func DockerfilePath(homeDir, name string) string {
	return filepath.Join(Dir(homeDir, name), "Dockerfile")
}

// ConfigPath returns the absolute path to an assistant's devcontainer.json file.
func ConfigPath(homeDir, name string) string {
	return filepath.Join(Dir(homeDir, name), "devcontainer.json")
}

// DataPath returns the absolute path to an assistant's persistent data directory.
func DataPath(homeDir, name string) string {
	return filepath.Join(homeDir, ".confine-ai", "data", name)
}

// BaseDockerfilePath returns the absolute path to the user-owned base
// Dockerfile at ~/.confine-ai/base/Dockerfile.
func BaseDockerfilePath(homeDir string) string {
	return filepath.Join(homeDir, ".confine-ai", "base", "Dockerfile")
}

// Exists reports whether the assistant configuration directory exists.
// Returns false if the path does not exist or is not a directory.
func Exists(homeDir, name string) bool {
	info, err := os.Stat(Dir(homeDir, name))
	if err != nil {
		return false
	}
	return info.IsDir()
}

// ListNames returns the sorted list of valid assistant names found under
// ~/.confine-ai/assistants/. It returns nil (not an error) if the directory does not
// exist or is unreadable, matching the graceful degradation expected by shell
// completion.
func ListNames(homeDir string) []string {
	entries, err := os.ReadDir(AssistantsDir(homeDir))
	if err != nil {
		return nil
	}

	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if ValidateName(name) != nil {
			continue
		}
		names = append(names, name)
	}

	slices.Sort(names)
	return names
}

// knownAssistantPostInitHints maps assistant names to guidance printed after
// a successful init. Each hint helps the user complete setup that confine-ai
// cannot automate (provider configuration, auth, etc.).
var knownAssistantPostInitHints = map[string]string{
	"opencode": `opencode requires a configured AI provider. After init, either:

  1. Copy your host config into the container data dir:
     cp ~/.config/opencode/opencode.json ~/.confine-ai/data/opencode/

  2. Or configure a provider inside the container:
     confine-ai opencode --shell
     opencode providers          # interactive provider setup

Common providers: ollama (local), openrouter (API key), github-copilot (OAuth)`,
}

// PostInitHint returns setup guidance for the named assistant, or empty string
// if no hint is defined.
func PostInitHint(name string) string {
	return knownAssistantPostInitHints[name]
}

// NeedsProviderHint reports whether the assistant's data directory still
// contains only the default seed config with no user-configured providers.
// When true, the caller should print the post-init hint to guide provider
// setup. Returns false if the assistant has no seed files, the seed file has
// been modified, or the file cannot be read.
func NeedsProviderHint(homeDir, name string) bool {
	extras := knownAssistantExtraFiles[name]
	if len(extras) == 0 {
		return false
	}
	for _, ef := range extras {
		if ef.Content == "" {
			continue
		}
		seedPath := filepath.Join(DataPath(homeDir, name), ef.Source)
		content, err := os.ReadFile(seedPath)
		if err != nil {
			return false
		}
		if string(content) == ef.Content {
			// Config still matches the default seed — no provider configured.
			return true
		}
	}
	return false
}

// EnsureExtraFiles creates any missing extra files in the assistant's data
// directory. This is called during init and at assistant startup to handle
// assistants initialized before extra file support was added.
func EnsureExtraFiles(homeDir, name string) error {
	dataDir := DataPath(homeDir, name)
	for _, ef := range knownAssistantExtraFiles[name] {
		seedPath := filepath.Join(dataDir, ef.Source)
		if _, err := os.Stat(seedPath); os.IsNotExist(err) {
			if err := os.WriteFile(seedPath, []byte(ef.Content), 0o644); err != nil {
				return fmt.Errorf("seed extra file %s: %w", ef.Source, err)
			}
		}
	}
	return nil
}
