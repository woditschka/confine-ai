package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/woditschka/confine-ai/internal/sanitize"
)

// isHexString reports whether s is a non-empty string of lowercase hex digits.
func isHexString(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// FindByLabels queries the container runtime for containers matching the
// folder-set labels. Returns all matching containers (zero, one, or more).
// The caller decides how to handle the count.
//
// Returns an error if folderSet is empty or if the executor fails.
// An empty result (no matching containers) is not an error.
func FindByLabels(ctx context.Context, executor Executor, folderSet []string) ([]Container, error) {
	if len(folderSet) == 0 {
		return nil, errors.New("find containers: empty folder set")
	}
	containers, err := findContainers(ctx, executor, NewLabels(folderSet))
	if err != nil {
		return nil, fmt.Errorf("find containers: %w", err)
	}
	return containers, nil
}

// FindByAssistant queries the container runtime for containers matching both the
// assistant-name label and the folder-set metadata ID. Returns containers for a
// specific (assistant, folder-set) pair. All container states are included.
//
// Returns an error if assistantName is empty, folderSet is empty, or if the
// executor fails. An empty result (no matching containers) is not an error.
func FindByAssistant(ctx context.Context, executor Executor, assistantName string, folderSet []string) ([]Container, error) {
	if assistantName == "" {
		return nil, errors.New("find assistant containers: empty assistant name")
	}
	if len(folderSet) == 0 {
		return nil, errors.New("find assistant containers: empty folder set")
	}
	containers, err := findContainers(ctx, executor, NewAssistantLabels(assistantName, folderSet))
	if err != nil {
		return nil, fmt.Errorf("find assistant containers: %w", err)
	}
	return containers, nil
}

// findContainers queries the container runtime for containers matching the
// given labels. All container states are included (--all flag).
func findContainers(ctx context.Context, executor Executor, labels Labels) ([]Container, error) {
	args := []string{"ps", "--all"}
	args = append(args, labels.FilterArgs()...)
	args = append(args, "--format", "{{.ID}}")

	output, err := executor.Output(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseContainerIDs(output), nil
}

// ContainerInfo holds extended container metadata for the status command.
// Contains the container ID, status string, optional assistant name, and workspace
// path.
type ContainerInfo struct {
	ID        string // Container ID (hex string).
	Status    string // Container status from docker ps (e.g., "Up 2 hours").
	Assistant string // Assistant name from label, or empty for project-local containers.
	Workspace string // Workspace path from the local folder label.
}

// FindAllManaged queries the container runtime for all confine-ai-managed
// assistant containers. Returns ContainerInfo values with assistant name,
// workspace, and status extracted from labels and docker ps output. Emits
// labels as a JSON object via the `{{json .Labels}}` template, which both
// Docker and Podman support portably (the older `{{.Label "name"}}` accessor
// is Docker-only). Filters by the assistant-name label so every result is
// guaranteed to carry an assistant identity.
func FindAllManaged(ctx context.Context, executor Executor) ([]ContainerInfo, error) {
	args := []string{
		"ps", "--all",
		"--filter", "label=" + labelAssistantName,
		"--format", `{{.ID}}	{{.Status}}	{{json .Labels}}`,
	}

	output, err := executor.Output(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("find managed containers: %w", err)
	}

	return parseContainerInfos(output), nil
}

// parseContainerInfos splits tab-separated container info lines from docker ps
// output into ContainerInfo values. Each line has the form
// "<id>\t<status>\t<labels-json>". Lines that do not have exactly 3 fields or
// whose label payload is not a JSON object are skipped.
func parseContainerInfos(output string) []ContainerInfo {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}

	var infos []ContainerInfo
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		labels := map[string]string{}
		if err := json.Unmarshal([]byte(fields[2]), &labels); err != nil {
			continue
		}
		infos = append(infos, ContainerInfo{
			ID:        fields[0],
			Status:    sanitize.ControlChars(fields[1]),
			Assistant: labels[labelAssistantName],
			Workspace: labels[labelLocalFolder],
		})
	}
	return infos
}

// parseContainerIDs splits newline-separated container IDs from docker ps
// output into Container values. Empty lines are skipped.
func parseContainerIDs(output string) []Container {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}

	var containers []Container
	for line := range strings.SplitSeq(output, "\n") {
		id := strings.TrimSpace(line)
		if id == "" { // Skip blank lines from trailing newlines in docker ps output.
			continue
		}
		if isHexString(id) {
			containers = append(containers, Container{ID: id})
		}
	}
	return containers
}
