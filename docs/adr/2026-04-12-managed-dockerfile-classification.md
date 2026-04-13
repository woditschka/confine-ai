# Managed Dockerfile Line Classification via Comment Markers

**Status:** Accepted

## Context

REQ-AS-006 moves the base Dockerfile into the user's home directory and makes
it editable. The `confine-ai update` command (REQ-AS-008) rewrites managed lines to pick up upstream updates. It was designed with the following constraints (out of scope for
the current cycle) will rewrite managed lines (the `FROM` image tag, the
version ARGs, the sha256 ARGs) to pick up upstream updates.

REQ-AS-008 must:

1. Locate every managed line in the user's edited Dockerfile.
2. Classify each managed line so it can apply a tool-specific update policy.
   Java uses "latest LTS major, prompt on major jumps." Go and the base image
   use "latest stable, no prompt." The classification axis is the update
   policy.
3. Work across Java distributions (Corretto, Temurin, Zulu, Microsoft Build
   of OpenJDK, Liberica, and future additions) without pattern-matching the
   string `corretto` or any other distribution-specific string.
4. Locate the `FROM` line when the user has swapped `debian:bookworm-slim`
   for any other base image.

REQ-AS-006 (this cycle) must choose a convention that does not block any of
these. REQ-AS-006 does not implement the rewrite logic.

## Options Considered

1. **Distribution-specific ARG names with a prefix registry** — Keep
   `GO_VERSION`, `CORRETTO_VERSION`, `GO_SHA256`, `CORRETTO_SHA256`. REQ-AS-008
   ships a registry mapping ARG-name prefixes to tool classes (`GO_*` → `go`,
   `CORRETTO_*` → `java`). Brittle: a user who swaps Corretto for Temurin and
   renames the ARG falls out of the registry until REQ-AS-008 is updated; a
   user who swaps without renaming preserves the misleading `CORRETTO_*`
   name. The `FROM` line is not classifiable; REQ-AS-008 would have to scan
   for the keyword and guess which line is "the" `FROM`.

2. **Generic ARG names with a distribution selector** — Rename to
   `JAVA_VERSION`, `JAVA_SHA256`, `JAVA_DISTRIBUTION`. REQ-AS-008 classifies by
   ARG name alone. Clean classifier, but the seed must either carry shell
   logic for every supported distribution (dead code in REQ-AS-006) or
   hardcode Corretto and defer the branch to REQ-AS-008's rewrite logic
   (expanding REQ-AS-008's scope from line rewrites to shell rewrites). The
   `FROM` line still needs a second convention (a `BASE_IMAGE` ARG plus
   `FROM $BASE_IMAGE` indirection).

3. **Structured comment markers above each managed line** — Every managed
   line (one `FROM`, every managed `ARG`) is preceded by a comment with a
   fixed sentinel and key-value fields. REQ-AS-006 writes the markers into
   the embedded seed. REQ-AS-008 parses the markers to classify and locate.
   ARG names and the `FROM` line can be anything the user wants.

## Decision

We use option 3: structured comment markers immediately preceding each
managed line in the embedded seed.

The marker grammar:

```text
# confine-ai:managed tool=<tool> kind=<kind> [arch=<arch>] [distribution=<distribution>]
```

- `tool` (required): `base-image`, `go`, `java`. The classification axis
  REQ-AS-008's LTS rule fires on (`tool=java` only).
- `kind` (required): `image`, `version`, `sha256`. What the managed line
  represents. `image` for `FROM`, `version` for version ARGs, `sha256` for
  checksum ARGs.
- `arch` (conditional): `amd64`, `arm64`. Required on `kind=sha256` lines.
- `distribution` (conditional): `corretto` (future: `temurin`, `zulu`,
  `msopenjdk`, `liberica`). Required on `tool=java` lines. Encodes Java
  distribution identity without URL pattern matching.

Rules:

1. Each marker sits on the line immediately preceding the line it classifies,
   with no blank line between them.
2. Each managed line has exactly one marker.
3. The sentinel `# confine-ai:managed` is reserved. Ordinary Dockerfile comments
   not starting with that sentinel are ignored by REQ-AS-008.
4. REQ-AS-006 writes the markers. REQ-AS-008 reads them. REQ-AS-006 does not
   parse them at runtime.
5. Unknown tokens in a marker are ignored by REQ-AS-008 (forward-compatible).

## Consequences

Positive:

- Classification is decoupled from ARG naming. Users can rename ARGs freely
  (e.g., `CORRETTO_VERSION` → `TEMURIN_VERSION`) without losing REQ-AS-008's
  management.
- `FROM` and managed ARGs share one mechanism. No second convention for the
  base image.
- Distribution identity lives in `distribution=<name>`, readable without
  touching the URL or the ARG name.
- The seed stays pure Dockerfile. Markers are ordinary comments, ignored by
  Docker, rendered correctly by linters and editors. No build-time cost.
- Extensible: new `tool=` or `kind=` values add capability without renaming.

Negative:

- Users who delete a marker drop the associated line from REQ-AS-008's
  management. Mitigation: REQ-AS-008 warns on managed-looking ARGs
  (e.g., `ARG GO_VERSION=...`) that have no preceding marker, and on orphan
  markers. The warnings are advisory; they do not auto-repair.
- The grammar is a private confine-ai convention. Other tools do not recognize
  it. Acceptable: confine-ai owns the file.
- REQ-AS-008 carries a small marker parser (a few dozen lines of Go using
  `bufio.Scanner` and `strings.Cut`). Acceptable parse complexity.

## Implementation

**Requirements:** [REQ-AS-006](../prd.md#req-as-006)

- `samples/base/Dockerfile` carries the markers for the embedded seed.
- `internal/assistant/base_test.go` asserts marker presence and shape on the
  embedded seed so reviewers catch accidental deletion.
- REQ-AS-006 does not read markers at runtime. REQ-AS-008 will read them when
  it ships.

## References

- [system-design.md#managed-dockerfile-markers](../system-design.md#managed-dockerfile-markers) — managed Dockerfile marker grammar
- [REQ-AS-006](../prd.md#req-as-006) — the requirement this serves
- [REQ-AS-008](../prd.md#req-as-008) — the update command that consumes the markers
