package container

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// FindManagedImages returns all container images whose repository name starts
// with "confine-ai-". This covers workspace/assistant images
// (confine-ai-<workspace>:latest). The base image
// (localhost/confine-ai-base:latest) is intentionally excluded because the
// `confine-ai-*` reference filter does not match the `localhost/` prefix; the
// base image is managed by `confine-ai update base` and is not subject to bulk
// removal.
func FindManagedImages(ctx context.Context, executor Executor) ([]string, error) {
	output, err := executor.Output(ctx,
		"image", "ls",
		"--filter", "reference=confine-ai-*",
		"--format", "{{.Repository}}:{{.Tag}}",
	)
	if err != nil {
		return nil, fmt.Errorf("find managed images: %w", err)
	}

	output = strings.TrimSpace(output)
	if output == "" {
		return nil, nil
	}

	return strings.Split(output, "\n"), nil
}

// RemoveImages removes the listed images, skipping the base image
// (localhost/confine-ai-base:latest, and the unqualified short-name form
// confine-ai-base:latest without the localhost/ prefix) which is rebuilt
// rather than removed. Removal is best-effort: individual failures
// are logged to stderr but do not stop processing. Returns the first error
// encountered, if any.
func RemoveImages(ctx context.Context, executor Executor, images []string, stderr io.Writer) error {
	var firstErr error
	for _, img := range images {
		if img == "localhost/confine-ai-base:latest" || img == "confine-ai-base:latest" {
			continue
		}
		fmt.Fprintf(stderr, "Removing image %s\n", img)
		if err := executor.Run(ctx, nil, stderr, "rmi", img); err != nil {
			fmt.Fprintf(stderr, "warning: failed to remove image %s: %v\n", img, err)
			if firstErr == nil {
				firstErr = fmt.Errorf("remove image %s: %w", img, err)
			}
		}
	}
	return firstErr
}

// RemoveContainersByAssistant stops and removes every confine-ai-managed container
// labelled with the given assistant name. Removal is best-effort: a stop or rm
// failure is logged to stderr and processing continues with the next
// container. The first error encountered is returned so callers can surface
// a non-zero exit, but every container is still attempted.
func RemoveContainersByAssistant(ctx context.Context, executor Executor, assistantName string, stderr io.Writer) error {
	if assistantName == "" {
		return errors.New("remove containers by assistant: empty assistant name")
	}

	output, err := executor.Output(ctx,
		"ps", "--all",
		"--filter", "label="+labelMetadataID,
		"--filter", "label="+labelAssistantName+"="+assistantName,
		"--format", "{{.ID}}",
	)
	if err != nil {
		return fmt.Errorf("remove containers by assistant %s: %w", assistantName, err)
	}

	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}

	var firstErr error
	for id := range strings.SplitSeq(output, "\n") {
		id = strings.TrimSpace(id)
		if id == "" || !isHexString(id) {
			continue
		}
		fmt.Fprintf(stderr, "Stopping container %s...\n", id[:min(12, len(id))])
		if err := StopAndRemove(ctx, executor, id); err != nil {
			fmt.Fprintf(stderr, "warning: failed to remove container %s: %v\n", id, err)
			if firstErr == nil {
				firstErr = fmt.Errorf("remove container %s: %w", id, err)
			}
		}
	}
	return firstErr
}

// RemoveAllContainers stops and removes every confine-ai-managed container.
// Returns the IDs of successfully removed containers.
func RemoveAllContainers(ctx context.Context, executor Executor, stderr io.Writer) ([]string, error) {
	infos, err := FindAllManaged(ctx, executor)
	if err != nil {
		return nil, fmt.Errorf("remove all containers: %w", err)
	}

	var removed []string
	for _, info := range infos {
		fmt.Fprintf(stderr, "Stopping container %s...\n", info.ID[:min(12, len(info.ID))])
		if err := StopAndRemove(ctx, executor, info.ID); err != nil {
			fmt.Fprintf(stderr, "warning: failed to remove container %s: %v\n", info.ID, err)
			continue
		}
		removed = append(removed, info.ID)
	}
	return removed, nil
}
