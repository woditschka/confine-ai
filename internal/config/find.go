// Package config discovers and loads devcontainer.json configuration files.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Find discovers the devcontainer.json configuration file for the given
// workspace folder. It searches in the order defined by the devcontainer
// specification and returns the absolute path to the configuration file.
//
// Find returns an error if the workspace folder does not exist, no
// configuration file is found, or multiple ambiguous subfolders exist
// under .devcontainer/.
//
// All returned paths are resolved through [filepath.EvalSymlinks] and
// verified to reside within the workspace folder.
func Find(workspaceFolder string) (string, error) {
	// Resolve workspace to a real path for symlink containment checks.
	realWorkspace, err := filepath.EvalSymlinks(workspaceFolder)
	if err != nil {
		return "", fmt.Errorf("stat workspace folder %q: %w", workspaceFolder, err)
	}

	// Step 1: .devcontainer/devcontainer.json
	direct := filepath.Join(realWorkspace, ".devcontainer", "devcontainer.json")
	if path, ok := resolveConfigFile(direct, realWorkspace); ok {
		return path, nil
	}

	// Step 2: .devcontainer.json
	dotfile := filepath.Join(realWorkspace, ".devcontainer.json")
	if path, ok := resolveConfigFile(dotfile, realWorkspace); ok {
		return path, nil
	}

	// Step 3: .devcontainer/<subfolder>/devcontainer.json
	devcontainerDir := filepath.Join(realWorkspace, ".devcontainer")
	entries, err := os.ReadDir(devcontainerDir)
	if err != nil {
		// The .devcontainer directory does not exist or is not readable.
		return "", fmt.Errorf("no devcontainer.json found in %s; create .devcontainer/devcontainer.json to get started", realWorkspace)
	}

	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(devcontainerDir, entry.Name(), "devcontainer.json")
		if _, ok := resolveConfigFile(candidate, realWorkspace); ok {
			matches = append(matches, entry.Name())
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no devcontainer.json found in %s; create .devcontainer/devcontainer.json to get started", realWorkspace)
	case 1:
		return filepath.Join(devcontainerDir, matches[0], "devcontainer.json"), nil
	default:
		slices.Sort(matches)
		quoted := make([]string, len(matches))
		for i, m := range matches {
			quoted[i] = fmt.Sprintf("%q", m)
		}
		return "", fmt.Errorf("multiple .devcontainer subfolders found: %s; use --config to specify which one", strings.Join(quoted, ", "))
	}
}

// resolveConfigFile checks whether path exists, is a regular file, and
// resolves (via symlinks) to a location within workspaceRoot. It returns
// the resolved path and true if all checks pass.
func resolveConfigFile(path, workspaceRoot string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() {
		return "", false
	}
	// Ensure the resolved path is still within the workspace.
	rel, err := filepath.Rel(workspaceRoot, resolved)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return resolved, true
}
