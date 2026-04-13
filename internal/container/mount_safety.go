package container

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// MountRisk describes a single mount safety finding. Returned by
// ValidateMounts and probeWorkspaceRisks.
type MountRisk struct {
	Source string // The mount source path that triggered the finding.
	Tier   int    // 1 (blocked, unconditional) or 2 (risky, requires --allow-risky-mounts).
	Reason string // Human-readable explanation of the risk.
}

// blockedPathDefs lists canonical paths and their tier 1 block reasons.
// These are resolved to real paths at init time to handle platform symlinks
// (e.g., macOS /etc → /private/etc).
var blockedPathDefs = []struct {
	path   string
	reason string
}{
	{"/", "exposes entire host filesystem"},
	{"/etc", "exposes host system configuration"},
	{"/tmp", "shared temporary directory; use a dedicated mount instead"},
	{"/var/run/docker.sock", "enables container escape via Docker API"},
	{"/var/run/podman/podman.sock", "enables container escape via Podman API"},
}

// blockedPaths maps absolute host paths to their tier 1 block reasons.
// Populated by init() with both canonical and symlink-resolved forms.
// Read-only after init(); safe for concurrent reads without synchronization.
var blockedPaths = map[string]string{}

func init() {
	for _, def := range blockedPathDefs {
		blockedPaths[def.path] = def.reason
		if resolved, err := filepath.EvalSymlinks(def.path); err == nil && resolved != def.path {
			blockedPaths[resolved] = def.reason
		}
	}
}

// sensitiveNames lists file and directory names that indicate sensitive
// content. Used by both mount path classification (tier 2 risky) and
// workspace filesystem probing.
var sensitiveNames = []string{".ssh", ".gnupg", ".aws", ".env", "credentials"}

// broadDirectories lists mount source paths that trigger tier 2 warnings
// due to their broad scope. Matched by exact path only.
var broadDirectories = map[string]bool{
	"/opt":       true,
	"/usr/local": true,
}

// ValidateMounts classifies mount source paths into blocked (tier 1) and
// risky (tier 2) categories. Paths are resolved through the resolve
// function before classification to prevent symlink-based bypasses.
// Unresolvable paths (broken symlinks, permission errors) are blocked
// as tier 1. Pass filepath.EvalSymlinks for production use.
//
// The workspaceFolder is validated as an implicit mount source alongside
// explicit mounts from the Config.Mounts slice.
//
// When homeDir is empty (os.UserHomeDir failed), /home and /Users are
// blocked as a fail-safe fallback.
func ValidateMounts(workspaceFolder string, mounts []string, homeDir string, resolve func(string) (string, error)) (blocked []MountRisk, risky []MountRisk) {
	// Collect all source paths to validate: workspace + explicit mounts.
	sources := []string{workspaceFolder}

	for _, m := range mounts {
		source, isBind := extractMountSource(m)
		if !isBind || source == "" {
			continue
		}
		sources = append(sources, source)
	}

	for _, s := range sources {
		resolved, err := resolve(s)
		if err != nil {
			// Unresolvable path (broken symlink, permissions): block as tier 1.
			blocked = append(blocked, MountRisk{
				Source: s,
				Tier:   1,
				Reason: fmt.Sprintf("cannot resolve path: %v", err),
			})
			continue
		}
		cleaned := filepath.Clean(resolved)

		b, r := classifyPath(cleaned, homeDir)
		if b != nil {
			blocked = append(blocked, *b)
		}
		if r != nil {
			risky = append(risky, *r)
		}
	}

	return blocked, risky
}

// classifyPath checks a single normalized source path against the tier 1
// and tier 2 rules. Returns at most one blocked and one risky finding.
// Tier 1 takes precedence: if a path is blocked, no risky finding is returned.
func classifyPath(path, homeDir string) (blocked *MountRisk, risky *MountRisk) {
	// Tier 1: static deny list.
	if reason, ok := blockedPaths[path]; ok {
		return &MountRisk{Source: path, Tier: 1, Reason: reason}, nil
	}

	// Tier 1: exact home directory match.
	if homeDir != "" && path == filepath.Clean(homeDir) {
		return &MountRisk{Source: path, Tier: 1, Reason: "exposes user home directory"}, nil
	}

	// Tier 1: parent-of-home check.
	if homeDir != "" {
		cleanHome := filepath.Clean(homeDir)
		if isParentOf(path, cleanHome) {
			return &MountRisk{Source: path, Tier: 1, Reason: "parent of home directory; exposes user data"}, nil
		}
	}

	// Tier 1 fallback: when homeDir is empty, block /home and /Users.
	if homeDir == "" {
		if path == "/home" || path == "/Users" {
			return &MountRisk{Source: path, Tier: 1, Reason: "parent of home directory; exposes user data (home detection failed)"}, nil
		}
	}

	// Tier 2: broad directories (exact match).
	if broadDirectories[path] {
		return nil, &MountRisk{Source: path, Tier: 2, Reason: "broad directory mount; larger scope than typically needed"}
	}

	// Tier 2: risky path segment patterns.
	segments := splitPathSegments(path)
	for _, name := range sensitiveNames {
		if slices.Contains(segments, name) {
			return nil, &MountRisk{Source: path, Tier: 2, Reason: "contains sensitive path " + name}
		}
	}

	return nil, nil
}

// isParentOf returns true if parent is a proper ancestor of child.
// Both paths must be cleaned (no trailing slashes, no .. segments).
func isParentOf(parent, child string) bool {
	if parent == child {
		return false
	}
	// Special case: "/" is parent of everything.
	if parent == "/" {
		return true
	}
	return strings.HasPrefix(child, parent+"/")
}

// splitPathSegments splits an absolute path into its individual directory
// and file name components. For "/home/user/.ssh", it returns
// ["home", "user", ".ssh"].
func splitPathSegments(path string) []string {
	// filepath.Clean already handles double slashes and trailing slashes.
	parts := strings.Split(path, string(filepath.Separator))
	var segments []string
	for _, seg := range parts {
		if seg != "" {
			segments = append(segments, seg)
		}
	}
	return segments
}

// extractMountSource parses a Docker CLI mount format string and returns
// the source path and whether it is a bind mount. Volume mounts (type=volume
// or named volumes without absolute paths) return empty source and false.
func extractMountSource(mount string) (source string, isBind bool) {
	var mountType, mountSource string

	for part := range strings.SplitSeq(mount, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "type":
			mountType = kv[1]
		case "source", "src":
			mountSource = kv[1]
		}
	}

	// Determine if this is a bind mount.
	switch mountType {
	case "volume":
		return "", false
	case "bind":
		return mountSource, true
	case "":
		// No explicit type: bind if source is an absolute path.
		if filepath.IsAbs(mountSource) {
			return mountSource, true
		}
		return "", false
	default:
		// Unknown type: treat as non-bind.
		return "", false
	}
}

// probeWorkspaceRisks checks whether the workspace directory contains
// sensitive files or directories. Returns tier 2 findings for any matches.
// Stat failures are skipped silently.
func probeWorkspaceRisks(workspaceFolder string) []MountRisk {
	var risks []MountRisk

	for _, name := range sensitiveNames {
		path := filepath.Join(workspaceFolder, name)
		if _, err := os.Stat(path); err == nil {
			risks = append(risks, MountRisk{
				Source: workspaceFolder,
				Tier:   2,
				Reason: "workspace contains sensitive path " + name,
			})
		}
	}

	return risks
}

// formatMountErrors builds a human-readable error message from a list of
// mount risks. The prefix describes the category (e.g., "mount blocked").
// Individual violations are separated by semicolons.
func formatMountErrors(prefix string, risks []MountRisk) string {
	var parts []string
	for _, r := range risks {
		parts = append(parts, fmt.Sprintf("source %q %s", r.Source, r.Reason))
	}
	return prefix + ": " + strings.Join(parts, "; ")
}
