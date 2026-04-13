# Folder-Set Container Identity

**Status:** Accepted

## Context

REQ-CO-001 defines container identification via labels. The original scheme derives the `devcontainer.metadata_id` label from a single workspace folder path: `SHA-256(workspaceFolder)`. REQ-MF-001 introduces multi-folder mounts. Under the original scheme, `confine-ai claude . ../A` and `confine-ai claude .` produce the same identity because only the primary workspace participates in the hash. The second invocation mounts fewer folders than the first, but the container runtime returns the same container. The user expects a container whose mounts match the invocation.

REQ-MF-001 AC 10 (updated) requires that different folder sets produce different containers. REQ-CO-001 AC 4 (new) requires argument-order independence: `confine-ai claude . ../A` and `confine-ai claude ../A .` must find the same container.

## Options Considered

1. **Include all folders in the identity hash (folder-set identity).** Sort all absolute folder paths lexicographically, encode each with a length prefix and null-byte terminator, and compute SHA-256. The `metadata_id` label stores the digest. The `local_folder` label stores the individual paths (newline-separated) for display.

2. **Include only additional folders as a secondary label.** Keep the primary `metadata_id` as `SHA-256(workspaceFolder)`. Add a second label for the additional-folder hash. Query by both labels.

3. **Maintain backward compatibility with a flag-based opt-in.** Keep the old scheme by default. Add a `--folder-identity` flag to enable multi-folder identity.

## Decision

Option 1: folder-set identity. The identity function sorts input paths, encodes each with a length prefix and null-byte terminator, and computes SHA-256. The label constructor accepts a set of paths. A single-folder invocation passes a one-element set.

**Rationale:**

- One identity function covers both single-folder and multi-folder cases. No conditional logic in the query path.
- Sort-then-hash achieves argument-order independence without a separate normalization step.
- Option 2 adds a second label axis and requires compound label queries. This is more complex and the two-label scheme has no advantage over a single hash that incorporates all inputs.
- Option 3 adds flag complexity for a backward compatibility concern that affects zero production users (the project has not shipped a stable release).

**Trade-off: no migration path.** The length-prefix encoding changes the hash for single-folder containers. Existing containers created under the old scheme are not found by the new scheme. The user must `confine-ai rm` and restart. This is acceptable because confine-ai has not shipped a stable release. No user data is inside the container (workspaces are bind-mounted).

**Trade-off: more containers.** Different folder sets produce different containers. A user who runs `confine-ai claude .` and `confine-ai claude . ../lib` will have two containers. Each container consumes disk and memory. This is the intended behavior per REQ-MF-001 AC 10.

## Consequences

**Positive:**
- 1:1 mapping between folder combinations and containers. The user gets the mounts they requested.
- Argument-order independence prevents duplicate containers from ordering accidents.
- Single code path for identity computation regardless of folder count.

**Negative:**
- Existing single-folder containers are not found after upgrade. Users must `confine-ai rm` and restart.
- More containers on disk when the same primary workspace is used with different folder sets.

## Implementation

**Requirements:** REQ-CO-001 (AC 1-6), REQ-MF-001 (AC 10, AC 11), REQ-AS-004

**Changes:**
- `internal/container/labels.go`: label constructors accept a folder set instead of a single string. The old single-path identity function is replaced by a folder-set identity function.
- `internal/container/find.go`: container lookup functions accept a folder set instead of a single string.
- `internal/container/reconnect.go`: extracted reconnect-or-recreate logic with config-hash validation and firewall re-application.
- `internal/cli/assistant.go`: builds the folder set from the primary workspace and additional folders before lookup and label construction.

## References

- [system-design.md#labels](../system-design.md#labels) — Labels type and identity computation
- [REQ-CO-001: Container Identification](../prd.md#req-co-001) — acceptance criteria
- [REQ-MF-001: Multi-Folder Workspace Mounting](../prd.md#req-mf-001) — folder-set identity requirement
