# Outbound Network Allowlist via iptables

**Status:** Accepted

## Context

REQ-NR-001 requires restricting container outbound network access to a declared list of hosts. The tool already uses `NET_ADMIN` and iptables for gateway blocking (REQ-CO-009). The question is how to extend that mechanism for default-deny outbound filtering.

Two sub-decisions are involved:
1. How to implement the default-deny outbound policy.
2. How to resolve hostnames to IP addresses for the ACCEPT rules.

## Options Considered

### Outbound Policy Mechanism

1. **iptables OUTPUT chain default-deny via `docker exec`** — Set OUTPUT policy to DROP, add ACCEPT rules for allowed IPs, DNS, loopback, and established connections. Reuses the existing `docker exec` iptables pattern from gateway blocking.

2. **Network policy via Docker plugin** — Use a CNI plugin or Docker network driver that enforces outbound rules. Requires plugin installation and configuration.

3. **Proxy-based filtering (squid, mitmproxy)** — Route all traffic through an HTTP proxy that enforces the allowlist. Requires proxy setup, certificate injection for HTTPS, and application configuration.

4. **eBPF-based filtering** — Attach eBPF programs to the container's network namespace. Requires BPF capabilities and kernel support.

### Hostname Resolution

1. **Resolve inside the container via `getent ahostsv4`** — Uses the container's DNS configuration. Consistent with what applications inside the container would use.

2. **Resolve on the host before container creation** — Uses the host's DNS. May differ from the container's DNS configuration.

3. **Resolve dynamically via DNS interception** — Install a DNS proxy that maps resolved IPs to iptables rules in real time. Complex and fragile.

## Decision

Option 1 for both: iptables OUTPUT chain default-deny via `docker exec`, with hostname resolution inside the container via `getent ahostsv4`.

**Rationale:**

- Reuses the existing iptables infrastructure from REQ-CO-009. No new capabilities, no new dependencies.
- `NET_ADMIN` is already granted for gateway blocking. No additional capability needed.
- `getent ahostsv4` resolves using the container's DNS, matching application behavior.
- Network plugins (option 2) require installation on the host. Not acceptable for a zero-dependency tool.
- Proxy-based filtering (option 3) requires HTTPS interception (MITM), which breaks TLS certificate pinning used by services like `api.anthropic.com`.
- eBPF (option 4) requires `CAP_BPF` or `CAP_SYS_ADMIN`, a larger capability surface than `NET_ADMIN`.

**Trade-off:** Hostname-to-IP resolution is done once at container start. If a service rotates IPs, the container cannot reach the new IPs until restarted. This is acceptable for the target use case (API endpoints with stable IPs).

**Trade-off:** IPv6 is blocked entirely (`ip6tables -P OUTPUT DROP`). The tool resolves only IPv4 via `getent ahostsv4`. If IPv6 were permitted without corresponding rules, it would bypass the allowlist.

## Consequences

**Positive:**
- No new capabilities beyond existing `NET_ADMIN`.
- No new dependencies. Standard iptables, already required by REQ-CO-009.
- Gateway blocking and allowlist rules compose in the same OUTPUT chain.
- Fail-secure: container is removed if any rule fails to apply.
- Non-root users inside the container cannot modify iptables rules (no `NET_ADMIN` capability for the container user when `containerUser` is non-root).

**Negative:**
- Static IP resolution. IP rotation requires container restart.
- IPv6 blocked entirely. IPv6-only services are unreachable.
- Timing gap between container start and policy application (mitigated by `sleep infinity` entrypoint).

## Implementation

**Requirements:** REQ-NR-001

## References

- [system-design.md#outbound-allowlist](../system-design.md#outbound-allowlist) — mechanism details
- [system-design.md#gateway-blocking](../system-design.md#gateway-blocking) — foundation mechanism
- [ADR: Gateway Blocking via iptables](2026-04-12-gateway-blocking-via-iptables.md) — predecessor decision
- [REQ-NR-001: Outbound Network Allowlist](../prd.md#req-nr-001) — acceptance criteria
