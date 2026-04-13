package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/woditschka/confine-ai/internal/config"
)

// labelConfigHash is the container label key storing the SHA-256 hex digest
// of the resolved configuration. Used for change detection on subsequent up
// commands.
const labelConfigHash = "devcontainer.config_hash"

// Outcome values for UpResult.
const (
	// OutcomeSuccess indicates the container was created or started successfully.
	OutcomeSuccess = "success"

	// OutcomeError indicates a failure during the up operation.
	OutcomeError = "error"
)

// UpOptions carries all parameters for the up operation.
type UpOptions struct {
	WorkspaceFolder         string                       // Absolute path to host workspace (primary workspace).
	Config                  config.Config                // Fully resolved config (post-substitution).
	ConfigPath              string                       // Path to devcontainer.json.
	AdditionalFolders       []string                     // Absolute paths to additional host folders for bind mounting at /workspaces/<basename>.
	AdditionalFolderBase    string                       // Base path for additional folder mounts. Defaults to "/workspaces".
	RemoveExistingContainer bool                         // --remove-existing-container flag.
	AllowRiskyMounts        bool                         // --allow-risky-mounts flag.
	HomeDir                 string                       // Current user's home directory. Empty triggers fallback blocking.
	Network                 string                       // --network flag value.
	AllowedHosts            []string                     // --allowed-hosts flag values. Hostnames or IPs.
	ResourceLimits          config.ResourceLimits        // Resolved memory and CPU limits from config.ResolveResourceLimits.
	RuntimeName             string                       // Container runtime name ("docker" or "podman") for runtime-specific flags.
	ResolveSymlinks         func(string) (string, error) // Symlink resolver for mount validation. Defaults to filepath.EvalSymlinks.
	ConfirmFunc             func(string) bool            // Confirmation callback. Nil means non-interactive (no prompting).
	Labels                  Labels                       // Container labels. Zero value uses NewLabels([]string{WorkspaceFolder}). Assistant containers pass NewAssistantLabels.
}

// UpResult represents the outcome of the up operation. It maps directly
// to the JSON output contract in REQ-CO-002.
type UpResult struct {
	Outcome               string `json:"outcome"`                         // "success" or "error".
	ContainerID           string `json:"containerId,omitempty"`           // Container ID when available.
	RemoteUser            string `json:"remoteUser,omitempty"`            // User identity inside container.
	RemoteWorkspaceFolder string `json:"remoteWorkspaceFolder,omitempty"` // Workspace path inside container.
	Message               string `json:"message,omitempty"`               // Error message (error outcome only).
	Description           string `json:"description,omitempty"`           // Error description (error outcome only).
}

// Up builds (if needed) and starts a development container. It returns an
// UpResult for JSON serialization. Operational errors produce an UpResult
// with Outcome "error". An error return occurs only for infrastructure
// failures where no result can be produced.
func Up(ctx context.Context, executor Executor, opts UpOptions, stderr io.Writer) (UpResult, error) {
	if stderr == nil {
		stderr = io.Discard
	}

	// Validate network mode.
	if opts.Network == "host" {
		return UpResult{
			Outcome: OutcomeError,
			Message: "host networking is blocked for security; use bridge, none, or a named network",
		}, nil
	}

	if opts.Network == "none" && len(opts.AllowedHosts) > 0 {
		return UpResult{
			Outcome: OutcomeError,
			Message: "--allowed-hosts cannot be used with --network none",
		}, nil
	}

	// Resolve workspace mount. Default the container path before computing
	// the mount string so both use the same value.
	containerPath := opts.Config.WorkspaceFolder
	if containerPath == "" {
		containerPath = "/workspaces/" + filepath.Base(opts.WorkspaceFolder)
	}
	wsMountStr, err := workspaceMount(opts.WorkspaceFolder, containerPath)
	if err != nil {
		return UpResult{
			Outcome: OutcomeError,
			Message: fmt.Sprintf("workspace mount: %v", err),
		}, nil
	}

	// Build additional folder mount strings.
	additionalBase := "/workspaces"
	if opts.AdditionalFolderBase != "" {
		additionalBase = opts.AdditionalFolderBase
	}
	var additionalMountStrings []string
	for _, folder := range opts.AdditionalFolders {
		mountStr, mountErr := workspaceMount(folder, additionalBase+"/"+filepath.Base(folder))
		if mountErr != nil {
			return UpResult{
				Outcome: OutcomeError,
				Message: fmt.Sprintf("additional folder mount: %v", mountErr),
			}, nil
		}
		additionalMountStrings = append(additionalMountStrings, mountStr)
	}

	// Validate mount safety before any container operations.
	resolve := opts.ResolveSymlinks
	if resolve == nil {
		resolve = filepath.EvalSymlinks
	}

	// Auto-create missing bind mount directories (REQ-CO-010).
	if result := autoCreateMissingDirs(opts, resolve); result != nil {
		return *result, nil
	}

	// Include additional folder mount strings in validation so they pass
	// through the same tier 1/tier 2 classification as config mounts.
	allMounts := slices.Concat(opts.Config.Mounts, additionalMountStrings)
	blocked, risky := ValidateMounts(opts.WorkspaceFolder, allMounts, opts.HomeDir, resolve)
	wsRisks := probeWorkspaceRisks(opts.WorkspaceFolder)
	risky = append(risky, wsRisks...)

	// Probe workspace risks for each additional folder.
	for _, folder := range opts.AdditionalFolders {
		additionalRisks := probeWorkspaceRisks(folder)
		risky = append(risky, additionalRisks...)
	}

	if len(blocked) > 0 {
		return UpResult{
			Outcome: OutcomeError,
			Message: formatMountErrors("mount blocked", blocked),
		}, nil
	}

	if len(risky) > 0 {
		if !opts.AllowRiskyMounts {
			return UpResult{
				Outcome: OutcomeError,
				Message: formatMountErrors("risky mounts detected", risky) + "; use --allow-risky-mounts to proceed",
			}, nil
		}
		fmt.Fprintf(stderr, "warning: %s\n", formatMountErrors("risky mounts acknowledged", risky))
	}

	// Check for existing container.
	fmt.Fprintf(stderr, "Looking for existing container...\n")
	labels := opts.Labels
	if labels.IsZero() {
		labels = NewLabels([]string{opts.WorkspaceFolder})
	}
	folderSet := slices.Concat([]string{opts.WorkspaceFolder}, opts.AdditionalFolders)
	containers, err := FindByLabels(ctx, executor, folderSet)
	if err != nil {
		return UpResult{
			Outcome: OutcomeError,
			Message: fmt.Sprintf("find containers: %v", err),
		}, nil
	}

	if len(containers) > 0 {
		existing := containers[0]

		if opts.RemoveExistingContainer {
			// Force remove.
			if err := StopAndRemove(ctx, executor, existing.ID); err != nil {
				return UpResult{
					Outcome:     OutcomeError,
					ContainerID: existing.ID,
					Message:     fmt.Sprintf("remove existing container: %v", err),
				}, nil
			}
		} else {
			// Check config hash for reuse.
			storedHash, err := InspectConfigHash(ctx, executor, existing.ID)
			if err != nil {
				return UpResult{
					Outcome:     OutcomeError,
					ContainerID: existing.ID,
					Message:     fmt.Sprintf("inspect container: %v", err),
				}, nil
			}

			currentHash := ConfigHashWithFolders(opts.Config, opts.AdditionalFolders)
			if storedHash == currentHash {
				// Reuse: start the container (no-op if already running).
				fmt.Fprintf(stderr, "Starting existing container...\n")
				if err := executor.Run(ctx, nil, nil, "start", existing.ID); err != nil {
					return UpResult{
						Outcome:     OutcomeError,
						ContainerID: existing.ID,
						Message:     fmt.Sprintf("start container: %v", err),
					}, nil
				}

				// Reapply firewall rules (iptables rules do not persist across stop/start).
				if opts.Network != "none" {
					fmt.Fprintf(stderr, "Applying firewall rules...\n")
					if err := setupFirewall(ctx, executor, existing.ID, opts.Network, opts.AllowedHosts, stderr); err != nil {
						_ = StopAndRemove(ctx, executor, existing.ID)
						return UpResult{
							Outcome: OutcomeError,
							Message: fmt.Sprintf("firewall setup: %v", err),
						}, nil
					}
				}

				return UpResult{
					Outcome:               OutcomeSuccess,
					ContainerID:           existing.ID,
					RemoteUser:            resolveRemoteUser(opts.Config),
					RemoteWorkspaceFolder: containerPath,
				}, nil
			}

			// Config changed: replace.
			fmt.Fprintf(stderr, "Configuration changed, replacing container...\n")
			if err := StopAndRemove(ctx, executor, existing.ID); err != nil {
				return UpResult{
					Outcome:     OutcomeError,
					ContainerID: existing.ID,
					Message:     fmt.Sprintf("replace container: %v", err),
				}, nil
			}
		}
	}

	// Build image if needed.
	image := opts.Config.Image
	if opts.Config.Build != nil {
		fmt.Fprintf(stderr, "Building container image...\n")
		imageTag, err := buildImage(ctx, executor, buildParams{
			Build:           opts.Config.Build,
			ConfigPath:      opts.ConfigPath,
			WorkspaceFolder: opts.WorkspaceFolder,
			Stderr:          stderr,
		})
		if err != nil {
			return UpResult{
				Outcome: OutcomeError,
				Message: fmt.Sprintf("build: %v", err),
			}, nil
		}
		image = imageTag
	}

	// Create container.
	fmt.Fprintf(stderr, "Creating container...\n")
	hash := ConfigHashWithFolders(opts.Config, opts.AdditionalFolders)
	containerID, err := createContainer(ctx, executor, createParams{
		Image:            image,
		Labels:           labels,
		Hash:             hash,
		WSMount:          wsMountStr,
		AdditionalMounts: additionalMountStrings,
		Config:           opts.Config,
		Network:          opts.Network,
		ResourceLimits:   opts.ResourceLimits,
		RuntimeName:      opts.RuntimeName,
	})
	if err != nil {
		return UpResult{
			Outcome: OutcomeError,
			Message: fmt.Sprintf("create: %v", err),
		}, nil
	}

	// Apply firewall rules to block host gateway access.
	if opts.Network != "none" {
		fmt.Fprintf(stderr, "Applying firewall rules...\n")
		if err := setupFirewall(ctx, executor, containerID, opts.Network, opts.AllowedHosts, stderr); err != nil {
			// Fail-secure: remove the container if firewall setup fails.
			_ = StopAndRemove(ctx, executor, containerID)
			return UpResult{
				Outcome: OutcomeError,
				Message: fmt.Sprintf("firewall setup: %v", err),
			}, nil
		}
	}

	return UpResult{
		Outcome:               OutcomeSuccess,
		ContainerID:           containerID,
		RemoteUser:            resolveRemoteUser(opts.Config),
		RemoteWorkspaceFolder: containerPath,
	}, nil
}

// autoCreateMissingDirs detects bind mount sources where the leaf directory
// does not exist but could safely be created. If any are found and a
// ConfirmFunc is set, it prompts the user and creates directories on
// confirmation. Returns a non-nil *UpResult only when directory creation
// fails and the caller should return that error result. Returns nil to
// continue normal flow.
func autoCreateMissingDirs(opts UpOptions, resolve func(string) (string, error)) *UpResult {
	creatable := collectCreatableDirs(opts.Config.Mounts, opts.HomeDir, resolve)
	if len(creatable) == 0 {
		return nil
	}

	if opts.ConfirmFunc == nil {
		// Non-interactive mode: skip to ValidateMounts.
		return nil
	}

	// Build prompt listing all creatable directories.
	var msg strings.Builder
	msg.WriteString("The following bind mount directories do not exist:\n")
	for _, dir := range creatable {
		fmt.Fprintf(&msg, "  %q\n", dir)
	}
	msg.WriteString("Create them? [Y/n] ")

	if !opts.ConfirmFunc(msg.String()) {
		// User declined: skip to ValidateMounts.
		return nil
	}

	// Create the directories.
	for _, dir := range creatable {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return &UpResult{
				Outcome: OutcomeError,
				Message: fmt.Sprintf("create directory %q: %v", dir, err),
			}
		}
	}

	return nil
}

// collectCreatableDirs identifies bind mount source directories that do not
// exist but whose parent exists and whose resolved path passes mount safety
// classification. Returns the list of source paths eligible for creation.
func collectCreatableDirs(mounts []string, homeDir string, resolve func(string) (string, error)) []string {
	var creatable []string

	for _, m := range mounts {
		source, isBind := extractMountSource(m)
		if !isBind || source == "" {
			continue
		}

		// Skip if the source already exists.
		if _, err := os.Stat(source); err == nil {
			continue
		}

		// Check that the parent directory exists and can be resolved.
		parent := filepath.Dir(source)
		resolvedParent, err := resolve(parent)
		if err != nil {
			// Parent does not exist or cannot be resolved: skip.
			continue
		}

		// Classify the resolved parent itself. A child of a blocked
		// directory (e.g., /etc/new-subdir) must not be created.
		parentBlocked, _ := classifyPath(resolvedParent, homeDir)
		if parentBlocked != nil {
			continue
		}

		// Classify the full resolved path (parent + leaf).
		fullResolved := filepath.Join(resolvedParent, filepath.Base(source))
		blocked, risky := classifyPath(fullResolved, homeDir)
		if blocked != nil || risky != nil {
			// Path would not pass classification: skip.
			continue
		}

		creatable = append(creatable, source)
	}

	return creatable
}

// resolveRemoteUser determines the user identity for the result.
// Priority: RemoteUser > ContainerUser > empty (image default).
func resolveRemoteUser(cfg config.Config) string {
	if cfg.RemoteUser != "" {
		return cfg.RemoteUser
	}
	return cfg.ContainerUser
}

// buildParams holds all parameters for building a container image.
type buildParams struct {
	Build           *config.Build // Build configuration from devcontainer.json.
	ConfigPath      string        // Path to devcontainer.json (resolves Dockerfile relative path).
	WorkspaceFolder string        // Absolute path to host workspace (containment check + image tag).
	Stderr          io.Writer     // Build progress output. May be nil.
}

// createParams holds all parameters for creating a container.
type createParams struct {
	Image            string
	Labels           Labels
	Hash             string
	WSMount          string
	AdditionalMounts []string // Additional bind mount strings for extra workspace folders.
	Config           config.Config
	Network          string
	ResourceLimits   config.ResourceLimits
	RuntimeName      string // Container runtime name for runtime-specific flags.
}

// createContainer runs docker run -d with all the configured options and
// returns the container ID from stdout.
func createContainer(ctx context.Context, executor Executor, p createParams) (string, error) {
	args := []string{"run", "-d"}

	// Workspace identity labels.
	args = append(args, p.Labels.ForArgs()...)

	// Config hash label for change detection.
	args = append(args, "--label", labelConfigHash+"="+p.Hash)

	// Workspace mount (always first mount).
	args = append(args, "--mount", p.WSMount)

	// Additional workspace folder mounts.
	for _, m := range p.AdditionalMounts {
		args = append(args, "--mount", m)
	}

	// Config mounts.
	for _, m := range p.Config.Mounts {
		args = append(args, "--mount", m)
	}

	// Environment variables sorted for deterministic args.
	args = appendSortedMap(args, "-e", p.Config.ContainerEnv)

	// User identity.
	if p.Config.ContainerUser != "" {
		args = append(args, "--user", p.Config.ContainerUser)
	}

	// NET_ADMIN capability for firewall rules (not needed for --network none).
	if p.Network != "none" {
		args = append(args, "--cap-add=NET_ADMIN")
	}

	// Resource limits.
	if p.ResourceLimits.Memory != "" {
		args = append(args, "--memory", p.ResourceLimits.Memory)
	}
	if p.ResourceLimits.CPUs != "" {
		args = append(args, "--cpus", p.ResourceLimits.CPUs)
	}

	// Network mode.
	args = append(args, "--network", p.Network)

	// Podman rootless UID mapping: --userns=keep-id maps the host user's
	// UID to the same UID inside the container. Without this, bind-mounted
	// directories appear as root-owned and the container user cannot write
	// to them (e.g. persisting credentials in ~/.claude).
	if p.RuntimeName == "podman" {
		args = append(args, "--userns=keep-id")
	}

	// Image and entrypoint.
	args = append(args, p.Image, "sleep", "infinity")

	output, err := executor.Output(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	return strings.TrimSpace(output), nil
}

// buildImage builds a container image from the build configuration.
// Returns the image tag.
func buildImage(ctx context.Context, executor Executor, p buildParams) (string, error) {
	configDir := filepath.Dir(p.ConfigPath)
	dockerfilePath := filepath.Join(configDir, p.Build.Dockerfile)
	imageTag := "confine-ai-" + sanitizeImageTag(filepath.Base(p.WorkspaceFolder)) + ":latest"

	args := []string{
		"build",
		"-f", dockerfilePath,
		"-t", imageTag,
	}

	// Build args sorted for determinism.
	args = appendSortedMap(args, "--build-arg", p.Build.Args)

	// Build context: defaults to config directory.
	buildContext := configDir
	if p.Build.Context != "" {
		buildContext = filepath.Join(configDir, p.Build.Context)
	}

	// Verify Dockerfile and build context do not escape both the config
	// directory and the workspace. Paths may legitimately live outside one
	// of these (assistant configs live outside the workspace; "context: .."
	// references the workspace root from .devcontainer/). Traversal to an
	// unrelated directory must fail both checks.
	if err := containedWithinAny(dockerfilePath, configDir, p.WorkspaceFolder); err != nil {
		return "", fmt.Errorf("dockerfile path escapes workspace: %w", err)
	}
	if err := containedWithinAny(buildContext, configDir, p.WorkspaceFolder); err != nil {
		return "", fmt.Errorf("build context escapes workspace: %w", err)
	}

	args = append(args, buildContext)

	if err := executor.Run(ctx, p.Stderr, p.Stderr, args...); err != nil {
		return "", fmt.Errorf("build image: %w", err)
	}

	return imageTag, nil
}

// StopAndRemove stops and removes a container by ID. Used by Up
// (replacing containers with changed config), Down (removing all
// workspace containers), and the assistant reconnect path (recreating
// on config-hash mismatch).
func StopAndRemove(ctx context.Context, executor Executor, containerID string) error {
	if err := executor.Run(ctx, nil, nil, "stop", containerID); err != nil {
		return fmt.Errorf("stop container: %w", err)
	}
	if err := executor.Run(ctx, nil, nil, "rm", containerID); err != nil {
		return fmt.Errorf("remove container: %w", err)
	}
	return nil
}

// InspectConfigHash reads the config hash label from an existing container.
// Returns an empty string if the label is not set (e.g., container created
// by a different tool).
func InspectConfigHash(ctx context.Context, executor Executor, containerID string) (string, error) {
	formatArg := `{{index .Config.Labels "` + labelConfigHash + `"}}`
	output, err := executor.Output(ctx, "inspect", "--format", formatArg, containerID)
	if err != nil {
		return "", fmt.Errorf("inspect config hash: %w", err)
	}
	return strings.TrimSpace(output), nil
}

// workspaceMount returns the Docker CLI mount string for the workspace bind
// mount. If containerPath is empty, it defaults to /workspaces/<basename>.
// Returns an error if either path contains characters that would inject extra
// mount options (commas or equals signs).
func workspaceMount(hostPath, containerPath string) (string, error) {
	if containerPath == "" {
		containerPath = "/workspaces/" + filepath.Base(hostPath)
	}
	if strings.ContainsAny(hostPath, ",=") {
		return "", fmt.Errorf("workspace host path contains invalid characters: %q", hostPath)
	}
	if strings.ContainsAny(containerPath, ",=") {
		return "", fmt.Errorf("workspace container path contains invalid characters: %q", containerPath)
	}
	return "type=bind,source=" + hostPath + ",target=" + containerPath, nil
}

// ConfigHashWithFolders computes a deterministic SHA-256 hex digest of the
// resolved config including additional folder paths. When additionalFolders
// is nil or empty, the hash is identical to configHash (backward compatible).
func ConfigHashWithFolders(cfg config.Config, additionalFolders []string) string {
	h := sha256.New()

	data := canonicalConfigBytes(cfg)
	h.Write(data)

	// Append additional folders after existing content. When empty/nil,
	// no extra content is written, preserving the existing hash.
	for _, f := range additionalFolders {
		h.Write([]byte(f))
		h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil))
}

// canonicalConfigBytes produces a deterministic byte representation of the
// config by sorting all map keys before serialization. Null bytes (\x00)
// separate fields to prevent value concatenation from producing collisions.
// This is safe because JSON string values cannot contain literal null bytes.
func canonicalConfigBytes(cfg config.Config) []byte {
	var b strings.Builder

	b.WriteString(cfg.Name)
	b.WriteByte(0)
	b.WriteString(cfg.Image)
	b.WriteByte(0)
	b.WriteString(cfg.WorkspaceFolder)
	b.WriteByte(0)
	b.WriteString(cfg.RemoteUser)
	b.WriteByte(0)
	b.WriteString(cfg.ContainerUser)
	b.WriteByte(0)

	// Build section.
	if cfg.Build != nil {
		b.WriteString(cfg.Build.Dockerfile)
		b.WriteByte(0)
		b.WriteString(cfg.Build.Context)
		b.WriteByte(0)
		writeSortedMap(&b, cfg.Build.Args)
	}
	b.WriteByte(0)

	// Customizations.
	if cfg.Customizations != nil {
		b.WriteString(cfg.Customizations.Memory)
		b.WriteByte(0)
		b.WriteString(cfg.Customizations.CPUs)
		b.WriteByte(0)
	}
	b.WriteByte(0)

	// Mounts (already ordered by slice position).
	for _, m := range cfg.Mounts {
		b.WriteString(m)
		b.WriteByte(0)
	}
	b.WriteByte(0)

	// ContainerEnv sorted by key.
	writeSortedMap(&b, cfg.ContainerEnv)

	return []byte(b.String())
}

// sanitizeImageTag replaces characters invalid in Docker image tag components
// with hyphens. Docker tags allow [a-zA-Z0-9._-].
func sanitizeImageTag(s string) string {
	result := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '-'
	}, s)
	if result == "" {
		return "unnamed"
	}
	return result
}

// containedWithin checks that path is a descendant of root. Both paths
// must be absolute or both relative to the same base.
func containedWithin(path, root string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path %q is outside %q", path, root)
	}
	return nil
}

// containedWithinAny checks that path is a descendant of at least one of the
// provided roots. Returns the error from the last root if none match.
func containedWithinAny(path string, roots ...string) error {
	var lastErr error
	for _, root := range roots {
		err := containedWithin(path, root)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return lastErr
}

// appendSortedMap appends flag key=value pairs from m to args, sorted by key.
// Returns args unchanged if m is empty.
func appendSortedMap(args []string, argFlag string, m map[string]string) []string {
	for _, k := range slices.Sorted(maps.Keys(m)) {
		args = append(args, argFlag, k+"="+m[k])
	}
	return args
}

// writeSortedMap writes map entries to b sorted by key, with null byte
// separators between key-value pairs.
func writeSortedMap(b *strings.Builder, m map[string]string) {
	for _, k := range slices.Sorted(maps.Keys(m)) {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m[k])
		b.WriteByte(0)
	}
}
