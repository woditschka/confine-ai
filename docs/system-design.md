# System Design

confine-ai is a single-binary launcher for assistant-isolation containers built from a user-owned Dockerfile. Its architecture is deliberately thin: `main.go` owns command dispatch, `internal/` packages own single-responsibility domains (container lifecycle, config parsing, assistant management, update orchestration, runtime detection), and the source code is authoritative for every type, interface, and constant. This document describes the structure and invariants that hold across that code — package boundaries, type contracts, cross-cutting patterns, and the guardrails each feature must honor — so that implementers and reviewers can reason about the whole system without reading every file.

The document is organized in layers. The Package Structure diagram below is the Level 1 map. Types, Interfaces, and the per-feature sections (CLI Command Dispatch, Update Command) are Level 2 navigation into each subsystem. Per-type and per-function detail blocks are Level 3 and state design contracts rather than narrating control flow: the source code is authoritative for sequencing, variable names, and helper call chains. The Implementation Order table at the end is Level 4. Requirements live in [`prd.md`](prd.md); design trade-offs live in [`adr/`](adr/); this document is the bridge between the two.

## Package Structure

```text
.
├── main.go                  # Entry point, global flag parsing, dispatch switch (REQ-CL-001, REQ-CL-002, REQ-CL-004)
├── embed.go                 # //go:embed of base and assistant sample Dockerfiles (root-scoped)
├── cmd/update-samples/      # Developer tool: probe upstreams and rewrite samples/base/Dockerfile (not installed)
├── internal/                # Internal packages
│   ├── assistant/           # Assistant name validation, config paths, template embedding, base image (REQ-AS-*)
│   ├── cli/                 # Subcommand handlers (RunRm, RunInit, RunUpdate, RunStatus, RunCompletion, RunComplete, RunAssistant) and CLI-layer helpers: flag parsing, folder argument parsing, confirmation prompts, TTY detection (REQ-CL-*, REQ-MF-001)
│   ├── completion/          # Shell completion scripts and suggestion logic (REQ-SC-001, REQ-SC-002)
│   ├── config/              # Configuration discovery, parsing, variable substitution, and the LoadFromWorkspace pipeline (REQ-CF-*, REQ-VS-*)
│   ├── container/           # Container identification and lifecycle operations (REQ-CO-*, REQ-AS-004)
│   ├── gitenv/              # Host git identity reader and env merger for assistant shortcut (REQ-CL-003)
│   ├── runtime/             # Container runtime detection (REQ-RT-001)
│   ├── sanitize/            # Shared text sanitization helpers (control character removal)
│   └── update/              # Upstream probes, Dockerfile rewrite, per-assistant gate (REQ-AS-008)
```

`main.go` is a thin dispatcher: it parses global flags, resolves the workspace folder, and routes each subcommand to an `internal/cli` entry point. Because `//go:embed` directives cannot cross the module root, embedded sample Dockerfile bytes live in `embed.go` at the repository root and are passed into `internal/cli` handlers as explicit parameters. `internal/gitenv` takes plain `map[string]string` environments so it stays free of a dependency on `internal/config`.

## Constants and Package Variables

Tunable values, label names, regex patterns, and deny lists live next to their owning code. Security-relevant values (mount deny list, hostname pattern, shell-metacharacter blocklist, config size cap, assistant name pattern, base image tag) are cited by name in the [Threat Model](#threat-model) below. See `internal/config/`, `internal/container/`, `internal/assistant/`, `internal/runtime/`.

## Types

<!-- Summarize Go types here. Source code is authoritative; this describes the design contract. -->

### RawConfig

Holds the raw JSON content of a parsed devcontainer.json after JSONC extensions are stripped. Carries source-file provenance for error messages and bridges parse (REQ-CF-002) to load (REQ-CF-003). See `internal/config/parse.go`.

**Implements:** REQ-CF-002

### Config

Typed representation of a devcontainer.json file's supported fields, produced by `Load(RawConfig)`. Zero values mean absent. See `internal/config/load.go`.

**Implements:** REQ-CF-003

### Customizations

Parsed `customizations.confine-ai` settings. Nil when absent. Other `customizations` namespaces are reported by `UnsupportedCustomizations`. See `internal/config/load.go`.

**Implements:** REQ-CF-003, REQ-RL-001

### ResourceLimits

Validated memory and CPU limit strings ready for the runtime CLI. Produced by `ResolveResourceLimits` and passed through `UpOptions` into container creation. See `internal/config/resource.go`.

**Implements:** REQ-RL-001

### ResolveResourceLimits

Produces a validated `ResourceLimits` value from `customizations.confine-ai`. Returns a validation error if either memory (Docker unit format) or CPU (finite positive decimal, rejecting zero, negative, infinity, and NaN) fails its format check. See `internal/config/resource.go`.

**Implements:** REQ-RL-001

### ValidateMemory

Validates a memory limit string against Docker's memory format.

See `internal/config/resource.go`.

### ValidateCPUs

Validates a CPU limit string as a finite positive decimal number. Rejects zero, negative, infinity, and NaN.

See `internal/config/resource.go`.

### UnsupportedCustomizations

Inspects the `customizations` field in raw JSON and returns unsupported namespace names (e.g., `"customizations.vscode"`). The `confine-ai` namespace is recognized and excluded from the result. Returns nil if no unsupported namespaces exist.

**Implements:** REQ-CF-004

See `internal/config/load.go`.

### Build

Build configuration for a custom container image, nested inside `Config`. See `internal/config/load.go`.

<a id="supportedfields"></a>
### SupportedFields

Detects top-level JSON fields present in the `supportedFields` set. Returns field names sorted alphabetically, or nil if no supported fields are present. Returns trusted map keys, not raw JSON keys.

See `internal/config/load.go`.

### UnsupportedFields

Detects top-level JSON fields not in the `supportedFields` set. Returns field names sorted alphabetically, or nil if all fields are supported. Field names are sanitized to remove control characters before returning.

**Implements:** REQ-CF-004

See `internal/config/load.go`.

### Substitute

Resolves `${...}` variable patterns in all string fields of a `Config`, producing a new `Config` without mutating the input. Sits at the tail of the config pipeline: `Find -> Parse -> Load -> Substitute`. Supports `localEnv` (with optional default), `localWorkspaceFolder`, `localWorkspaceFolderBasename`, and `devcontainerId` (lowercase hex SHA-256 of the workspace path). Unresolvable `localEnv` without default, unknown patterns, and unclosed `${` references all return errors. Map keys are not substituted and there is no recursive expansion. See `internal/config/vars.go`.

**Implements:** REQ-VS-001

### Runtime

Immutable value object identifying a detected container runtime CLI (name and absolute path). Produced by `Detect`. This is deliberately not an interface — the runtime interface is defined at the first consumer site. See `internal/runtime/detect.go`.

**Implements:** REQ-RT-001

### Detect

Finds a Docker-compatible container runtime on the host by searching PATH in priority order (`docker`, then `podman`), or by honoring an explicit path from `--docker-path`. Errors if no runtime is found, if an explicit path is missing, non-executable, or has an unrecognized basename. Accepts an injected `LookPathFunc` seam for testability. See `internal/runtime/detect.go`.

**Implements:** REQ-RT-001

### Labels

Holds the set of container labels for a folder set. Created by `NewLabels(folderSet)` where `folderSet` is a slice of one or more absolute paths. Provides `ForArgs()` for `docker run` and `FilterArgs()` for `docker ps` queries. The primary query key is `devcontainer.metadata_id`, a SHA-256 hex digest of the sorted absolute folder paths joined by newlines. The `devcontainer.local_folder` label stores the individual folder paths (newline-separated) for display by `confine-ai status` (REQ-CO-001 AC 6).

**Identity computation:** The `folderSetID` function sorts the input paths lexicographically, joins them with newline separators, and computes the SHA-256 hex digest. Sorting ensures argument-order independence: `NewLabels([]string{"/B", "/A"})` and `NewLabels([]string{"/A", "/B"})` produce the same `metadata_id`. A single-folder invocation passes a one-element slice, preserving backward compatibility with the hash value (the sorted join of one path equals the path itself, but the newline suffix changes the hash, so existing single-folder containers are not found by the new scheme --- see [ADR: Folder-Set Container Identity](adr/2026-04-12-folder-set-container-identity.md)).

**Implements:** REQ-CO-001

See `internal/container/labels.go`.

### Container

Holds the identity of a container found by a label query. Contains the container ID as a hex string.

**Implements:** REQ-CO-001

See `internal/container/container.go`.

### FindByLabels

Queries the container runtime for containers matching folder-set labels. Accepts a folder set (one or more absolute paths). Uses `docker ps --all` to include stopped containers. Returns all matching containers. An empty result is not an error.

**Implements:** REQ-CO-001

See `internal/container/find.go`.

### ExitError

Error type that carries a process exit code through the error chain. Returned by `ExecInteractive` on a non-zero container command exit; extracted by `main()` via `errors.As` so the assistant shortcut forwards the exact code. See `internal/container/exec.go`.

**Implements:** REQ-AS-002

<a id="upoptions"></a>
### UpOptions

Value object carrying every parameter `container.Up` needs: the primary workspace path and resolved config, any additional bind-mount folders, network and mount policy flags, resolved resource limits, the assistant label set (`NewAssistantLabels` with folder set), and injected seams (`ResolveSymlinks`, `ConfirmFunc`) for testability. The `Labels` field carries the folder-set identity computed by `NewLabels` or `NewAssistantLabels`. See `internal/container/up.go`.

**Implements:** REQ-AS-002, REQ-CO-008, REQ-CO-010, REQ-CL-003, REQ-NR-001, REQ-RL-001, REQ-MF-001

### MountRisk

Immutable value object describing a single mount safety finding: source path, tier (1 blocked unconditionally, 2 risky and requiring `--allow-risky-mounts`), and a human-readable reason. Returned by `ValidateMounts` and `probeWorkspaceRisks`. See `internal/container/mount_safety.go`.

**Implements:** REQ-CO-008

### ValidateMounts

Classifies mount source paths into tier 1 (blocked) and tier 2 (risky) findings. Resolves symlinks via an injected resolver before classification so a symlink cannot bypass the deny list; unresolvable paths (broken symlinks, permission errors) are treated as tier 1. Tier 1 covers the root, system directories, runtime sockets, and the home directory and its parents; tier 2 covers sensitive-filename segments (`.ssh`, `.gnupg`, `.aws`, `.env`, `credentials`) and broad directory sources (`/opt`, `/usr/local`). The caller (`Up`) combines these findings with `probeWorkspaceRisks` before enforcing policy. See `internal/container/mount_safety.go`.

**Implements:** REQ-CO-008

### probeWorkspaceRisks

Stats a bounded set of sensitive filenames as direct children of the workspace folder and returns tier 2 findings for any matches. Complements `ValidateMounts` (which classifies declared mount sources) by catching sensitive content inside the implicit workspace mount. See `internal/container/mount_safety.go`.

**Implements:** REQ-CO-008

### UpResult

Outcome value for the `up` operation. Carries the container ID and resolved user identity consumed by the assistant shortcut handler after the container is started. See `internal/container/up.go`.

**Implements:** REQ-AS-002

### ExecInteractive

Executes a command inside a running container. Takes the container ID directly (no workspace lookup), wires stdin, and sets `-i -t` when TTY allocation is requested. Returns `*ExitError` on non-zero command exit and a plain error on infrastructure failure. Used by the assistant shortcut handler after `Up` returns or a reuse lookup succeeds. See `internal/container/exec.go`.

**Implements:** REQ-AS-002

### DownResult

Outcome value for the `confine-ai rm` command, carrying the list of removed container IDs. See `internal/container/down.go`.

**Implements:** REQ-CO-004

<a id="rm"></a>
<a id="down"></a>
### Down

`Down` is the library entry point behind `confine-ai rm`. The verb `down` is retained in source (function name, file name) because the operation removes every container for a folder set regardless of assistant --- a broader semantics than the single-target `DownAssistant`. The CLI exposes both through the `rm` subcommand.

Stops and removes all containers for a folder set. Locates containers via `FindByLabels` (including stopped), stops any running ones, and removes them all. An empty result is not an error. Warns to stderr when multiple containers match the same folder set. See `internal/container/down.go`.

**Implements:** REQ-CO-004

<a id="gateway-blocking"></a>
### Gateway Blocking

Blocks outbound traffic from the container to the host machine. Applied after container creation via `docker exec` with iptables DROP rules on the OUTPUT chain. Requires `NET_ADMIN` capability.

Per [ADR: Gateway Blocking via iptables](adr/2026-04-12-gateway-blocking-via-iptables.md), the mechanism uses iptables via `docker exec` after container start.

**Implements:** REQ-CO-009 (AC 5-7)

See `internal/container/firewall.go`.

**Sequence:** After `createContainer` or `docker start` (reuse path), `setupFirewall` detects the gateway IP by running `network inspect` (without `--format`) and parsing the JSON output. The parser handles both Docker format (`.IPAM.Config[].Gateway`) and Podman format (`.subnets[].gateway`). It then resolves `host.docker.internal` via `docker exec getent hosts` and applies iptables DROP rules for all discovered host IPs.

**When applied:**

| Network Value | Gateway Blocking | Allowlist (if `--allowed-hosts`) | NET_ADMIN Added |
|---------------|-----------------|----------------------------------|-----------------|
| `bridge` | Yes | Yes | Yes |
| `<named>` | Yes | Yes | Yes |
| `none` | No | N/A (rejected with `--allowed-hosts`) | No |
| `host` | N/A (rejected) | N/A (rejected) | N/A |

**Fail-secure behavior:** If firewall rules cannot be applied (missing iptables, executor failure), the container is removed. No container runs without intended network restrictions.

**IP validation:** Gateway IPs are extracted from JSON via `parseGatewayIP`, which parses a single unified struct covering both Docker (`.IPAM.Config[].Gateway`) and Podman (`.subnets[].gateway`) schemas and returns the first non-empty gateway string. All IP addresses are then validated with `net.ParseIP` before use. Invalid IPs cause a hard error. Each iptables rule is applied via a separate `docker exec` call with the IP as a discrete argument, avoiding shell interpretation.

<a id="outbound-allowlist"></a>
### Outbound Allowlist

Restricts container outbound network access to a declared list of hosts. Applied via the same `setupFirewall` orchestration as gateway blocking, using iptables OUTPUT chain rules. Requires `NET_ADMIN` capability (already granted by REQ-CO-009).

Per [ADR: Outbound Network Allowlist via iptables](adr/2026-04-12-outbound-network-allowlist-via-iptables.md), the mechanism reuses the existing iptables infrastructure.

**Implements:** REQ-NR-001

See `internal/container/firewall.go`.

**Activation:** When `UpOptions.AllowedHosts` is non-empty. When empty, only gateway blocking applies (REQ-CO-009 behavior).

**OUTPUT chain invariant:** The chain is default-deny (`-P OUTPUT DROP`) with exceptions for loopback, established/related conntrack, DNS (UDP and TCP port 53), and each resolved allowlist IP. Gateway and `host.docker.internal` IPs are inserted at the top as explicit DROP rules. `ip6tables` policy is DROP, so IPv6 outbound is unconditionally blocked when an allowlist is active. The exact rule order and iptables invocation sequence live in `internal/container/firewall.go` — source is authoritative. Each rule is applied through a separate `docker exec` call with discrete arguments so no shell interpretation is involved. The DROP policy is set before the ACCEPT exceptions are added to close the TOCTOU window between container start and full rule installation.

**Hostname resolution:** `getent ahostsv4 <hostname>` inside the container via `docker exec`. Returns IPv4 addresses only. All returned IPs are added as ACCEPT rules. Resolution happens once at setup time. Failure to resolve any hostname triggers fail-secure container removal.

**Input validation:** Each `--allowed-hosts` entry must be a valid hostname (`^[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?$`) or a valid IPv4/IPv6 address (`net.ParseIP`). Wildcards, CIDR ranges, and shell metacharacters are rejected.

**Conflict:** `--allowed-hosts` with `--network none` returns an error. The `none` network disables networking entirely, making an allowlist meaningless.

**Fail-secure behavior:** If allowlist rules cannot be applied (missing iptables, executor failure, hostname resolution failure), the container is removed. Same pattern as gateway blocking.

### ValidateName

Validates an assistant name string against the `assistantNamePattern` regex and length constraints. Returns an error if the name is invalid. The validation prevents path traversal (no dots or slashes in the allowed character set), collision with single-character CLI flags, and overly long labels.

**Implements:** REQ-AS-001

See `internal/assistant/assistant.go`.

### ConfigPath

Returns the absolute path to an assistant's `devcontainer.json`: `<homeDir>/.confine-ai/assistants/<name>/devcontainer.json`. The name must pass `ValidateName` before calling this function.

**Implements:** REQ-AS-001

See `internal/assistant/assistant.go`.

### DataPath

Returns the absolute path to an assistant's persistent data directory: `<homeDir>/.confine-ai/data/<name>/`. Used as a bind mount source for credential storage.

**Implements:** REQ-AS-001

See `internal/assistant/assistant.go`.

### Init

Scaffolds assistant configuration from built-in templates. For known assistant names (`claude`, `copilot`, `opencode`), uses assistant-specific Dockerfiles embedded from `samples/`. For unknown names, generates a generic template. The `devcontainer.json` is generated (not embedded) with the correct `~/.confine-ai/data/<name>/` mount path. Known assistants may additionally declare **extra file mounts** (`knownAssistantExtraFiles`), sourced from within `~/.confine-ai/data/<name>/` and seeded by `EnsureExtraFiles`, and **host mounts** (`knownAssistantHostMounts`), sourced from live host paths outside confine-ai's data tree via `${localEnv:HOME}` expansion and never seeded. At launch, `opencode` uses a host mount to pass through `~/.local/share/opencode` for authentication state and a seed-only extra file for `opencode.json` (no separate bind mount — visible through the data-directory mount); `claude` uses an extra file mount for `claude.json`; `copilot` uses neither.

The overwrite policy is not implemented in this library function — it is implemented by the CLI wrapper in `internal/cli/init.go`, which inspects `-y`/stdin TTY state and decides whether to call `os.RemoveAll` on an existing target before delegating to the library seeding call. The library `Init` function itself refuses to overwrite: callers are responsible for removing the target first if the user opted into an overwrite.

**Implements:** REQ-AS-003

See `internal/assistant/init.go`.

<a id="init-overwrite-flow"></a>
### Init Overwrite Flow

`confine-ai init` may have to seed into a directory that already contains a previous install. The overwrite decision is a CLI-layer concern, not a library concern: the library seeders are strictly non-overwriting, and a dedicated CLI helper decides whether to remove the existing target before delegating. This split keeps the library callable from tests and from future non-interactive contexts without duplicating the prompt logic.

**Design invariants:**

- **Two targets, fixed order.** The base Dockerfile is seeded before the assistant directory so that a base-seed failure (disk full, permission denied) halts the command before any assistant directory is touched.
- **Three overwrite modes.** `-y` / `--yes` forces overwrite; an interactive TTY prompts and defaults to yes on empty input; a non-interactive run without `-y` reports "already present" and exits cleanly with no modification.
- **Remove-then-seed, never overwrite-in-place.** When the user opts into overwrite, the CLI helper removes the existing target and calls the library seeder on the empty slot. The library seeders themselves refuse to overwrite, so a caller that forgets the removal step gets a deterministic error rather than a silent clobber.
- **Credential survival.** `~/.confine-ai/data/<name>/` is created on first install and is never touched by any subsequent `confine-ai init`, regardless of flags or mode. Resetting assistant state requires a manual `rm -rf` outside confine-ai. This is the contract REQ-AS-003 promises and the test suite guards it.

**Implements:** REQ-AS-003, REQ-AS-006. See `internal/cli/init.go` for the CLI helpers and `internal/assistant/init.go` for the non-overwriting library seeders.

<a id="ensurebaseimage"></a>
### EnsureBaseImage

Checks whether the base image (`localhost/confine-ai-base:latest`) exists in the local container runtime. If missing, builds it from the resolved base Dockerfile bytes (see `ResolveBaseDockerfile`). If present, returns without action.

**Implements:** REQ-AS-006

See `internal/assistant/base.go`.

### BuildBaseImage

Builds the base image unconditionally. Writes the provided Dockerfile bytes to a temporary directory, runs the build, and cleans up. Callers obtain the bytes from `ResolveBaseDockerfile` so the user-owned copy at `~/.confine-ai/base/Dockerfile` takes precedence over the embedded seed. Accepts optional build arguments for version overrides.

**Implements:** REQ-AS-006

See `internal/assistant/base.go`.

<a id="ensureassistantimage"></a>
### EnsureAssistantImage

Checks whether the assistant image (`confine-ai-assistant-<name>:latest`) exists in the local container runtime. If missing, builds it from `~/.confine-ai/assistants/<name>/Dockerfile` with the container runtime's layer cache honored (no `--no-cache`). If present, returns without action. Emits a single stderr breadcrumb on the absent-image path, parallel to `EnsureBaseImage`. Invoked only by the assistant shortcut (REQ-AS-002); `confine-ai update <assistant>` (REQ-AS-008) uses `BuildAssistantImage` directly with `--no-cache`.

**Implements:** REQ-AS-002 (ACs 15-19), REQ-AS-006

See `internal/assistant/assistant_build.go`.

### BuildAssistantImage

Builds the assistant image from the assistant's user-owned Dockerfile at `~/.confine-ai/assistants/<name>/Dockerfile`. Accepts a cache-control option so callers choose between a cached build (shortcut auto-ensure) and a `--no-cache` build (`confine-ai update <assistant>`). Does not pass `--pull` because the `FROM` reference `localhost/confine-ai-base:latest` has no remote source. The helper reads no fields from the assistant's `devcontainer.json` — in particular it never interprets `build.args`, `build.context`, or `build.dockerfile`. This is the fixed-Dockerfile invariant: both writers of `confine-ai-assistant-<name>:latest` produce byte-identical images by construction.

**Implements:** REQ-AS-002 (ACs 18-19), REQ-AS-008 (ACs 42-43)

See `internal/assistant/assistant_build.go`.

<a id="assistant-image-lifecycle"></a>
### Assistant Image Lifecycle

The assistant image `confine-ai-assistant-<name>:latest` has a single-owner model. `confine-ai update <assistant>` is the sole refresh writer. The assistant shortcut `confine-ai <name>` is the sole consumer. The shortcut's first-use auto-ensure is the only other writer, and it is absence-only.

| Path | Role | Writer? | Cache policy |
|------|------|---------|--------------|
| `confine-ai update <assistant>` | Refresh writer | Yes | `--no-cache` (the only cache-bust path) |
| `confine-ai <name>` (image present) | Consumer | No | N/A |
| `confine-ai <name>` (image absent, first use) | Auto-ensure writer | Yes (absence-only) | Cached (layer cache honored) |

The shortcut overrides `cfg.Image = assistant.AssistantImageTag(name)` and clears `cfg.Build` before calling `container.Up`, so the generic `buildImage` branch in `internal/container/up.go` is not reached on the assistant path. The generic `buildImage` continues to serve regular devcontainer workspaces with the workspace-basename tag convention; the assistant shortcut bypasses it entirely. `build.args`, `build.context`, and `build.dockerfile` in the assistant's `devcontainer.json` are ignored on both writer paths — both call `BuildAssistantImage`, which builds from the fixed Dockerfile path and passes no build args.

**Implements:** REQ-AS-002 (ACs 12-19), REQ-AS-006, REQ-AS-008 (ACs 40-43)

See [ADR: Assistant Image Tag Single-Owner Model](adr/2026-04-17-assistant-image-tag-single-owner.md).

### Layout Helpers

`internal/assistant/assistant.go` is the single source of truth for on-disk layout. All production code resolves confine-ai paths through these helpers; no caller constructs `.confine-ai/...` path fragments by hand.

| Helper | Returns |
|---|---|
| `AssistantsDir(homeDir)` | `~/.confine-ai/assistants` |
| `Dir(homeDir, name)` | `~/.confine-ai/assistants/<name>` |
| `DockerfilePath(homeDir, name)` | `~/.confine-ai/assistants/<name>/Dockerfile` |
| `ConfigPath(homeDir, name)` | `~/.confine-ai/assistants/<name>/devcontainer.json` |
| `DataPath(homeDir, name)` | `~/.confine-ai/data/<name>` |
| `BaseDockerfilePath(homeDir)` | `~/.confine-ai/base/Dockerfile` |

All helpers are pure path joins and perform no I/O. Callers must validate `name` via `ValidateName` beforehand.

**Implements:** REQ-AS-006

See `internal/assistant/assistant.go`.

### SeedBaseDockerfile

Writes the embedded seed Dockerfile to `~/.confine-ai/base/Dockerfile` if the file does not exist. Creates `~/.confine-ai/base/` with mode `0o755` if missing. Writes the file with mode `0o644`. Never overwrites an existing file — the CLI wrapper `handleBaseDockerfile` handles the "delete then call this function" pattern when the user opts into an overwrite (see [Init Overwrite Flow](#init-overwrite-flow)). Returns `wrote=true` when the file was newly written, `wrote=false` when the file already existed; returns an error only on I/O failure.

**Implements:** REQ-AS-003, REQ-AS-006

See `internal/assistant/init.go`.

### ResolveBaseDockerfile

Returns the bytes of the base Dockerfile to build from. Reads `~/.confine-ai/base/Dockerfile` when the file exists and returns the embedded seed when the file is absent. When `announceFallback` is true and the fallback is taken, writes an informational line to stderr. A present-but-unreadable user copy is a hard error (no silent fallback).

**Implements:** REQ-AS-006

See `internal/assistant/base.go`.

<a id="managed-dockerfile-markers"></a>
### Managed Dockerfile Markers

The embedded seed Dockerfile carries structured comment markers immediately preceding each line that `confine-ai update` rewrites. REQ-AS-006 defines the markers as part of the user-owned base Dockerfile contract; REQ-AS-008 parses them at runtime via `internal/update.ParseDockerfile` (see [Update Command](#update-command)).

**Grammar:**

```text
# confine-ai:managed tool=<tool> kind=<kind> [arch=<arch>] [distribution=<distribution>]
```

| Field | Required | Values | Purpose |
|---|---|---|---|
| `tool` | yes | `base-image`, `go`, `java` | Classification axis. A future update rule that is Java-specific fires only when `tool=java`. |
| `kind` | yes | `image`, `version`, `sha256` | Line role. `image` for `FROM`, `version` for version ARGs, `sha256` for checksum ARGs. |
| `arch` | on `kind=sha256` | `amd64`, `arm64` | Architecture for per-architecture sha256 ARGs. |
| `distribution` | on `tool=java` | `corretto` (future: `temurin`, `zulu`, `msopenjdk`, `liberica`) | Java distribution identity, readable without pattern-matching the download URL or the ARG name. |

Each marker sits on the line immediately preceding the line it classifies, with no blank line between them. Each managed line has exactly one marker. Ordinary Dockerfile comments that do not start with the `# confine-ai:managed` sentinel are ignored. Unknown tokens in a marker are reserved for forward compatibility.

**Implements:** REQ-AS-006, REQ-AS-008

See [ADR: Managed Dockerfile Classification via Comment Markers](adr/2026-04-12-managed-dockerfile-classification.md) and [Update Command](#update-command) for the parser that consumes these markers.

### ImageBuilder

Minimal interface for running container image operations (`Output` and `Run` wrappers over the runtime CLI). Defined at the consumer site in `internal/assistant/base.go` to avoid import coupling back into `internal/container/`; `CLIExecutor` from `internal/container/cli.go` is the production implementation.

**Implements:** REQ-AS-006

<a id="newassistantlabels"></a>
### NewAssistantLabels

Creates the label set for an assistant container. Accepts the assistant name and a folder set (one or more absolute paths). Includes folder-set labels from `NewLabels` plus the `devcontainer.assistant_name` label. Assistant containers carry three labels (metadata ID, local folder paths, assistant name); folder-set-only labels (two) are used for queries across the entire folder set.

**Implements:** REQ-AS-004

See `internal/container/labels.go`.

### ContainerInfo

Extended container metadata for the `status` command: ID, runtime status string, optional assistant name, and folder paths. The folder paths are extracted from the `devcontainer.local_folder` label, which stores all mounted folder paths (newline-separated). Returned by `FindAllManaged`. See `internal/container/find.go`.

**Implements:** REQ-AS-005

### FindByAssistant

Queries the container runtime for containers matching both the assistant-name label and the folder-set metadata ID. Accepts the assistant name and a folder set (one or more absolute paths). Returns containers for a specific (assistant, folder-set) pair.

**Implements:** REQ-AS-004

See `internal/container/find.go`.

### FindAllManaged

Queries the container runtime for all containers with confine-ai labels (any container with a `devcontainer.metadata_id` label). Returns `ContainerInfo` values with assistant name, workspace, and status extracted from labels and `docker ps` output.

**Implements:** REQ-AS-005

See `internal/container/find.go`.

### DownAssistant

Stops and removes the container for a specific (assistant, folder-set) pair. Uses `FindByAssistant` for targeted lookup. Complements the existing `Down` function which removes all containers for a folder set.

**Implements:** REQ-AS-004

See `internal/container/down.go`.

<a id="assistant-reconnect-config-hash"></a>
### Assistant Reconnect Config-Hash Validation

The `Up` path (container creation and reuse via `container.Up`) checks the config hash label and recreates the container when it differs. The assistant reconnect path in `cli.RunAssistant` must perform the same validation. When `FindByAssistant` returns an existing container for the (assistant, folder-set) pair, the handler reads the container's stored `devcontainer.config_hash` label via `inspectConfigHash` and compares it against the hash computed from the current configuration via `configHashWithFolders`. If the hashes match, the handler starts the container (if stopped) and execs in. If the hashes differ, the handler stops, removes, and recreates the container through `container.Up` before exec. A stderr message indicates recreation due to a configuration change.

This validation closes a gap where editing `devcontainer.json` (image, resource limits, mounts, allowed hosts) had no effect on the next `confine-ai <assistant>` invocation. Without this check, the reconnect path silently reuses a stale container. The `inspectConfigHash` and `configHashWithFolders` functions already exist for the `Up` path and are reused here.

**Implements:** REQ-AS-002 (AC 10, AC 11)

See `internal/cli/assistant.go` and `internal/container/up.go`.

<a id="interfaces"></a>
## Interfaces

### Executor

Seam for running container runtime CLI commands. The interface has `Output`, `Run`, and `RunInteractive` methods (the last wires stdin for TTY `exec`). Defined at the consumer site in `internal/container/container.go` per the Go style guide. The production implementation is `CLIExecutor` in `internal/container/cli.go`, which wraps `exec.CommandContext`.

**Implements:** REQ-CO-001

<a id="cli-command-dispatch"></a>
## CLI Command Dispatch

CLI dispatch is split across two layers. `main.go` owns the entry point, global flag parsing, workspace resolution, and a single switch that routes each subcommand to a `Run*` entry point in `internal/cli`. The subcommand handlers themselves — `RunRm`, `RunInit`, `RunUpdate`, `RunStatus`, `RunCompletion`, `RunComplete`, `RunAssistant` — and the CLI-layer helpers they share (flag parsing via `cli.ParseFlags`/`cli.IgnoreHelp`, positional folder argument parsing, interactive confirmation prompts, TTY detection, executor construction) live in `internal/cli`. Each handler takes `ctx`, `stdout`, `stderr`, the resolved workspace folder, the runtime path, and its own argument slice; embedded Dockerfile bytes are passed as parameters so `//go:embed` can stay at the repository root in `embed.go`.

The `run` function in `main.go` accepts explicit dependencies for testability: `args` (the argument slice), `stdout`/`stderr` writers, and a `getwd` function. The `config.LoadFromWorkspace` pipeline (in `internal/config/pipeline.go`) is the single entry point handlers use to go from a workspace folder and optional explicit config path to a resolved `Config`; it bundles discovery, parsing, typed load, warning emission, and variable substitution so no handler reimplements that sequence.

**Implements:** REQ-CL-001, REQ-CL-002, REQ-CL-005

See `main.go` and `internal/cli/`.

### Global Flags

Defined on a `flag.FlagSet` with `flag.ContinueOnError` to prevent `os.Exit` on parse errors. Flags are parsed before command dispatch.

| Flag | Variable | Default | Integration |
|------|----------|---------|-------------|
| `--workspace-folder` | string | `getwd()` | Resolved to absolute path via `filepath.Abs`. Passed to `container.NewLabels`. Overridden when positional folder args are present (REQ-CL-005). |
| `--docker-path` | string | `""` (empty = auto-detect) | Passed as `explicitPath` to `runtime.Detect`. |
| `--version` | bool | `false` | Prints version and exits. |

### Command Dispatch

After global flag parsing, the first positional argument is the command name. Known subcommands (`init`, `status`, `rm`, `update`, `completion`, `__complete`) dispatch to their `cli.Run*` handler. If the argument does not match a known subcommand, it is validated with `assistant.ValidateName` and dispatched to `cli.RunAssistant` (REQ-CL-004). If the assistant directory does not exist, an error is returned with an init suggestion.

| Command | Behavior | Implementation |
|---------|----------|----------------|
| `rm` | `cli.RunRm`: detects runtime, calls `container.Down` or `container.DownAssistant`, prints confirmation | REQ-CO-004, REQ-AS-004 |
| `rm <assistant>` | `cli.RunRm` with assistant name: targets the specific (assistant, workspace) container | REQ-AS-004 |
| `init` / `init -y` | `cli.RunInit` with no assistant name: writes `~/.confine-ai/base/Dockerfile` from the embedded seed (or prompts `Overwrite? [Y/n]` interactively, accepts `-y` unconditionally, leaves unchanged in non-interactive mode without `-y`). See [Init Overwrite Flow](#init-overwrite-flow). | REQ-AS-003, REQ-AS-006 |
| `init <assistant>` / `init -y <assistant>` | `cli.RunInit`: same base-step behavior, then scaffolds `~/.confine-ai/assistants/<name>/` from the built-in template with identical overwrite rules. `~/.confine-ai/data/<name>/` is created on first install and **never** touched on subsequent overwrites — credential survival contract (REQ-AS-003). | REQ-AS-003, REQ-AS-006 |
| `status` | `cli.RunStatus`: queries all confine-ai-managed containers, prints table | REQ-AS-005 |
| `update` | `cli.RunUpdate`: least-work walk of base and every scaffolded assistant. See [Update Command](#update-command). | REQ-AS-008 |
| `update base` | `cli.RunUpdate` with `base` target: probe upstreams, marker-driven rewrite of `~/.confine-ai/base/Dockerfile` if anything changed, rebuild with `--pull` only when rewrite happened, drop stale containers. | REQ-AS-008 |
| `update <assistant>` | `cli.RunUpdate` with assistant target: consult the assistant's least-work probe (REQ-AS-008); if installed version matches upstream, skip the rebuild; otherwise `podman build --no-cache` against the assistant Dockerfile (no `--pull`, since `localhost/confine-ai-base:latest` is a local-only FROM) and drop stale containers for that assistant. | REQ-AS-008 |
| `completion` | `cli.RunCompletion`: emits bash/zsh completion script | REQ-SC-001 |
| `__complete` | `cli.RunComplete`: hidden entry point used by the completion script to produce dynamic suggestions | REQ-SC-002 |
| `<assistant-name>` | `cli.RunAssistant`: resolves assistant config via `config.LoadFromWorkspace`, ensures base image, starts or reconnects with config-hash validation, TTY exec | REQ-AS-002, REQ-CL-004 |
| `<assistant-name> . ../A` | Assistant shortcut with multi-folder mounts. Folders before `--` are mount paths; args after `--` pass to assistant binary | REQ-MF-001, REQ-CL-005 |

### Positional Folder Arguments

**Implements:** REQ-MF-001, REQ-CL-005

The assistant shortcut accepts positional folder paths after its flags and before `--`. The first folder is the primary workspace (config discovery, container identity, shell working directory). Additional folders are mounted at `/workspaces/<basename>` inside the container.

**Resolution pipeline:**

1. Raw paths resolved to absolute via `filepath.Abs`.
2. Each path validated: must exist and be a directory.
3. Basename collision detection: if two paths share a `filepath.Base` value, the tool reports an error listing both paths and the shared basename.
4. When positional folders are present, the first folder overrides `--workspace-folder`.
5. Additional folders are set on `UpOptions.AdditionalFolders`.

Arguments before `--` are folder paths; arguments after `--` are assistant passthrough args. The folder-argument splitter lives in `internal/cli/args.go` and is shared by every handler that accepts positional folders.

**Interaction with `--workspace-folder`:** When positional folders are present, they take precedence. When no positional folders are given, `--workspace-folder` (or cwd) is the sole workspace.

### Assistant Shortcut Flags

The assistant shortcut dispatch owns its own `flag.FlagSet`, parsed inside `cli.RunAssistant` after the assistant name is resolved. The flags that the design cares about: `--remove-existing-container` and `--allow-risky-mounts` gate safety checks, and `--network` and repeatable `--allowed-hosts` drive firewall setup. Resource limits (memory, CPU) are sourced exclusively from the assistant's `devcontainer.json` under `customizations.confine-ai` (see REQ-RL-001); there are no CLI flags for limits on the shortcut. See `internal/cli/assistant.go`.

**Implements:** REQ-CL-003, REQ-NR-001, REQ-RL-001

<a id="output-ordering-invariant"></a>
### Output Ordering Invariant

Commands that dispatch to more than one target (today: `confine-ai update`; future: any walk-style command) must emit each target's user-visible output in natural per-target order: the target's inline progress, probe lines, and warnings followed immediately by that target's summary line, all before the next target begins. End-of-run batched summaries are prohibited because they interleave with inline output from earlier targets and surprise the user with a non-linear log (for example, a later assistant's probe line appearing before the base summary).

Concretely: dispatch loops write per-target results as they complete, not after the loop. Helpers that format a single result (e.g. `update.FormatResult`) exist for this purpose. Helpers that format a batch of results (e.g. `update.Aggregate`) are retained only for unit tests of the formatter and must not be used by the dispatch path.

### Entry Point Pattern

`main()` calls `main1()`, which creates a `context.Context` via `signal.NotifyContext` for graceful SIGINT cancellation, then calls `run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getwd)`. The context is threaded through all container operations (`Up`, `ExecInteractive`, `Down`) to interrupt in-progress runtime CLI calls on signal. If `run` returns nil, `main1()` returns 0. If `run` returns a `*container.ExitError`, `main1()` returns that error's code (so the assistant shortcut forwards the in-container process exit code). For all other errors, `main1()` prints to stderr and returns 1.

## Design Principles

These principles govern every implementation decision in confine-ai. They are listed in order of precedence: when two principles conflict, the earlier one takes priority.

- **Prefer the fewest moving parts that solve the problem.** Reuse existing adapters and infrastructure before adding new ones. Use a single registry driven by configuration rather than separate implementations per type. Every abstraction, adapter, or protocol is a maintenance commitment — add one only when no existing mechanism covers the need.
- **Minimize external dependencies.** See [Dependency Policy](#dependency-policy).

## Dependency Policy

Minimize external dependencies. Every dependency is an attack surface and a maintenance burden.

External dependencies must be in active use by Kubernetes (`k8s.io`, `sigs.k8s.io`) or Prometheus (`github.com/prometheus`). A dependency in active use by either ecosystem benefits from that project's security auditing, vulnerability response, and community review, so confine-ai inherits their vetting rather than performing its own.

<a id="approved-sources"></a>
### Approved Sources

| Source | Examples | Rationale |
|--------|----------|-----------|
| Go standard library | `net/http`, `encoding/json`, `log/slog` | Zero supply chain risk |
| `github.com/google/*` | `go-cmp` | Google-maintained, used by k8s and Prometheus |
| `golang.org/x/*` | `vuln`, `sync` | Go team extended stdlib, used by k8s and Prometheus |
| `gopkg.in/yaml.v3` | YAML parsing | De facto standard, used by k8s and Prometheus |

### Adding a New Dependency

Before adding a dependency, verify:

1. **Necessity** — Can the standard library solve the problem?
2. **Source** — Is the module listed in [Approved Sources](#approved-sources)? If not, create an ADR.
3. **Ecosystem use** — Is the module imported by Kubernetes or Prometheus? Check [pkg.go.dev](https://pkg.go.dev) "Imported By" tab.
4. **Audit** — Check `go list -m all` for transitive dependencies. Flag unknown modules.
5. **Verification** — Run `go mod verify` and commit `go.sum`.

### Prohibited

- Assertion libraries (`testify`, `gomega`) — use standard `if/t.Errorf`
- Logging frameworks (`zap`, `logrus`) — use `log/slog`
- HTTP routers (`gin`, `chi`, `mux`) — use `net/http` (Go 1.22+ routing)
- DI frameworks (`wire`, `dig`) — use constructor functions
- Mock generators (`mockgen`, `mockery`) — hand-write mocks at system boundaries
- Prometheus client (`github.com/prometheus/*`) — 40+ transitive deps; use `expvar` or OpenTelemetry stdlib bridge
- Kubernetes client (`k8s.io/*`, `sigs.k8s.io/*`) — 100+ transitive deps; if K8s integration is required, create an ADR justifying the attack surface

### Supply Chain Controls

| Control | Mechanism |
|---------|-----------|
| Checksum verification | `go.sum` committed, `go mod verify` in CI |
| Vulnerability scanning | `govulncheck` in `make security` |
| Dependency review | `go list -m all` reviewed on changes |
| Minimal transitive deps | Prefer stdlib; fewer deps = smaller attack surface |

## Threat Model

| Threat | Attack Vector | Mitigation |
|--------|--------------|------------|
| Oversized configuration file | User points `--config` at a large file, causing memory exhaustion | `maxConfigFileSize` (10 MB) enforced before reading file content |
| Symlink escape in config discovery | Symlink inside `.devcontainer/` resolves to a file outside the workspace, reading arbitrary host files | `Find` resolves all paths through `filepath.EvalSymlinks` and verifies containment within the workspace root. See `internal/config/find.go`. |
| Malicious JSON field names | Untrusted `devcontainer.json` contains field names with control characters, enabling log injection when displayed | `UnsupportedFields` sanitizes control characters before returning field names. See `internal/config/load.go`. |
| Mount argument injection | Mount object values containing commas or equals signs inject extra parameters into Docker CLI mount arguments | `mountObjectToString` rejects values containing `,` or `=`. See `internal/config/load.go`. |
| Workspace mount string injection | Workspace path containing commas or equals signs injects extra Docker mount options | `workspaceMount` rejects paths containing `,` or `=`. See `internal/container/up.go`. |
| Malformed image tag from workspace name | Workspace basename with special characters produces invalid Docker image tag | `sanitizeImageTag` replaces non-`[a-zA-Z0-9._-]` characters with hyphens. See `internal/container/up.go`. |
| Environment variable leakage via substitution | `${localEnv:SECRET}` resolves a secret into a config value visible via `docker inspect` or logs | Credential pattern warnings (REQ-CO-007) flag suspicious keys. Mount safety (REQ-CO-008) validates paths after substitution. `Substitute` does not log resolved values. Defense in depth: substitution resolves, downstream validators check. |
| Mount source symlink bypass | Symlink in mount source path resolves to a blocked path (e.g., `/home/user/link` -> `/`), bypassing the static deny list | `ValidateMounts` resolves symlinks via `filepath.EvalSymlinks` before tier classification. Unresolvable symlinks (broken, permissions) are blocked as tier 1. See `internal/container/mount_safety.go`. |
| Broad directory exposure via mounts | Mount source like `/opt` or `/usr/local` exposes more host data than needed for the development workflow | Tier 2 classification warns about broad directory mounts. Requires explicit `--allow-risky-mounts` flag to proceed. See `internal/container/mount_safety.go`. |
| Sensitive file exposure via workspace mount | Workspace directory contains `.env`, `.ssh/`, `.aws/`, `.gnupg/`, or `credentials` that would be visible inside the container | `probeWorkspaceRisks` checks for these files before container creation. Tier 2 warning requires `--allow-risky-mounts` to proceed. See `internal/container/mount_safety.go`. |
| Container reaches host via gateway IP | Process inside the container connects to the Docker gateway IP to access host services | iptables DROP rules on the OUTPUT chain block traffic to the gateway IP. Applied via `docker exec` after container creation. See `internal/container/firewall.go`. |
| Container reaches host via host.docker.internal | Process uses the `host.docker.internal` DNS name to bypass gateway IP blocking | `host.docker.internal` IP is resolved and blocked with an additional iptables DROP rule. If the name does not resolve, no rule is needed. See `internal/container/firewall.go`. |
| Command injection via gateway IP | Crafted `network inspect` JSON output contains shell metacharacters in the gateway field, injected into `docker exec` iptables command | Gateway IPs are extracted via JSON parsing (`parseGatewayIP`), then validated with `net.ParseIP` before use. Invalid IPs cause a hard error. JSON parsing provides structural defense; `net.ParseIP` provides value defense. See `internal/container/firewall.go`. |
| Firewall rules not applied | Container starts but iptables rules fail (missing binary, permission error), leaving the container without network restrictions | Fail-secure: container is removed if firewall setup fails. No container runs without intended restrictions. See `internal/container/firewall.go`. |
| Allowlist bypass via IPv6 | Process uses IPv6 to reach a host not covered by IPv4 iptables rules | `ip6tables -P OUTPUT DROP` blocks all IPv6 outbound when `--allowed-hosts` is specified. See `internal/container/firewall.go`. |
| Allowlist bypass via DNS tunneling | Process encodes data in DNS queries to exfiltrate information through the allowed DNS port | DNS (port 53) is allowed for hostname resolution. DNS tunneling is a known limitation. Mitigated by the trust boundary: the container runs a known tool (Claude Code) with declared endpoints. |
| Command injection via allowed-hosts | Crafted `--allowed-hosts` value contains shell metacharacters injected into `docker exec` iptables command | `ValidateAllowedHosts` rejects entries containing shell metacharacters, whitespace, wildcards, and CIDR notation. Only valid hostnames and IP addresses are accepted. See `internal/container/firewall.go`. |
| Allowlist hostname resolution to attacker IP | DNS response maps an allowed hostname to an attacker-controlled IP, granting outbound access to that IP | Resolution uses the container's DNS (Docker's embedded resolver). The risk is equivalent to the application itself resolving the hostname. The allowlist limits reachable IPs, not resolvable names. |
| Timing gap before allowlist policy | Between container start and OUTPUT DROP policy, outbound traffic is unrestricted | Container runs `sleep infinity`. No user process executes until `container.Up` returns to the assistant shortcut handler. The tool controls the sequence. |
| Path traversal via assistant name | Assistant name containing `../` escapes `~/.confine-ai/assistants/` to read or write arbitrary files | `ValidateName` restricts names to `[a-z0-9-]`. No dots, slashes, or path separators are allowed. See `internal/assistant/assistant.go`. |
| Symlink attack on assistant config directory | Attacker creates symlink at `~/.confine-ai/assistants/<name>/` pointing to a sensitive directory before `confine-ai init` writes files | `Init` checks if the directory exists before writing. If it exists (including as a symlink), it reports the directory exists and makes no changes. |
| Base image build context injection | Attacker-controlled content enters the base image build context | The build context contains only the resolved Dockerfile bytes (user copy at `~/.confine-ai/base/Dockerfile` or the embedded seed). Written to a temp directory created with `os.MkdirTemp` (unique, restricted permissions). The user owns and edits the user copy by design; no new trust boundary is introduced by REQ-AS-006. |
| User edits base Dockerfile to remove sha256 verification | User editing for a new Java distribution drops the `sha256sum -c` step, allowing an unverified archive into the base image | Scope: the user owns the file. The embedded seed ships with verification wired in and carries managed-line markers (see Managed Dockerfile Markers). A future `confine-ai update` will re-emit verification when it rewrites managed lines. Documentation notes the contract. |
| Fallback to embedded seed hides a broken user copy | User-owned Dockerfile exists but is unreadable (permission error); silent fallback would mask the problem | `ResolveBaseDockerfile` fails closed on a present-but-unreadable user copy. Fallback to the embedded seed only occurs when the file is absent. |
| Assistant config outside workspace | Assistant configs in `~/.confine-ai/` bypass the workspace containment check for `--config` | The assistant shortcut handler constructs the config path from a validated assistant name and the home directory. It does not use the `--config` flag path. The name validation prevents path traversal. |
| Resource exhaustion via untrusted config | Malicious `devcontainer.json` sets memory/CPU to values near zero, making the container non-functional | Validation rejects non-positive CPU values. The memory format is validated before passing to the runtime. Users control their own config files. |
| CLI flag injection via memory/cpus values | Crafted `--memory` value contains shell metacharacters | Values are validated against strict patterns (`memoryPattern` regex, `strconv.ParseFloat`) before use. Values are passed as discrete arguments to the container runtime CLI, not through shell interpretation. The existing command execution model (REQ-RT-001) provides structural defense. |
| Additional folder mount injection | Additional folder path contains commas or equals signs, injecting extra Docker mount options | Additional folder mount strings are constructed using the same `workspaceMount` function that validates the primary workspace path. Paths containing `,` or `=` are rejected before container creation. See `internal/container/up.go`. |
| Additional folder mount safety bypass | Additional folder is a blocked path (e.g., `/etc`) or a risky path (e.g., containing `.ssh/`) | Additional folders are converted to synthetic bind mount strings and passed through `ValidateMounts` alongside config mounts. `probeWorkspaceRisks` is also run on each additional folder. The same tier 1/tier 2 classification applies. See `internal/container/up.go`. |
| Basename collision mount override | Two additional folders with the same basename silently override each other's mount target | Basename collision is detected before container creation. The folder resolver builds a map from basename to paths and returns an error if any basename maps to multiple paths. See `internal/cli/args.go`. |

<a id="update-command"></a>
## Update Command

`confine-ai update` is the single least-work path for keeping confine-ai-managed container images current. It walks two kinds of targets behind one verb: a marker-driven base update that probes upstreams, rewrites `~/.confine-ai/base/Dockerfile` only when a value changed, and rebuilds `localhost/confine-ai-base:latest` only when the rewrite happened; and an assistant update that consults a per-assistant version probe, skips the rebuild when the installed CLI version already matches upstream, and otherwise re-runs the assistant's Dockerfile against the existing base image. Both paths share one invariant: the rebuild runs only when it is the least work needed to reach the user's intent, and every probe failure falls through to "rebuild anyway" so the gate never fails an update on its own.

### Assistant management lifecycle context

`confine-ai update` sits inside the assistant management mental model defined in `docs/prd.md` (the Overview subsection at the top of the Assistant management group):

- **Two images** per assistant container: `localhost/confine-ai-base:latest` (toolchain layer, shared) and `confine-ai-assistant-<name>:latest` (assistant layer, one per assistant; `FROM localhost/confine-ai-base:latest`).
- **Two directory trees** per assistant: `~/.confine-ai/assistants/<name>/` (config, safe to overwrite) and `~/.confine-ai/data/<name>/` (state, never touched by any confine-ai command).
- **Two file writers**: `confine-ai init` and `confine-ai update base`. `confine-ai update <assistant>` and the assistant shortcut write image layers only, never `~/.confine-ai/` files.
- **Single-owner assistant image tag.** `confine-ai update <assistant>` is the sole refresh writer of `confine-ai-assistant-<name>:latest` and the only `--no-cache` path. The assistant shortcut is the sole consumer; its first-use auto-ensure is an absence-only cached writer for the same tag. See [Assistant Image Lifecycle](#assistant-image-lifecycle) and [ADR: Assistant Image Tag Single-Owner Model](adr/2026-04-17-assistant-image-tag-single-owner.md).
- **Three commands**: `init`, `<assistant>` shortcut, `update`. `update` is the only one meant to be run routinely; the others handle install and recreate edge cases.

The update feature is implemented across:

- `internal/cli/update.go` — `RunUpdate` command dispatch and executor wiring.
- `internal/update/` — update package: marker parser, upstream probe transport for base versions, assistant version probe registry (`assistant_probe.go`), npm upstream adapter (`npm.go`), sha256 fetch and cross-verification, atomic rewrite strategy, orchestration for base and assistant targets.
- `internal/assistant/base.go` — reused `BuildBaseImage` for the post-rewrite rebuild.
- `internal/container/refresh.go` — reused `RemoveAllContainers` for the base stale-container drop and a new `RemoveContainersByAssistant` helper for the per-assistant stale-container drop.

**Implements:** REQ-AS-008

Dispatch and orchestration live in `internal/cli/update.go`. Parser, transport, rewrite, and classification live in `internal/update/`. The two layers are separated so the pure units (parser, upstream probe, rewrite) can be exercised without touching the container runtime.

<a id="update-command-dispatch"></a>
### Command Dispatch

The dispatch layer translates `confine-ai update [targets...] [--dry-run] [--yes]` into a sequence of per-target orchestrator calls and aggregates their results into a single exit code. It uses an injectable-executor pattern: a thin outer wrapper supplies a real executor factory, and an inner function handles flag parsing, target validation, and orchestration so tests can substitute a fake builder without touching the container runtime.

**Implements:** REQ-AS-008.

**Design invariants:**

- **Flag parsing and target validation happen before the executor is constructed.** `--help` and unknown-target errors must surface without requiring the container runtime to be present on the host.
- **Targets are resolved to a fixed order.** The no-arg walk is `base` first, then every subdirectory of `~/.confine-ai/assistants/` containing a `Dockerfile`, alphabetically. Explicit targets are dispatched in the order the user supplied them.
- **Per-target results, never a monolithic error.** Each orchestrator returns a `TargetResult` with its own exit code, action, and optional error. The dispatch layer aggregates these via `update.ExitCode` (highest severity wins) and returns the aggregate through an `*ExitError`-shaped wrapper so `main()` can forward it through the normal exit-code path.
- **Options struct, not globals.** `update.Options{DryRun, AutoYes, Stdin io.Reader}` is threaded through the orchestrators. `Stdin` is an `io.Reader` so tests can substitute a `strings.Reader` for the major-jump prompt.
- **Inline summaries, never batched.** Per-target output ordering is governed by the [Output Ordering Invariant](#output-ordering-invariant): a target's inline output and its summary stay adjacent in dispatch order.
- **`refresh` is removed, not aliased.** The `refresh` subcommand is gone from the command switch, from REQ-CL-004's known-subcommand set, and from REQ-SC-002's completion list. `confine-ai refresh` hits the "unknown command or assistant" branch and reports the updated help text.

| Command | Target resolution | Orchestrator |
|---|---|---|
| `confine-ai update` | No-arg walk | Base, then each assistant directory alphabetically |
| `confine-ai update base` | Literal `base` | Base orchestrator |
| `confine-ai update <assistant>` | Validated via `assistant.ValidateName` | Assistant orchestrator |
| `confine-ai update --dry-run` | Same as above | Orchestrators receive `Options.DryRun=true` |
| `confine-ai update --yes` | Same as above | Orchestrators receive `Options.AutoYes=true` |

<a id="update-marker-parser"></a>
### Marker Parser

The marker parser lives in `internal/update/parser.go`. REQ-AS-006 defines the markers; REQ-AS-008 reads them. The parser lives in `internal/update/` rather than in a dedicated `internal/dockerfile/` package because every consumer (classification, rewrite, orchestration) is in the same package, and the parser is intentionally limited to the marker subset — it is not a general Dockerfile parser.

**Implements:** REQ-AS-008

The parser operates on raw bytes, not on `bufio.Scanner` output, because REQ-AS-008 requires byte-identical preservation of non-managed lines (including CRLF endings and the trailing-newline-or-not state of the whole file) and `bufio.Scanner` silently normalizes both. The input is a `[]byte`; the output is a pure `*ParsedDockerfile` value that holds the classified line slice, the managed-group index, recoverable warnings, a multi-stage flag, and the trailing-newline flag. Exact field names and shapes live in `internal/update/parser.go`.

**Design constraints:**

- **Byte-identical preservation of non-managed content.** Every line the parser does not rewrite — including comments, blank lines, the `FROM` line, line-ending bytes (LF or CRLF), and the trailing-newline-or-not state of the whole file — must survive a round trip through parse and rewrite unchanged. This is why the parser operates on raw bytes rather than `bufio.Scanner`, which silently normalizes CRLF and loses trailing-newline state.
- **Strict marker-adjacency rule.** A `# confine-ai:managed` marker classifies the immediately following line, and only if that line is a `FROM` or an `ARG`. A blank line between a marker and the next non-blank line breaks adjacency and is reported as an orphan warning. This keeps the marker grammar unambiguous and prevents silent drift when users reformat the file.
- **Forward-compatible token extraction.** Unknown marker keys are preserved in a pass-through slot rather than rejected, so future additions to the grammar (per the classification ADR) do not break older `confine-ai update` binaries reading newer seeds.
- **Multi-stage detection is independent of markers.** The parser records every `FROM` line and sets a `MultiStage` flag when more than one is present. REQ-AS-008's edge-case table rejects multi-stage base Dockerfiles, so the parser must surface this regardless of marker state.
- **Managed-looking unmarked ARGs are a warning, not a classifier input.** An `ARG` that looks like a managed version or sha256 ARG but lacks a preceding marker is reported to stderr so the user notices drift, but it does not participate in classification. The lint pattern is deliberately loose and advisory.
- **Duplicate markers skip the group, not the file.** Two consecutive markers before the same managed line are reported and the affected group is dropped. Other groups in the same file continue to process normally.

**Unit-test seams:** the parser's only input is `[]byte` and its only output is a pure value, so golden files under `internal/update/testdata/` drive every edge case (multi-stage rejection, orphan markers, blank-line breaks, duplicates, unmarked managed-looking ARGs, CRLF, missing trailing newline, `tool=java` without `distribution=`, unknown `distribution=` value).

<a id="update-multi-stage-detection"></a>
### Multi-Stage Detection

The parser's `MultiStage` flag is checked by the base-target branch of `cli.RunUpdate` before any probe runs. If set, the base update fails with exit 1 and a message that `confine-ai update` does not support multi-stage base Dockerfiles. No network traffic is issued. This ordering guarantees that a user with a non-network preflight (a multi-stage file in a CI sandbox) fails fast.

**Implements:** REQ-AS-008 (AC-13)

<a id="update-classifier"></a>
### Classifier

Classification in `internal/update/classifier.go` walks the `ManagedGroup` slice and resolves each group to an `UpdateTarget`:

- `tool=go` → target policy "latest stable overall", prompt disabled.
- `tool=java distribution=corretto` → target policy "latest LTS major", prompt on major-jump.
- `tool=java` with any other `distribution=` → error; exit 1.
- `tool=java` with no `distribution=` → error; exit 1.
- `tool=base-image kind=image` → explicitly ignored. REQ-AS-008 never rewrites `FROM`.

The classifier never reads the ARG name, never reads the `FROM` URL, and never pattern-matches distribution strings outside the marker's `distribution=` field. This is enforced by code review and by a dedicated test that asserts the classifier's input surface is the `ManagedGroup` slice alone.

**Implements:** REQ-AS-008 (constraint: classification is marker-driven)

<a id="update-probe-transport"></a>
### Probe Transport

The HTTP client lives in `internal/update/client.go` and is the sole place `net/http` is constructed for outbound update traffic. Every call site receives the client as a parameter. Tests substitute an `httptest.Server`-backed client.

**Implements:** REQ-AS-008

Per the [Outbound HTTP Trust Boundary ADR](adr/2026-04-12-outbound-http-trust-boundary.md), the client enforces the following invariants:

- **Scheme:** https-only. Other schemes are rejected pre-flight.
- **TLS:** minimum version 1.2, platform trust store, no pinning in v1.
- **Timeout:** 30 seconds per request.
- **Proxy:** `http.ProxyFromEnvironment`.
- **Response cap:** 10 MiB via `io.LimitReader`.
- **User-Agent:** `confine-ai/<version>`.

The client is constructed with an explicit `http.Transport` and `tls.Config`. `http.DefaultClient` is never used. Lint and code review enforce this; the build does not.

**Go upstream adapter** (`internal/update/goupstream.go`). Reads `https://go.dev/dl/?mode=json`, selects the first stable release, and looks up the `(os=linux, kind=archive, arch)` sha256 for each managed group's marker arch. Exit-code mapping: non-200, JSON parse error, or no stable release in the response is a probe failure (exit 2); a missing `(os, arch, kind)` tuple for any requested arch is a sha256 failure (exit 3).

**Corretto upstream adapter** (`internal/update/corretto.go`). Discovers the version via a redirect-disabled HEAD against `https://corretto.aws/downloads/latest/amazon-corretto-<major>-<arch>-linux-jdk.tar.gz`, parsing the archive filename out of the 302 `Location` header, and probes both the current pinned major and `<major>+1` to catch an LTS bump against a hardcoded LTS set. The sha256 comes from `https://corretto.aws/downloads/latest_sha256/...` and must match `^[0-9a-f]{64}$`. Exit-code mapping: 4xx/5xx on the version probe is a probe failure (exit 2); 4xx/5xx on the sha256 fetch or a body that is not 64 hex chars is a sha256 failure (exit 3).

Both adapters use only `net/http` and `encoding/json`, so no new external dependencies enter the module graph.

<a id="update-sha256-verification"></a>
### Sha256 Cross-Verification

Per the trust boundary ADR, v1 uses single-origin trust: the sha256 and the archive both come from the same TLS origin, and the defense-in-depth check is the existing `RUN ... | sha256sum -c -` step inside the Dockerfile.

For Go, the sha256 and the version metadata arrive in the same HTTP response, so there is no temporal gap. For Corretto, the sha256 fetch follows the version probe against the same origin; both succeed or the group fails atomically.

**The sha256 pipeline:**

1. Probe upstream for candidate version.
2. For each `arch` in the managed group, fetch the sha256.
3. Validate each sha256 against `^[0-9a-f]{64}$` (defense against origin-side content-type confusion).
4. Return the `(version, map[arch]sha256)` pair to the orchestrator.

Any failure in steps 1-3 returns an error. Atomicity is enforced at the orchestrator level: no `ParsedDockerfile.Rewrite` call happens until every managed group has a validated `(version, sha256Map)` tuple.

**Implements:** REQ-AS-008 (AC-1, AC-5, exit codes 2/3)

<a id="update-rewrite"></a>
### Atomic Rewrite

`internal/update/rewrite.go` holds the rewrite logic. Given a `*ParsedDockerfile` and a map from `ManagedGroup` to resolved `(version, sha256Map)`, `Rewrite` produces a new byte slice and writes it atomically.

**Implements:** REQ-AS-008 (AC-17, rewrite contract constraints)

**Byte-preservation rules:**

- Non-managed lines are copied byte-identically from the input. Their `Raw` bytes (including the exact line-ending sequence) are passed through unchanged.
- Marker lines are copied byte-identically. The rewrite never touches marker comment text.
- `FROM` lines are copied byte-identically regardless of whether the user has edited them.
- Managed `ARG` lines are rewritten by replacing only the value portion. The rewrite locates the first `=` after the ARG name and writes `<prefix>=<new-value><suffix>` where `<prefix>` is every byte up to and including `=` and `<suffix>` is the original line's trailing whitespace plus line-ending bytes. ARG names, surrounding whitespace, and comments on the same line (if any) are preserved.
- The trailing-newline flag is preserved: if the input ended without a newline, the output ends without a newline. If the input ended with `\n`, the output ends with `\n`.
- CRLF is preserved: if the input line ended in `\r\n`, the rewritten line ends in `\r\n`. No normalization.

**Atomicity invariants:**

- **No partial-file states.** New bytes are written to a temp file in the same directory as the target, fsynced, and renamed over the target. A crash or error at any point leaves either the original file or the new file, never a truncated or partially written file.
- **Same-directory temp file.** The temp file must be in the target's directory so the rename is a same-filesystem operation. `os.Rename` across filesystems is not atomic.
- **Mode is preserved, not reset.** The new file inherits the mode of the existing file (expected `0o644`, but the user's mode wins if it differs). The rewrite never silently upgrades or downgrades file permissions.
- **Durable commit.** Both the temp file and its parent directory are fsynced so the rename survives an unclean shutdown on supported platforms. Directory fsync is best-effort: platforms where `os.Open` on a directory is meaningless are tolerated.
- **Temp files are always cleaned up.** A failed write removes the temp file; a successful rename leaves nothing behind.
- **Dry-run stops before any write.** When `Options.DryRun=true` the new bytes are computed so deltas can be reported, but no temp file is created and the target is never touched.

<a id="update-base-orchestration"></a>
### Base Update Orchestration

The base update reads `~/.confine-ai/base/Dockerfile`, probes the upstream version for every managed group, rewrites the file in place if anything changed, rebuilds `localhost/confine-ai-base:latest`, and drops stale confine-ai-managed containers so the next shortcut invocation picks up the new image.

**Implements:** REQ-AS-008.

**Design invariants:**

- **User file is the only source of truth.** The embedded seed is never consulted for updates; an absent user file is an error with a `confine-ai init` hint. `confine-ai update base` and `confine-ai init` are the only writers of the user file.
- **All-or-nothing probing.** No file byte is written until every probe — version and sha256, for every managed group — has succeeded. A single probe failure aborts the run with the planned deltas still reported in the summary, and the file is untouched.
- **Rewrite gates the rebuild.** The rebuild runs only when the rewrite actually happened. A no-op update (every probe returned the already-pinned value) keeps the existing image and reports `unchanged`.
- **Fail-forward on rebuild failure.** REQ-AS-008 defers rollback: a build failure after a successful rewrite leaves the file rewritten and exits non-zero. The PRD's edge-case table is authoritative for the exit code and the follow-up user action.
- **Major-jump prompting is terminal-gated.** Java major version jumps prompt on stderr only when stdin is a TTY and `--yes` is unset. Non-TTY runs implicitly skip the group; `--yes` runs auto-accept.
- **Dry-run stops at the rewrite boundary.** Probing and sha256 fetching still execute so CI preflight catches upstream breakage, but neither the rewrite nor the rebuild is invoked.
- **Multi-stage and malformed markers fail fast.** A multi-stage base Dockerfile, an unknown `distribution` value, or a `tool=java` marker missing its `distribution` field is rejected before any probe runs. The file is untouched.

<a id="update-assistant-orchestration"></a>
### Assistant Update Orchestration

An assistant update consults the per-assistant version probe registry first, short-circuits the rebuild when the installed CLI version already matches upstream, and otherwise cache-bust-rebuilds the assistant image from `~/.confine-ai/assistants/<name>/Dockerfile`. It then drops stale containers for that assistant across every workspace so the next shortcut invocation picks up the new image.

**Implements:** REQ-AS-008.

**Design invariants:**

- **The probe gate never fails an update.** Every probe failure — network error, parse error, missing image — falls through to the rebuild path with a single stderr warning. The least-work rule is an optimization, never a blocker.
- **Probe short-circuit is the only "did nothing" path.** When the probe reports installed == upstream, the update is reported as `unchanged` and neither the rebuild nor the container drop runs.
- **Ensure-base is a precondition, not an update step.** Before rebuilding, the orchestrator ensures `localhost/confine-ai-base:latest` exists in the local image store. The base is built only when absent; this is cheap on repeated runs and makes an assistant update self-contained even on a fresh install.
- **Assistant rebuilds never pull.** The assistant Dockerfile's `FROM` is `localhost/confine-ai-base:latest`, a locally built image with no remote source. `--pull` on the assistant build would cause the runtime to fail re-resolving that tag against registries. Cache-bust is achieved with `--no-cache`, not `--pull`.
- **Container drop is per-assistant, not global.** Only containers carrying the assistant-name label are removed. Other assistants' containers are untouched. Drop failures are stderr warnings, not update failures.
- **Failures flow through `TargetResult`, never through return values.** Missing Dockerfile, ensure-base failure, build failure, and container drop failure are all recorded on `TargetResult` so the dispatch layer can aggregate uniformly. The orchestrator never returns an error to its caller.
- **Dry-run short-circuits after the probe.** A dry-run with a version mismatch reports `would rebuild <assistant> without cache` and invokes no runtime commands.

The unregistered-probe case (`copilot`, `opencode` at launch) falls through to unconditional rebuild — the same path as a probe miss — until a per-assistant upstream version source is added to the registry.

<a id="update-assistant-probe"></a>
### Assistant Version Probe

The least-work rule (REQ-AS-008, Assistant update behavior subsection) is implemented as a per-assistant registry of `AssistantVersionProbe` values, consulted by the assistant update orchestrator in `internal/update/assistant.go` before the rebuild path runs. The registry lives in `internal/update/assistant_probe.go` and is seeded at package init. At launch, `claude` is registered; `copilot` and `opencode` are not, and their rebuilds run unconditionally pending a follow-up that identifies their upstream version sources.

**Probe interface.**

```go
type AssistantVersionProbe interface {
    Probe(ctx context.Context, exec container.Executor) (installed, upstream string, err error)
}
```

A probe reads both the installed and upstream versions and returns them to the gate helper. The gate helper is responsible for comparison, stdout shaping, and the never-error contract.

**Installed version source of truth.** The installed version is read from the assistant's local image (`confine-ai-assistant-<name>:latest`) by running the assistant's version command inside a one-shot container and parsing the first version-shaped token from stdout. The probe container runs with `--rm`, `--network=none`, and an emptied entrypoint: `--network=none` is a hard confinement invariant enforced in code, not by convention, because a probe that hit the network from inside the assistant image would be a confinement leak. The probe reads stdout only — no bind mounts, no writes, no interactive stdin — and the container state at exit is discarded. Before the run the probe also calls `image inspect` to distinguish "image missing" from "exec failed" in the warning text; both cases fall through to rebuild.

**Upstream version source of truth.** The upstream version for `claude` is the npm registry entry for `@anthropic-ai/claude-code` at the `latest` dist-tag, read via the `NpmLatestUpstream` adapter in `internal/update/npm.go`. The adapter runs through the same outbound HTTP trust boundary used by base probes (the shared `*update.Client`) and the same proxy/TLS/timeout settings. The response JSON's `.version` field is returned verbatim.

**Comparison.** `runAssistantGate` compares the two strings by equality. Equal → `ActionUnchanged`, short-circuit the rebuild. Not equal → fall through to the REQ-AS-008 rebuild path (or `ActionWouldUpdate` in dry-run). No semantic version ordering, no pre-release handling, no normalization.

**Graceful degradation.** Every probe failure mode — image missing, exec failure, unparseable version, network error, unreachable registry, non-2xx status, unparseable JSON, missing `.version` field — lands in the probe's error return. The gate helper catches it, emits a single stderr warning naming the assistant and the reason, and returns `(_, false)` so the caller falls through to the rebuild. The gate is never allowed to fail the update.

**Adding a probe for a new assistant.** Three steps, all local to the new assistant's code:

1. Add an `AssistantVersionProbe` implementation to `internal/update/assistant_probe.go` that wires the installed-version command and the upstream adapter.
2. Register it in the `assistantProbes` map's `init()`.
3. Add upstream-adapter code (or reuse `NpmLatestUpstream` when npm is the source).

No change to `cli.RunUpdate`, the Command Dispatch switch in `main.go`, or any unrelated probe is required.

**Implements:** REQ-AS-008 (Assistant update behavior: least-work rebuild)

<a id="update-no-arg-walk"></a>
### No-Arg Walk

When no targets are given, `runUpdateWithExecutor` (in `internal/cli/update.go`) builds an ordered target list:

1. `base` — always first.
2. Every subdirectory of `~/.confine-ai/assistants/` that contains a `Dockerfile`, sorted alphabetically by directory name via `slices.Sort`.

Walk rules:

- Base failure halts the walk (AC-23). No assistant is attempted. Exit code is base's code.
- Assistant directories missing `Dockerfile` are warned to stderr and recorded as `skipped`; the walk continues (AC-22).
- Assistant rebuild failures are recorded in the summary but do not stop the walk (AC-24).
- After all targets complete, the exit code is the highest-severity code observed (`max(baseCode, assistantCodes...)`).

**Implements:** REQ-AS-008 (AC-22, AC-23, AC-24, AC-26)

<a id="update-testing"></a>
### Testing Strategy

Tests sit at three layers. Parser and rewrite unit tests drive byte-identical round-trips and synthetic-delta rewrites off golden files under `internal/update/testdata/`, asserting the preservation contract (AC-17) independently of any probe. Probe unit tests stand up `httptest.NewTLSServer` as `go.dev` and `corretto.aws`, covering happy path, HTTP error classes, malformed JSON, missing arch, and sha256 hex validation, and assert the TLS-1.2-minimum and proxy-respect invariants from the [Probe Transport](#update-probe-transport) section. Orchestration tests use a `captureBuilder` test double that implements `container.Executor` to stand in for the container runtime, inject a fake upstream via a client-backed `httptest.Server`, and map each REQ-AS-008 acceptance criterion to a named subtest that asserts stdout summary, stderr warnings, on-disk file bytes, and the sequence of executor calls.

## Implementation Order

| ID | Name | Depends On |
|----|------|------------|
| REQ-CF-001 | Configuration File Discovery | None |
| REQ-CF-002 | JSONC Parsing | REQ-CF-001 |
| REQ-CF-003 | Supported Configuration Fields | REQ-CF-001, REQ-CF-002 |
| REQ-CF-004 | Unsupported Field Reporting | REQ-CF-003 |
| REQ-VS-001 | Variable Substitution | REQ-CF-003 |
| REQ-RT-001 | Container Runtime Detection | None |
| REQ-CO-001 | Container Identification | REQ-RT-001 |
| REQ-CO-006 | Workspace Mount | REQ-CF-003 |
| REQ-CO-008 | Mount Safety Validation | REQ-CO-006 |
| REQ-CO-010 | Auto-Create Missing Bind Mount Directories | REQ-AS-002, REQ-CO-008 |
| REQ-CO-007 | Credential Safety | REQ-AS-002, REQ-CO-006 |
| REQ-CO-009 | Network Isolation from Host | REQ-AS-002 |
| REQ-CO-004 | Container Remove | REQ-CO-001 |
| REQ-CL-001 | CLI Command Structure (stubs) | REQ-CL-002 |
| REQ-CL-001 | CLI Command Structure (full) | REQ-CO-004, REQ-CO-009, REQ-CL-002, REQ-CL-003, REQ-AS-002 |
| REQ-CL-002 | Global Flags | REQ-CF-001, REQ-RT-001 |
| REQ-CL-003 | Assistant Shortcut Flags | REQ-AS-002 |
| REQ-DI-001 | Static Binary | None |
| REQ-NR-001 | Outbound Network Allowlist | REQ-CO-009 |
| REQ-AS-001 | Centralized Assistant Configuration Directory | None |
| REQ-AS-004 | Assistant Container Identity | REQ-CO-001, REQ-AS-001 |
| REQ-AS-003 | Assistant Init Command | REQ-AS-001 |
| REQ-AS-006 | Base Image and User-Owned Dockerfile | REQ-RT-001 |
| REQ-AS-005 | Assistant Status Command | REQ-CO-001, REQ-AS-004 |
| REQ-AS-002 | Assistant Shortcut Invocation | REQ-AS-001, REQ-AS-004, REQ-AS-006, REQ-CF-003, REQ-VS-001, REQ-CO-001 |
| REQ-CL-004 | Assistant-Aware Command Routing | REQ-CL-001, REQ-AS-001, REQ-AS-002, REQ-AS-003, REQ-AS-005, REQ-AS-006 |
| REQ-RL-001 | Container Resource Limits | REQ-CF-003, REQ-AS-002 |
| REQ-MF-001 | Multi-Folder Workspace Mounting | REQ-CO-006, REQ-CO-008, REQ-AS-002, REQ-CL-001 |
| REQ-CL-005 | Positional Folder Arguments | REQ-CL-001, REQ-CL-004, REQ-MF-001 |
| REQ-AS-008 | Update Command | REQ-AS-002, REQ-AS-003, REQ-AS-006 |
