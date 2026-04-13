# Gateway Blocking via iptables in Container

**Status:** Accepted

## Context

REQ-CO-009 AC 5-7 require blocking container access to the Docker gateway IP and `host.docker.internal` while preserving external internet access. REQ-NR-001 (outbound network allowlist) will reuse the same firewall mechanism. The choice of mechanism determines the capability surface, image compatibility, and extensibility for future allowlist rules.

## Options Considered

1. **iptables via `docker exec` after container start** -- Add `NET_ADMIN` capability to the container, start it, then run `docker exec` to install iptables DROP rules for the gateway IP. Gateway IP discovered via `docker network inspect`.

2. **nftables via `docker exec` after container start** -- Same as option 1, but use nftables. Requires `nft` binary inside the container.

3. **Entrypoint wrapper script** -- Mount a shell script that runs iptables rules before the user process. Requires modifying the container entrypoint.

4. **DNS-only blocking (modify `/etc/hosts`)** -- Write entries to `/etc/hosts` to redirect `host.docker.internal` to a dead IP. Does not block direct IP access.

## Decision

Option 1: iptables via `docker exec` after container start.

**Rationale:**

- iptables is available in the kernel via netfilter; it does not require a userspace binary inside the container. The `iptables` command is a thin wrapper around kernel netfilter, and `NET_ADMIN` grants the capability to configure it. If `iptables` is missing from the image, the tool can detect this and fall back to `/sbin/iptables` or report a clear error.
- nftables (option 2) has less adoption in container images. `iptables` commands work on both legacy iptables and nftables-backed kernels via compatibility mode.
- Entrypoint wrappers (option 3) are fragile: they interact with existing entrypoints, HEALTHCHECK, and init systems. They require bind-mounting a script and modifying the `--entrypoint` argument.
- DNS-only blocking (option 4) does not prevent direct IP access, failing AC 5.
- `docker exec` after start (vs. entrypoint) has a timing gap: between container start and rule application, the container process could reach the gateway. This is acceptable because the container runs `sleep infinity` (no user process yet) and the tool controls the sequence. The user process starts only after `confine-ai up` returns.
- REQ-NR-001 will extend this approach with OUTPUT chain rules for allowlist enforcement.

**Trade-off:** Adding `NET_ADMIN` is the only capability exception beyond defaults. REQ-NR-001 AC 5 explicitly permits this. The capability is scoped: it allows network configuration changes but not host-level access (which requires `--network host`, already blocked).

## Consequences

**Positive:**
- Gateway IP blocking works without requiring specific binaries in the container image beyond the kernel netfilter interface.
- The same mechanism extends to REQ-NR-001 allowlist rules.
- No entrypoint modification; existing container images work unchanged.
- `NET_ADMIN` is the minimum capability for firewall rules.

**Negative:**
- Images without iptables userspace tools require the tool to handle the missing-binary case gracefully.
- Timing gap between container start and rule application (mitigated by `sleep infinity` entrypoint).
- `NET_ADMIN` adds a capability beyond Docker defaults; documented trade-off.

## Implementation

**Requirements:** REQ-CO-009 (AC 5-7), REQ-NR-001 (foundation)

## References

- [system-design.md#gateway-blocking](../system-design.md#gateway-blocking) — mechanism details
- [REQ-CO-009: Network Isolation from Host](../prd.md#req-co-009) — acceptance criteria
- [REQ-NR-001: Outbound Network Allowlist](../prd.md#req-nr-001) — future extension
