package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
)

// hostnamePattern matches valid hostnames per RFC 952 / RFC 1123.
// Each label starts and ends with alphanumeric, may contain hyphens.
// Labels are separated by dots.
var hostnamePattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?$`)

// inputBlockedChars is the set of characters rejected in --allowed-hosts
// entries. Prevents shell metacharacters and whitespace in hostnames/IPs.
const inputBlockedChars = "*;&|$`(){}\\/ \t\n\r"

// ValidateAllowedHosts checks that each entry is a valid hostname or IP address.
// Returns an error describing the first invalid entry.
func ValidateAllowedHosts(hosts []string) error {
	for _, h := range hosts {
		if h == "" {
			return errors.New("invalid entry: empty hostname")
		}

		// Reject shell metacharacters and whitespace.
		if strings.ContainsAny(h, inputBlockedChars) {
			return fmt.Errorf("invalid entry %q: contains prohibited characters", h)
		}

		// Accept valid IP addresses.
		if net.ParseIP(h) != nil {
			continue
		}

		// Validate hostname pattern.
		if !hostnamePattern.MatchString(h) {
			return fmt.Errorf("invalid entry %q: not a valid hostname or IP address", h)
		}
	}
	return nil
}

// resolveAllowedHosts resolves hostnames to IPv4 addresses inside the container.
// IP address entries are validated and passed through. Returns a deduplicated
// list of IPs. Fails if any hostname cannot be resolved.
func resolveAllowedHosts(ctx context.Context, executor Executor, containerID string, hosts []string) ([]string, error) {
	seen := make(map[string]struct{})
	var ips []string

	for _, host := range hosts {
		// IP addresses pass through without resolution.
		if net.ParseIP(host) != nil {
			if _, ok := seen[host]; !ok {
				seen[host] = struct{}{}
				ips = append(ips, host)
			}
			continue
		}

		// Resolve hostname inside the container. No --user 0 needed:
		// getent is read-only and does not require root privileges.
		output, err := executor.Output(ctx, "exec", containerID, "getent", "ahostsv4", host)
		if err != nil {
			return nil, fmt.Errorf("resolve allowed host %q: %w", host, err)
		}

		resolved := parseGetentOutput(output)
		if len(resolved) == 0 {
			return nil, fmt.Errorf("resolve allowed host %q: no addresses returned", host)
		}

		for _, ip := range resolved {
			if net.ParseIP(ip) == nil {
				return nil, fmt.Errorf("resolve allowed host %q: invalid IP %q in getent output", host, ip)
			}
			if _, ok := seen[ip]; !ok {
				seen[ip] = struct{}{}
				ips = append(ips, ip)
			}
		}
	}

	return ips, nil
}

// parseGetentOutput extracts unique IP addresses from getent ahostsv4 output.
// Each line has the format: "<ip>  <type> [<hostname>]".
func parseGetentOutput(output string) []string {
	seen := make(map[string]struct{})
	var ips []string

	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		ip := fields[0]
		if _, ok := seen[ip]; !ok {
			seen[ip] = struct{}{}
			ips = append(ips, ip)
		}
	}

	return ips
}

// applyAllowlistRules sets the OUTPUT chain to default-deny with ACCEPT
// exceptions for loopback, established connections, DNS, and the provided IPs.
// Also drops all IPv6 OUTPUT traffic.
//
// The DROP policy is set first to close the TOCTOU window: no outbound traffic
// is permitted between policy activation and ACCEPT rule insertion. Each rule
// is applied via a separate executor.Run call to avoid shell interpretation.
func applyAllowlistRules(ctx context.Context, executor Executor, containerID string, allowedIPs []string) error {
	// Set default-deny first to close any window of unrestricted access.
	// Then add ACCEPT exceptions. Order matters: loopback, conntrack, DNS, then allowed IPs.
	rules := [][]string{
		{"exec", "--user", "0", containerID, "iptables", "-P", "OUTPUT", "DROP"},
		{"exec", "--user", "0", containerID, "ip6tables", "-P", "OUTPUT", "DROP"},
		{"exec", "--user", "0", containerID, "iptables", "-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT"},
		{"exec", "--user", "0", containerID, "iptables", "-A", "OUTPUT", "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
		{"exec", "--user", "0", containerID, "iptables", "-A", "OUTPUT", "-p", "udp", "--dport", "53", "-j", "ACCEPT"},
		{"exec", "--user", "0", containerID, "iptables", "-A", "OUTPUT", "-p", "tcp", "--dport", "53", "-j", "ACCEPT"},
	}
	for _, ip := range allowedIPs {
		rules = append(rules, []string{"exec", "--user", "0", containerID, "iptables", "-A", "OUTPUT", "-d", ip, "-j", "ACCEPT"})
	}
	return runRules(ctx, executor, rules, "apply allowlist rules")
}

// runRules executes a sequence of container commands in order. Returns the
// first error, wrapped with the provided context string.
func runRules(ctx context.Context, executor Executor, rules [][]string, errContext string) error {
	for _, args := range rules {
		if err := executor.Run(ctx, nil, nil, args...); err != nil {
			return fmt.Errorf("%s: %w", errContext, err)
		}
	}
	return nil
}

// gatewayIP returns the gateway IP address for the given container network.
// Supports both Docker (.IPAM.Config[].Gateway) and Podman (.subnets[].gateway)
// JSON structures. Returns empty string if no gateway is configured.
func gatewayIP(ctx context.Context, executor Executor, network string) (string, error) {
	output, err := executor.Output(ctx, "network", "inspect", network)
	if err != nil {
		return "", fmt.Errorf("network inspect: %w", err)
	}

	ip := parseGatewayIP(output)
	if ip == "" {
		return "", nil
	}

	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("invalid gateway IP %q from network %s", ip, network)
	}

	return ip, nil
}

// parseGatewayIP extracts the first gateway IP from network inspect JSON.
// Handles both Docker (.IPAM.Config[].Gateway) and Podman (.subnets[].gateway)
// formats in a single pass. Both runtimes return a JSON array; the struct
// covers both schemas and returns the first non-empty gateway found.
func parseGatewayIP(jsonOutput string) string {
	var networks []struct {
		IPAM struct {
			Config []struct {
				Gateway string `json:"Gateway"`
			} `json:"Config"`
		} `json:"IPAM"`
		Subnets []struct {
			Gateway string `json:"gateway"`
		} `json:"subnets"`
	}
	if err := json.Unmarshal([]byte(jsonOutput), &networks); err != nil {
		return ""
	}
	for _, n := range networks {
		for _, c := range n.IPAM.Config {
			if c.Gateway != "" {
				return c.Gateway
			}
		}
		for _, s := range n.Subnets {
			if s.Gateway != "" {
				return s.Gateway
			}
		}
	}
	return ""
}

// hostInternalIP resolves host.docker.internal from inside the container.
// Returns empty string if the name does not resolve (not an error).
// No --user 0 needed: getent is read-only and does not require root privileges.
func hostInternalIP(ctx context.Context, executor Executor, containerID string) (string, error) {
	output, err := executor.Output(ctx, "exec", containerID, "getent", "hosts", "host.docker.internal")
	if err != nil {
		// Resolution failure is not an error — the name simply does not exist
		// in this environment (e.g., Linux Docker Engine without --add-host).
		return "", nil
	}

	output = strings.TrimSpace(output)
	if output == "" {
		return "", nil
	}

	// getent hosts output format: "<ip>  <hostname>"
	fields := strings.Fields(output)
	if len(fields) == 0 {
		return "", nil
	}

	ip := fields[0]
	if net.ParseIP(ip) == nil {
		// Invalid IP in getent output — treat as unresolved.
		return "", nil
	}

	return ip, nil
}

// applyFirewallRules blocks outbound traffic to the specified IPs using
// iptables DROP rules on the OUTPUT chain. Requires NET_ADMIN capability.
// Each rule is applied via a separate executor.Run call to avoid shell
// interpretation of IP addresses.
func applyFirewallRules(ctx context.Context, executor Executor, containerID string, blockIPs []string) error {
	rules := make([][]string, len(blockIPs))
	for i, ip := range blockIPs {
		rules[i] = []string{"exec", "--user", "0", containerID, "iptables", "-I", "OUTPUT", "-d", ip, "-j", "DROP"}
	}
	return runRules(ctx, executor, rules, "apply firewall rules")
}

// setupFirewall orchestrates gateway IP detection, host.docker.internal
// resolution, and iptables rule application. When allowedHosts is non-empty,
// it resolves hostnames and applies allowlist rules before gateway blocking.
// It is called after container creation and after container reuse-start for
// non-"none" networks.
func setupFirewall(ctx context.Context, executor Executor, containerID, network string, allowedHosts []string, stderr io.Writer) error {
	// Without allowedHosts the OUTPUT chain keeps its default ACCEPT policy;
	// only the gateway (and host.docker.internal) IPs are blocked. Internet
	// egress to non-gateway destinations remains unrestricted. When
	// allowedHosts is non-empty, applyAllowlistRules sets OUTPUT to DROP and
	// adds explicit ACCEPT rules for resolved IPs.

	// Apply allowlist rules first (when specified).
	if len(allowedHosts) > 0 {
		resolvedIPs, err := resolveAllowedHosts(ctx, executor, containerID, allowedHosts)
		if err != nil {
			return fmt.Errorf("resolve allowed hosts: %w", err)
		}

		if err := applyAllowlistRules(ctx, executor, containerID, resolvedIPs); err != nil {
			return fmt.Errorf("allowlist rules: %w", err)
		}
	}

	gw, err := gatewayIP(ctx, executor, network)
	if err != nil {
		return fmt.Errorf("detect gateway: %w", err)
	}

	if gw == "" {
		fmt.Fprintf(stderr, "warning: no gateway IP found for network %q, skipping firewall rules\n", network)
		return nil
	}

	blockIPs := []string{gw}

	hostIP, err := hostInternalIP(ctx, executor, containerID)
	if err != nil {
		return fmt.Errorf("detect host.docker.internal: %w", err)
	}

	if hostIP != "" && hostIP != gw {
		blockIPs = append(blockIPs, hostIP)
	}

	if err := applyFirewallRules(ctx, executor, containerID, blockIPs); err != nil {
		return fmt.Errorf("firewall rules: %w", err)
	}

	return nil
}
