// Package container provides container identification and lifecycle operations.
package container

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/woditschka/confine-ai/internal/sanitize"
)

// Label keys used to identify devcontainer workspaces.
const (
	// labelLocalFolder stores the folder paths (newline-separated) for human
	// readability and display by confine-ai status (REQ-CO-001 AC 6).
	labelLocalFolder = "devcontainer.local_folder"

	// labelMetadataID stores the SHA-256 hex digest of the sorted folder set.
	// This is the primary query key for container lookups.
	labelMetadataID = "devcontainer.metadata_id"

	// labelAssistantName stores the assistant name for assistant-managed containers.
	// Absent on project-local containers created via `confine-ai up`.
	labelAssistantName = "devcontainer.assistant_name"
)

// Labels holds the set of container labels for a folder set. The zero value
// has a nil values map, which signals that no explicit labels were provided.
// Use NewLabels or NewAssistantLabels to create a valid Labels instance.
type Labels struct {
	values map[string]string
}

// IsZero reports whether l is the zero value (no labels set).
func (l Labels) IsZero() bool {
	return l.values == nil
}

// NewLabels creates the label set for a folder set. The folderSet must contain
// at least one non-empty path; callers are responsible for validation.
// Paths are sorted lexicographically for argument-order independence (REQ-CO-001 AC 4).
// The local_folder label stores sorted paths joined by newlines for display.
// The metadata_id label stores the SHA-256 hex digest of the sorted, newline-joined paths.
func NewLabels(folderSet []string) Labels {
	sorted := sortedCopy(folderSet)
	return Labels{
		values: map[string]string{
			labelLocalFolder: sanitizeAndJoin(sorted),
			labelMetadataID:  folderSetID(sorted),
		},
	}
}

// NewAssistantLabels creates the label set for an assistant container. It includes the
// folder-set labels from NewLabels plus the assistant name label. Project-local
// containers use NewLabels (two labels); assistant containers use NewAssistantLabels
// (three labels).
func NewAssistantLabels(assistantName string, folderSet []string) Labels {
	sorted := sortedCopy(folderSet)
	return Labels{
		values: map[string]string{
			labelLocalFolder:   sanitizeAndJoin(sorted),
			labelMetadataID:    folderSetID(sorted),
			labelAssistantName: assistantName,
		},
	}
}

// Values returns the label key-value pairs.
func (l Labels) Values() map[string]string {
	return l.values
}

// ForArgs returns command-line arguments for applying labels to a container.
// The result is suitable for appending to docker run or docker create commands:
// ["--label", "key=value", "--label", "key=value"].
func (l Labels) ForArgs() []string {
	args := []string{
		"--label", labelLocalFolder + "=" + l.values[labelLocalFolder],
		"--label", labelMetadataID + "=" + l.values[labelMetadataID],
	}
	if assistant, ok := l.values[labelAssistantName]; ok {
		args = append(args, "--label", labelAssistantName+"="+assistant)
	}
	return args
}

// FilterArgs returns command-line arguments for filtering containers by
// folder-set identity. The result is suitable for appending to docker ps:
// ["--filter", "label=key=value"].
// Queries use the metadata_id label (SHA-256 hex) because it has no special
// characters that interact with shell quoting or --filter parsing.
// When an assistant name label is present, the filter includes both metadata_id
// and assistant_name to narrow results to a specific (assistant, folder-set) pair.
func (l Labels) FilterArgs() []string {
	args := []string{
		"--filter", "label=" + labelMetadataID + "=" + l.values[labelMetadataID],
	}
	if assistant, ok := l.values[labelAssistantName]; ok {
		args = append(args, "--filter", "label="+labelAssistantName+"="+assistant)
	}
	return args
}

// folderSetID returns a deterministic identifier derived from a set of folder paths.
// It encodes each path as a length-prefixed, null-terminated entry and computes the
// SHA-256 hex digest. Null bytes cannot appear in POSIX paths, so this encoding is
// unambiguous — a single path containing a newline cannot collide with two separate
// paths. See ADR: Folder-Set Container Identity.
func folderSetID(sortedPaths []string) string {
	var b strings.Builder
	for _, p := range sortedPaths {
		fmt.Fprintf(&b, "%d:%s\x00", len(p), p)
	}
	hash := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(hash[:])
}

// sanitizeAndJoin sanitizes each path individually (replacing control characters)
// then joins them with newline separators for the local_folder label.
func sanitizeAndJoin(sortedPaths []string) string {
	sanitized := make([]string, len(sortedPaths))
	for i, p := range sortedPaths {
		sanitized[i] = sanitize.ControlChars(p)
	}
	return strings.Join(sanitized, "\n")
}

// sortedCopy returns a sorted copy of the input slice. The input is not modified.
func sortedCopy(paths []string) []string {
	sorted := make([]string, len(paths))
	copy(sorted, paths)
	slices.Sort(sorted)
	return sorted
}
