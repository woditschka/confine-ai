# confine-ai

[![CI](https://github.com/woditschka/confine-ai/actions/workflows/ci.yml/badge.svg)](https://github.com/woditschka/confine-ai/actions/workflows/ci.yml)

A security-focused tool for running AI coding assistants in isolated containers. Uses the [devcontainer.json](https://containers.dev/) configuration format to define container environments. Single static binary, zero host dependencies beyond a Docker-compatible container runtime.

## Why not just Podman and a shell script?

You absolutely can. If you run Linux with Podman and one assistant, a hand-rolled script works fine. confine-ai exists for everyone else:

- **Multiple operating systems.** macOS, Linux, Windows each have different mount semantics, socket paths, and permission models. confine-ai handles the differences so the same `devcontainer.json` works everywhere.
- **Multiple container runtimes.** Docker Desktop, Podman, Rancher Desktop -- each has quirks around rootless mode, volume ownership, and networking. One binary, same behavior.
- **Multiple assistants.** Claude Code, GitHub Copilot, and OpenCode each need different images, credential mounts, and environment variables. confine-ai manages them side by side, in the same project or across projects.
- **Security guardrails.** Mount validation, symlink detection, iptables firewall rules, and a blocklist of dangerous paths are easy to forget in a script and tedious to maintain. confine-ai enforces them by default.
- **Experience levels.** Not everyone is comfortable writing container scripts. `confine-ai init claude` followed by `confine-ai claude` gets a new user from zero to an isolated assistant session in two commands.

The goal is to make container isolation the easy default rather than an exercise left to the reader.

## Build and install

Requires Go 1.26+.

```bash
# Build and install to /usr/local/bin
make install

# User-local install (no sudo required)
make install PREFIX=~/.local

# Just build without installing
make build
# Binary: bin/confine-ai
```

## Quick start

```bash
# One-time setup: scaffold assistant configuration
confine-ai init claude

# Run Claude Code in any project directory
cd ~/my-project
confine-ai claude

# Pass arguments to the assistant
confine-ai claude -- --continue

# Stop the assistant container
confine-ai rm claude
```

That's it. `confine-ai init` creates assistant configuration in `~/.confine-ai/` and builds the base image. `confine-ai claude` starts or reconnects to a container for the current directory.

## Assistant management

Assistant configurations live in `~/.confine-ai/`, separate from your projects:

```text
~/.confine-ai/
├── assistants/
│   ├── claude/          ← devcontainer.json + Dockerfile
│   ├── copilot/         ← devcontainer.json + Dockerfile
│   └── opencode/        ← devcontainer.json + Dockerfile
└── data/
    ├── claude/          ← credentials (persisted across containers)
    ├── copilot/
    └── opencode/
```

### Commands

```bash
# Initialize an assistant (one-time)
confine-ai init claude
confine-ai init copilot
confine-ai init opencode

# Start or reconnect to an assistant
confine-ai claude
confine-ai copilot
confine-ai opencode

# Stop a specific assistant container
confine-ai rm claude

# Stop all containers for the current directory
confine-ai rm

# List all running confine-ai containers
confine-ai status

# Update the shared base image and assistants
confine-ai update
```

### Parallel sessions

Multiple assistants can run simultaneously in the same project, and the same assistant can run in different projects:

```bash
# Two assistants in the same project
cd ~/my-project
confine-ai claude     # terminal 1
confine-ai copilot    # terminal 2

# Same assistant in different projects
cd ~/project-a && confine-ai claude    # terminal 1
cd ~/project-b && confine-ai claude    # terminal 2
```

## Supported devcontainer.json fields

| Field | Description |
|-------|-------------|
| `image` | Pre-built container image |
| `build.dockerfile` | Path to Dockerfile for building the image |
| `build.context` | Build context directory |
| `build.args` | Build arguments passed to `docker build` |
| `workspaceFolder` | Working directory inside the container |
| `mounts` | Additional bind mounts and volumes |
| `containerEnv` | Environment variables set inside the container |
| `remoteUser` | User to run as inside the container |
| `containerUser` | User that the container runs as |

Variable substitution is supported: `${localEnv:VAR}`, `${localEnv:VAR:default}`, `${devcontainerId}`, `${localWorkspaceFolder}`, `${localWorkspaceFolderBasename}`.

## Excluded by design

- **OCI Features** -- install scripts from third-party registries are a supply chain risk
- **Lifecycle commands** (`postCreateCommand`, etc.) -- attack vector for untrusted repos
- **Port forwarding** -- not needed for CLI assistant workflows
- **Docker Compose** -- use `docker compose` directly
- **IDE customizations** -- out of scope

## Security

confine-ai validates mounts against a blocklist (`/`, `/etc`, `/tmp`, Docker socket, home directories), rejects `--network host`, and sets up iptables firewall rules when running on a bridge network. Symlink-based mount escapes are detected and blocked.

Missing bind mount directories are created only after interactive user confirmation. Non-interactive mode (CI) never auto-creates directories.

## Container runtimes

Works with any Docker-compatible runtime:

- Docker Desktop / Docker Engine
- Rancher Desktop
- Podman

## Development

Requires Go 1.26+ and [golangci-lint](https://golangci-lint.run/) v2 (installed automatically by `make lint`).

See [Build and install](#build-and-install) above for building the binary.

### Run tests

```bash
make test           # Unit tests
make test-race      # Unit tests with race detector (requires gcc)
make test-e2e       # Integration tests (requires container runtime)
make test-samples   # Sample build tests (builds base + assistant images, slow)
make test-all       # All of the above (unit + e2e + samples)
make test-coverage  # Unit tests with coverage report
```

### Lint and CI

```bash
make lint           # Run golangci-lint
make lint-fix       # Run golangci-lint with auto-fix
make ci             # Full CI pipeline: tidy, fmt, vet, lint, deps-check, test, build
```

### Update seed versions

The embedded sample Dockerfiles ship with pinned Go and Corretto versions. Before a release, update them to the latest upstream versions:

```bash
make update-samples   # Probe upstreams, rewrite samples/base/Dockerfile
git diff              # Review changes
```

### Other targets

```bash
make tidy           # go mod tidy
make clean          # Remove build artifacts
make security       # Run govulncheck and go mod verify
make deps-check     # Verify no prohibited dependencies
make hooks          # Install git hooks from .githooks/
```

## Disclaimer

This is an experimental project. Not affiliated with Anthropic, GitHub, or any AI assistant provider. Use at your own risk.

## Documentation

- [Product requirements](docs/prd.md)
- [System design](docs/system-design.md)
- [Architecture decisions](docs/adr/)
