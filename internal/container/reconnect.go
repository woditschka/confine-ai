package container

import (
	"context"
	"fmt"
	"io"

	"github.com/woditschka/confine-ai/internal/config"
)

// ReconnectOutcome describes the result of a reconnect attempt.
type ReconnectOutcome int

// reconnectUnknown is the zero value sentinel. It is never returned by
// ReconnectOrRecreate; its presence makes accidental zero reads detectable.
const reconnectUnknown ReconnectOutcome = 0

const (
	// ReconnectStarted means the existing container was started (or was
	// already running) and is ready for exec. The config hash matched.
	ReconnectStarted ReconnectOutcome = iota + 1

	// ReconnectRecreated means the existing container was stopped and
	// removed because the config hash differed. The caller must create a
	// new container via Up.
	ReconnectRecreated
)

// ReconnectOptions groups the parameters for ReconnectOrRecreate.
type ReconnectOptions struct {
	ContainerID       string
	Config            config.Config
	AdditionalFolders []string
	Network           string
	AllowedHosts      []string
}

// ReconnectOrRecreate validates the config hash of an existing container and
// either starts it (hash matches) or stops and removes it (hash differs).
//
// When the hash matches, the container is started (docker start is a no-op for
// running containers), firewall rules are re-applied, and the caller should
// exec into it.
//
// When the hash differs, a message is written to stderr, the container is
// stopped and removed, and the caller should create a new container via Up.
func ReconnectOrRecreate(ctx context.Context, executor Executor, opts ReconnectOptions, stderr io.Writer) (ReconnectOutcome, error) {
	storedHash, err := InspectConfigHash(ctx, executor, opts.ContainerID)
	if err != nil {
		return reconnectUnknown, err
	}

	currentHash := ConfigHashWithFolders(opts.Config, opts.AdditionalFolders)
	if storedHash != currentHash {
		// Config changed: recreate the container (REQ-AS-002 AC 11).
		fmt.Fprintf(stderr, "Configuration changed, recreating container...\n")
		if err := StopAndRemove(ctx, executor, opts.ContainerID); err != nil {
			return reconnectUnknown, fmt.Errorf("recreate container: %w", err)
		}
		return ReconnectRecreated, nil
	}

	// Config matches: start the container (no-op if already running).
	if err := executor.Run(ctx, nil, nil, "start", opts.ContainerID); err != nil {
		return reconnectUnknown, fmt.Errorf("start container: %w", err)
	}

	// Reapply firewall rules (iptables rules do not persist across stop/start).
	if opts.Network != "none" {
		if err := setupFirewall(ctx, executor, opts.ContainerID, opts.Network, opts.AllowedHosts, stderr); err != nil {
			_ = StopAndRemove(ctx, executor, opts.ContainerID)
			return reconnectUnknown, fmt.Errorf("firewall setup: %w", err)
		}
	}

	return ReconnectStarted, nil
}
