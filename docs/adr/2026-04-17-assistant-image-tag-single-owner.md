# Assistant Image Tag Single-Owner Model

**Status:** Accepted

## Context

REQ-AS-008 (`confine-ai update <assistant>`) rebuilds and tags the assistant image as `confine-ai-assistant-<name>:latest` (see `internal/assistant/assistant_build.go` and `internal/update/assistant.go`). The least-work probe in `internal/update/assistant_probe.go` reads the installed CLI version from this exact tag.

REQ-AS-002 (assistant shortcut `confine-ai <name>`) flows through `container.Up`. Because the assistant's scaffolded `devcontainer.json` declares a `build` block, `Up` takes the `buildImage` branch in `internal/container/up.go`, which derives the image tag from the workspace-folder basename: `confine-ai-<sanitized-basename>:latest`.

The two paths therefore produced and consumed different image tags:

| Command | Tag produced / consumed |
|---------|------------------------|
| `confine-ai update claude` | `confine-ai-assistant-claude:latest` |
| `confine-ai claude` in workspace `project-a` | `confine-ai-project-a:latest` |

Rebuilds written by `update` never reached the shortcut's runtime path. Observed symptom: `confine-ai update` reports `claude already at 2.1.112` while `confine-ai claude` launches Claude Code 2.1.104 from a stale workspace-basename-tagged image.

REQ-AS-002 amendments (ACs 12-19) and REQ-AS-008 amendments (ACs 40-43) now bind the shortcut and `update` to a single canonical tag.

## Options Considered

1. **Share `buildImage` with the assistant shortcut and change its tag convention.** Teach `internal/container/up.go:buildImage` to detect an assistant-shortcut caller and emit `confine-ai-assistant-<name>:latest` instead of the workspace-basename tag.

2. **Single-owner tag ownership (this ADR).** `confine-ai update` is the sole refresh writer of `confine-ai-assistant-<name>:latest`. The shortcut is a consumer only: it overrides `cfg.Image` to the canonical tag, clears `cfg.Build`, and calls `container.Up` with no build step. A first-use auto-ensure (absence-only, cached) covers the `init -> shortcut` flow without an explicit `update`. `buildImage` remains untouched for the non-assistant devcontainer workspace path.

3. **Make the shortcut call `confine-ai update <assistant>` on first run.** Treat first use as an implicit update, reusing the existing rebuild path end-to-end.

## Decision

Option 2: single-owner tag ownership.

**Rationale:**

- **One canonical tag, one production writer.** Only `confine-ai update <assistant>` refreshes `confine-ai-assistant-<name>:latest`. The shortcut's auto-ensure is an absence-only first-use path, not a refresh. This makes image provenance unambiguous: if the tag is stale, `update` is the place to look.
- **Layer boundary respected.** `internal/container/up.go` is the generic devcontainer runtime. Option 1 would bake an assistant-specific tag convention into it, coupling the generic `Up` code path to `internal/assistant/` concerns. Option 2 keeps the generic code path ignorant of assistants: the shortcut caller performs the ownership decision before `Up` runs.
- **Fixed Dockerfile, byte-identical images.** Both writers (the `update` refresh path and the shortcut's first-use auto-ensure) invoke the same helper that builds from the fixed path `~/.confine-ai/assistants/<name>/Dockerfile` and ignores `build.args` / `build.context` / `build.dockerfile` from the assistant's `devcontainer.json`. A user editing those fields cannot silently diverge the two paths. This is an invariant enforced by code structure, not by a runtime `if`.
- **Cache policy split.** The shortcut's auto-ensure is a **cached** build (first-run ergonomics, fast). `confine-ai update <assistant>` is the sole `--no-cache` path. Cache-busting is the exclusive contract of `update`, so first-use latency is minimized without eroding the update contract.
- **Option 3 rejected.** Implicit `update` on first run would drag the assistant version probe, the HTTP client, and the upstream trust boundary into the shortcut invocation path. That is too much machinery for a first-run ensure. It also inverts the least-work rule: the shortcut would always hit the network.

**Trade-offs:**

- **Existing assistant containers are recreated once.** `cfg.Image` and `cfg.Build` participate in `ConfigHashWithFolders`. After this change, the hash for an existing container is different from the hash for the new configuration (because `Image` is now set and `Build` is now nil). The reconnect path detects the mismatch and recreates. This is the documented REQ-AS-002 AC 10 behavior on config change and is exactly what lets the fix take effect without a manual `confine-ai rm`.
- **Stale `confine-ai-<workspace-basename>:latest` images remain.** Images produced by the bug before the fix are not cleaned up. The new code never produces them, so they stop accumulating, but existing ones linger until the user prunes manually. This is accepted per the feature's Out of Scope list; a migration step would add risk (misclassified deletion) and complexity for a cosmetic benefit.

## Consequences

**Positive:**
- `confine-ai update <assistant>` rebuilds reliably reach the next shortcut invocation. REQ-AS-002 AC 14 and REQ-AS-008 ACs 40-41 are now enforceable by construction.
- The generic `container.Up` / `buildImage` path is untouched. Regular devcontainer workspaces keep their workspace-basename tag convention; no regression in the non-assistant path.
- Image provenance for assistant images is unambiguous: the canonical tag has exactly one refresh writer.
- The cache-policy contract becomes explicit and machine-enforced: shortcut auto-ensure is cached, `update` is `--no-cache`, and both paths invoke the same fixed-Dockerfile helper.

**Negative:**
- One-time container recreate on upgrade for existing assistant containers. Mitigated: the recreate is the normal config-hash-change path users already experience.
- Stale workspace-basename-tagged images linger on upgrade. Mitigated: accepted in Out of Scope; manual `podman image prune` clears them.

## Implementation

**Requirements:** REQ-AS-002 (ACs 12-19), REQ-AS-008 (ACs 40-43), REQ-AS-006 (lifecycle overview tightening).

**Changes:**

- `internal/assistant/assistant_build.go`: add `EnsureAssistantImage(ctx, builder, homeDir, name, stderr)`, a sibling of `EnsureBaseImage`. Absence-only, cached build, single stderr breadcrumb on entry. Refactor `BuildAssistantImage` to accept a cache-control option (mirroring `BuildBaseImage`'s `BuildOptions`) so both writers share the same fixed-Dockerfile core: the `update` caller passes `NoCache=true`, the auto-ensure caller passes `NoCache=false`. The refactor preserves `update`'s existing `--no-cache` contract byte-for-byte.
- `internal/cli/assistant.go` (`runAssistantWithExecutor`): after `EnsureBaseImage`, call `assistant.EnsureAssistantImage`. Before `container.Up`, override `cfg.Image = assistant.AssistantImageTag(p.assistantName)` and set `cfg.Build = nil`. The shortcut becomes a pure consumer.
- `internal/container/up.go`: untouched. The `buildImage` branch is simply not reached by the shortcut because `cfg.Build` is nil on that path. Regular devcontainer workspaces continue to use `buildImage` with the workspace-basename tag.

## References

- [REQ-AS-002: Assistant Shortcut Invocation](../prd.md#req-as-002) — amended ACs 12-19
- [REQ-AS-008: Update Command](../prd.md#req-as-008) — amended ACs 40-43
- [REQ-AS-006: Base Image and User-Owned Dockerfile](../prd.md#req-as-006) — lifecycle overview
- [system-design.md#assistant-image-lifecycle](../system-design.md#assistant-image-lifecycle) — lifecycle table and tag ownership
- [internal/assistant/assistant_build.go](../../internal/assistant/assistant_build.go) — `AssistantImageTag`, `BuildAssistantImage`, `EnsureAssistantImage` implementations
- [internal/cli/assistant.go](../../internal/cli/assistant.go) — shortcut consumer path with `cfg.Image` / `cfg.Build` override
- [internal/update/assistant.go](../../internal/update/assistant.go) — refresh writer path passing `BuildOptions{NoCache: true}`
