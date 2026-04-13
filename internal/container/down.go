package container

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// DownResult represents the outcome of the down operation.
type DownResult struct {
	Removed []string // Container IDs that were stopped and removed.
}

// DownAssistant stops and removes the container for a specific (assistant, folder-set)
// pair. It uses FindByAssistant for targeted lookup, complementing the existing
// Down function which removes all folder-set containers.
//
// An empty result (no containers found) is not an error. On partial failure,
// the result includes containers that were removed before the error.
func DownAssistant(ctx context.Context, executor Executor, assistantName string, folderSet []string, stderr io.Writer) (DownResult, error) {
	containers, err := FindByAssistant(ctx, executor, assistantName, folderSet)
	if err != nil {
		return DownResult{}, fmt.Errorf("down assistant: %w", err)
	}
	return removeContainers(ctx, executor, containers, "down assistant", stderr)
}

// Down stops and removes all containers for a folder set, regardless of their
// current state. It finds containers via FindByLabels (all states), then stops
// and removes each one. An empty result (no containers found) is not an error.
// On partial failure, the result includes containers that were removed before
// the error.
func Down(ctx context.Context, executor Executor, folderSet []string, stderr io.Writer) (DownResult, error) {
	containers, err := FindByLabels(ctx, executor, folderSet)
	if err != nil {
		return DownResult{}, fmt.Errorf("down: %w", err)
	}
	return removeContainers(ctx, executor, containers, "down", stderr)
}

// removeContainers stops and removes the given containers. When multiple
// containers are found, a warning is printed to stderr. On partial failure,
// the result includes containers removed before the error.
func removeContainers(ctx context.Context, executor Executor, containers []Container, errContext string, stderr io.Writer) (DownResult, error) {
	if len(containers) == 0 {
		return DownResult{}, nil
	}

	if len(containers) > 1 {
		ids := make([]string, len(containers))
		for i, c := range containers {
			ids[i] = c.ID
		}
		fmt.Fprintf(stderr, "warning: %d containers found (%s), removing all\n",
			len(containers), strings.Join(ids, ", "))
	}

	removed := make([]string, 0, len(containers))
	for _, c := range containers {
		if err := StopAndRemove(ctx, executor, c.ID); err != nil {
			return DownResult{Removed: removed}, fmt.Errorf("%s: %w", errContext, err)
		}
		removed = append(removed, c.ID)
	}

	return DownResult{Removed: removed}, nil
}
