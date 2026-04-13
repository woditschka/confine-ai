# Outbound HTTP Trust Boundary for `confine-ai update`

**Status:** Accepted

## Context

REQ-AS-008 introduces `confine-ai update base`, the first confine-ai command that
deliberately issues HTTP requests from the host. Every prior command is either
local (file I/O, process exec) or delegates networking to the container
runtime (image pulls). `confine-ai update` is different: the confine-ai binary
itself opens TCP sockets to external hosts, parses response bodies, and
rewrites a file on disk based on what those responses say.

This establishes a new trust surface. Before implementation we must decide:
which upstreams are authoritative, what transport guarantees we require, how
we cross-verify the sha256 values we write into `~/.confine-ai/base/Dockerfile`,
how we behave with proxies and offline, and what is explicitly out of scope
for v1.

REQ-NR-001 already defines an outbound network allowlist — but it scopes
container-side traffic, not host-side traffic. REQ-NR-001 runs inside the
container via iptables rules attached to the container's network namespace.
`confine-ai update` runs in the host's network namespace, under the user's own
network policy, before any container is involved. These are two distinct
trust surfaces and this ADR governs the host-side one.

## Options Considered

1. **No ADR, implicit defaults.** Implementation relies on the standard
   library HTTP client with its default settings. Rejected: transport
   guarantees (TLS version, proxy handling, cross-verification) become an
   implementation detail reviewers cannot audit against requirements. A
   future reviewer asking "is non-TLS
   ever allowed?" has no document to answer with.

2. **GPG / cosign / in-toto attestation verification in v1.** The update
   command verifies signatures over upstream metadata before trusting it.
   Rejected for v1: Go's release checksum JSON is not itself signed in a way
   the stdlib can verify without adding PGP dependencies, Corretto's download
   manifest is not signed either, and the prohibited-dependency list rules
   out third-party cryptography libraries. Single-origin TLS trust is the
   current ceiling for stdlib-only. Documented as future work.

3. **Two-origin cross-verification in v1.** Fetch version metadata from one
   origin (e.g., `go.dev/dl/?mode=json`) and the same sha256 from a second
   unrelated origin (e.g., a third-party mirror). Rejected: Go publishes only
   one authoritative source. Corretto publishes only one authoritative source.
   Adding a second origin would mean trusting a third party we have no
   relationship with, which is strictly worse than trusting the vendor.

4. **Single-origin TLS trust with stdlib defaults, documented boundary.**
   Accepted. Each managed tool has exactly one authoritative upstream. We
   require HTTPS, modern TLS, system trust store, and respect for the user's
   proxy environment. We document the limitations so future hardening work
   has a baseline.

## Decision

We adopt option 4 for v1: single-origin TLS trust, stdlib-only transport,
explicit limitations.

### Authoritative upstreams at launch

| Tool | Metadata endpoint | Content | Trust statement |
|------|-------------------|---------|-----------------|
| `tool=go` | `https://go.dev/dl/?mode=json` | JSON array of Go releases. Each entry carries `version`, `stable`, and a `files[]` array. Each file entry carries `filename`, `os`, `arch`, `kind`, `size`, and `sha256`. | `go.dev` is the Go project's canonical release channel. Sha256 values are published alongside the version metadata from the same TLS origin. |
| `tool=java distribution=corretto` | `https://corretto.aws/downloads/latest/amazon-corretto-<major>-<arch>-linux-jdk.tar.gz` (302 redirect, used for version discovery) and `https://corretto.aws/downloads/latest_sha256/amazon-corretto-<major>-<arch>-linux-jdk.tar.gz` (per-archive sha256 body) | The version string is discoverable by following the redirect on `/latest/` and parsing the resolved filename. The plain-text sha256 is fetched from the sibling `/latest_sha256/` endpoint. (Note: the `/latest_checksum/` endpoint returns MD5, not sha256, and is deliberately not used.) | `corretto.aws` is Amazon's canonical Corretto distribution channel. The version and the sha256 come from the same TLS origin. |
| assistant=claude | `https://registry.npmjs.org/@anthropic-ai/claude-code/latest` | JSON document with `.version` field for the latest published version of the `@anthropic-ai/claude-code` npm package. | `registry.npmjs.org` is the canonical npm public registry. Used by the assistant version gate to compare installed vs upstream version. Read-only; no tarball is downloaded. |
| assistant=copilot | `https://registry.npmjs.org/@github/copilot/latest` | JSON document with `.version` field for the latest published version of the `@github/copilot` npm package. | Same trust model as the claude entry above. |
| assistant=opencode | `https://api.github.com/repos/opencode-ai/opencode/releases/latest` | JSON document with `.tag_name` field for the latest GitHub release. | `api.github.com` is GitHub's canonical API endpoint. Used by the assistant version gate. Read-only; no assets are downloaded. Public endpoint; no authentication required. |

For Go, the version-probe and the sha256-fetch are a single request: the
`mode=json` response carries both values together, so there is no temporal
gap in which an attacker could swap one without the other. For Corretto, the
version-probe and sha256-fetch are two requests against the same origin,
serialized per managed group. Both requests must succeed or the group fails
atomically (REQ-AS-008's all-or-nothing constraint).

### Transport requirements

1. **HTTPS only, no exceptions.** The update code never issues a plaintext
   HTTP request. URL schemes other than `https` are rejected before the
   request is issued. Servers that respond with a 3xx redirect to an
   `http://` location are treated as fetch failures.
2. **Minimum TLS version: TLS 1.2.** TLS 1.3 is preferred when both sides
   support it.
3. **Certificate validation: system trust store.** The platform trust store
   is authoritative. Insecure-skip-verify is forbidden in production code.
   Tests use their own in-memory TLS roots.
4. **No certificate pinning in v1.** Pinning requires rotating the binary on
   upstream certificate rotation, which is operationally heavier than our
   current release cadence supports. Documented as future work.
5. **Client timeout: 30 seconds per request.** Probe failures surface as
   REQ-AS-008 exit code 2; sha256 fetch failures surface as exit code 3.
6. **User-Agent: `confine-ai/<version>`.** Identifies the client to upstream
   operators. Not a security control; it's courtesy and makes abuse reports
   actionable.
7. **Response size cap: 10 MiB per response body.** Defense in depth against
   memory exhaustion from a tampered or hijacked origin.

### Proxy handling

The update command honors the standard `HTTPS_PROXY` and `NO_PROXY`
environment variables. No bespoke proxy configuration, no flag, no config
file entry. Corporate environments that already set these variables work
without confine-ai-specific knowledge; environments that do not set them get
direct connections.

The proxy itself is a trust sink: a user who configures `HTTPS_PROXY` has
consented to the proxy seeing the TLS handshake's SNI and routing the
connection. This is identical to the trust model of `go mod download`, `curl
https://...`, and every other stdlib-based HTTPS client on the host.

### Cross-verification of sha256 values

Sha256 values that `confine-ai update` writes to `~/.confine-ai/base/Dockerfile`
are cross-verified against upstream-published sources as follows:

- **Go:** The sha256 published inside `go.dev/dl/?mode=json` is the
  canonical Go project checksum. No second source is consulted. Cross-origin
  verification would require trusting a third party whose relationship to
  the Go project is weaker than `go.dev` itself. Single-origin trust is
  acceptable.

- **Corretto:** The sha256 is fetched from Amazon's per-archive
  `latest_sha256` endpoint. The archive itself lives on the same
  `corretto.aws` origin. The sha256 and the archive come from the same TLS
  origin; tampering would require compromising Amazon's distribution
  endpoint, at which point the tampered sha256 would match a tampered
  archive and no single-origin check could detect it. Single-origin trust is
  acceptable and is identical to what `Dockerfile` itself does when it
  fetches the archive and runs `sha256sum -c`.

The sha256 verification that matters in the defense-in-depth sense happens
at build time inside the Dockerfile: the `RUN ... curl ... | sha256sum -c -`
step fails the build if the archive bytes do not match what `confine-ai update`
wrote. `confine-ai update` is the tool that chooses which sha256 to pin;
`docker build` is the tool that enforces the pin. Compromise of `confine-ai
update`'s trust path produces a bad pin; compromise of the archive would
still be caught at build time unless the attacker controlled both the pin
source and the archive source, which is the single-origin limitation
documented above.

### Offline behavior

`confine-ai update` requires network access. When a probe or sha256 fetch
fails for any reason (DNS failure, TLS handshake failure, HTTP error,
timeout, unparseable response), the command exits with code 2 (probe
failure) or code 3 (sha256 failure) per REQ-AS-008 and writes no bytes. We
do not cache upstream responses to disk; there is no offline-replay mode in
v1. Users who need to run `confine-ai update` without network access must pin
versions manually by editing the Dockerfile, which is an explicit workflow
REQ-AS-006 supports.

### Non-TLS allowance

Never. There is no flag, environment variable, or config knob that allows
`confine-ai update` to make a plaintext HTTP request. This is a hard rule. A
future deployment in a locked-down corporate environment that strips TLS at
a proxy will work via `HTTPS_PROXY`, which still establishes TLS from
confine-ai's perspective; the proxy's decryption is invisible to the client and
covered by the proxy-as-trust-sink rule above.

### Relationship to REQ-NR-001

REQ-NR-001 governs container-side outbound traffic. It attaches iptables
rules to the container's network namespace after creation and is enforced
for the life of the container.

`confine-ai update` runs in the host's network namespace before any container
is started. REQ-NR-001 does not apply to it and should not be extended to
cover it. The two surfaces have different threat models:

- Container-side (REQ-NR-001): a potentially hostile program running inside
  the container must be kept from reaching the host and from exfiltrating
  data. Default-deny is appropriate.
- Host-side (this ADR): the user's own confine-ai binary, running under the
  user's UID with the user's network access, contacts vendor release
  endpoints. Default-deny would break every reasonable workflow; the user
  already has full network access via their shell. The security question is
  transport integrity (TLS, trust store, sha256 pinning), not destination
  policy.

A future requirement may add a host-side outbound allowlist to constrain
what `confine-ai update` is permitted to contact. That is out of scope for v1
and would be a new requirement, not an extension of REQ-NR-001.

### What is NOT in scope for this ADR

The following are explicit non-goals for v1 and are documented here so that
future hardening work has a starting list:

- GPG or OpenPGP signature verification of upstream metadata.
- Sigstore / cosign / Rekor verification.
- in-toto attestations or SLSA provenance.
- TUF (The Update Framework) for metadata integrity.
- Reproducible-build verification of the archives we pin.
- Certificate pinning.
- A second independent sha256 source per tool.
- Host-side outbound allowlist for `confine-ai update`.
- Offline replay cache for upstream responses.

Each of these is a legitimate future improvement. None block v1 because v1
inherits the trust model of the existing `docker build` path that already
fetches and checksums these archives.

## Consequences

Positive:

- Transport guarantees are auditable. A reviewer asking "does confine-ai ever
  issue plaintext HTTP?" has a definitive answer.
- Stdlib-only implementation. No new dependencies. The standard library's
  HTTP, TLS, and JSON packages are sufficient. This keeps the dependency
  policy intact.
- Proxy support is free: `HTTPS_PROXY` and `NO_PROXY` work automatically
  because the standard library HTTP client honors them by default.
- The trust model matches `docker build`: the sha256 pin travels from
  upstream through `confine-ai update` to the Dockerfile, and `sha256sum -c`
  in the `RUN` step catches any mismatch at build time.
- Future hardening (signature verification, pinning, allowlist) can be
  added without rewriting the existing transport layer.

Negative:

- Single-origin trust is weaker than two-origin cross-verification. A
  compromise of `go.dev` or `corretto.aws` would produce a bad pin. The
  mitigation is upstream vendor security plus the build-time sha256 check.
- No offline mode. Users without network access cannot run `confine-ai
  update`. The mitigation is manual Dockerfile editing, which REQ-AS-006
  already supports.
- No certificate pinning. A compromise of the upstream's TLS certificate
  (or a trusted-but-hostile CA) would be invisible to confine-ai. The
  mitigation is the same system trust store every other HTTPS client on the
  host relies on.

## Implementation

**Requirements:** [REQ-AS-008](../prd.md#req-as-008)

The transport, upstream adapters, and cross-verification flow are specified
in [system-design.md#update-command](../system-design.md#update-command).
This ADR governs the decision; the system design document owns the code
structure and any language-level enforcement.

## References

- [REQ-AS-008: Update Command](../prd.md#req-as-008) — the requirement this ADR unblocks
- [REQ-AS-006: User-Owned Base Dockerfile](../prd.md#req-as-006) — the marker contract REQ-AS-008 consumes
- [REQ-NR-001: Outbound Network Allowlist](../prd.md#req-nr-001) — the container-side outbound policy (distinct from this host-side boundary)
- [ADR: Managed Dockerfile Classification via Comment Markers](2026-04-12-managed-dockerfile-classification.md) — the classification ADR whose parser this ADR's trust model feeds
- [system-design.md#update-command](../system-design.md#update-command) — the implementation specification this ADR governs
- [Dependency Policy](../system-design.md#dependency-policy) — the stdlib-first rule this ADR honors
