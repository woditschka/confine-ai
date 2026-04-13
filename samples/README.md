# Sample Devcontainer Configurations

Example devcontainer setups for three AI coding assistants. Shared base image with Go and Java, thin per-assistant layers.

## Structure

```
samples/
├── base/Dockerfile                        ← Go 1.26 + Java 25 (Corretto) + system tools
├── claude/.devcontainer/                  ← Claude Code assistant
├── github-copilot/.devcontainer/          ← GitHub Copilot CLI assistant
├── opencode/.devcontainer/                ← OpenCode assistant
└── Makefile                               ← Build helpers
```

## Base Image

`base/Dockerfile` provides the shared foundation:

- **OS:** `debian:bookworm-slim` (auditable, minimal)
- **Languages:** Go 1.26, Java 25 (Amazon Corretto)
- **Tools:** git, curl, make, gcc, ripgrep, jq
- **User:** non-root `dev` user
- **Multi-arch:** amd64 and arm64

Each assistant Dockerfile is 2-3 lines: `FROM localhost/confine-ai-base:latest` plus the assistant install.

## Usage

These samples are the source for `confine-ai init <assistant>`, which scaffolds `~/.confine-ai/assistants/<name>/` from the embedded templates. For normal use, run:

```bash
confine-ai init claude
cd ~/my-project
confine-ai claude
```

To build the sample images directly (e.g. for testing Dockerfile changes under `samples/`):

```bash
cd samples
make base          # Build the base image once
make all           # Build base + every assistant image
```

## Updating Versions

Override via make variables:

```bash
make base GO_VERSION=1.27.0 CORRETTO_VERSION=25.0.1.1.1
```

## Assistant Authentication

Each assistant stores credentials in a bind-mounted host folder under `~/.confine-ai/data/`. This keeps secrets on your filesystem and avoids API keys in environment variables.

| Assistant | Login command | Host folder | Container target |
|-------|-------------|-------------|-----------------|
| Claude Code | `claude login` | `~/.confine-ai/data/claude/` | `/home/dev/.claude` |
| GitHub Copilot | `copilot login` | `~/.confine-ai/data/copilot/` | `/home/dev/.copilot` |
| OpenCode | n/a (API keys) | `~/.confine-ai/data/opencode/` | `/home/dev/.config/opencode` |

**First run:** launch the assistant and run the login command from inside its shell.

```bash
confine-ai init claude
cd ~/my-project
confine-ai claude --shell
# then inside the container:
claude login
```

Credentials persist across container rebuilds. For OpenCode, place API keys in `~/.confine-ai/data/opencode/config.json` or pass them as environment variables.
