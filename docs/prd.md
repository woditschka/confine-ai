# Product Requirements Document (PRD)

<!-- AGENT: Optimized for agent consumption per docs/documentation.md -->
<!-- AGENT: Requirement template: <a id="req-xx-nnn"></a> ### REQ-XX-NNN: Name -->
<!-- AGENT: PRD boundary: no Go code, no function signatures, no internal references -->

confine-ai: A security-focused tool for running AI coding assistants in isolated containers. Compiles to a single static binary with zero host dependencies. Provides centralized assistant management with shortcut invocation for the primary use case: `confine-ai <assistant-name>` starts or reconnects to an assistant container with zero project-level configuration. The internal container runtime honors the subset of the devcontainer.json schema needed for assistant workflows and excludes OCI features, lifecycle commands, and other fields that expand the attack surface.

**Document map.** This PRD is organized into ten functional groups, listed in the order a user encounters them. The groups are: **CLI surface** (what the user types), **Configuration input** (how the assistant's internal `devcontainer.json` is parsed), **Container lifecycle** (how containers are started, reconnected, and removed), **Workspace and mount handling** (how host directories enter the container), **Resource isolation** (memory and CPU), **Network isolation** (host-gateway block and outbound allowlist), **Assistant management** (centralized `~/.confine-ai/` layout and the assistant shortcut), **Base image and update** (user-owned Dockerfile and the `confine-ai update` command), **Runtime and distribution** (how confine-ai selects a runtime and ships as a static binary), and **Shell completion**. Each requirement is a self-contained unit with Input, Output, Behavior, and Acceptance Criteria, identified by a stable `REQ-XX-NNN` ID that does not change when the PRD is reorganized. Architecture and implementation detail live in `docs/system-design.md`; design trade-offs and alternatives considered live in `docs/adr/`. Out-of-scope items are consolidated at the end of this document.

## Motivation

For developers running AI coding assistants (Claude Code, Codex, GitHub Copilot, OpenCode) in isolated containers, the launcher must have a minimal supply chain. The most widely used alternative (`@devcontainers/cli`) requires Node.js and npm on the host, which adds 1,200+ npm packages to the supply chain. The container is the isolation boundary; the tooling that launches it should have zero dependencies beyond the container runtime.

confine-ai compiles to a single static binary with no host dependencies beyond a Docker-compatible container runtime. Assistant images extend a shared base image so each assistant ships as a thin layer instead of reinstalling a full toolchain.

## Primary Use Case

Running AI coding assistants (Claude Code, GitHub Copilot, OpenCode) in isolated containers, with access limited to the project folder and credentials managed by each assistant's authentication flow.

**Shortcut workflow:**

1. `confine-ai init claude` scaffolds `~/.confine-ai/assistants/claude/` from a built-in template (one-time setup)
2. `confine-ai claude` in any project directory starts or reconnects to a Claude Code container for that project
3. Assistant-specific arguments pass through after `--`: `confine-ai claude -- --continue`
4. `confine-ai rm claude` stops and removes the assistant container for the current project

This workflow requires:

- Mounting only the project folder and explicitly declared mounts
- Running as a non-root user inside the container
- Working with any Docker-compatible runtime (Docker Desktop, Rancher Desktop, Podman)
- Building assistant images from user-owned Dockerfiles under `~/.confine-ai/assistants/<name>/`, layered on a shared base image at `~/.confine-ai/base/Dockerfile`

## Goals

| ID | Goal | Success Metric |
|----|------|----------------|
| G-1 | Zero runtime dependencies on the host | Single static binary; no Node.js, npm, or shared libraries required |
| G-2 | Minimal supply chain attack surface | No OCI feature downloads, no automatic lifecycle commands |
| G-3 | Compatible with all Docker-compatible runtimes on macOS and Linux | Works with Docker Desktop, Rancher Desktop, and Podman |
| G-4 | Single-command assistant invocation | `confine-ai <assistant-name>` starts or reconnects to an assistant container with zero project-level configuration |

## Non-Goals

| ID | Non-Goal | Rationale |
|----|----------|-----------|
| NG-1 | OCI Features (`features`) | OCI-hosted install scripts executed during image build introduce supply chain risks equivalent to npm packages. Install tools directly in Dockerfile instead. |
| NG-2 | Lifecycle commands (`postCreateCommand`, `postStartCommand`, `postAttachCommand`, `initializeCommand`) | Arbitrary commands that execute automatically are a known attack vector when opening untrusted repositories. Run setup commands manually or in Dockerfile. |
| NG-3 | Port forwarding (`forwardPorts`, `portsAttributes`, `appPort`) | Not needed for CLI-only assistant workflows. Use `docker run -p` directly for port mapping. |
| NG-4 | IDE customizations (`customizations.vscode`, `customizations.jetbrains`, etc.) | IDE-specific extensions, settings, and editor configuration are out of scope. The `customizations.confine-ai` namespace is supported (see REQ-CF-003, REQ-RL-001). Other `customizations.*` namespaces are reported as unsupported IDE configuration. |
| NG-5 | Docker Compose (`dockerComposeFile`, `service`) | Multi-container orchestration is out of scope. Use `docker compose` directly. |
| NG-6 | Privileged capabilities (`privileged`, `securityOpt`, broad `capAdd`) | Granting elevated privileges to the container is excluded by design. The only exception: `NET_ADMIN` is added by the tool to block host gateway access (REQ-CO-009). No other capabilities are granted. |
| NG-7 | GPU and hardware forwarding (`hostRequirements`, `gpus`) | Hardware passthrough configuration is out of scope. |
| NG-8 | User-installable OCI features | The OCI feature download and injection mechanism is excluded (see NG-1). |

## Requirements

Each requirement is a self-contained unit with Input, Output, Behavior, and Acceptance Criteria. Requirements are grouped by function in the order a user encounters them, from the CLI surface inward. Stable `REQ-XX-NNN` identifiers are preserved across reorganizations — cross-references keep working even when sections move.

### CLI surface

The commands, flags, and positional arguments the user types. Every feature in this document terminates in a CLI verb: either a management subcommand (`init`, `status`, `update`, `rm`, `completion`) or an assistant shortcut (`confine-ai <assistant-name>`). Flag parsing, positional-folder capture, and the assistant-vs-subcommand routing rules all live here.

<a id="req-cl-001"></a>
### REQ-CL-001: CLI Command Structure

The binary is named `confine-ai`. Its user-facing surface is the assistant shortcut plus a small set of management subcommands for installing, listing, updating, and removing assistant containers.

**Status:** Approved

**Input:**
- None (commands are dispatched based on positional arguments)

**Output:**
- Exit code and text written to stdout/stderr

**Behavior:**

| Command | Delegates To |
|---------|-------------|
| `confine-ai init` | REQ-AS-003 |
| `confine-ai status` | REQ-AS-005 |
| `confine-ai update` | REQ-AS-008 |
| `confine-ai rm` | REQ-CO-004, REQ-AS-004 |
| `confine-ai completion <shell>` | REQ-SC-001 |
| `confine-ai <assistant-name>` | REQ-AS-002, REQ-CL-004 |

The assistant shortcut (`confine-ai <assistant-name>`) is dispatched when the first positional argument does not match any known subcommand; see REQ-CL-004 for the full dispatch and routing rules.

**Acceptance Criteria:**
1. Given an unknown command, when the tool runs, then it reports the error and lists available commands
2. Given `--help`, when the tool runs, then it displays usage information
3. Given `--version`, when the tool runs, then it displays the version

**Depends On:** REQ-CO-004, REQ-CO-009, REQ-CL-002, REQ-CL-003, REQ-SC-001

---

<a id="req-cl-002"></a>
### REQ-CL-002: Global Flags

All commands accept a common flag for container runtime override.

**Status:** Approved

**Input:**
- None (flags are parsed before command dispatch)

**Output:**
- None (flags modify command behavior)

**Behavior:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--version` | boolean | `false` | Display version information and exit |
| `--docker-path` | string | Auto-detected (REQ-RT-001) | Explicit path to container runtime CLI |
| `--workspace-folder` | string | Current working directory | Path to the project root. Overridden when positional folder arguments are present (REQ-CL-005). |

When `--docker-path` is provided, it overrides the runtime auto-detection (REQ-RT-001). When `--workspace-folder` is provided, it overrides the current working directory as the host workspace folder. When positional folder arguments are present (REQ-CL-005), they take precedence over `--workspace-folder`.

**Acceptance Criteria:**
1. Given `--docker-path /usr/local/bin/podman`, when the tool runs, then it uses that binary for container operations

**Depends On:** REQ-RT-001

---

<a id="req-cl-003"></a>
### REQ-CL-003: Assistant Shortcut Flags

The assistant shortcut (`confine-ai <assistant-name>`) accepts flags for controlling container creation behavior when a new container is started.

**Status:** Approved

**Input:**
- Command-line flags parsed from `confine-ai <assistant-name>` invocation

**Output:**
- Resolved flag values passed to the container creation path

**Behavior:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--remove-existing-container` | boolean | `false` | Remove existing container before creating a new one |
| `--allow-risky-mounts` | boolean | `false` | Acknowledge tier 2 mount risks and proceed (see REQ-CO-008) |
| `--network` | string | `bridge` | Container network mode: `bridge`, `none`, or a named network. `host` is blocked. (see REQ-CO-009) |
| `--allowed-hosts` | string (repeatable) | none | Outbound allowlist entries (hostnames or IPs). See REQ-NR-001. |
| `--shell` | boolean | `false` | Open an interactive shell inside the container instead of running the assistant binary. Intended for one-off debugging, credential login, and config inspection. |
| `--no-git-identity` | boolean | `false` | Skip forwarding the host git user identity into the container environment. |

Resource limits (memory, CPU) are sourced exclusively from the assistant's `devcontainer.json` under `customizations.confine-ai`. There are no CLI flags for resource limits on the assistant shortcut. See REQ-RL-001.

When the assistant shortcut reconnects to an existing running container, these flags have no effect on the already-running container. They are applied only when a new container is created.

**Acceptance Criteria:**
1. Given `confine-ai claude --remove-existing-container`, when a container exists, then it is removed before creating a new one
2. Given `confine-ai claude --allow-risky-mounts`, when tier 2 risky mounts are detected, then the tool warns and proceeds
3. Given `confine-ai claude --network none`, when a new container is created, then the container has no network access

**Depends On:** REQ-AS-002

---

<a id="req-cl-004"></a>
### REQ-CL-004: Assistant-Aware Command Routing

The CLI routes the first positional argument to either a subcommand handler or the assistant shortcut handler.

**Status:** Proposed

**Input:**
- First positional argument after global flags

**Output:**
- Dispatch to the matching subcommand or assistant shortcut

**Behavior:**

The tool maintains a set of known subcommands: `init`, `status`, `update`, `rm`, `completion`. If the first positional argument matches a known subcommand, the tool dispatches to that handler. Otherwise, the tool treats the argument as an assistant name and dispatches to the assistant shortcut handler (REQ-AS-002).

`confine-ai rm` accepts an optional assistant name: `confine-ai rm <assistant-name>` stops and removes the named assistant's container for the current workspace. `confine-ai rm` without an assistant name stops and removes all confine-ai-managed containers for the current workspace (REQ-CO-004).

| Command | Behavior |
|---------|----------|
| `confine-ai rm` | Stop and remove all containers for workspace (REQ-CO-004) |
| `confine-ai rm <assistant-name>` | Stop and remove the named assistant's container for workspace (REQ-AS-004) |
| `confine-ai init [-y]` | Seed or overwrite base Dockerfile (REQ-AS-003) |
| `confine-ai init [-y] <assistant-name>` | Seed or overwrite base Dockerfile and scaffold assistant config (REQ-AS-003) |
| `confine-ai status` | List containers (REQ-AS-005) |
| `confine-ai update` | Least-work update of base and all assistants (REQ-AS-008) |
| `confine-ai update base` | Base update only (REQ-AS-008) |
| `confine-ai update <assistant-name>` | Assistant update only; rebuild skipped if installed version matches upstream (REQ-AS-008) |
| `confine-ai completion <shell>` | Print shell completion script (REQ-SC-001) |
| `confine-ai <assistant-name>` | Assistant shortcut (REQ-AS-002) |
| `confine-ai <assistant-name> . ../A ../B` | Assistant shortcut with multi-folder mounts (REQ-MF-001) |
| `confine-ai <assistant-name> -- <args>` | Assistant shortcut with passthrough args (REQ-AS-002) |
| `confine-ai <assistant-name> . ../A -- <args>` | Assistant shortcut with multi-folder mounts and passthrough args (REQ-MF-001) |

When the first argument is not a known subcommand and `~/.confine-ai/assistants/<name>/` does not exist, the tool reports an error: `unknown command or assistant "<name>"; run 'confine-ai init <name>' to create assistant configuration, or 'confine-ai --help' for available commands`.

**Acceptance Criteria:**
1. Given `confine-ai claude` where `~/.confine-ai/assistants/claude/` exists, then the tool dispatches to the assistant shortcut handler
2. Given `confine-ai claude` where `~/.confine-ai/assistants/claude/` does not exist, then the tool reports an error with init suggestion
3. Given `confine-ai rm claude`, then the tool stops and removes the claude container for the current workspace
4. Given `confine-ai rm` (no assistant name), then the tool stops and removes all containers for the current workspace
5. Given `confine-ai init claude`, then the tool dispatches to the init handler
6. Given `confine-ai status`, then the tool dispatches to the status handler
7. Given `confine-ai update`, then the tool dispatches to the update handler
8. Given `confine-ai completion bash`, then the tool dispatches to the completion handler

**Depends On:** REQ-CL-001, REQ-AS-001, REQ-AS-002, REQ-AS-003, REQ-AS-005, REQ-SC-001

---

<a id="req-cl-005"></a>
### REQ-CL-005: Positional Folder Arguments

The assistant shortcut accepts folder paths as positional arguments before the `--` separator.

**Status:** Proposed

**Input:**
- Positional arguments between the assistant name and `--`

**Output:**
- Parsed list of folder paths passed to the container creation pipeline

**Behavior:**

Positional arguments after the assistant name and before `--` are interpreted as folder paths. The `--` separator marks the boundary between folder arguments and assistant passthrough arguments. Arguments after `--` pass through to the assistant binary:

| Example | Folders | Assistant Args |
|---------|---------|------------|
| `confine-ai claude` | cwd | none |
| `confine-ai claude .` | cwd | none |
| `confine-ai claude . ../A` | cwd, `../A` | none |
| `confine-ai claude . ../A -- --continue` | cwd, `../A` | `--continue` |
| `confine-ai claude -- --continue` | cwd | `--continue` |

When no folder arguments are provided, the tool uses the current working directory as the single workspace folder.

The `init`, `status`, `update`, `rm`, and `completion` commands do not accept folder arguments.

**Acceptance Criteria:**
1. Given `confine-ai claude . ../A -- --continue`, when the tool parses arguments, then folders are `.` and `../A`, and assistant args are `--continue`
2. Given `confine-ai claude -- --continue`, when the tool parses arguments, then the folder list is empty (defaults to cwd), and assistant args are `--continue`
3. Given `confine-ai claude`, when the tool parses arguments, then the folder list is empty (defaults to cwd) and assistant args are empty
4. Given `confine-ai rm`, when the tool parses arguments, then no folder arguments are accepted

**Depends On:** REQ-CL-001, REQ-CL-004, REQ-MF-001

---

### Configuration input

How each assistant's `devcontainer.json` (scaffolded under `~/.confine-ai/assistants/<name>/`) is parsed, validated, and substituted into container definitions. Everything in this group runs before the container runtime is touched, so a malformed or unsupported configuration fails fast without invoking podman or docker. Configuration files are owned by the assistant scaffold — there is no project-local `devcontainer.json` discovery.

<a id="req-cf-001"></a>
### REQ-CF-001: Configuration File Location

The tool reads the assistant's configuration file from the fixed path under `~/.confine-ai/assistants/<name>/`.

**Status:** Approved

**Input:**
- `assistant-name` (string): Name of the assistant to load

**Output:**
- `config-path` (path): `~/.confine-ai/assistants/<assistant-name>/devcontainer.json`

**Behavior:**

The tool resolves the configuration path as `~/.confine-ai/assistants/<assistant-name>/devcontainer.json`. If the file does not exist, the tool reports an error directing the user to run `confine-ai init <assistant-name>`.

**Acceptance Criteria:**
1. Given assistant name `claude`, when the tool resolves the config path, then it returns `~/.confine-ai/assistants/claude/devcontainer.json`
2. Given the assistant directory does not exist, when the tool loads the config, then it reports an error suggesting `confine-ai init <assistant-name>`

---

<a id="req-cf-002"></a>
### REQ-CF-002: JSONC Parsing

The tool parses `devcontainer.json` files that contain comments and trailing commas (JSONC format).

**Status:** Approved

**Input:**
- `config-path` (path): Path to a `devcontainer.json` file

**Output:**
- Parsed configuration object

**Behavior:**

`devcontainer.json` files use JSONC format: single-line comments (`//`), block comments (`/* */`), and trailing commas. The parser strips these before processing the JSON content.

**Acceptance Criteria:**
1. Given a file with `//` comments, when parsed, then the comments are ignored and the JSON is valid
2. Given a file with `/* */` block comments, when parsed, then the comments are ignored
3. Given a file with trailing commas in objects and arrays, when parsed, then the commas are ignored
4. Given invalid JSON (beyond JSONC extensions), when parsed, then the tool reports a parse error

---

<a id="req-cf-003"></a>
### REQ-CF-003: Supported Configuration Fields

The tool reads and applies the following `devcontainer.json` fields.

**Status:** Approved

**Input:**
- Parsed configuration object

**Output:**
- Container configuration parameters passed to the container runtime

**Behavior:**

The tool supports these fields:

| Field | Purpose |
|-------|---------|
| `name` | Display name for the development container |
| `image` | Base container image reference |
| `build.dockerfile` | Path to a Dockerfile for building a custom image |
| `build.context` | Build context directory |
| `build.args` | Build-time arguments passed to the image build |
| `workspaceFolder` | Working directory inside the container |
| `mounts` | Volume and bind mount declarations |
| `containerEnv` | Environment variables set at container creation time (all processes) |
| `remoteUser` | User identity for commands executed via `exec` |
| `containerUser` | User identity for the container's main process |
| `customizations.confine-ai` | confine-ai-specific configuration: resource limits and tool settings |

A configuration must specify either `image` or `build.dockerfile`, but not both. If both are present, the tool reports an error. If neither is present, the tool reports an error.

`containerEnv` is set at container creation time. All processes in the container see these values. Values are static for the container's lifetime and visible via `docker inspect`. Use only for non-secret configuration (timezone, locale, tool paths).

If `containerEnv` contains keys matching common credential patterns (`*_API_KEY`, `*_TOKEN`, `*_SECRET`, `*_PASSWORD`, `*_CREDENTIAL`), the tool warns that these values are visible via `docker inspect` and recommends using Claude Code's built-in OAuth authentication instead.

`remoteUser` sets the user for commands run via `exec`. `containerUser` sets the user for the container's main process started by `up`. If only `remoteUser` is set, the container's main process runs as the image default user. If only `containerUser` is set, `exec` commands also run as that user.

The `customizations` field is partially supported. The tool parses `customizations.confine-ai` for tool-specific settings (see REQ-RL-001). Other `customizations` namespaces (e.g., `customizations.vscode`) are reported as unsupported IDE configuration per REQ-CF-004.

All other fields are unsupported (see REQ-CF-004).

**Acceptance Criteria:**
1. Given a configuration with `image`, when the tool starts a container, then it uses that image
2. Given a configuration with `build.dockerfile`, when the tool starts a container, then it builds the image from the Dockerfile
3. Given a configuration with both `image` and `build.dockerfile`, when parsed, then the tool reports an error
4. Given a configuration with neither `image` nor `build.dockerfile`, when parsed, then the tool reports an error
5. Given a configuration with `mounts`, when the tool starts a container, then it applies the mount declarations
6. Given a configuration with `containerEnv`, when the tool starts a container, then those environment variables are set at creation time for all processes
7. Given a configuration with `remoteUser`, when `exec` runs a command, then the command runs as that user
8. Given a configuration with `containerUser`, when `up` starts the container, then the main process runs as that user
9. Given a configuration with `name`, when the tool runs, then the name is used in container labels
10. Given `containerEnv` with key `ANTHROPIC_API_KEY`, when parsed, then the tool warns that credentials should use OAuth authentication instead
11. Given a configuration with `customizations.confine-ai.memory` set to `"8g"`, when parsed, then the tool reads the memory limit value
12. Given a configuration with `customizations.vscode`, when parsed, then the tool reports it as unsupported IDE configuration

**Depends On:** REQ-CF-001, REQ-CF-002

---

<a id="req-cf-004"></a>
### REQ-CF-004: Unsupported Field Reporting

The tool reports which unsupported fields are present in a configuration file rather than silently ignoring them.

**Status:** Approved

**Input:**
- Parsed configuration object

**Output:**
- List of unsupported field names present in the configuration

**Behavior:**

When the configuration file contains fields not in the supported set (REQ-CF-003), the tool reports each unsupported field name to the user. The tool does not fail on unsupported fields; it proceeds with the supported fields and warns about the rest.

The `customizations` field receives special handling. When `customizations` is present, the tool inspects its child namespaces. `customizations.confine-ai` is a supported namespace (parsed by REQ-RL-001). All other `customizations` child namespaces (e.g., `customizations.vscode`, `customizations.jetbrains`) are reported as unsupported IDE configuration. The warning message identifies each unsupported namespace by its full path (e.g., "customizations.vscode").

**Acceptance Criteria:**
1. Given a configuration with `features`, when parsed, then the tool warns that `features` is unsupported
2. Given a configuration with `postCreateCommand`, when parsed, then the tool warns that `postCreateCommand` is unsupported
3. Given a configuration with only supported fields, when parsed, then no warnings are emitted
4. Given a configuration with unsupported fields, when the tool runs, then it proceeds with supported fields
5. Given a configuration with `customizations.confine-ai`, when parsed, then no warning is emitted for the `customizations` field
6. Given a configuration with `customizations.vscode`, when parsed, then the tool warns that `customizations.vscode` is unsupported IDE configuration
7. Given a configuration with both `customizations.confine-ai` and `customizations.vscode`, when parsed, then only `customizations.vscode` triggers a warning

**Depends On:** REQ-CF-003

---

<a id="req-vs-001"></a>
### REQ-VS-001: Variable Substitution in Configuration Values

The tool resolves variable patterns in `devcontainer.json` string values.

**Status:** Approved

**Input:**
- Configuration string values containing `${...}` patterns
- Host environment variables
- Workspace folder path

**Output:**
- Resolved string values with variables replaced

**Behavior:**

The tool resolves these variable patterns in all string values within the configuration, including values in `containerEnv`, `build.args`, `mounts` entries, `workspaceFolder`, and `name`:

| Pattern | Resolves To |
|---------|-------------|
| `${localEnv:VARIABLE}` | Host environment variable value |
| `${localEnv:VARIABLE:default}` | Host environment variable value, or `default` if unset |
| `${devcontainerId}` | Deterministic identifier derived from the project path |
| `${localWorkspaceFolder}` | Absolute path to the project on the host |
| `${localWorkspaceFolderBasename}` | Project folder name (last path segment) |

Unresolvable variables without defaults cause an error.

**Acceptance Criteria:**
1. Given `${localEnv:HOME}`, when resolved, then it returns the host `HOME` value
2. Given `${localEnv:MISSING:fallback}`, when `MISSING` is unset, then it returns `fallback`
3. Given `${localEnv:MISSING}` with no default, when `MISSING` is unset, then the tool reports an error
4. Given `${localWorkspaceFolder}`, when the workspace is `/home/user/project`, then it returns `/home/user/project`
5. Given `${localWorkspaceFolderBasename}`, when the workspace is `/home/user/project`, then it returns `project`
6. Given `${devcontainerId}`, when resolved, then it returns a deterministic value derived from the workspace path

**Depends On:** REQ-CF-003

---

### Container lifecycle

Container identification and removal. The assistant shortcut (REQ-AS-002) owns the start-and-reconnect path; this group defines the label-based identity scheme the shortcut and `confine-ai rm` share.

<a id="req-co-001"></a>
### REQ-CO-001: Container Identification

The tool labels containers to associate them with a workspace folder set, and uses those labels to find containers for subsequent operations.

**Status:** Approved

**Input:**
- `folder-set` (path[]): One or more absolute paths to host directories. Single-folder invocations pass only the primary workspace folder. Multi-folder invocations pass all mounted folders (primary + additional).

**Output:**
- Container labels that uniquely identify the container's folder set

**Behavior:**

When creating a container, the tool applies metadata labels that include a deterministic identifier derived from the complete set of mounted folders. The identifier is computed by sorting the absolute folder paths lexicographically and hashing the sorted list. Argument order does not affect the identifier: two invocations with the same folders in different order produce the same container identity.

When removing or reconnecting to containers, the tool queries the container runtime for containers matching the folder-set identifier label.

This mechanism allows containers for different folder sets to coexist on the same host. A single-folder invocation and a multi-folder invocation that share the same primary workspace but differ in additional folders produce different identities and therefore different containers.

The tool stores individual folder paths in a label so that display commands (REQ-AS-005) can show which folders a container was started with.

**Acceptance Criteria:**
1. Given a container started for folder set [/home/user/project-a], when the tool queries for that folder set, then it finds exactly that container
2. Given containers for two different folder sets, when the tool queries for one folder set, then it does not return the other
3. Given no container for a folder set, when the tool queries for it, then it returns no results
4. Given a container started for folder set [/home/user/A, /home/user/B], when the tool queries for [/home/user/B, /home/user/A] (reversed order), then it finds the same container (argument-order independence)
5. Given a container started for folder set [/home/user/project-a] and another for [/home/user/project-a, /home/user/lib], when the tool queries for [/home/user/project-a], then it returns only the single-folder container
6. Given a container with folder set [/home/user/A, /home/user/B], when `confine-ai status` runs, then both folder paths appear in the output

**Depends On:** REQ-RT-001

---

<a id="req-co-004"></a>
### REQ-CO-004: Container Remove

The `rm` command stops and removes confine-ai-managed containers for the current workspace.

**Status:** Approved

**Input:**
- `workspace-folder` (path): Path to the current workspace (defaults to the current working directory)
- `assistant-name` (string, optional): Name of a specific assistant whose container should be removed

**Output:**
- Confirmation that the container(s) were stopped and removed

**Behavior:**

The tool identifies confine-ai-managed containers for the current workspace (per REQ-CO-001), regardless of state (running or stopped), stops any that are running, and removes them.

`confine-ai rm` without an assistant name removes every confine-ai-managed container whose workspace label matches the current workspace. `confine-ai rm <assistant-name>` narrows the selection to the single container whose assistant label also matches (per REQ-AS-004). If no matching container exists, the tool reports that no container was found and exits with code `0`.

**Acceptance Criteria:**
1. Given a running container, when `confine-ai rm` runs, then the container is stopped and removed
2. Given a stopped container, when `confine-ai rm` runs, then the container is removed
3. Given no container for the workspace, when `confine-ai rm` runs, then the tool reports no container found and exits `0`
4. Given two assistant containers (`claude` and `copilot`) in the same workspace, when `confine-ai rm claude` runs, then only the `claude` container is removed and the `copilot` container is left running
5. Given two assistant containers in the same workspace, when `confine-ai rm` runs without an assistant name, then both containers are removed

**Depends On:** REQ-CO-001

---


### Workspace and mount handling

How host directories enter the container: primary workspace mounting, credential directories under `~/.confine-ai/`, mount safety classification, auto-creation of missing bind mount sources, and multi-folder mount support. Every mount declared here passes through the same safety validation regardless of whether it came from a `devcontainer.json` field in the assistant scaffold or a positional folder argument on the assistant shortcut.

<a id="req-co-006"></a>
### REQ-CO-006: Workspace Mount

The tool mounts the workspace folder into the container. This is the only implicit mount.

**Status:** Approved

**Input:**
- `workspace-folder` (path): Path to the project root on the host
- `workspaceFolder` (config field): Target path inside the container (defaults to `/workspaces/<basename>`)

**Output:**
- Bind mount from host workspace to container workspace

**Behavior:**

The tool bind-mounts the host workspace folder into the container at the path specified by `workspaceFolder` in the configuration. If `workspaceFolder` is not specified, the tool defaults to `/workspaces/<workspace-folder-basename>`.

This is the only mount the tool creates implicitly. All other mounts must be explicitly declared in the `mounts` configuration field. The tool does not mount the host home directory, Docker socket, or any other host path unless the configuration declares it.

**Acceptance Criteria:**
1. Given a workspace at `/home/user/project` and `workspaceFolder` set to `/workspaces/project`, when a container starts, then `/home/user/project` is mounted at `/workspaces/project` inside the container
2. Given no `workspaceFolder` in the configuration, when a container starts, then the workspace is mounted at `/workspaces/<basename>`
3. Given no `mounts` in the configuration, when a container starts, then the workspace mount is the only mount attached to the container
4. Given `mounts` in the configuration, when a container starts, then the workspace mount and the declared mounts are attached — no others

**Depends On:** REQ-CF-003

---

<a id="req-co-007"></a>
### REQ-CO-007: Credential Safety

The tool does not handle credentials directly. Authentication is managed by Claude Code's built-in OAuth flow, with credentials stored inside the container on a user-mounted volume.

**Status:** Approved

**Input:**
- Parsed configuration object (specifically `containerEnv` keys and `mounts` entries)

**Output:**
- Warnings if credential patterns detected in `containerEnv`

**Behavior:**

The tool enforces these constraints:

1. The tool never accepts credentials as command-line arguments or flags
2. The tool never injects host environment variables or external secrets into the container — only values explicitly declared in `containerEnv` are passed via `-e`
3. `containerEnv` values are passed to the runtime at creation time; the tool warns if key names match credential patterns (`*_API_KEY`, `*_TOKEN`, `*_SECRET`, `*_PASSWORD`, `*_CREDENTIAL`)
4. The tool does not log `containerEnv` values at any log level

Credential management follows Anthropic's recommended approach:

1. The user declares a bind mount for `.claude/` in `mounts` (e.g., `source=${localWorkspaceFolder}/.claude,target=/home/dev/.claude,type=bind`)
2. On first run, Claude Code prompts for OAuth login inside the container
3. OAuth credentials are stored in `/home/dev/.claude/.credentials.json` (0600 permissions) on the mounted volume
4. Subsequent sessions reuse the stored credentials without re-authentication

The `.claude/` directory should be added to `.gitignore` to prevent accidental commit of credentials.

**Acceptance Criteria:**
1. Given `containerEnv` with key `ANTHROPIC_API_KEY`, when parsed, then the tool warns that credentials should use OAuth authentication instead
2. Given `containerEnv` with key `NODE_OPTIONS`, when parsed, then no warning is emitted
3. Given the tool starts a container, then no credential values appear in the tool's command-line arguments visible via `ps aux`
4. Given a `.claude/` bind mount in the configuration, when the assistant shortcut starts a container, then the mount is applied and Claude Code can persist OAuth credentials

**Depends On:** REQ-AS-002, REQ-CO-006

---

<a id="req-co-008"></a>
### REQ-CO-008: Mount Safety Validation

The tool validates all mounts (including the workspace) and either blocks or warns about paths that could expose sensitive host data.

**Status:** Approved

**Input:**
- All mounts from the `mounts` configuration field and the implicit workspace mount

**Output:**
- Error if a blocked mount is detected (refuses to start)
- Warning with required `--allow-risky-mounts` confirmation if a risky mount is detected

**Behavior:**

Before starting a container, the tool inspects all mounts. Mounts are classified into three tiers:

**Tier 1 — Blocked (unconditional, no override):**

| Blocked Source Path | Reason |
|-------------------|--------|
| `/` | Exposes entire host filesystem |
| `/etc` | Exposes host system configuration |
| `/tmp` | Shared temporary directory; use a dedicated mount instead |
| `/var/run/docker.sock` | Enables container escape via Docker API |
| `/var/run/podman/podman.sock` | Enables container escape via Podman API |
| Host user home directory (e.g., `/home/<user>`, `/Users/<user>`) | Exposes SSH keys, shell history, credentials |

The tool also blocks any mount whose source is a parent of the host home directory (e.g., `/home`, `/Users`).

**Tier 2 — Risky (warning, requires `--allow-risky-mounts` to proceed):**

| Risky Pattern | Reason |
|--------------|--------|
| Workspace or mount contains `.env`, `.ssh/`, `.gnupg/`, `.aws/`, or `credentials` files/directories | Sensitive files would be exposed to the container |
| Mount source is a broad directory (e.g., `/opt`, `/usr/local`) | Larger scope than typically needed |

When a risky mount is detected, the tool lists the specific risks found and refuses to start unless the user passes `--allow-risky-mounts`. This flag acknowledges the risk without disabling the tier 1 deny list.

**Tier 3 — Allowed:**

Mounts to subdirectories of the home directory (e.g., `/home/user/.config/specific-tool`) and workspace folders that pass the tier 2 checks are allowed without confirmation.

**Acceptance Criteria:**
1. Given a mount with source `/`, when the assistant shortcut starts a container, then the tool refuses to start and reports the violation
2. Given a mount with source `/var/run/docker.sock`, when the assistant shortcut starts a container, then the tool refuses to start
3. Given a mount with source `/home/user` (the user's home), when the assistant shortcut starts a container, then the tool refuses to start
4. Given a mount with source `/home/user/.config/git`, when the assistant shortcut starts a container, then the mount is allowed
5. Given a mount with source `/home`, when the assistant shortcut starts a container, then the tool refuses to start
6. Given the workspace folder is `/home/user/projects/myapp`, when the assistant shortcut starts a container, then the workspace mount is allowed (it is a subdirectory, not the home directory itself)
7. Given a workspace folder containing a `.env` file, when the assistant shortcut runs without `--allow-risky-mounts`, then the tool warns and refuses to start
8. Given a workspace folder containing a `.env` file, when the assistant shortcut runs with `--allow-risky-mounts`, then the tool warns and proceeds
9. Given a mount with source `/home/user/.ssh`, when the assistant shortcut runs without `--allow-risky-mounts`, then the tool warns and refuses to start
10. Given a mount with source `/`, when the assistant shortcut runs with `--allow-risky-mounts`, then the tool still refuses to start (tier 1 is unconditional)

**Depends On:** REQ-CO-006

---

<a id="req-co-010"></a>
### REQ-CO-010: Auto-Create Missing Bind Mount Directories

The tool offers to create missing bind mount source directories when the assistant shortcut starts a container, with user confirmation, when the directory can be created safely.

**Status:** Approved

**Input:**
- All bind mount sources from the `mounts` configuration field
- Whether the tool is running in interactive mode (terminal attached) or non-interactive mode (no terminal)

**Output:**
- Directories created on the host filesystem if the user confirms
- Mount blocked error if the user declines, the tool is non-interactive, or the directory cannot be created safely

**Behavior:**

Before mount validation (during assistant shortcut container creation), the tool inspects each bind mount source path from the configuration. For each source path where:

1. The leaf directory does not exist
2. The parent directory exists and resolves cleanly to an accessible path
3. The parent directory is not itself a blocked path (tier 1)
4. The full resolved path would pass the mount safety classification (not blocked or risky)

The tool collects these paths and prompts the user for confirmation. The prompt lists all directories to create and asks for a single yes/no response. The default answer is yes (pressing Enter confirms).

If the user confirms, the tool creates the directories (including intermediate directories) and proceeds with the normal mount validation flow.

If the user declines, the missing directories remain. Mount validation then reports a "cannot resolve path" tier 1 block error for each missing directory.

In non-interactive mode, the tool does not prompt. Mount validation reports the same block error for each missing directory.

The security model is unchanged. Only directories that would pass the existing mount safety classification (REQ-CO-008) are eligible for creation. Blocked paths (tier 1) and risky paths (tier 2 without `--allow-risky-mounts`) are never auto-created.

**Acceptance Criteria:**
1. Given a bind mount source `/home/user/.confine-ai/claude` where `/home/user/.confine-ai` exists but `claude/` does not, when the assistant shortcut runs interactively and the user confirms, then the directory is created and mount validation succeeds
2. Given the same missing directory, when the assistant shortcut runs interactively and the user declines, then the tool reports a mount blocked error
3. Given the same missing directory, when the assistant shortcut runs non-interactively (no terminal attached), then the tool reports a mount blocked error without prompting
4. Given a bind mount source `/home/user/.ssh/keys` where the resolved path would be classified as risky (tier 2), when the assistant shortcut runs, then the tool does not offer to create the directory
5. Given a bind mount source `/var/run/docker.sock` where the path is blocked (tier 1), when the assistant shortcut runs, then the tool does not offer to create the directory
6. Given two missing bind mount directories that both pass classification, when the assistant shortcut runs interactively, then the prompt lists both directories in a single confirmation
7. Given a bind mount source where the parent directory does not exist, when the assistant shortcut runs, then the tool does not offer to create it (parent must resolve cleanly)
8. Given all bind mount sources already exist, when the assistant shortcut runs, then no prompt appears and mount validation proceeds as before

**Implementation:** See [system-design.md#upoptions](system-design.md#upoptions) for the options value object.

**Depends On:** REQ-AS-002, REQ-CO-008

---

<a id="req-mf-001"></a>
### REQ-MF-001: Multi-Folder Workspace Mounting

The assistant shortcut mounts one or more workspace folders into a container via positional arguments.

**Status:** Proposed

**Input:**
- `folders` (path[]): Zero or more positional arguments before `--`. Each is a path to a directory on the host.
- `workspace-folder` (path): The first folder in the list (or cwd if no folders are specified). This is the primary workspace.

**Output:**
- One bind mount per folder. The first folder mounts at the `workspaceFolder` config path (defaulting to `/workspaces/<basename>`). Additional folders mount at `/workspaces/<basename>`.
- Container shell starts in the primary workspace folder.

**Behavior:**

When no positional folder arguments are provided, the tool uses the current working directory as the single workspace folder. This preserves existing behavior (REQ-CO-006).

When one or more folder paths are provided as positional arguments, the first folder is the primary workspace. The primary workspace determines `workspaceFolder` inside the container (the shell starting directory) and is the folder used for variable substitution (REQ-VS-001).

All folders (primary + additional) participate in container identity (REQ-CO-001). The identity key is derived from the sorted set of absolute folder paths. Argument order does not affect identity: `confine-ai claude . ../A` and `confine-ai claude ../A .` find the same container. The first argument still determines the exec working directory.

Additional folders are mounted read-write at `/workspaces/<basename>` inside the container. The container path for additional folders is always `/workspaces/<basename>`, regardless of the `workspaceFolder` configuration value.

All folder paths are resolved to absolute paths before processing. Relative paths (e.g., `../A`) are resolved relative to the current working directory.

The `.` path is equivalent to the current working directory. `confine-ai claude` and `confine-ai claude .` produce identical behavior.

**Basename collision detection:** Before mounting, the tool resolves the container-side basename for each folder. If two or more folders resolve to the same basename, the tool reports an error listing the colliding folders and their shared basename. The tool does not attempt automatic deduplication.

**Mount safety:** All folders (primary and additional) pass through the existing mount safety validation (REQ-CO-008). A blocked or risky folder causes the same error or warning as a blocked or risky mount source. The `--allow-risky-mounts` flag applies to all folders.

**Folder existence:** Each folder path must exist and be a directory. If a path does not exist or is not a directory, the tool reports an error before starting the container.

| Invocation | Primary Workspace | Additional Mounts | Assistant Args |
|------------|------------------|-------------------|------------|
| `confine-ai claude` | cwd | none | none |
| `confine-ai claude .` | cwd | none | none |
| `confine-ai claude . ../A ../B` | cwd | `../A`, `../B` | none |
| `confine-ai claude . ../A -- --continue` | cwd | `../A` | `--continue` |

**Acceptance Criteria:**
1. Given `confine-ai claude` with no folder arguments, when the container starts, then cwd is mounted at `/workspaces/<cwd-basename>` (existing behavior unchanged)
2. Given `confine-ai claude .`, when the container starts, then cwd is mounted at `/workspaces/<cwd-basename>` (identical to no-argument invocation)
3. Given `confine-ai claude . ../project-a ../project-b`, when the container starts, then cwd mounts at `/workspaces/<cwd-basename>`, `../project-a` mounts at `/workspaces/project-a`, and `../project-b` mounts at `/workspaces/project-b`
4. Given `confine-ai claude . ../A -- --continue`, when the container starts, then cwd and `../A` are mounted, and `--continue` passes to the assistant binary
5. Given `confine-ai claude . ../A ../B`, when the container starts, then all 3 folders are mounted
6. Given two folders that resolve to the same basename (e.g., `../x/shared` and `../y/shared`), when the tool validates mounts, then it reports an error listing both paths and the basename `shared`
7. Given `confine-ai claude . ../secret-project` where `../secret-project` contains a `.env` file, when the assistant shortcut runs without `--allow-risky-mounts`, then the tool reports an error and refuses to start
8. Given `confine-ai claude . ../nonexistent`, when the tool validates paths, then it reports an error that `../nonexistent` does not exist
9. Given `confine-ai claude . ../A`, when the container starts, then the shell working directory is the primary workspace (`/workspaces/<cwd-basename>`), not `/workspaces/A`
10. Given `confine-ai claude . ../A` and `confine-ai claude .` (same primary workspace, different folder sets), when both are run, then two separate containers exist — one with 2 folders, one with 1
11. Given `confine-ai claude . ../A` and `confine-ai claude ../A .` (same folders, different argument order), when queried, then the tool finds the same container (argument-order independence)

**Depends On:** REQ-CO-006, REQ-CO-008, REQ-AS-002, REQ-CL-001, REQ-CL-005

---


### Resource isolation

Every confine-ai-managed container runs under explicit memory and CPU upper bounds so a runaway assistant process cannot exhaust the host. Limits are declared in the assistant's `devcontainer.json` under `customizations.confine-ai`. This group defines the single requirement that governs how those limits are resolved, validated, and enforced.

<a id="req-rl-001"></a>
### REQ-RL-001: Container Resource Limits

The tool sets memory and CPU upper limits on containers to prevent unbounded host resource consumption.

**Status:** Approved

**Input:**
- `customizations.confine-ai.memory` (string, optional): Memory limit in Docker format (e.g., `"8g"`, `"512m"`, `"2048m"`) declared in the assistant's `devcontainer.json`.
- `customizations.confine-ai.cpus` (string, optional): CPU limit as a decimal number (e.g., `"4"`, `"2.5"`, `"0.5"`) declared in the assistant's `devcontainer.json`.

**Output:**
- Container started with memory and CPU limits passed to the container runtime
- Warning on stderr when no memory limit is configured

**Behavior:**

Resource limits for an assistant container are sourced exclusively from `customizations.confine-ai` in the assistant's `devcontainer.json` at `~/.confine-ai/assistants/<name>/devcontainer.json`. There is no CLI flag source and no other override path. The built-in assistant templates ship with default values of `memory: "8g"` and `cpus: "4"`, which users may edit per-assistant.

The `memory` value uses Docker's memory format: a number followed by a unit suffix (`b`, `k`, `m`, `g`). The tool passes this value to the container runtime's memory-limit flag. An invalid format causes an error before container creation. An empty or absent value means no memory limit is applied.

The `cpus` value is a decimal number representing the CPU quota. `"4"` means the container can use up to 4 CPU cores. `"0.5"` means half a core. The tool passes this value to the container runtime's CPU-limit flag. Non-positive or non-numeric values cause an error before container creation. An empty or absent value means no CPU limit is applied.

When no memory limit is configured, the tool emits a warning to stderr: "no memory limit set; container can consume all host memory". This follows the pattern of existing risky-mount warnings (REQ-CO-008). No warning is emitted for a missing CPU limit.

These limits are upper bounds (cgroup limits), not reservations. The container runtime enforces them. A container exceeding its memory limit is killed by the OOM killer. A container exceeding its CPU limit is throttled.

**Acceptance Criteria:**
1. Given `customizations.confine-ai.memory` set to `"8g"` in the assistant's `devcontainer.json`, when the container starts, then the container runtime receives an `8g` memory limit
2. Given `customizations.confine-ai.cpus` set to `"4"` in the assistant's `devcontainer.json`, when the container starts, then the container runtime receives a CPU limit of `4`
3. Given `customizations.confine-ai.memory` is absent, when the assistant shortcut runs, then the tool emits a warning to stderr containing "no memory limit set"
4. Given `customizations.confine-ai.cpus` is absent, when the assistant shortcut runs, then no warning is emitted for CPU and no CPU-limit flag is passed to the runtime
5. Given `customizations.confine-ai.memory` set to `"invalid"`, when the tool validates the value, then it reports an error before creating the container
6. Given `customizations.confine-ai.cpus` set to `"0"`, when the tool validates the value, then it reports an error before creating the container
7. Given `customizations.confine-ai.cpus` set to `"-1"`, when the tool validates the value, then it reports an error before creating the container
8. Given `confine-ai claude` with `customizations.confine-ai.cpus` set to `"2"` in the assistant's `devcontainer.json`, when the container starts, then the container runtime receives a CPU limit of `2`

**Depends On:** REQ-CF-003, REQ-AS-002

---


### Network isolation

The two-layer network boundary for confine-ai-managed containers. REQ-CO-009 establishes host-gateway isolation by default, so a freshly-started container cannot reach services bound to the host's loopback or private interfaces. REQ-NR-001 adds an optional per-invocation outbound allowlist that flips the default from "reach the internet freely" to "reach only the declared hosts," for workflows that run untrusted code from freshly-cloned repositories. Together they form a deny-by-default outbound posture: the host is unreachable out of the box, and the internet is unreachable when an allowlist is in effect.

<a id="req-co-009"></a>
### REQ-CO-009: Network Isolation from Host

The container has no direct network access to the host machine. The tool provides a `--network` flag to control the container's network mode and blocks access to the Docker gateway IP.

**Status:** Approved

**Input:**
- `--network` flag value (optional)

**Output:**
- Container started with the specified network mode, host access blocked

**Behavior:**

| Value | Behavior |
|-------|----------|
| `bridge` | Standard Docker bridge networking with host gateway blocked (default) |
| `none` | No network access — fully isolated |
| `<name>` | User-created Docker network with host gateway blocked |

The tool blocks host access:

1. `--network host` is rejected unconditionally. Host networking gives the container full access to the host's network stack.
2. For `bridge` and named networks, the tool blocks all outbound traffic to the host machine, including the Docker gateway IP and `host.docker.internal`. The container cannot reach services running on the host.

The container retains internet access (can reach external services like `api.anthropic.com`) but cannot connect to the host machine.

**Implementation:** See [system-design.md#gateway-blocking](system-design.md#gateway-blocking)

**Acceptance Criteria:**
1. Given no `--network` flag, when the assistant shortcut runs, then the container uses bridge networking
2. Given `--network none`, when the assistant shortcut runs, then the container has no network access
3. Given `--network my-restricted-net`, when the assistant shortcut runs, then the container uses that Docker network
4. Given `--network host`, when the assistant shortcut runs, then the tool refuses to start and reports that host networking is blocked
5. Given bridge networking, when a process inside the container tries to connect to the Docker gateway IP, then the connection is blocked
6. Given bridge networking, when a process inside the container tries to connect to `host.docker.internal`, then the connection is blocked
7. Given bridge networking, when a process inside the container tries to connect to `api.anthropic.com`, then the connection succeeds

**Depends On:** REQ-AS-002

---

<a id="req-nr-001"></a>
### REQ-NR-001: Outbound Network Allowlist

The tool restricts container outbound network access to a declared list of hosts.

**Status:** Approved

**Input:**
- `--allowed-hosts` flag (repeatable): list of hostnames or IP addresses the container may reach. CLI-only — not supported as a `devcontainer.json` field because untrusted repositories must not control their own network allowlist.

**Output:**
- Container that can only reach the declared hosts and DNS; all other outbound traffic is blocked

**Behavior:**

When `--allowed-hosts` is specified, the tool blocks all outbound network traffic from the container except to the declared hosts and DNS resolution. The container can resolve hostnames and connect to allowed hosts. All other outbound connections are dropped.

A non-root user inside the container cannot disable or modify the network restrictions.

For Claude Code, the allowed hosts are:

- `api.anthropic.com`
- `statsig.anthropic.com`
- `sentry.io`

This is the only capability exception to NG-6 beyond REQ-CO-009. An ADR is required to document this trade-off.

**Implementation:** iptables default-deny OUTPUT chain with ACCEPT exceptions. See [ADR: Outbound Network Allowlist](adr/2026-04-12-outbound-network-allowlist-via-iptables.md) and [system-design.md#outbound-allowlist](system-design.md#outbound-allowlist).

**Acceptance Criteria:**
1. Given `--allowed-hosts api.anthropic.com`, when the container runs, then it can reach `api.anthropic.com`
2. Given `--allowed-hosts api.anthropic.com`, when the container tries to reach `evil.com`, then the connection is blocked
3. Given no `--allowed-hosts`, when the container runs, then no firewall rules are applied (bridge networking, full access per REQ-CO-009)
4. Given `--allowed-hosts`, when a non-root user inside the container attempts to disable the restrictions, then the attempt fails
5. Given `--allowed-hosts`, when the container is inspected, then `NET_ADMIN` is the only added capability beyond REQ-CO-009

**Depends On:** REQ-CO-009

**Design Rationale:** [ADR: Outbound Network Allowlist](adr/2026-04-12-outbound-network-allowlist-via-iptables.md) — `NET_ADMIN` exception to NG-6

---


### Assistant management

The centralized `~/.confine-ai/` layout and the `confine-ai <assistant-name>` shortcut that starts or reconnects to an assistant container in a single invocation. This group also covers the assistant-aware directory structure, per-assistant container identity, the init command with its overwrite-with-confirm flow, and the status command that lists all confine-ai-managed containers on a host.

#### Overview

Before reading the individual requirements in this group, the following short model describes the whole surface. Every requirement is a detailed spec for one piece of it.

**Two images.** Every assistant container boots from the same two-layer image stack:

| Image | Source | Built by | Purpose |
|-------|--------|----------|---------|
| `localhost/confine-ai-base:latest` | `~/.confine-ai/base/Dockerfile` | `confine-ai <assistant>` (auto-build on first run when absent), `confine-ai update base` | Toolchain layer: Go, Java, common system packages. Shared across all assistants. |
| `confine-ai-assistant-<name>:latest` | `~/.confine-ai/assistants/<name>/Dockerfile` | `confine-ai <assistant>` (auto-build on first run when absent), `confine-ai update <assistant>` | Assistant layer: CLI binary + assistant-specific dependencies. One per assistant. `FROM localhost/confine-ai-base:latest`. |

Both images live only in the local container runtime's image store. `confine-ai-base:latest` is `localhost/`-qualified to satisfy Podman's strict short-name resolution; the `localhost/` prefix is safe on all supported runtimes.

**Two directory trees per assistant.** `~/.confine-ai/` is split into config and state, and the two have different lifetimes:

| Tree | Example | Safe to overwrite? | Owner |
|------|---------|-------------------|-------|
| `~/.confine-ai/assistants/<name>/` | `Dockerfile`, `devcontainer.json` | Yes — rewritten from the built-in template on `init -y` | confine-ai |
| `~/.confine-ai/data/<name>/` | Default config seed files, OAuth tokens, CLI config, shell history | **Never** — seed files are written only when absent; existing files are never removed or modified by any confine-ai command | the user and the assistant at runtime |

The split makes "recreate the setup" and "reset the assistant state" two distinct operations. Recreate is `confine-ai init -y <assistant>`. Reset is a manual `rm -rf ~/.confine-ai/data/<assistant>/` the user drives.

**Two file writers.** Only two commands write under `~/.confine-ai/`:

1. **`confine-ai init [-y] [assistant]`** — writes the base Dockerfile and (when given an assistant name) the assistant's config directory from the built-in templates. Full overwrite, no merging. See REQ-AS-003.
2. **`confine-ai update base`** — rewrites only the value portion of marker-annotated `ARG` lines in `~/.confine-ai/base/Dockerfile`. Byte-preserving otherwise. See REQ-AS-008.

Every other command either reads `~/.confine-ai/` or writes into the runtime's image store — never directly into `~/.confine-ai/`.

**Three commands.** The assistant-management command surface is:

| Command | What it does | File writes | Image writes |
|---------|-------------|-------------|--------------|
| `confine-ai init [-y] [assistant]` | Create or recreate configuration files | Base Dockerfile, assistant config | None |
| `confine-ai <assistant>` | Start or reconnect to an assistant container | None | Base + assistant image (only if missing on first run) |
| `confine-ai update [target...]` | Least-work refresh: version-compare upstream; rebuild only what's out of date | Base Dockerfile (value rewrites only, when upstream differs) | Base and/or assistant images (only when something actually changed) |

All three commands converge on the same two images listed in the table. The steady-state invariant is that `confine-ai update` is the only path that should be run routinely; `init` handles first install and recreate, and the assistant shortcut auto-builds on first use.

**Supported assistants.**

| Assistant | `~/.confine-ai/assistants/<name>/` | Upstream version source | Least-work rule (REQ-AS-008) |
|-------|---|---|---|
| `claude` | `claude/` | npm: `@anthropic-ai/claude-code` at `latest` | Active |
| `copilot` | `copilot/` | *(probe not yet declared)* | Rebuilds unconditionally (tracked gap) |
| `opencode` | `opencode/` | *(probe not yet declared)* | Rebuilds unconditionally (tracked gap) |

Assistant names are lowercase-alphanumeric with hyphens (see REQ-AS-001). Unknown names are accepted for `init`, which scaffolds a generic template the user must customize. The table lists the assistants with built-in templates; adding a built-in template is a per-assistant change and does not require modifying unrelated requirements.

<a id="req-as-001"></a>
### REQ-AS-001: Centralized Assistant Configuration Directory

Assistant configurations live in a fixed directory structure under `~/.confine-ai/`.

**Status:** Proposed

**Input:**
- `assistant-name` (string): Name of the assistant (e.g., `claude`, `copilot`, `opencode`)

**Output:**
- Configuration path: `~/.confine-ai/assistants/<assistant-name>/devcontainer.json`
- Data path: `~/.confine-ai/data/<assistant-name>/` (credential storage, bind-mounted into containers)

**Behavior:**

The tool uses this directory structure:

| Path | Purpose |
|------|---------|
| `~/.confine-ai/assistants/<name>/devcontainer.json` | Assistant-specific devcontainer configuration |
| `~/.confine-ai/assistants/<name>/Dockerfile` | Assistant-specific Dockerfile (referenced by devcontainer.json) |
| `~/.confine-ai/data/<name>/` | Persistent data directory (credentials, config); bind-mounted into the container |

The tool resolves `~` to the current user's home directory. The `assistants/` directory contains configuration files. The `data/` directory contains persistent state (credentials, tool config) that is bind-mounted into containers. These directories are separate to allow distinct backup and permission policies.

Assistant names must be lowercase alphanumeric with hyphens, conforming to the format and length constraints defined in [system-design.md#constants](system-design.md#constants). Single-character assistant names are not allowed to prevent collision with CLI flags.

**Acceptance Criteria:**
1. Given assistant name `claude`, when the tool resolves the config path, then it returns `~/.confine-ai/assistants/claude/devcontainer.json`
2. Given assistant name `claude`, when the tool resolves the data path, then it returns `~/.confine-ai/data/claude/`
3. Given assistant name `CLAUDE` (uppercase), when validated, then the tool reports an error (names are lowercase only)
4. Given assistant name `a` (single character), when validated, then the tool reports an error
5. Given assistant name `my_assistant` (underscore), when validated, then the tool reports an error
6. Given `~/.confine-ai/assistants/claude/` does not exist, when the tool looks up assistant `claude`, then it reports the directory is missing with a suggestion to run `confine-ai init claude`

---

<a id="req-as-002"></a>
### REQ-AS-002: Assistant Shortcut Invocation

`confine-ai <assistant-name>` starts or reconnects to an assistant container for the current workspace.

**Status:** Proposed

**Input:**
- `assistant-name` (string): First positional argument that does not match a known subcommand
- Current working directory: Used as the workspace folder
- Arguments after `--`: Passed through to the assistant command inside the container

**Output:**
- If no container exists for the (assistant, folder-set) pair: starts a new container and attaches interactively to the assistant's default command
- If a container exists and the config hash matches: reconnects and attaches interactively to the assistant's default command
- If a container exists but the config hash differs: recreates the container (stop, remove, create new) and attaches
- Assistant arguments after `--` are appended to the assistant command

**Behavior:**

The tool treats the first positional argument as an assistant name when it does not match a known subcommand (`init`, `status`, `update`, `rm`, `completion`). It loads the assistant's `devcontainer.json` from `~/.confine-ai/assistants/<name>/`.

The current working directory is the workspace folder. No `--workspace-folder` flag is needed (though it still works as an override).

Container identity for assistant containers uses the (assistant-name, folder-set) pair (REQ-AS-004). This allows multiple assistants with the same folder set and the same assistant with different folder sets to run simultaneously.

The assistant command is determined from the assistant configuration. The default command runs the assistant binary (e.g., `claude` for the Claude Code assistant). Arguments after `--` are appended to this command.

**Reconnect path with config-hash validation:** When a container already exists for the (assistant, folder-set) pair, the tool reads the container's stored config hash label and compares it against the config hash computed from the current configuration. If the hashes match, the tool reconnects: it starts the container if stopped, then execs in. If the hashes differ, the configuration has changed since the container was created. The tool stops and removes the existing container, then creates a new container with the current configuration. This ensures configuration changes (image, resource limits, mounts, allowed hosts) take effect on the next invocation without requiring a manual `confine-ai rm`.

When no container exists, the tool creates and starts a new container and then attaches.

**Image consumption (single-owner model).** The assistant shortcut is a *consumer* of pre-built assistant images. It always launches the container from the canonical assistant tag `confine-ai-assistant-<name>:latest` and never derives an image tag from any other input (workspace folder, basename, `devcontainer.json` `build` block, or any other source). This is the same tag that `confine-ai update <assistant>` (REQ-AS-008) writes. Ownership of the tag's contents belongs to `update`; the shortcut path must not rebuild the image, rewrite its layers, or produce any variant tag. The shortcut therefore ignores any `build.dockerfile` / `build.context` / `build.args` fields that the assistant's scaffolded `devcontainer.json` may declare for the purpose of selecting an image — those fields describe how `update` rebuilds the image and are not a shortcut-invocation input.

**Auto-ensure on first use.** On first invocation, if the base image `localhost/confine-ai-base:latest` is absent from the local runtime, the tool auto-builds it from `~/.confine-ai/base/Dockerfile` (or, when that file is absent, from the embedded seed) before creating the container. If the assistant image `confine-ai-assistant-<name>:latest` is absent from the local image store, the tool auto-builds it from `~/.confine-ai/assistants/<name>/Dockerfile` before creating the container. The assistant auto-ensure is a **cached** build — the container runtime's layer cache is honored and `--no-cache` is not set. Cache-busting is exclusively the responsibility of `confine-ai update <assistant>` (REQ-AS-008); the shortcut auto-ensure is a first-use ensure, not a refresh. Before invoking the builder, the shortcut emits a single breadcrumb to stderr naming the image being built, parallel to the base-image auto-ensure breadcrumb — first builds can take minutes (base pull plus install script) and silent blocking would look like a hang. The auto-ensure path never interprets `build.args`, `build.context`, or any other field from the assistant's `devcontainer.json`; it invokes the same fixed-Dockerfile build helper that `confine-ai update <assistant>` uses, so the shortcut and `update` produce byte-identical images by construction. A user editing `build.args` cannot silently diverge the two paths. The auto-ensure paths exist solely to let first-run invocations succeed without requiring the user to run `confine-ai update` first; every subsequent invocation must find both images already present and skip the ensure step. The assistant auto-ensure produces the canonical tag `confine-ai-assistant-<name>:latest` — the single tag shared with `update`. No other tag is ever produced by the shortcut path.

The attach step allocates a TTY and connects stdin when the caller's stdin is a terminal, enabling interactive assistant sessions.

**Acceptance Criteria:**
1. Given `confine-ai claude` in `/home/user/project-a`, when no container exists for (claude, [/home/user/project-a]), then the tool creates a new container using `~/.confine-ai/assistants/claude/devcontainer.json` and attaches to the assistant command
2. Given `confine-ai claude` in `/home/user/project-a`, when a running container exists for (claude, [/home/user/project-a]) and the config hash matches, then the tool reconnects and attaches without creating a new container
3. Given `confine-ai claude -- --continue`, when the container runs, then `--continue` is passed as an argument to the assistant command
4. Given `confine-ai claude` and `confine-ai copilot` in the same directory, then two separate containers run simultaneously
5. Given `confine-ai claude` in `/home/user/project-a` and `confine-ai claude` in `/home/user/project-b`, then two separate containers run simultaneously
6. Given `confine-ai nonexistent` where `~/.confine-ai/assistants/nonexistent/` does not exist, then the tool reports an error with suggestion: `run 'confine-ai init nonexistent' to create assistant configuration`
7. Given `confine-ai claude` with `--workspace-folder /other/path`, then `/other/path` is used as the workspace instead of the current directory
8. Given `confine-ai claude` and the base image is absent from the local runtime, then the tool builds the base image before creating the container
9. Given `confine-ai claude` when a stopped container exists and the config hash matches, then the tool starts the container and reconnects without recreating it
10. Given `confine-ai claude` when a container exists (running or stopped) and the config hash differs from the current configuration, then the tool stops, removes, and recreates the container with the current configuration before attaching
11. Given `confine-ai claude` when a container exists and the config hash differs, then the tool writes a message to stderr indicating the container is being recreated due to a configuration change
12. Given `confine-ai claude` runs in a workspace named `project-a` and the assistant image `confine-ai-assistant-claude:latest` is present, then the container is created from the image reference `confine-ai-assistant-claude:latest` and the tool does not invoke the image-build path of the container runtime
13. Given `confine-ai claude` runs in any workspace, then the tool never builds, tags, or consumes an image whose tag is derived from the workspace folder name (no `confine-ai-<workspace-basename>:latest` or similar tag is created)
14. Given `confine-ai update claude` has just rewritten the `confine-ai-assistant-claude:latest` image with a new CLI version, when `confine-ai claude` is invoked next, then the launched container runs the CLI version that `update` installed (the shortcut and `update` operate on the same image tag)
15. Given `confine-ai claude` runs for the first time after `confine-ai init claude` with no intervening `confine-ai update`, when the assistant image `confine-ai-assistant-claude:latest` is absent from the local image store, then the tool builds it from `~/.confine-ai/assistants/claude/Dockerfile`, tags it as `confine-ai-assistant-claude:latest`, and proceeds to create the container
16. Given `confine-ai claude` runs and `confine-ai-assistant-claude:latest` is already present in the local image store, then the tool does not invoke any image-build step for the assistant image and proceeds directly to the container-create step
17. Given `confine-ai claude` triggers an assistant-image auto-ensure (the image is absent), then the tool writes a single breadcrumb line to stderr naming the image being built (parallel to the base-image auto-ensure breadcrumb emitted when the base image is absent) before the builder is invoked, so that the user sees the reason for the upcoming multi-minute wait
18. Given `confine-ai claude` triggers an assistant-image auto-ensure, then the build honors the container runtime's layer cache; `--no-cache` is not passed by the shortcut under any circumstance (cache-busting is exclusively the contract of `confine-ai update <assistant>`, REQ-AS-008)
19. Given `~/.confine-ai/assistants/claude/devcontainer.json` declares a `build` block with `args` or `context` fields and the assistant image is absent, when `confine-ai claude` triggers the auto-ensure, then the tool ignores those fields entirely, builds from the fixed path `~/.confine-ai/assistants/claude/Dockerfile`, and produces an image byte-identical to what `confine-ai update claude` would produce from the same source tree on the same host

**Depends On:** REQ-AS-001, REQ-AS-003, REQ-AS-004, REQ-AS-008

**Design Rationale:**
- [ADR: Assistant Image Tag Single-Owner Model](adr/2026-04-17-assistant-image-tag-single-owner.md) — single-owner tag ownership, cache-policy split, fixed-Dockerfile invariant that binds the shortcut and `update` to one canonical tag.

---

<a id="req-as-003"></a>
### REQ-AS-003: Assistant Init Command

`confine-ai init [-y] [assistant-name]` is the single command for creating or recreating a confine-ai setup. It seeds `~/.confine-ai/base/Dockerfile` and, when an assistant name is given, scaffolds that assistant's configuration. The same command handles first install, seed refresh after a binary upgrade, and full recreate from a wiped `~/.confine-ai/` directory.

**Status:** Proposed

**Input:**
- `assistant-name` (string, optional, positional): Name of the assistant to initialize. When omitted, only the base Dockerfile is handled.
- `-y` / `--yes` (boolean, optional): Accept overwrite prompts without asking. Required when stdin is not a terminal and the target files already exist.

**Output:**
- `~/.confine-ai/base/Dockerfile` written from the embedded seed (if absent, or if overwrite is accepted).
- `~/.confine-ai/assistants/<name>/` scaffolded from the built-in template (if absent, or if overwrite is accepted). Contains `devcontainer.json` and `Dockerfile`.
- `~/.confine-ai/data/<name>/` created on first install with assistant-specific default configuration files seeded into it. Never removed or touched on an overwrite — credentials, tokens, configuration edits, and shell history inside it are preserved across every invocation of `confine-ai init`.

**Behavior:**

The tool ships built-in templates for the known assistants listed in the Supported Assistants table at the top of the Assistant management group. Templates are bundled with the tool at build time. Unknown assistant names receive a generic template with placeholder values that the user must customize. Assistant names are validated against the naming pattern in [system-design.md#constants](system-design.md#constants); invalid names are rejected with an error and exit `1`.

The command walks two steps in order: **base** (`~/.confine-ai/base/Dockerfile`), then **assistant** (`~/.confine-ai/assistants/<name>/` and `~/.confine-ai/data/<name>/`, when an assistant name is given). Base runs before assistant so that a seed failure (disk full, permission) halts the command before any assistant directory is touched.

Each step follows the same overwrite rule:

| Precondition | Interactive (TTY), `-y` unset | Non-interactive, `-y` unset | `-y` set |
|---|---|---|---|
| Target does not exist | Create | Create | Create |
| Target exists | Prompt `Overwrite? [Y/n]`; default yes; any other answer leaves target unchanged | Report "already present"; leave target unchanged; exit `0` | Overwrite without prompting |

On assistant overwrite, the tool removes `~/.confine-ai/assistants/<name>/` and rewrites it from the built-in template. It **never** removes or touches `~/.confine-ai/data/<name>/`. This makes "recreate the assistant config" and "reset the assistant state" two distinct operations: the first is `confine-ai init -y <assistant>`, the second is manually deleting the data directory. The PRD does not define a "reset state" command because the data directory is the user's work product and the tool does not own it.

The tool creates intermediate directories (`~/.confine-ai/`, `~/.confine-ai/base/`, `~/.confine-ai/assistants/`, `~/.confine-ai/data/`) with permissions `0o755` if they do not exist.

Built-in templates include `customizations.confine-ai` with default resource limits of `memory: "8g"` and `cpus: "4"`. Users can edit the generated `devcontainer.json` to adjust these values.

*Default configuration seeding.* Known assistants may declare a default configuration file in their data directory so the assistant starts with a valid configuration on first launch. The tool seeds this file when the data directory is created and on subsequent assistant startup if the file is missing. The seed file is never overwritten once present, so user edits are preserved. Assistants that declare no seed file receive an empty data directory. The goal is zero-friction first launch: `confine-ai init <assistant>` followed by `confine-ai <assistant>` starts the assistant without further manual configuration.

**Acceptance Criteria:**
1. Given a wiped `~/.confine-ai/` directory, when `confine-ai init claude` runs (with or without `-y`), then the tool creates `~/.confine-ai/base/Dockerfile`, `~/.confine-ai/assistants/claude/devcontainer.json`, `~/.confine-ai/assistants/claude/Dockerfile`, and `~/.confine-ai/data/claude/` (with default configuration seed files), and exits with code `0`.
2. Given `~/.confine-ai/assistants/claude/` exists, when `confine-ai init claude` runs in a non-interactive shell without `-y`, then the tool reports the directory is already present, makes no changes, and exits with code `0`.
3. Given `~/.confine-ai/assistants/claude/` exists with user modifications and `~/.confine-ai/data/claude/` contains credential files, when `confine-ai init -y claude` runs, then `~/.confine-ai/assistants/claude/` is rewritten from the built-in template and `~/.confine-ai/data/claude/` is byte-identical (file contents, file modes, and mtime) to its pre-run state.
4. Given `~/.confine-ai/base/Dockerfile` exists, when `confine-ai init -y` runs with no assistant name, then the base file is overwritten from the current embedded seed and no assistant directory is touched.
5. Given `confine-ai init custom-assistant` (unknown name, valid per the naming pattern), then the tool creates a generic template with placeholder values.
6. Given `confine-ai init invalid_name` (invalid characters), then the tool reports a validation error and exits with code `1`.
7. Given `confine-ai init claude`, when the tool creates `devcontainer.json`, then the file contains `customizations.confine-ai` with `memory` and `cpus` defaults.
8. Given a completely wiped `~/.confine-ai/`, when `confine-ai init -y claude` is run followed by `confine-ai claude`, then the assistant shortcut invocation succeeds end-to-end with no additional setup commands required.
9. Given `confine-ai init claude`, when the tool creates `~/.confine-ai/data/claude/`, then a default configuration seed file is written into the data directory.
10. Given `confine-ai init opencode`, when the tool creates `~/.confine-ai/data/opencode/`, then a default configuration seed file is written into the data directory.
11. Given `~/.confine-ai/data/opencode/opencode.json` already exists with user modifications, when `confine-ai init -y opencode` runs, then the seed file is byte-identical to its pre-run state.
12. Given `~/.confine-ai/data/claude/` exists but the seed file is missing (assistant initialized before seed support), when `confine-ai claude` starts, then the seed file is created before the container launches.

**Depends On:** REQ-AS-001

---

<a id="req-as-004"></a>
### REQ-AS-004: Assistant Container Identity

Assistant containers use a composite identity of (assistant-name, folder-set) for container labeling and lookup.

**Status:** Proposed

**Input:**
- `assistant-name` (string): Name of the assistant
- `folder-set` (path[]): Absolute paths to all mounted folders (primary + additional). Single-folder invocations pass one path.

**Output:**
- Container labels that uniquely identify the (assistant, folder-set) pair

**Behavior:**

When creating a container via the assistant shortcut, the tool applies an additional label for the assistant name, alongside the folder-set labels (REQ-CO-001). Container lookups for assistant operations filter by both the assistant name label and the folder-set identifier label.

This composite identity allows:
- Multiple assistants with the same folder set (e.g., `claude` and `copilot` in [/home/user/project-a])
- The same assistant with different folder sets (e.g., `claude` in [/home/user/project-a] and `claude` in [/home/user/project-a, /home/user/lib])

`confine-ai rm <assistant-name>` stops only the container matching the (assistant, folder-set) pair. `confine-ai rm` without an assistant name stops all confine-ai-managed containers for the folder set.

**Acceptance Criteria:**
1. Given assistant `claude` with folder set [/home/user/project-a], when the container is created, then it has labels for both the assistant name and the folder-set identifier
2. Given assistant `claude` and assistant `copilot` with the same folder set, when querying for `claude`, then only the `claude` container is returned
3. Given assistant `claude` with two different folder sets, when querying for `claude` with folder set A, then only folder set A's container is returned
4. Given `confine-ai rm claude` with a folder set, then only the `claude` container for that folder set is stopped and removed
5. Given `confine-ai rm` (no assistant name) with a folder set containing both a `claude` container and a `copilot` container, then both are stopped and removed

**Depends On:** REQ-CO-001

---

<a id="req-as-005"></a>
### REQ-AS-005: Assistant Status Command

`confine-ai status` lists running assistant containers with metadata.

**Status:** Proposed

**Input:**
- None (lists all confine-ai-managed containers on the host)

**Output:**
- Table of running containers with: assistant name, workspace path, container ID (short), and status

**Behavior:**

The tool queries the container runtime for all containers with confine-ai labels (REQ-CO-001). For each container, it displays:

| Column | Source |
|--------|--------|
| Assistant | Assistant name label |
| Workspace | Workspace path from the local folder label |
| Container | Short container ID (first 12 characters) |
| Status | Container status (e.g., running, stopped) |

The output is a formatted text table written to stdout. If no confine-ai-managed containers exist, the tool reports that no containers are found.

**Acceptance Criteria:**
1. Given 2 running assistant containers for different workspaces, when `confine-ai status` runs, then both appear in the output
2. Given no confine-ai-managed containers, when `confine-ai status` runs, then it reports no containers found
3. Given a running `claude` container for workspace `/home/user/project-a`, when `confine-ai status` runs, then the output shows assistant `claude`, workspace `/home/user/project-a`, and the container ID

**Depends On:** REQ-CO-001

---


### Base image and update

The user-owned `~/.confine-ai/base/Dockerfile` and the single `confine-ai update` command that keeps both the base image and every scaffolded assistant image current. The update command is the least-work path: it probes upstreams, rewrites or rebuilds only when something changed, and short-circuits assistant rebuilds when the installed CLI version already matches upstream.

<a id="req-as-006"></a>
### REQ-AS-006: Base Image and User-Owned Dockerfile

The user-owned base Dockerfile at `~/.confine-ai/base/Dockerfile` is the authoritative base image definition for every build. The embedded seed is the starting template — seeded on first install by `confine-ai init`, reapplied via `confine-ai init -y`, and value-rewritten (but never wholesale replaced) by `confine-ai update base`. confine-ai builds the base image (`localhost/confine-ai-base:latest`) from this file on first assistant shortcut invocation (when the image is absent) and on every successful `confine-ai update base`.

**Lifecycle overview (for readers of REQ-AS-006 and REQ-AS-008).** The user-facing flow is:

1. **`confine-ai init [-y] [assistant]`** (REQ-AS-003) — creates or recreates the configuration files under `~/.confine-ai/`. Overwrite is prompted (default yes) or accepted via `-y`. The `~/.confine-ai/data/<assistant>/` directory is preserved across every overwrite, so credentials survive.
2. **`confine-ai <assistant>`** (REQ-AS-002) — first invocation builds `localhost/confine-ai-base:latest` if absent, builds `confine-ai-assistant-<name>:latest` if absent, then starts the container. Subsequent invocations reconnect.
3. **`confine-ai update [target...]`** (REQ-AS-008) — the least-work path. For base: probe upstreams, rewrite Dockerfile if anything changed, rebuild image if rewrite happened (otherwise keep the existing image). For an assistant: consult the least-work probe; if installed matches upstream, skip the rebuild; otherwise rebuild the assistant image without `--pull`.

All three commands converge on the same two images: `localhost/confine-ai-base:latest` and `confine-ai-assistant-<name>:latest`. These tags are the single points of truth for the base and assistant images respectively — no other image tag is produced by any command in the `confine-ai` surface for the purpose of running an assistant. The only file writers are `confine-ai init` (seeds, full overwrite) and `confine-ai update base` (marker-driven value rewrite). The assistant shortcut auto-build path writes image layers only, never Dockerfile bytes, and writes them only under the canonical tags listed above.

**Status:** Approved

**Design Rationale:**
- See [ADR: Managed Dockerfile Line Classification via Comment Markers](adr/2026-04-12-managed-dockerfile-classification.md) — the marker contract this requirement defines and REQ-AS-008 consumes.

**Input:**
- Embedded seed Dockerfile (compiled into the binary).
- User copy path: `~/.confine-ai/base/Dockerfile`.
- Assistant shortcut invocation (auto-build trigger when the base image is absent).
- `confine-ai init` invocation (seed placement and overwrite, see REQ-AS-003).
- `confine-ai update base` invocation (marker-driven value rewrite, see REQ-AS-008).

**Output:**
- File at `~/.confine-ai/base/Dockerfile` — created on first `confine-ai init`, overwritten by `confine-ai init -y`, value-rewritten in place by `confine-ai update base`, never modified by the assistant shortcut.
- Base image tagged `localhost/confine-ai-base:latest` in the local container runtime.

**Behavior:**

*Authoritative source.* The tool treats `~/.confine-ai/base/Dockerfile` as the source of truth for every build. The embedded seed is the starting template and is never modified at runtime; it is written to the user copy path on first install and can be reapplied via `confine-ai init -y`. Seed placement, the overwrite prompt, and the credential-survival stability model are defined in REQ-AS-003 and are not re-stated here.

*Auto-build on first use.* During assistant shortcut invocation, before creating the container, the tool checks whether the base image exists in the local runtime. If it does not exist, the tool builds it automatically from the resolved base Dockerfile. If it exists, the tool skips the build.

*Build path resolution.* The assistant shortcut auto-build path reads the base Dockerfile from `~/.confine-ai/base/Dockerfile` when present, and falls back to the embedded seed when absent (silently, because the first-run build output already makes the action visible). `confine-ai update base` does not fall back to the embedded seed; it requires the user-owned file to exist and reports an error otherwise.

*Checksum verification in the seed.* The embedded seed Dockerfile declares `ARG GO_SHA256_AMD64`, `ARG GO_SHA256_ARM64`, `ARG CORRETTO_SHA256_AMD64`, and `ARG CORRETTO_SHA256_ARM64` alongside the corresponding version ARGs. The Go and Java download steps compute the sha256 of the downloaded archive and compare it to the expected value before extraction. A mismatch aborts the image build. The seed ships with the values for `GO_VERSION=1.26.0` and `CORRETTO_VERSION=25.0.2.10.1`. Per-architecture sha256 values are selected by the same `dpkg --print-architecture` switch that selects the download URL.

The seed ships with Amazon Corretto as the Java distribution. The sha256 ARGs for Java are the sha256 values of the Corretto archives at the pinned version. If the user edits the seed to swap Corretto for a different Java distribution (Temurin, Zulu, Microsoft Build of OpenJDK, Liberica, etc.), the user becomes responsible for updating the download URL and the checksum.

*Managed line markers.* The embedded seed uses structured comment markers of the form `# confine-ai:managed tool=<tool> kind=<kind> [arch=<arch>] [distribution=<distribution>]` immediately preceding each managed line (the single `FROM` line and each managed `ARG`). The marker grammar is recorded in [ADR: Managed Dockerfile Classification](adr/2026-04-12-managed-dockerfile-classification.md) and is consumed by `confine-ai update base` (REQ-AS-008).

*Future-compatibility constraints consumed by REQ-AS-008.* Three contracts the base Dockerfile layout preserves so the update command does not need hardcoded assumptions:

1. **The `FROM` line is user-editable.** The seed uses `debian:bookworm-slim`, but users may edit the `FROM` line to any base image (ubuntu, alpine, fedora, internal registries, etc.). `confine-ai update base` must act on whatever `FROM` the user has written and never rewrite the line itself.
2. **The LTS update policy is Java-specific, not global.** `confine-ai update base` applies a "track latest LTS major, prompt on major jumps" rule only when the tool being updated is a Java distribution. All other managed tools (Go, base image, etc.) use "latest stable, no prompt." The marker convention (`tool=java` vs other values) carries this classification.
3. **Any Java distribution must be supported, not just Corretto.** A user who swaps Corretto for Temurin, Zulu, Microsoft Build of OpenJDK, Liberica, or another OpenJDK distribution must still be recognizable as "this ARG is a Java distribution" so the LTS rule fires. The marker convention carries `distribution=<name>` so classification does not pattern-match distribution-specific strings.

**Constraints:**
- Base Dockerfile path: `~/.confine-ai/base/Dockerfile`.
- Directory permissions: `0o755`.
- File permissions: `0o644`.
- The `FROM` line appears once, on its own line, with no build-stage aliasing in the seed, so REQ-AS-008's line-rewrite implementation can locate and update it unambiguously.
- Each managed ARG appears on its own line with no inline expression, so the update command can rewrite the value without reparsing shell syntax.
- Each managed line is preceded by exactly one `# confine-ai:managed` marker with no blank line between them.
- The assistant shortcut auto-build path never modifies `~/.confine-ai/base/Dockerfile` — only reads it (or falls back to the seed).

**Acceptance Criteria:**
1. Given `confine-ai claude` and the base image does not exist, then the tool builds the base image before creating the container.
2. Given `confine-ai claude` and the base image exists, then the tool skips the base image build.
3. Given `~/.confine-ai/base/Dockerfile` exists with user modifications, when `confine-ai update base` runs, then the tool builds the base image from the user file.
4. Given `~/.confine-ai/base/Dockerfile` does not exist, when assistant shortcut invocation triggers auto-build, then the tool builds from the embedded seed without emitting the fallback message.
5. Given the embedded seed Dockerfile, when the Go archive download has a sha256 mismatch, then the image build aborts with a non-zero exit code before extraction.
6. Given the embedded seed Dockerfile, when the Java archive download has a sha256 mismatch, then the image build aborts with a non-zero exit code before extraction.
7. Given the embedded seed Dockerfile with the pinned Go version (`1.26.0`) and the pinned Corretto version (`25.0.2.10.1`), when the assistant shortcut triggers auto-build, then both downloads pass sha256 verification and the image builds.
8. Given the seed Dockerfile, when REQ-AS-008's classifier inspects the file, then each managed ARG can be classified as `java` or `other` by reading the marker's `tool=` field without pattern-matching distribution-specific strings such as `corretto`.
9. Given the seed Dockerfile, when REQ-AS-008's classifier inspects the file, then the `FROM` line is locatable by the `# confine-ai:managed tool=base-image kind=image` marker, enabling unambiguous identification (even though REQ-AS-008 never rewrites the `FROM` line).

**Implementation:** See [system-design.md#managed-dockerfile-markers](system-design.md#managed-dockerfile-markers) for the marker grammar and [system-design.md#ensurebaseimage](system-design.md#ensurebaseimage) for the auto-build path.

**Depends On:** REQ-RT-001, REQ-AS-002, REQ-AS-003

---

<a id="req-as-008"></a>
### REQ-AS-008: Update Command

`confine-ai update` is the single verb for keeping confine-ai-managed container images current. It has two modes behind one command. A base update is a marker-driven rewrite of `~/.confine-ai/base/Dockerfile` that bumps pinned tool versions and their verified sha256 values, then rebuilds `localhost/confine-ai-base:latest`. An assistant update is a cache-bust rebuild of a scaffolded assistant image (`~/.confine-ai/assistants/<name>/Dockerfile`) that re-fetches any unversioned upstream content the assistant's Dockerfile installs. A no-argument invocation walks base first and then every scaffolded assistant.

**How `confine-ai update` works.** `confine-ai update` walks one or more targets in a fixed order: `base` first, then every scaffolded assistant alphabetically. Each target is one of two kinds. A **base target** reads `~/.confine-ai/base/Dockerfile`, probes the pinned upstream version for every marker-annotated managed group (REQ-AS-006 marker contract), and rewrites the file in place when any value has changed. An **assistant target** reads the assistant's local image, runs the assistant's version probe if one is declared (assistants declare their own per-assistant probes in scaffold templates; the base marker contract does not apply), and rebuilds the image only when the installed version differs from upstream.

Both targets share one principle: the rebuild runs only when it is the least work needed to reach the user's intent. A base with no upstream changes keeps its existing image. An assistant whose installed CLI already matches upstream is reported as `unchanged` and its rebuild is skipped entirely. Every probe failure (network, parse, missing image) falls through to "rebuild anyway" — the least-work rule never fails an update; it only short-circuits one.

The command is atomic at the target level. A base update is all-or-nothing across every managed group; an assistant update is a single rebuild that either succeeds or is recorded as `failed`. The walk halts if the base target fails but continues past individual assistant failures, exiting with the highest-severity code observed across every attempted target. Dry-run mode (`--dry-run`) reports what a real run would do without invoking the container runtime, and still propagates probe failures as real exit codes so CI preflight catches network breakage. The rest of this requirement specifies each piece of this shape in detail: command surface, source-of-truth rules, per-target behavior, atomicity, dry-run semantics, and the edge-case table.

**Status:** Approved

**Design Rationale:**
- [ADR: Managed Dockerfile Line Classification via Comment Markers](adr/2026-04-12-managed-dockerfile-classification.md) — the marker contract consumed by base updates.
- [ADR: Outbound HTTP Trust Boundary](adr/2026-04-12-outbound-http-trust-boundary.md) — authoritative upstreams, TLS requirements, proxy and offline behavior, and sha256 cross-verification for `confine-ai update`'s host-side HTTP.
- [ADR: Assistant Image Tag Single-Owner Model](adr/2026-04-17-assistant-image-tag-single-owner.md) — single-owner tag ownership, cache-policy split, and fixed-Dockerfile invariant shared with the shortcut's first-use auto-ensure.
- [REQ-AS-006](#req-as-006) future-compatibility constraints — the `FROM` line is user-editable, the LTS update policy is Java-specific, and any Java distribution must be classifiable by marker field.

**Input:**
- `targets` (string[], optional, positional): Zero or more update targets. Each target is the literal `base` or the name of an assistant scaffolded under `~/.confine-ai/assistants/<name>/`. When omitted, the tool updates `base` first and then every scaffolded assistant.
- `--dry-run` (boolean, optional): Report what would change without writing to the Dockerfile and without invoking the container runtime. Upstream probing and sha256 fetching still execute so that dry-run catches upstream failures in CI preflight.
- `--yes` (boolean, optional): Auto-accept interactive prompts. Currently this applies only to Java major-version jump prompts during a base update. Non-interactive callers use this flag to proceed without a terminal.
- `~/.confine-ai/base/Dockerfile` (read and rewritten for a base update; not read for assistant updates).
- `~/.confine-ai/assistants/<name>/Dockerfile` (read for an assistant update; never rewritten).
- Terminal state of stdin (governs whether the Java major-jump prompt is shown or the group is implicitly skipped).

**Output:**
- Rewritten `~/.confine-ai/base/Dockerfile` on a successful base update when at least one managed group's value changed and the run is not a dry-run.
- Rebuilt `localhost/confine-ai-base:latest` image on a successful base update.
- Rebuilt assistant image on a successful assistant update.
- Stale confine-ai-managed containers dropped so that the next shortcut invocation picks up the rebuilt image. A base update drops all confine-ai-managed containers; an assistant update drops only containers for that assistant.
- A per-target summary written to stdout reporting target name and action (`updated`, `unchanged`, `skipped`, `failed`, or `would update` in dry-run), and for base updates the per-group version deltas. Each target's summary is emitted inline immediately after that target finishes, never batched at the end of the run, so that any inline stdout the target produces (e.g. the claude version-gate line `claude already at <version>`) stays grouped with its own summary in natural dispatch order. For a no-arg walk this produces `base:` followed by the base deltas, then each assistant's inline output followed by that assistant's summary, in alphabetical assistant order.
- Progress and diagnostic messages (probe activity, warnings, prompt text, rebuild progress) written to stderr.
- Exit codes:

  | Code | Meaning |
  |------|---------|
  | `0` | Success, including "nothing to update" and dry-run runs that complete without error. |
  | `1` | Generic error: missing `~/.confine-ai/base/Dockerfile`, malformed base Dockerfile, multi-stage base Dockerfile, unknown `distribution=` value, `tool=java` missing `distribution=`, explicit assistant target not found, explicit assistant target missing `Dockerfile`, rebuild failure. |
  | `2` | Upstream probe failed during a base update (network error, unparseable upstream response). No file was written. |
  | `3` | Sha256 fetch or cross-verification failed during a base update. No file was written. |
  | `4` | User aborted the Java major-version jump prompt. |

  When a no-arg run observes multiple failures, the exit code is the highest-severity code observed across all attempted targets.

**Behavior:**

**Command surface.**

The command is invoked as `confine-ai update [target...] [--dry-run] [--yes]`. Each `target` is `base` or an assistant name. `--dry-run` and `--yes` apply to every target in the invocation.

**Source of truth.**

A base update reads and rewrites `~/.confine-ai/base/Dockerfile`. If the file does not exist, the tool reports an error directing the user to run `confine-ai init` and exits with code `1`. `confine-ai update` never falls back to the embedded seed; that fallback exists only in REQ-AS-006's build-path resolution for the assistant shortcut auto-build.

An assistant update reads `~/.confine-ai/assistants/<name>/Dockerfile`. If the file does not exist and the assistant was explicitly named, the tool errors with code `1`. If the file does not exist for an assistant enumerated in no-arg mode, the tool warns on stderr and continues with the next target.

**Base update behavior (marker-driven rewrite).**

Base updates identify managed lines exclusively by the `# confine-ai:managed` marker contract defined in REQ-AS-006 and [system-design.md#managed-dockerfile-markers](system-design.md#managed-dockerfile-markers). Classification reads the marker's `tool=`, `kind=`, `arch=`, and `distribution=` fields. The classifier must not pattern-match distribution-specific strings such as `corretto`, `temurin`, or `zulu` anywhere: Java identity comes from `tool=java`, and the specific distribution comes from the marker's `distribution=<name>` field. Unknown marker tokens are ignored per the ADR's forward-compatibility rule.

A "managed group" is a `kind=version` marker plus the `kind=sha256` markers that share the same `tool=` (and, for Java, the same `distribution=`) value. A managed group updates as a unit: the version bump and its matching per-architecture sha256 values either all apply or none do.

For each managed group in the file, the tool probes the upstream for the candidate version per the following policy:

| Marker `tool=` | Target version policy | Prompt on major jump |
|----------------|-----------------------|----------------------|
| `go` | Latest stable Go release (latest minor overall). A jump from `1.26.0` to `1.27.1` applies silently. | No |
| `java` with `distribution=corretto` | Latest LTS major published by Corretto. | Yes |

Corretto is the only Java distribution supported at launch. The `distribution=` field remains enumerated so future distributions can be added in later requirements without changing the classifier contract. Any `distribution=` value other than `corretto` is an error, not a skip, because there is exactly one supported value at launch.

The tool must not rewrite the `FROM` line. Base image version management is out of scope (see REQ-AS-008's Out of Scope section).

For every candidate version the tool intends to write, the tool must obtain the sha256 for each `arch=` in the managed group from an upstream-authoritative source before any bytes are written. The origin, transport, cross-verification against upstream-published checksum files, TLS requirements, proxy handling, and offline behavior are specified by the outbound HTTP trust boundary ADR (see Design Rationale) and are not reiterated here.

**Atomicity (base).** A base update is all-or-nothing. If any probe fails for any group, or if any sha256 fetch or cross-verification fails for any `arch=` of any group, the tool writes nothing, reports every outcome in the summary, and exits with the appropriate non-zero code. A partially-updated base Dockerfile is never produced.

**Dockerfile rewrite contract (base).** The rewrite touches only the value portion of managed `ARG` lines identified by their markers. Everything else is preserved byte-identical:

| Preserved byte-identical | Rewritten |
|--------------------------|-----------|
| ARG names (`GO_VERSION`, `CORRETTO_VERSION`, or any other name the user has chosen) | The text after `=` on each managed `ARG` line |
| The `FROM` line | (none) |
| Non-managed lines and non-managed comments | (none) |
| `# confine-ai:managed` marker comments | (none) |
| Line order, indentation, blank lines, trailing newline | (none) |
| File mode `0o644`, directory mode `0o755` | (none) |

The tool does not reflow, reformat, or canonicalize the file. The rewrite is atomic at the filesystem level (temporary file plus rename).

After a successful rewrite, the tool rebuilds `localhost/confine-ai-base:latest` from the updated Dockerfile so that the upstream image layer referenced by `FROM` is re-resolved. The tool then drops stale confine-ai-managed containers so their next shortcut invocation picks up the new base image.

**Java major-version jump prompt.** The Java major version is the integer before the first `.` in the version string (e.g., `25` in `25.0.2.10.1`). When the candidate major differs from the current pinned major, the tool prompts before modifying anything. The prompt is displayed on stderr and reads a single line from stdin.

The prompt includes:
- The marker's `distribution=<name>` value.
- The current version string and its major.
- The candidate version string and its major.
- A one-line warning that this is a major-version jump and may contain breaking changes.
- The three choices: `proceed`, `skip`, `abort`.

| Choice | Effect |
|--------|--------|
| `proceed` | Continue with sha256 verification and the atomic rewrite. |
| `skip` | Leave the Java managed group unchanged, record it as `skipped` in the summary, and continue processing other managed groups in this base update. |
| `abort` | No file write occurs for the whole base update. The base update exits with code `4`. In no-arg mode, assistants are not attempted because a base failure halts the walk. |

When stdin is not a terminal and `--yes` is not set, the tool does not prompt. It records the Java managed group as `skipped` (implicit skip, not abort) and continues with other managed groups. When `--yes` is set, the tool proceeds without prompting. Non-major Java updates never prompt.

**Assistant update behavior (least-work rebuild).**

Assistant updates must do the least work needed: if the installed assistant CLI version already matches the latest upstream release, the rebuild is skipped. This is the default contract for every assistant, not an opt-in. An assistant is *gated* iff its built-in scaffold declares an installed-version probe and an upstream-version probe; at launch only `claude` is gated. An assistant that cannot express a probe (no publicly-queryable upstream, no CLI `--version` command) rebuilds unconditionally, and that is a per-assistant tracked gap — not a design choice of the update system.

Assistant updates do NOT read `~/.confine-ai/base/Dockerfile` markers, do NOT probe upstream sources for anything other than the assistant CLI version, and do NOT fetch sha256 values from the assistant's Dockerfile. Scaffolded assistants are `FROM localhost/confine-ai-base:latest` and carry unversioned install lines (`curl | bash`, `go install @latest`, `npm install -g`, and so on). When a rebuild runs, it exists to re-run those installs so they pick up new upstream content.

*Installed version source of truth.* The installed version is read from the assistant's local image `confine-ai-assistant-<name>:latest` by running the assistant's version command inside a one-shot container. For `claude`, the command is `claude --version`. The probe parses the first version-shaped token from stdout. The image is the single source of truth; no sidecar file, Dockerfile ARG, or lockfile records the installed version.

*Probe must be offline and non-mutating.* The installed-version read runs with no network access and must not mutate any container, volume, or image. This is a hard constraint for two reasons. A probe that issues outbound traffic from inside the assistant image would be a confinement leak. A probe that mutates image or volume state would corrupt the artifact being classified. The one-shot container runs with network disabled and the default entrypoint bypassed. The probe reads stdout only: it does not write to any bind-mounted path and exits without side effects.

*Upstream version source of truth.* The upstream version is fetched from the assistant's registered upstream endpoint. For `claude`, the endpoint is the npm registry entry for `@anthropic-ai/claude-code` at the `latest` dist-tag, and the version field is the JSON `.version` string. The probe runs through the same outbound HTTP trust boundary used by base probes (see Design Rationale).

*Comparison.* The probe compares installed and upstream versions by string equality on the version field. Equal means unchanged. Not equal means rebuild. No semantic version ordering is applied; any string difference triggers a rebuild, including "installed is newer than upstream".

*Least-work outcomes.*

| Condition | Action | Stdout summary line |
|-----------|--------|---------------------|
| Assistant scaffold declares no probe (tracked per-assistant gap) | Rebuild unconditionally | `rebuilding <assistant> (without cache)` |
| Probe declared, installed version equals upstream version | Skip rebuild. Action is `unchanged`. Exit code `0` for this target. | `<assistant> already at <version>` |
| Probe declared, installed version differs from upstream version | Rebuild. Action is `updated` on success. | `rebuilding <assistant> (<installed> -> <upstream>)` |
| Installed image `confine-ai-assistant-<name>:latest` is missing from the local image store | Warn on stderr, skip the probe, rebuild anyway. | `rebuilding <assistant> (without cache)` |
| Assistant version command fails, exits non-zero, or produces no parseable version | Warn on stderr, skip the probe, rebuild anyway. | `rebuilding <assistant> (without cache)` |
| Upstream probe fails (network error, DNS error, HTTP non-2xx, unparseable JSON, missing version field) | Warn on stderr, skip the probe, rebuild anyway. | `rebuilding <assistant> (without cache)` |

*Graceful degradation.* Every probe failure mode falls through to "rebuild anyway". The probe must never fail an update; it only short-circuits one. A probe failure is reported as a single stderr warning naming the assistant and the reason. It does not produce a `failed` action and does not change the exit code.

*When the rebuild runs.* The tool rebuilds the assistant image from `~/.confine-ai/assistants/<name>/Dockerfile` with no layer cache reuse and without `--pull`. The base image reference (`localhost/confine-ai-base:latest`) is a local-only tag with no remote source, so `--pull` would cause the container runtime to fail trying to re-resolve it against remote registries. The base image was already refreshed by the preceding `update base` step in a no-arg walk, or is assumed to already exist locally in an explicit assistant-only invocation. The assistant build never reaches out to a remote registry for the base layer, and an assistant update never causes the base image itself to update — the base-first ordering in no-arg mode exists so that a no-arg `confine-ai update` has already refreshed the base before any assistant rebuild begins.

After a successful rebuild, the tool drops stale confine-ai-managed containers for that assistant so their next shortcut invocation picks up the rebuilt image. When the probe short-circuits the rebuild, the container drop is also skipped because no new image exists to pick up.

*Scope.* The least-work rule is active only inside `confine-ai update`. The shortcut invocation `confine-ai <assistant>` (REQ-AS-002) is not affected; a running assistant container is never interrupted by the probe, and the probe never runs during shortcut invocation. The probe changes no files. The assistant's Dockerfile and devcontainer.json are unchanged. No lockfile, no sidecar version file, no Dockerfile ARG is introduced.

*Single-owner ownership of the assistant image tag.* `confine-ai update <assistant>` is the only command that owns the contents of `confine-ai-assistant-<name>:latest` as a refresh operation. The shortcut invocation consumes whatever bytes the tag currently points to (see REQ-AS-002's "Image consumption (single-owner model)" contract). The shortcut's first-use auto-ensure path is the only other writer to the tag, and it writes only when the tag is absent from the local image store — it never rewrites or replaces an existing image. Because both paths agree on the same canonical tag, an `update` rebuild is immediately visible to the next shortcut invocation without any additional synchronization. This single-owner invariant is what keeps `confine-ai update claude` reporting "claude already at X" consistent with the CLI version the next `confine-ai claude` launches. The two writers are further disambiguated by their cache policy: the shortcut's auto-ensure is a **cached** build (first-use ergonomics), while `confine-ai update <assistant>` is the **only** path that cache-busts the image. Any user intent that translates to "re-fetch unversioned upstream content and rebuild the image" must go through `confine-ai update`; the shortcut will not do this even indirectly. The shortcut and `update` invoke the same fixed-Dockerfile build helper (no `build.args` / `build.context` interpretation on either path), so the two paths produce byte-identical images from the same source tree on the same host — a user editing `devcontainer.json`'s `build` block cannot cause the two paths to diverge.

*Never return error.* The probe is invoked from the assistant-update path. That path never returns an error to the caller: all outcomes, including probe failures, flow through per-target results. A probe failure is a warning plus a rebuild, never a propagated error.

**No-arg behavior.**

When no target is given, the tool enumerates targets as `base`, then every subdirectory of `~/.confine-ai/assistants/` that contains a `Dockerfile`. Enumeration order for assistants is alphabetical by directory name so that reports are reproducible.

The tool updates `base` first. If the base update fails for any reason (missing file, parse error, probe failure, sha256 failure, rebuild failure, user abort at Java major jump), the tool stops immediately and does not attempt any assistant update. The no-arg run exits with the base failure code.

If the base update succeeds, the tool updates each assistant in turn. An assistant failure is reported on stderr and recorded in the summary but does not stop subsequent assistants. After all assistants have been attempted, the tool exits with the highest-severity exit code observed across all targets. If every target succeeded, the exit code is `0`.

**Dry-run.**

`--dry-run` reports what would change without writing to the Dockerfile and without invoking the container runtime for a rebuild.

Base dry-run still probes upstream sources and still fetches sha256 values so that preflight use catches network failures in CI. The summary reports the planned version deltas per managed group. A dry-run propagates real exit codes: probe failure returns `2`, sha256 failure returns `3`, and so on. Hiding probe failures behind `0` would defeat the preflight purpose of `--dry-run`.

Assistant dry-run reports the same summary lines as a real run, except every `rebuilding <assistant>` line becomes `would rebuild <assistant>`. A probe is still consulted: a match reports `<assistant> already at <version>` with action `unchanged`, a mismatch reports `would rebuild <assistant> (<installed> -> <upstream>)` with action `would update`, and a probe failure reports the stderr warning plus `would rebuild <assistant> (without cache)`. Assistants without a probe declared report `would rebuild <assistant> (without cache)`. The runtime is not invoked for any build. Assistant dry-run exits `0` for the assistant portion unless a generic error applies (for example, explicit assistant target not found).

`--dry-run` combined with no-arg enumeration walks base and all assistants in the same order a real run would, reporting the planned outcome of each target. Base dry-run failure still halts the no-arg walk before any assistant is dry-reported, matching the real run's stop-on-base-failure rule.

**Error and edge-case behavior.**

| Condition | Behavior |
|-----------|----------|
| `~/.confine-ai/base/Dockerfile` does not exist | Error with a hint to run `confine-ai init`; exit `1`. No fallback to the embedded seed for updates. |
| A managed-looking `ARG` (e.g., `ARG GO_VERSION=1.26.0`) with no preceding `# confine-ai:managed` marker | Warn on stderr naming the line and ARG. Do not auto-repair. Continue processing other managed groups. |
| An orphan `# confine-ai:managed` marker with no following managed line (blank line, EOF, or non-ARG line) | Warn on stderr naming the marker's line number. Do not auto-repair. Continue processing other managed groups. |
| Multi-stage base Dockerfile (more than one `FROM` line, with or without `AS <alias>`) | Error: `confine-ai update` does not support multi-stage base Dockerfiles. Do not modify the file. Exit `1`. |
| `tool=java` marker with a `distribution=<name>` value other than `corretto` | Error naming the unknown distribution value. Do not modify the file. Exit `1`. |
| `tool=java` `kind=version` marker missing the `distribution=` field | Error naming the line. Do not modify the file. Exit `1`. |
| Duplicate markers (two `# confine-ai:managed` markers on consecutive lines before the same managed line) | Warn and skip that managed group. Do not auto-repair. |
| Sha256 cannot be obtained for one `arch=` in a managed group | The whole base update fails atomically. No file write. Exit `3`. |
| Explicit target `base` with `~/.confine-ai/base/Dockerfile` absent | Error with a hint to run `confine-ai init`; exit `1`. No fallback to the embedded seed for updates. |
| Explicit target `<assistant-name>` with `~/.confine-ai/assistants/<assistant-name>/` absent | Error naming the assistant. Exit `1`. |
| Explicit target `<assistant-name>` where the assistant directory lacks `Dockerfile` | Error naming the missing file. Exit `1`. |
| No-arg run where an assistant directory under `~/.confine-ai/assistants/` lacks `Dockerfile` | Warn on stderr, record as `skipped` in the summary, continue with the next assistant. |
| Base rewrite succeeds, rebuild fails | Rebuild failure is reported. The file has already been rewritten. Rollback of a successful rewrite after a failed rebuild is out of scope (see "Out of Scope"). Exit `1`. |
| Base rebuild succeeds but dropping stale containers fails | Warn on stderr. Do not roll back. |

**Interaction with REQ-AS-006.**

REQ-AS-008 consumes the marker contract defined by REQ-AS-006 and honors every future-compatibility constraint:

1. The `FROM` line is user-editable. REQ-AS-008 does not rewrite it.
2. The LTS update policy applies only on `tool=java`. `tool=go` uses latest stable, no prompt.
3. Classification is by marker field, never by URL or ARG-name pattern matching. Any future Java distribution added to the enumeration will be picked up via its `distribution=<name>` field without classifier changes.

`confine-ai update base` is the value-rewrite path for `~/.confine-ai/base/Dockerfile`: it changes only the value portion of marker-annotated `ARG` lines. `confine-ai init -y` is the full-seed-overwrite path: it replaces the entire file with the current embedded seed. The two commands serve different intents (incremental version bump vs. reset to seed) and the user chooses between them explicitly. Neither command touches `~/.confine-ai/data/<assistant>/`; credential survival is a REQ-AS-003 contract and is not re-litigated here.

**Constraints:**

- The only file `confine-ai update` modifies is `~/.confine-ai/base/Dockerfile`. The embedded seed is never modified. Assistant Dockerfiles are never modified. The least-work probe changes no files.
- Managed line identification is exclusively marker-driven. No fallback to ARG-name heuristics, URL substring checks, or distribution-string matching.
- Sha256 values must be obtained from an upstream-authoritative origin before any bytes are written to disk.
- A base update is all-or-nothing across every managed group in the invocation. Partial file states are not permitted.
- Preserved on base rewrite: file mode `0o644`, directory mode `0o755`, line order, non-managed line content (including non-managed comments), ARG names, the `FROM` line byte-identical, marker comments byte-identical, trailing newline.
- Multi-stage base Dockerfiles are rejected with exit `1`.
- The classifier must not read distribution-specific strings (`corretto`, `temurin`, etc.) anywhere outside the marker's `distribution=<name>` field.
- Markers are expected only on the base Dockerfile. Assistant Dockerfiles do not carry markers and assistant updates never read them.
- Assistant rebuilds run with `--no-cache` and without `--pull`. They must not read markers or fetch sha256 values from the assistant Dockerfile.
- The installed-version probe targets the image `confine-ai-assistant-<name>:latest`. No other image is consulted.
- The installed-version probe runs inside a one-shot container with network disabled (`--network=none`) and the default entrypoint bypassed (`--entrypoint ""`). It must not mutate any container, any volume, or any image.
- The upstream probe for `claude` queries the npm registry at `https://registry.npmjs.org/@anthropic-ai/claude-code/latest` and reads the JSON `.version` field. TLS, proxy, and offline behavior follow the outbound HTTP trust boundary ADR.
- Version comparison is string equality on the version field. No semantic ordering, no pre-release handling, no normalization.
- The least-work rule is the default contract for every assistant; "no probe declared" is a per-assistant gap, not a system-wide exception.
- The least-work probe must not introduce a new exit code; every outcome uses the exit codes declared in REQ-AS-008's Output section.
- The least-work probe must never cause the assistant update to return an error. All outcomes flow through per-target results.
- The probe runs before the rebuild path. When the probe short-circuits, the container-drop step is also skipped because no rebuild occurred.
- In no-arg mode, base is processed before any assistant. A base failure halts the run before any assistant is attempted.
- Assistant targets are processed serially. Parallel assistant rebuilds are out of scope.

**Out of Scope:**

- The outbound HTTP probing mechanism, endpoint selection, response parsing, and any caching strategy. These are specified by [ADR: Outbound HTTP Trust Boundary](adr/2026-04-12-outbound-http-trust-boundary.md) and `docs/system-design.md`.
- Base image `FROM` version updates. Users may edit the `FROM` line freely; REQ-AS-008 never rewrites it. Automated base-image bumping will be a separate future requirement.
- Java distributions other than Corretto. The classifier enumeration stays extensible, but only `distribution=corretto` is accepted at launch.
- Markers on assistant Dockerfiles. Assistant updates are intentionally cache-bust only.
- Pinning assistant CLI versions. Assistants install latest upstream content on each update by design.
- Parallel assistant rebuilds. v1 of `confine-ai update` is serial.
- Rollback after a successful base rewrite but a failed rebuild. The user re-runs `confine-ai update base` after resolving the rebuild failure.
- Sha256 pinning or supply-chain verification of assistant install script payloads. The least-work probe is a performance optimization, not a trust upgrade.
- Version probes for `copilot` or `opencode` at launch. These assistants currently rebuild unconditionally; adding probes is tracked as follow-up work and is a local change inside each assistant's scaffold.
- Semantic version ordering for the least-work probe. "Installed is newer than upstream" is treated as a mismatch and triggers a rebuild.
- Caching upstream probe results across `confine-ai update` invocations. Each run probes fresh.
- Rollback of a rebuild that produces a working but older assistant image. The probe runs before the rebuild; once a rebuild starts, this requirement owns the outcome.

**Acceptance Criteria:**

1. Given `~/.confine-ai/base/Dockerfile` with the seed markers and a pinned Go version older than the latest stable Go release, when `confine-ai update base` runs and all probes and sha256 fetches succeed, then the `GO_VERSION` ARG value and both `GO_SHA256_AMD64` and `GO_SHA256_ARM64` ARG values are rewritten to the new version and verified sha256 values, the ARG names and the `FROM` line are byte-identical to the pre-run file, the base image is rebuilt with `--pull`, stale confine-ai-managed containers are dropped, and the command exits with code `0`.
2. Given a base Dockerfile whose pinned Go version is already the latest stable, when `confine-ai update base` runs, then no ARG line is rewritten, the command reports `unchanged` for the Go group in the summary, the base image is not rebuilt, and the command exits with code `0`.
3. Given a base Dockerfile where Go and Corretto probes and sha256 fetches all succeed, when `confine-ai update base --dry-run` runs, then the file bytes on disk are unchanged after the command returns, the base image is not rebuilt, the summary on stdout lists the planned version deltas per managed group, and the command exits with code `0`.
4. Given a base Dockerfile where the Corretto sha256 fetch fails, when `confine-ai update base --dry-run` runs, then the file is unchanged, the runtime is not invoked, the summary reports the sha256 failure, and the command exits with code `3` (dry-run propagates real exit codes).
5. Given a base Dockerfile where the Go probe succeeds but the Corretto sha256 fetch fails, when `confine-ai update base` runs, then neither managed group is written to disk (atomic all-or-nothing), the summary reports the Go group as not written and Corretto as `failed`, no rebuild occurs, and the command exits with code `3`.
6. Given a base Dockerfile where the candidate Corretto version has a different Java major than the current pin and stdin is a terminal, when `confine-ai update base` runs and the user answers `proceed`, then the tool verifies sha256 values, rewrites the Corretto managed group, rebuilds the base image, and exits with code `0`.
7. Given the same conditions and the user answers `skip`, then the Corretto managed group is unchanged, the summary records it as `skipped`, other managed groups continue to be processed, and the command exits with code `0` if nothing else failed.
8. Given the same conditions and the user answers `abort`, then no managed group is written, no rebuild occurs, no assistants are updated in a no-arg run, and the command exits with code `4`.
9. Given a Corretto major-jump candidate and stdin is not a terminal, when `confine-ai update base` runs without `--yes`, then the tool does not prompt, records the Corretto managed group as `skipped`, processes other managed groups normally, and exits with code `0` if nothing else failed.
10. Given the same non-terminal condition with `--yes`, when `confine-ai update base` runs, then the tool proceeds without prompting and rewrites the Corretto managed group after sha256 verification.
11. Given a base Dockerfile with `distribution=temurin` on a `tool=java` marker, when `confine-ai update base` runs, then the tool reports an error naming the unsupported distribution value, does not modify the file, and exits with code `1`.
12. Given a base Dockerfile with a `tool=java kind=version` marker that has no `distribution=` field, when `confine-ai update base` runs, then the tool reports an error naming the line, does not modify the file, and exits with code `1`.
13. Given a base Dockerfile with two `FROM` lines, when `confine-ai update base` runs, then the tool reports that multi-stage base Dockerfiles are not supported, does not modify the file, and exits with code `1`.
14. Given `~/.confine-ai/base/Dockerfile` does not exist, when `confine-ai update base` runs, then the tool reports an error directing the user to run `confine-ai init`, does not read or write the embedded seed, and exits with code `1`.
15. Given a base Dockerfile with a managed-looking `ARG GO_VERSION=1.26.0` line that has no preceding `# confine-ai:managed` marker, when `confine-ai update base` runs, then the tool emits a stderr warning naming the line and ARG, does not rewrite that line, and processes any correctly-marked managed groups normally.
16. Given a base Dockerfile with a `# confine-ai:managed tool=go kind=version` marker followed by a blank line and then an unrelated comment, when `confine-ai update base` runs, then the tool emits a stderr warning about the orphan marker, does not synthesize a version, and processes the rest of the file.
17. Given a successful base update run, when the file is inspected after the command returns, then the file mode is `0o644`, the `# confine-ai:managed` marker comments are byte-identical to the pre-run file, the `FROM` line is byte-identical to the pre-run file, the non-managed line content (non-managed comments, `RUN` steps, `USER`, `WORKDIR`, and `ENV` lines) is byte-identical, the trailing newline is preserved, and only the rewritten `ARG` value portions differ.
18. Given `~/.confine-ai/assistants/claude/Dockerfile` exists, when `confine-ai update claude` runs, then the tool rebuilds the assistant image from that Dockerfile with `--no-cache` (and without `--pull`, because `localhost/confine-ai-base:latest` is a local-only tag), drops stale containers for that assistant, does not read any marker in the assistant Dockerfile, does not fetch any sha256 from the assistant Dockerfile, and exits with code `0` on rebuild success.
19. Given `~/.confine-ai/assistants/claude/Dockerfile` exists, when `confine-ai update claude --dry-run` runs, then the tool reports "would rebuild claude without cache", the runtime is not invoked, no container is dropped, and the command exits with code `0`.
20. Given `~/.confine-ai/assistants/claude/` does not exist, when `confine-ai update claude` runs, then the tool reports an error naming the assistant and exits with code `1`.
21. Given `~/.confine-ai/assistants/claude/` exists but has no `Dockerfile`, when `confine-ai update claude` runs as an explicit target, then the tool reports an error naming the missing file and exits with code `1`.
22. Given `~/.confine-ai/assistants/` contains `claude` (with `Dockerfile`) and `broken` (without `Dockerfile`), when `confine-ai update` runs with no target and the base update succeeds, then `claude` is rebuilt, `broken` is skipped with a warning, and the command exits with code `0` if base and claude succeeded.
23. Given `confine-ai update` runs with no target and the base update fails with exit code `2` (probe failure), then no assistant is attempted, the summary reports only the base failure, and the command exits with code `2`.
24. Given `confine-ai update` runs with no target, the base update succeeds, and one of two assistants fails to rebuild, then the other assistant is still processed, both outcomes are reported in the summary, and the command exits with code `1` (the highest-severity code observed).
25. Given a base Dockerfile hand-edited so that `FROM debian:bookworm-slim` now reads `FROM ubuntu:24.04`, when `confine-ai update base` runs, then the tool does not touch the `FROM` line, does not warn about the changed base image, and rewrites only the managed ARG groups whose markers are present.
26. Given a no-arg `confine-ai update` run, then the target walk order is exactly `base`, then every subdirectory of `~/.confine-ai/assistants/` that contains `Dockerfile` in alphabetical order.
27. Given a successful base update, when the run completes, then the base rebuild is invoked with `--pull` so that the image layer referenced by `FROM` is re-resolved.
28. Given `confine-ai-assistant-claude:latest` exists and `claude --version` inside it reports the same version string the npm registry returns for `@anthropic-ai/claude-code` at the `latest` dist-tag, when `confine-ai update claude` runs, then no rebuild is invoked, the summary reports `claude already at <version>`, the action is `unchanged`, and the command exits with code `0`.
29. Given the installed version differs from the upstream version, when `confine-ai update claude` runs, then the summary reports `rebuilding claude (<installed> -> <upstream>)`, the assistant rebuild runs, stale containers for `claude` are dropped, and on rebuild success the action is `updated` with exit code `0`.
30. Given the installed version matches the upstream version, when `confine-ai update --dry-run claude` runs, then the summary reports `claude already at <version>`, the runtime is not invoked, the action is `unchanged`, and the command exits with code `0`.
31. Given the installed version differs from the upstream version, when `confine-ai update --dry-run claude` runs, then the summary reports `would rebuild claude (<installed> -> <upstream>)`, the runtime is not invoked for a rebuild, the action is `would update`, and the command exits with code `0`.
32. Given `confine-ai-assistant-claude:latest` is absent from the local image store, when `confine-ai update claude` runs, then a stderr warning names the missing image, the probe is skipped, the rebuild runs, and the outcome matches the cache-bust rebuild path (exit `0` on success).
33. Given `confine-ai-assistant-claude:latest` exists but `claude --version` inside the image exits non-zero or produces output with no parseable version, when `confine-ai update claude` runs, then a stderr warning names the probe failure, the probe is skipped, the rebuild runs, and the outcome matches the cache-bust rebuild path.
34. Given the npm registry is unreachable, returns a non-2xx status, returns unparseable JSON, or returns a response with no `.version` field, when `confine-ai update claude` runs, then a stderr warning names the upstream probe failure, the probe is skipped, the rebuild runs, and the command exits with code `0` on rebuild success.
35. Given `confine-ai update copilot` runs and the `copilot` scaffold declares no version probe (launch state), then no version probe is attempted, no probe warning is emitted, and the assistant rebuild runs.
36. Given `confine-ai update opencode` runs and the `opencode` scaffold declares no version probe (launch state), then no version probe is attempted and behavior matches `copilot`.
37. Given any probe failure (image missing, version command failure, upstream probe failure), when `confine-ai update claude` runs, then the function that carries out the assistant update does not return an error to its caller; the outcome is carried entirely through the per-target result.
38. Given `confine-ai update` runs with no target, the base update succeeds, and the installed `claude` version matches upstream, then `claude` is reported as `unchanged`, any other assistants proceed per their own probe state, and the command exits with code `0` when nothing else fails.
39. Given `confine-ai claude` is invoked (shortcut, not update), then no version probe runs and no behavior change from REQ-AS-002 is observed.
40. Given `confine-ai update claude` rebuilds `confine-ai-assistant-claude:latest` to a new CLI version, when `confine-ai claude` is invoked next in any workspace, then the launched container runs the CLI version `update` installed (the shortcut and `update` operate on the same canonical image tag; no second image tag is involved on either path).
41. Given `confine-ai update claude` reports `claude already at <version>`, when `confine-ai claude` is invoked next in any workspace, then the launched container reports `<version>` as its CLI version.
42. Given `confine-ai-assistant-claude:latest` is absent and `confine-ai claude` triggers a first-use auto-ensure, then the build honors the layer cache (no `--no-cache`); `--no-cache` is used only by `confine-ai update <assistant>` when the probe determines a rebuild is required. The shortcut path and the `update` path never invoke the builder with the same cache policy — the shortcut is always cached, `update` is always cache-bust — and this is the only behavioral difference between the two writers of the canonical tag.
43. Given `~/.confine-ai/assistants/claude/devcontainer.json` declares `build.args` with a non-empty value, when `confine-ai update claude` runs, then those args are ignored entirely: the rebuild invokes the same fixed-Dockerfile build helper the shortcut auto-ensure uses, producing an image whose filesystem content depends only on `~/.confine-ai/assistants/claude/Dockerfile` and its referenced build context, not on `devcontainer.json`.

**Implementation:** See [system-design.md#managed-dockerfile-markers](system-design.md#managed-dockerfile-markers) for the marker grammar consumed by base updates, and [system-design.md#update-assistant-probe](system-design.md#update-assistant-probe) for the per-assistant probe registry, the one-shot exec mechanism used to read the installed version, the upstream adapter shape, and the wiring into the assistant-update path. The probe transport, sha256 fetch transport, upstream cross-verification, caching strategy, container rebuild invocation, and stale-container drop mechanism live in the system-design doc. The outbound HTTP trust boundary ADR (see Design Rationale) is a hard prerequisite for implementation and governs the upstream probe's transport.

**Depends On:** REQ-AS-002, REQ-AS-003, REQ-AS-006

---


### Runtime and distribution

confine-ai runs on top of whatever Docker-compatible container runtime the host already provides, and ships as a single static binary with no install-time dependencies. These two properties are intentional: the container is the isolation boundary, so the tool that launches it must not widen the host's attack surface with its own runtime stack. This group defines the runtime-detection rules and the static-binary contract that every release must satisfy.

<a id="req-rt-001"></a>
### REQ-RT-001: Container Runtime Detection

The tool detects and uses any Docker-compatible container runtime available on the host.

**Status:** Approved

**Input:**
- Host system PATH and available binaries

**Output:**
- Detected runtime command (e.g., `docker`, `podman`)

**Behavior:**

The tool shells out to the container runtime CLI for all operations (build, run, exec, stop, rm). It does not use the Docker API directly. This ensures compatibility with any runtime that provides a Docker-compatible CLI:

- Docker Desktop (macOS, Windows)
- Rancher Desktop (with dockerd/moby runtime)
- Podman (detected as `podman`)

The tool searches for runtimes in this priority order: `docker`, then `podman`. It uses the first one found. If no runtime is found, the tool reports an error.

**Acceptance Criteria:**
1. Given `docker` on PATH, when the tool runs, then it uses `docker` for container operations
2. Given `podman` on PATH but no `docker`, when the tool runs, then it uses `podman`
3. Given both `docker` and `podman` on PATH, when the tool runs, then it uses `docker`
4. Given no container runtime on PATH, when the tool runs, then it reports an error
5. Given a runtime available, when building an image, then the tool shells out to the runtime CLI

**Implementation:** See [system-design.md#interfaces](system-design.md#interfaces) for runtime abstraction

---

<a id="req-di-001"></a>
### REQ-DI-001: Static Binary

The tool compiles to a single static binary with no runtime dependencies.

**Status:** Approved

**Input:**
- None (build-time property)

**Output:**
- Statically linked binary for each supported platform

**Behavior:**

The tool produces a statically linked binary for each supported platform:

| OS | Architecture |
|----|-------------|
| macOS | arm64 |
| macOS | amd64 |
| Linux | arm64 |
| Linux | amd64 |

No runtime dependencies. No Node.js. No npm. No shared libraries.

**Acceptance Criteria:**
1. Given the compiled binary, when run on a supported platform, then it executes without additional runtime dependencies
2. Given the binary, when inspected, then it has no dynamically linked libraries (static linking)

---

### Shell completion

Tab completion for the confine-ai CLI is provided as a two-part contract: a `confine-ai completion <shell>` command that prints a shell-specific completion script to stdout, and a hidden callback subcommand that the printed script invokes on each Tab press to return matching suggestions. The static suggestion set (subcommands, flag names, fixed argument values) is compiled into the binary, while dynamic suggestions (assistant names) are discovered at completion time by listing `~/.confine-ai/assistants/`. This group defines both requirements that together cover the user-facing command and the internal completion callback.

<a id="req-sc-001"></a>
### REQ-SC-001: Completion Script Generation

`confine-ai completion <shell>` prints a shell completion script to stdout.

**Status:** Proposed

**Input:**
- `shell` (string): Shell name, one of `bash` or `zsh`

**Output:**
- Shell-specific completion script printed to stdout

**Behavior:**

The `completion` subcommand prints a completion script for the specified shell. The script registers a completion handler that calls back into the confine-ai binary when the user presses Tab. The user sources this script in their shell profile.

The command prints to stdout only. It does not modify shell configuration files, write to disk, or auto-install the completion. This follows the pattern used by kubectl, docker, and gh.

| Shell | Setup |
|-------|-------|
| bash | `source <(confine-ai completion bash)` in `~/.bashrc` |
| zsh | `source <(confine-ai completion zsh)` in `~/.zshrc` |

If the shell argument is missing or not one of the supported values, the command reports an error listing the valid options.

**Acceptance Criteria:**
1. Given `confine-ai completion bash`, when the command runs, then it prints a bash completion script to stdout
2. Given `confine-ai completion zsh`, when the command runs, then it prints a zsh completion script to stdout
3. Given `confine-ai completion fish`, when the command runs, then it reports an error listing `bash` and `zsh` as valid options
4. Given `confine-ai completion` with no argument, when the command runs, then it reports an error listing `bash` and `zsh` as valid options
5. Given `source <(confine-ai completion bash)` in a bash session, when the user types `confine-ai ` and presses Tab, then completion suggestions appear
6. Given `source <(confine-ai completion zsh)` in a zsh session, when the user types `confine-ai ` and presses Tab, then completion suggestions appear

**Depends On:** REQ-CL-001, REQ-SC-002

---

<a id="req-sc-002"></a>
### REQ-SC-002: Completion Callback

A hidden subcommand returns completion suggestions for the current command line.

**Status:** Proposed

**Input:**
- Command-line arguments representing the partial command being completed (passed by the shell completion handler)

**Output:**
- One completion suggestion per line on stdout

**Behavior:**

The shell completion scripts generated by REQ-SC-001 call back into the confine-ai binary with a hidden subcommand when the user presses Tab. The binary analyzes the partial command line and returns matching suggestions to stdout, one per line.

The hidden subcommand does not appear in `--help` output or usage text. It is an internal interface between the completion scripts and the binary.

**Static completions (known at build time):**

| Context | Completions |
|---------|-------------|
| First positional argument | Subcommands: `init`, `rm`, `update`, `status`, `completion`. Plus discovered assistant names (see dynamic completions). |
| `completion` argument | `bash`, `zsh` |
| `init` argument | Built-in template names: `claude`, `copilot`, `opencode` |
| Global flags | `--version`, `--workspace-folder`, `--docker-path` |
| Assistant shortcut flags | `--shell`, `--no-git-identity` |
| `update` argument | `base` plus discovered assistant names under `~/.confine-ai/assistants/` |
| `update` flags | `--dry-run`, `--yes` |

**Dynamic completions (discovered at completion time):**

| Context | Discovery Method |
|---------|-----------------|
| Assistant names (first positional argument, `rm` argument, `update` argument) | List directory entries under `~/.confine-ai/assistants/` |
| Directory paths for `--workspace-folder` and folder arguments | Delegate to shell's default directory/file completion |

The binary filters suggestions by the current prefix. If the user has typed `confine-ai r`, only suggestions starting with `r` are returned (e.g., `rm`).

**Completions not provided:**

| Context | Reason |
|---------|--------|
| Arguments after `--` in assistant shortcuts | Passthrough to assistant binary; not predictable |

**Acceptance Criteria:**
1. Given the user types `confine-ai ` and presses Tab, then the suggestions include all subcommands and discovered assistant names
2. Given the user types `confine-ai u` and presses Tab, then the suggestion is `update`
3. Given the user types `confine-ai claude --` and presses Tab, then the suggestions include `--shell` and `--no-git-identity`
4. Given the user types `confine-ai completion ` and presses Tab, then the suggestions are `bash` and `zsh`
5. Given the user types `confine-ai init ` and presses Tab, then the suggestions include `claude`, `copilot`, and `opencode`
6. Given `~/.confine-ai/assistants/` contains directories `claude` and `copilot`, when the user types `confine-ai ` and presses Tab, then `claude` and `copilot` appear alongside the subcommands
7. Given `~/.confine-ai/assistants/` does not exist, when the user types `confine-ai ` and presses Tab, then only subcommands are suggested (no error)
8. Given the user types `confine-ai rm ` and presses Tab, then the suggestions include discovered assistant names
9. Given the hidden subcommand, when `confine-ai --help` runs, then the hidden subcommand does not appear in the output

**Depends On:** REQ-CL-001, REQ-CL-004, REQ-AS-001

---

## Out of Scope

Items excluded from this project:

- Windows support (macOS and Linux only for initial release)
- Docker API direct usage (CLI shelling only)
- Container image registry management
- Remote (SSH) container execution
- `read-configuration` / `check` commands for field reporting (removed; config errors surface when the assistant shortcut runs)
- `--log-level` / `--log-format` flags (structured logging)
- `--id-label` flag (custom container labels)
- `--build-no-cache` flag (cacheless image builds)
- Homebrew tap, GitHub releases, and other distribution channels (local build only)
- Cross-compilation and release automation
- `remoteEnv` config field and `--remote-env` CLI flag. Credentials are managed by Claude Code's OAuth flow, not by the tool. See REQ-CO-007.
- Assistant configuration synchronization across machines (manual copy or dotfiles manager)
- Per-project overrides for assistant configs (the assistant shortcut always uses `~/.confine-ai/assistants/<name>/`; there is no project-local `devcontainer.json` workflow)
- Project-local `devcontainer.json` workflow (removed with the `up`/`exec`/`check`/`down` CLI surface; confine-ai is assistant-only)
- Automatic basename deduplication for multi-folder mounts (user must rename or restructure; the tool errors on collision)
- Container-side path override for additional folders (always `/workspaces/<basename>`; only the primary workspace respects `workspaceFolder` config)
- Read-only mounting of additional folders (all mounts are read-write; read-only semantics can be added later)
- Fish or PowerShell shell completion (bash and zsh cover the target platforms; others can be added later)
- Shell completion auto-install flag (print to stdout only; user sources manually)
- Completing values for `--network`, `--memory`, `--cpus` (free-form input; complete flag names only)
- Completing arguments after `--` in assistant shortcuts (passthrough to assistant binary)
- HTTP probing mechanism, response parsing, and any caching strategy used by `confine-ai update` (the *what* is specified in REQ-AS-008; the *how* is deferred to system-design-expert)
- Support in `confine-ai update` for Java distributions other than Corretto. At launch, `confine-ai update` supports only `distribution=corretto`; any other `distribution=<name>` value is an error. Additional distributions will be added by future requirements without changing the classifier contract.
- Rewriting version references that appear inside `RUN` steps or other non-`ARG`/non-`FROM` locations without a `# confine-ai:managed` marker (REQ-AS-008 only rewrites marker-identified managed lines)
- Automatic updates to the embedded seed at runtime (the seed is shipped in the binary; `confine-ai update` operates only on `~/.confine-ai/base/Dockerfile`)
- Multi-stage Dockerfile support in `confine-ai update` (the seed is single-stage; REQ-AS-008 rejects multi-stage files)
- Rewriting the `FROM` line in REQ-AS-008. `confine-ai update base` never touches the `FROM` line. Automated base-image version updates are deferred to a future requirement.
- Base-image swapping logic. Users may hand-edit the `FROM` line to any base image, but the tool does not detect, validate, or assist with base-image changes, and does not rewrite install steps to match the new base.
- Java-distribution swapping logic. Users may hand-edit the Dockerfile to replace Corretto with Temurin, Zulu, Microsoft Build of OpenJDK, Liberica, or another OpenJDK distribution, but the tool does not detect, validate, or assist with distribution changes, and does not rewrite the download URL or checksum.
- LTS-aware or "latest stable" version resolution for the embedded seed shipped with the binary. REQ-AS-006 pins the seed to specific versions and ships matching sha256 values; upstream probing happens only at `confine-ai update base` time against the user-owned copy of the file.
- Per-architecture sha256 divergence beyond the two architectures already supported (amd64, arm64)
- A `confine-ai reset` command that removes `~/.confine-ai/` wholesale. Credential survival is a first-class contract (REQ-AS-003, REQ-AS-006); resetting state is a deliberate manual `rm -rf ~/.confine-ai/data/<assistant>/` the user drives, not a tool operation.
