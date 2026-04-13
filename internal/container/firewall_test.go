package container

import (
	"io"
	"net"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestGatewayIP(t *testing.T) {
	tests := []struct {
		name      string
		network   string
		output    string
		outputErr error
		wantIP    string
		wantErr   string
		wantArgs  []string
	}{
		{
			name:    "docker format returns valid gateway IP",
			network: "bridge",
			output:  `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`,
			wantIP:  "172.17.0.1",
			wantArgs: []string{
				"network", "inspect", "bridge",
			},
		},
		{
			name:    "podman format returns valid gateway IP",
			network: "podman",
			output:  `[{"subnets":[{"subnet":"10.88.0.0/16","gateway":"10.88.0.1"}]}]`,
			wantIP:  "10.88.0.1",
			wantArgs: []string{
				"network", "inspect", "podman",
			},
		},
		{
			name:    "named network",
			network: "my-net",
			output:  `[{"IPAM":{"Config":[{"Gateway":"192.168.1.1"}]}}]`,
			wantIP:  "192.168.1.1",
			wantArgs: []string{
				"network", "inspect", "my-net",
			},
		},
		{
			name:    "empty IPAM config returns empty string",
			network: "bridge",
			output:  `[{"IPAM":{"Config":[]}}]`,
			wantIP:  "",
		},
		{
			name:    "empty JSON array returns empty string",
			network: "bridge",
			output:  `[]`,
			wantIP:  "",
		},
		{
			name:    "completely empty output returns empty string",
			network: "bridge",
			output:  "",
			wantIP:  "",
		},
		{
			name:      "executor error propagated",
			network:   "bridge",
			outputErr: errFake,
			wantErr:   "network inspect",
		},
		{
			name:    "invalid IP format rejected",
			network: "bridge",
			output:  `[{"IPAM":{"Config":[{"Gateway":"not-an-ip"}]}}]`,
			wantErr: "invalid gateway IP",
		},
		{
			name:    "injection payload rejected by net.ParseIP",
			network: "bridge",
			output:  `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1; rm -rf /"}]}}]`,
			wantErr: "invalid gateway IP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeMultiExecutor{
				outputResults: []outputResult{{output: tt.output, err: tt.outputErr}},
			}

			ip, err := gatewayIP(t.Context(), exec, tt.network)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("gatewayIP() = %q, want error containing %q", ip, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("gatewayIP() error = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("gatewayIP() unexpected error: %v", err)
			}

			if ip != tt.wantIP {
				t.Errorf("gatewayIP() = %q, want %q", ip, tt.wantIP)
			}

			if tt.wantArgs != nil {
				if len(exec.outputCalls) != 1 {
					t.Fatalf("gatewayIP() made %d Output calls, want 1", len(exec.outputCalls))
				}
				if diff := cmp.Diff(tt.wantArgs, exec.outputCalls[0]); diff != "" {
					t.Errorf("gatewayIP() args mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestParseGatewayIP(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantIP string
	}{
		{
			name:   "docker format",
			input:  `[{"IPAM":{"Config":[{"Subnet":"172.17.0.0/16","Gateway":"172.17.0.1"}]}}]`,
			wantIP: "172.17.0.1",
		},
		{
			name:   "podman format",
			input:  `[{"name":"podman","subnets":[{"subnet":"10.88.0.0/16","gateway":"10.88.0.1"}]}]`,
			wantIP: "10.88.0.1",
		},
		{
			name:   "empty docker config",
			input:  `[{"IPAM":{"Config":[]}}]`,
			wantIP: "",
		},
		{
			name:   "empty podman subnets",
			input:  `[{"subnets":[]}]`,
			wantIP: "",
		},
		{
			name:   "empty array",
			input:  `[]`,
			wantIP: "",
		},
		{
			name:   "invalid JSON",
			input:  `not json`,
			wantIP: "",
		},
		{
			name:   "injection payload in gateway field",
			input:  `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1; rm -rf /"}]}}]`,
			wantIP: "172.17.0.1; rm -rf /",
		},
		{
			name:   "empty string",
			input:  "",
			wantIP: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGatewayIP(tt.input)
			if got != tt.wantIP {
				t.Errorf("parseGatewayIP() = %q, want %q", got, tt.wantIP)
			}
		})
	}
}

func TestHostInternalIP(t *testing.T) {
	tests := []struct {
		name        string
		containerID string
		output      string
		outputErr   error
		wantIP      string
		wantErr     string
		wantArgs    []string
	}{
		{
			name:        "resolves host.docker.internal",
			containerID: "abc123",
			output:      "192.168.65.254  host.docker.internal\n",
			wantIP:      "192.168.65.254",
			wantArgs:    []string{"exec", "abc123", "getent", "hosts", "host.docker.internal"},
		},
		{
			name:        "does not resolve is not an error",
			containerID: "abc123",
			outputErr:   errFake,
			wantIP:      "",
		},
		{
			name:        "empty output returns empty string",
			containerID: "abc123",
			output:      "",
			wantIP:      "",
		},
		{
			name:        "invalid IP in getent output rejected",
			containerID: "abc123",
			output:      "not-an-ip  host.docker.internal\n",
			wantIP:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeMultiExecutor{
				outputResults: []outputResult{{output: tt.output, err: tt.outputErr}},
			}

			ip, err := hostInternalIP(t.Context(), exec, tt.containerID)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("hostInternalIP() = %q, want error containing %q", ip, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("hostInternalIP() error = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("hostInternalIP() unexpected error: %v", err)
			}

			if ip != tt.wantIP {
				t.Errorf("hostInternalIP() = %q, want %q", ip, tt.wantIP)
			}

			if tt.wantArgs != nil {
				if len(exec.outputCalls) != 1 {
					t.Fatalf("hostInternalIP() made %d Output calls, want 1", len(exec.outputCalls))
				}
				if diff := cmp.Diff(tt.wantArgs, exec.outputCalls[0]); diff != "" {
					t.Errorf("hostInternalIP() args mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestApplyFirewallRules(t *testing.T) {
	tests := []struct {
		name         string
		containerID  string
		blockIPs     []string
		runErr       error
		wantErr      string
		wantRunCalls [][]string
	}{
		{
			name:        "single IP blocked",
			containerID: "abc123",
			blockIPs:    []string{"172.17.0.1"},
			wantRunCalls: [][]string{
				{"exec", "--user", "0", "abc123", "iptables", "-I", "OUTPUT", "-d", "172.17.0.1", "-j", "DROP"},
			},
		},
		{
			name:        "multiple IPs blocked",
			containerID: "abc123",
			blockIPs:    []string{"172.17.0.1", "192.168.65.254"},
			wantRunCalls: [][]string{
				{"exec", "--user", "0", "abc123", "iptables", "-I", "OUTPUT", "-d", "172.17.0.1", "-j", "DROP"},
				{"exec", "--user", "0", "abc123", "iptables", "-I", "OUTPUT", "-d", "192.168.65.254", "-j", "DROP"},
			},
		},
		{
			name:        "executor error propagated",
			containerID: "abc123",
			blockIPs:    []string{"172.17.0.1"},
			runErr:      errFake,
			wantErr:     "apply firewall rules",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runResults := make([]runResult, len(tt.blockIPs))
			if tt.runErr != nil {
				runResults[0] = runResult{err: tt.runErr}
			}
			exec := &fakeMultiExecutor{
				runResults: runResults,
			}

			err := applyFirewallRules(t.Context(), exec, tt.containerID, tt.blockIPs)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("applyFirewallRules() = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("applyFirewallRules() error = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("applyFirewallRules() unexpected error: %v", err)
			}

			if diff := cmp.Diff(tt.wantRunCalls, exec.runCalls); diff != "" {
				t.Errorf("applyFirewallRules() run calls mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSetupFirewall(t *testing.T) {
	t.Run("gateway and host.docker.internal both found", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "192.168.65.254  host.docker.internal\n"},           // hostInternalIP
			},
			runResults: []runResult{
				{err: nil}, // iptables DROP gateway
				{err: nil}, // iptables DROP host.docker.internal
			},
		}

		var stderr strings.Builder
		err := setupFirewall(t.Context(), exec, "abc123", "bridge", nil, &stderr)
		if err != nil {
			t.Fatalf("setupFirewall() unexpected error: %v", err)
		}

		// Verify applyFirewallRules was called with both IPs as separate calls.
		wantCalls := [][]string{
			{"exec", "--user", "0", "abc123", "iptables", "-I", "OUTPUT", "-d", "172.17.0.1", "-j", "DROP"},
			{"exec", "--user", "0", "abc123", "iptables", "-I", "OUTPUT", "-d", "192.168.65.254", "-j", "DROP"},
		}
		if diff := cmp.Diff(wantCalls, exec.runCalls); diff != "" {
			t.Errorf("setupFirewall() run calls mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("gateway only no host.docker.internal", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: does not resolve
			},
			runResults: []runResult{
				{err: nil}, // iptables DROP gateway
			},
		}

		var stderr strings.Builder
		err := setupFirewall(t.Context(), exec, "abc123", "bridge", nil, &stderr)
		if err != nil {
			t.Fatalf("setupFirewall() unexpected error: %v", err)
		}

		wantCalls := [][]string{
			{"exec", "--user", "0", "abc123", "iptables", "-I", "OUTPUT", "-d", "172.17.0.1", "-j", "DROP"},
		}
		if diff := cmp.Diff(wantCalls, exec.runCalls); diff != "" {
			t.Errorf("setupFirewall() run calls mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("no gateway logs warning and skips", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: `[{"IPAM":{"Config":[]}}]`}, // gatewayIP: empty
			},
		}

		var stderr strings.Builder
		err := setupFirewall(t.Context(), exec, "abc123", "bridge", nil, &stderr)
		if err != nil {
			t.Fatalf("setupFirewall() unexpected error: %v", err)
		}

		// Should not have called applyFirewallRules.
		if len(exec.runCalls) != 0 {
			t.Errorf("setupFirewall() made %d Run calls, want 0 (no firewall rules)", len(exec.runCalls))
		}

		// Should have logged a warning.
		if !strings.Contains(stderr.String(), "warning") {
			t.Errorf("setupFirewall() stderr = %q, want containing %q", stderr.String(), "warning")
		}
	})

	t.Run("gateway inspect error propagated", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: "", err: errFake}, // gatewayIP: error
			},
		}

		err := setupFirewall(t.Context(), exec, "abc123", "bridge", nil, io.Discard)
		if err == nil {
			t.Fatal("setupFirewall() = nil, want error")
		}
		if !strings.Contains(err.Error(), "gateway") {
			t.Errorf("setupFirewall() error = %q, want containing %q", err.Error(), "gateway")
		}
	})

	t.Run("iptables failure propagated", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "", err: errFake},                                   // hostInternalIP: no resolve
			},
			runResults: []runResult{
				{err: errFake}, // first iptables call fails
			},
		}

		err := setupFirewall(t.Context(), exec, "abc123", "bridge", nil, io.Discard)
		if err == nil {
			t.Fatal("setupFirewall() = nil, want error")
		}
		if !strings.Contains(err.Error(), "firewall rules") {
			t.Errorf("setupFirewall() error = %q, want containing %q", err.Error(), "firewall rules")
		}
	})

	t.Run("duplicate gateway and host.docker.internal IP deduped", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`}, // gatewayIP
				{output: "172.17.0.1  host.docker.internal\n"},               // hostInternalIP: same as gateway
			},
			runResults: []runResult{
				{err: nil}, // single iptables call (deduped)
			},
		}

		var stderr strings.Builder
		err := setupFirewall(t.Context(), exec, "abc123", "bridge", nil, &stderr)
		if err != nil {
			t.Fatalf("setupFirewall() unexpected error: %v", err)
		}

		// Should block only one IP (deduped) — one run call.
		wantCalls := [][]string{
			{"exec", "--user", "0", "abc123", "iptables", "-I", "OUTPUT", "-d", "172.17.0.1", "-j", "DROP"},
		}
		if diff := cmp.Diff(wantCalls, exec.runCalls); diff != "" {
			t.Errorf("setupFirewall() run calls mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("with allowedHosts resolves and applies allowlist then gateway", func(t *testing.T) {
		// Allowlist: 2 policy + 4 base + 1 allowed = 7 calls.
		// Gateway: 1 call.
		// Total: 8 run calls.
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				// resolveAllowedHosts: getent ahostsv4 api.anthropic.com
				{output: "93.184.216.34   STREAM api.anthropic.com\n"},
				// gatewayIP
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`},
				// hostInternalIP
				{output: "", err: errFake},
			},
			runResults: make([]runResult, 8),
		}

		var stderr strings.Builder
		err := setupFirewall(t.Context(), exec, "abc123", "bridge", []string{"api.anthropic.com"}, &stderr)
		if err != nil {
			t.Fatalf("setupFirewall() unexpected error: %v", err)
		}

		wantCalls := [][]string{
			// Allowlist: DROP policy first.
			{"exec", "--user", "0", "abc123", "iptables", "-P", "OUTPUT", "DROP"},
			{"exec", "--user", "0", "abc123", "ip6tables", "-P", "OUTPUT", "DROP"},
			// Allowlist: ACCEPT exceptions.
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-p", "udp", "--dport", "53", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-p", "tcp", "--dport", "53", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-d", "93.184.216.34", "-j", "ACCEPT"},
			// Gateway blocking: INSERT at top.
			{"exec", "--user", "0", "abc123", "iptables", "-I", "OUTPUT", "-d", "172.17.0.1", "-j", "DROP"},
		}
		if diff := cmp.Diff(wantCalls, exec.runCalls); diff != "" {
			t.Errorf("setupFirewall() run calls mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("with allowedHosts resolution failure returns error", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				// resolveAllowedHosts: getent fails
				{output: "", err: errFake},
			},
		}

		err := setupFirewall(t.Context(), exec, "abc123", "bridge", []string{"unreachable.invalid"}, io.Discard)
		if err == nil {
			t.Fatal("setupFirewall() = nil, want error")
		}
		if !strings.Contains(err.Error(), "resolve allowed") {
			t.Errorf("setupFirewall() error = %q, want containing %q", err.Error(), "resolve allowed")
		}
	})

	t.Run("with allowedHosts allowlist rule failure returns error", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				// resolveAllowedHosts
				{output: "93.184.216.34   STREAM api.anthropic.com\n"},
			},
			runResults: []runResult{
				{err: errFake}, // first iptables policy call fails
			},
		}

		err := setupFirewall(t.Context(), exec, "abc123", "bridge", []string{"api.anthropic.com"}, io.Discard)
		if err == nil {
			t.Fatal("setupFirewall() = nil, want error")
		}
		if !strings.Contains(err.Error(), "allowlist rules") {
			t.Errorf("setupFirewall() error = %q, want containing %q", err.Error(), "allowlist rules")
		}
	})

	t.Run("with allowedHosts IP passthrough", func(t *testing.T) {
		// Allowlist: 2 policy + 4 base + 1 allowed = 7 calls. Gateway: 1 call. Total: 8.
		exec := &fakeMultiExecutor{
			outputResults: []outputResult{
				// No getent call for IP passthrough
				// gatewayIP
				{output: `[{"IPAM":{"Config":[{"Gateway":"172.17.0.1"}]}}]`},
				// hostInternalIP
				{output: "", err: errFake},
			},
			runResults: make([]runResult, 8),
		}

		var stderr strings.Builder
		err := setupFirewall(t.Context(), exec, "abc123", "bridge", []string{"10.0.0.1"}, &stderr)
		if err != nil {
			t.Fatalf("setupFirewall() unexpected error: %v", err)
		}

		// Verify the ACCEPT rule for the passed-through IP is present.
		found := false
		for _, call := range exec.runCalls {
			if len(call) >= 10 && call[7] == "-d" && call[8] == "10.0.0.1" && call[9] == "-j" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("setupFirewall() run calls missing ACCEPT rule for 10.0.0.1: %v", exec.runCalls)
		}
	})
}

func TestValidateAllowedHosts(t *testing.T) {
	tests := []struct {
		name    string
		hosts   []string
		wantErr string
	}{
		{
			name:  "valid hostname",
			hosts: []string{"api.anthropic.com"},
		},
		{
			name:  "valid IP address",
			hosts: []string{"93.184.216.34"},
		},
		{
			name:  "valid IPv6 address",
			hosts: []string{"::1"},
		},
		{
			name:  "multiple valid entries",
			hosts: []string{"api.anthropic.com", "93.184.216.34", "sentry.io"},
		},
		{
			name:  "single character hostname",
			hosts: []string{"a"},
		},
		{
			name:    "empty string rejected",
			hosts:   []string{""},
			wantErr: "empty",
		},
		{
			name:    "wildcard rejected",
			hosts:   []string{"*.example.com"},
			wantErr: "invalid",
		},
		{
			name:    "CIDR rejected",
			hosts:   []string{"10.0.0.0/8"},
			wantErr: "invalid",
		},
		{
			name:    "shell metacharacter semicolon rejected",
			hosts:   []string{"host;rm -rf /"},
			wantErr: "invalid",
		},
		{
			name:    "shell metacharacter ampersand rejected",
			hosts:   []string{"host&echo"},
			wantErr: "invalid",
		},
		{
			name:    "shell metacharacter pipe rejected",
			hosts:   []string{"host|cat"},
			wantErr: "invalid",
		},
		{
			name:    "shell metacharacter dollar rejected",
			hosts:   []string{"host$var"},
			wantErr: "invalid",
		},
		{
			name:    "shell metacharacter backtick rejected",
			hosts:   []string{"host`cmd`"},
			wantErr: "invalid",
		},
		{
			name:    "shell metacharacter paren rejected",
			hosts:   []string{"host(cmd)"},
			wantErr: "invalid",
		},
		{
			name:    "shell metacharacter braces rejected",
			hosts:   []string{"host{a,b}"},
			wantErr: "invalid",
		},
		{
			name:    "backslash rejected",
			hosts:   []string{"host\\path"},
			wantErr: "invalid",
		},
		{
			name:    "whitespace rejected",
			hosts:   []string{"host name"},
			wantErr: "prohibited",
		},
		{
			name:    "trailing dot rejected",
			hosts:   []string{"example.com."},
			wantErr: "invalid",
		},
		{
			name:    "leading dot rejected",
			hosts:   []string{".example.com"},
			wantErr: "invalid",
		},
		{
			name:    "leading hyphen rejected",
			hosts:   []string{"-example.com"},
			wantErr: "invalid",
		},
		{
			name:  "label-level trailing hyphen accepted (known limitation: regex validates overall pattern not per-label)",
			hosts: []string{"example-.com"},
		},
		{
			name:    "second entry invalid",
			hosts:   []string{"valid.com", "invalid;host"},
			wantErr: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAllowedHosts(tt.hosts)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ValidateAllowedHosts(%v) = nil, want error containing %q", tt.hosts, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("ValidateAllowedHosts(%v) error = %q, want containing %q", tt.hosts, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("ValidateAllowedHosts(%v) unexpected error: %v", tt.hosts, err)
			}
		})
	}
}

func TestResolveAllowedHosts(t *testing.T) {
	tests := []struct {
		name          string
		hosts         []string
		outputResults []outputResult
		wantIPs       []string
		wantErr       string
	}{
		{
			name:  "single hostname resolves to one IP",
			hosts: []string{"api.anthropic.com"},
			outputResults: []outputResult{
				{output: "93.184.216.34   STREAM api.anthropic.com\n93.184.216.34   DGRAM\n93.184.216.34   RAW\n"},
			},
			wantIPs: []string{"93.184.216.34"},
		},
		{
			name:  "single hostname resolves to multiple IPs",
			hosts: []string{"api.anthropic.com"},
			outputResults: []outputResult{
				{output: "93.184.216.34   STREAM api.anthropic.com\n104.16.0.1   STREAM api.anthropic.com\n"},
			},
			wantIPs: []string{"93.184.216.34", "104.16.0.1"},
		},
		{
			name:    "IP address passed through without resolution",
			hosts:   []string{"10.0.0.1"},
			wantIPs: []string{"10.0.0.1"},
		},
		{
			name:  "mixed hostnames and IPs",
			hosts: []string{"api.anthropic.com", "10.0.0.1"},
			outputResults: []outputResult{
				{output: "93.184.216.34   STREAM api.anthropic.com\n"},
			},
			wantIPs: []string{"93.184.216.34", "10.0.0.1"},
		},
		{
			name:  "resolution failure returns error",
			hosts: []string{"unreachable.invalid"},
			outputResults: []outputResult{
				{output: "", err: errFake},
			},
			wantErr: "resolve allowed host",
		},
		{
			name:  "duplicate IPs deduplicated",
			hosts: []string{"host1.example.com", "host2.example.com"},
			outputResults: []outputResult{
				{output: "93.184.216.34   STREAM host1.example.com\n"},
				{output: "93.184.216.34   STREAM host2.example.com\n"},
			},
			wantIPs: []string{"93.184.216.34"},
		},
		{
			name:  "invalid IP in getent output rejected",
			hosts: []string{"bad.example.com"},
			outputResults: []outputResult{
				{output: "not-an-ip   STREAM bad.example.com\n"},
			},
			wantErr: "invalid IP",
		},
		{
			name:  "empty getent output rejected",
			hosts: []string{"empty.example.com"},
			outputResults: []outputResult{
				{output: ""},
			},
			wantErr: "no addresses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeMultiExecutor{
				outputResults: tt.outputResults,
			}

			ips, err := resolveAllowedHosts(t.Context(), exec, "test-container", tt.hosts)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("resolveAllowedHosts() = %v, want error containing %q", ips, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("resolveAllowedHosts() error = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("resolveAllowedHosts() unexpected error: %v", err)
			}

			if diff := cmp.Diff(tt.wantIPs, ips); diff != "" {
				t.Errorf("resolveAllowedHosts() mismatch (-want +got):\n%s", diff)
			}

			// Verify IP addresses passed through without exec calls.
			for _, host := range tt.hosts {
				if net.ParseIP(host) != nil {
					// Should not have made an exec call for this host.
					for _, call := range exec.outputCalls {
						lastArg := call[len(call)-1]
						if lastArg == host {
							t.Errorf("resolveAllowedHosts() made exec call for IP %q, want passthrough", host)
						}
					}
				}
			}
		})
	}
}

func TestApplyAllowlistRules(t *testing.T) {
	t.Run("single allowed IP", func(t *testing.T) {
		// 2 policy rules + 4 base ACCEPT + 1 allowed IP = 7 calls.
		exec := &fakeMultiExecutor{
			runResults: make([]runResult, 7),
		}

		err := applyAllowlistRules(t.Context(), exec, "abc123", []string{"93.184.216.34"})
		if err != nil {
			t.Fatalf("applyAllowlistRules() unexpected error: %v", err)
		}

		wantCalls := [][]string{
			// DROP policy first (TOCTOU fix).
			{"exec", "--user", "0", "abc123", "iptables", "-P", "OUTPUT", "DROP"},
			{"exec", "--user", "0", "abc123", "ip6tables", "-P", "OUTPUT", "DROP"},
			// ACCEPT exceptions.
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-p", "udp", "--dport", "53", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-p", "tcp", "--dport", "53", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-d", "93.184.216.34", "-j", "ACCEPT"},
		}
		if diff := cmp.Diff(wantCalls, exec.runCalls); diff != "" {
			t.Errorf("applyAllowlistRules() run calls mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("multiple allowed IPs", func(t *testing.T) {
		// 2 policy + 4 base + 2 allowed = 8 calls.
		exec := &fakeMultiExecutor{
			runResults: make([]runResult, 8),
		}

		err := applyAllowlistRules(t.Context(), exec, "abc123", []string{"93.184.216.34", "104.16.0.1"})
		if err != nil {
			t.Fatalf("applyAllowlistRules() unexpected error: %v", err)
		}

		wantCalls := [][]string{
			{"exec", "--user", "0", "abc123", "iptables", "-P", "OUTPUT", "DROP"},
			{"exec", "--user", "0", "abc123", "ip6tables", "-P", "OUTPUT", "DROP"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-p", "udp", "--dport", "53", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-p", "tcp", "--dport", "53", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-d", "93.184.216.34", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-d", "104.16.0.1", "-j", "ACCEPT"},
		}
		if diff := cmp.Diff(wantCalls, exec.runCalls); diff != "" {
			t.Errorf("applyAllowlistRules() run calls mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("empty allowed IPs", func(t *testing.T) {
		// 2 policy + 4 base + 0 allowed = 6 calls.
		exec := &fakeMultiExecutor{
			runResults: make([]runResult, 6),
		}

		err := applyAllowlistRules(t.Context(), exec, "abc123", nil)
		if err != nil {
			t.Fatalf("applyAllowlistRules() unexpected error: %v", err)
		}

		wantCalls := [][]string{
			{"exec", "--user", "0", "abc123", "iptables", "-P", "OUTPUT", "DROP"},
			{"exec", "--user", "0", "abc123", "ip6tables", "-P", "OUTPUT", "DROP"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-p", "udp", "--dport", "53", "-j", "ACCEPT"},
			{"exec", "--user", "0", "abc123", "iptables", "-A", "OUTPUT", "-p", "tcp", "--dport", "53", "-j", "ACCEPT"},
		}
		if diff := cmp.Diff(wantCalls, exec.runCalls); diff != "" {
			t.Errorf("applyAllowlistRules() run calls mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("executor error propagated", func(t *testing.T) {
		exec := &fakeMultiExecutor{
			runResults: []runResult{{err: errFake}},
		}

		err := applyAllowlistRules(t.Context(), exec, "abc123", []string{"93.184.216.34"})
		if err == nil {
			t.Fatal("applyAllowlistRules() = nil, want error")
		}
		if !strings.Contains(err.Error(), "apply allowlist rules") {
			t.Errorf("applyAllowlistRules() error = %q, want containing %q", err.Error(), "apply allowlist rules")
		}
	})
}
