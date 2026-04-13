package container

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// cleanResolve is a test resolver that normalizes paths without filesystem
// access, used for pure classification tests.
func cleanResolve(path string) (string, error) {
	return filepath.Clean(path), nil
}

func TestExtractMountSource(t *testing.T) {
	tests := []struct {
		name       string
		mount      string
		wantSource string
		wantBind   bool
	}{
		{
			name:       "bind mount with source",
			mount:      "type=bind,source=/home/user/data,target=/data",
			wantSource: "/home/user/data",
			wantBind:   true,
		},
		{
			name:       "volume mount skipped",
			mount:      "type=volume,source=myvolume,target=/data",
			wantSource: "",
			wantBind:   false,
		},
		{
			name:       "bind mount with src alias",
			mount:      "type=bind,src=/home/user/data,target=/data",
			wantSource: "/home/user/data",
			wantBind:   true,
		},
		{
			name:       "no type defaults to bind for absolute path",
			mount:      "source=/host/path,target=/container/path",
			wantSource: "/host/path",
			wantBind:   true,
		},
		{
			name:       "no type with relative source is volume",
			mount:      "source=namedvol,target=/data",
			wantSource: "",
			wantBind:   false,
		},
		{
			name:       "missing source field",
			mount:      "type=bind,target=/data",
			wantSource: "",
			wantBind:   true,
		},
		{
			name:       "readonly option preserved",
			mount:      "type=bind,source=/host/data,target=/data,readonly",
			wantSource: "/host/data",
			wantBind:   true,
		},
		{
			name:       "unknown type treated as non-bind",
			mount:      "type=tmpfs,target=/tmp",
			wantSource: "",
			wantBind:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, isBind := extractMountSource(tt.mount)
			if source != tt.wantSource {
				t.Errorf("extractMountSource(%q) source = %q, want %q", tt.mount, source, tt.wantSource)
			}
			if isBind != tt.wantBind {
				t.Errorf("extractMountSource(%q) isBind = %v, want %v", tt.mount, isBind, tt.wantBind)
			}
		})
	}
}

// TestClassifyPath_Tier1Blocked tests pure path classification without
// filesystem access. classifyPath is the classification core of ValidateMounts.
func TestClassifyPath_Tier1Blocked(t *testing.T) {
	homeDir := "/home/testuser"

	tests := []struct {
		name        string
		path        string
		wantBlocked *MountRisk
	}{
		{
			name:        "root blocked",
			path:        "/",
			wantBlocked: &MountRisk{Source: "/", Tier: 1, Reason: "exposes entire host filesystem"},
		},
		{
			name:        "/etc blocked",
			path:        "/etc",
			wantBlocked: &MountRisk{Source: "/etc", Tier: 1, Reason: "exposes host system configuration"},
		},
		{
			name:        "/tmp blocked",
			path:        "/tmp",
			wantBlocked: &MountRisk{Source: "/tmp", Tier: 1, Reason: "shared temporary directory; use a dedicated mount instead"},
		},
		{
			name:        "docker socket blocked",
			path:        "/var/run/docker.sock",
			wantBlocked: &MountRisk{Source: "/var/run/docker.sock", Tier: 1, Reason: "enables container escape via Docker API"},
		},
		{
			name:        "podman socket blocked",
			path:        "/var/run/podman/podman.sock",
			wantBlocked: &MountRisk{Source: "/var/run/podman/podman.sock", Tier: 1, Reason: "enables container escape via Podman API"},
		},
		{
			name:        "home directory blocked",
			path:        "/home/testuser",
			wantBlocked: &MountRisk{Source: "/home/testuser", Tier: 1, Reason: "exposes user home directory"},
		},
		{
			name:        "parent of home directory blocked (/home)",
			path:        "/home",
			wantBlocked: &MountRisk{Source: "/home", Tier: 1, Reason: "parent of home directory; exposes user data"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked, risky := classifyPath(tt.path, homeDir)
			if diff := cmp.Diff(tt.wantBlocked, blocked); diff != "" {
				t.Errorf("classifyPath(%q) blocked mismatch (-want +got):\n%s", tt.path, diff)
			}
			if risky != nil {
				t.Errorf("classifyPath(%q) risky = %v, want nil", tt.path, risky)
			}
		})
	}
}

func TestClassifyPath_Tier1HomeDirFallback(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		wantBlocked *MountRisk
	}{
		{
			name:        "/home blocked when homeDir empty",
			path:        "/home",
			wantBlocked: &MountRisk{Source: "/home", Tier: 1, Reason: "parent of home directory; exposes user data (home detection failed)"},
		},
		{
			name:        "/Users blocked when homeDir empty",
			path:        "/Users",
			wantBlocked: &MountRisk{Source: "/Users", Tier: 1, Reason: "parent of home directory; exposes user data (home detection failed)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked, _ := classifyPath(tt.path, "")
			if diff := cmp.Diff(tt.wantBlocked, blocked); diff != "" {
				t.Errorf("classifyPath(%q) blocked mismatch (-want +got):\n%s", tt.path, diff)
			}
		})
	}
}

func TestClassifyPath_Tier2RiskyPatterns(t *testing.T) {
	homeDir := "/home/testuser"

	tests := []struct {
		name      string
		path      string
		wantRisky *MountRisk
	}{
		{
			name:      ".ssh directory risky",
			path:      "/home/testuser/.ssh",
			wantRisky: &MountRisk{Source: "/home/testuser/.ssh", Tier: 2, Reason: "contains sensitive path .ssh"},
		},
		{
			name:      ".gnupg directory risky",
			path:      "/home/testuser/.gnupg",
			wantRisky: &MountRisk{Source: "/home/testuser/.gnupg", Tier: 2, Reason: "contains sensitive path .gnupg"},
		},
		{
			name:      ".aws directory risky",
			path:      "/home/testuser/.aws",
			wantRisky: &MountRisk{Source: "/home/testuser/.aws", Tier: 2, Reason: "contains sensitive path .aws"},
		},
		{
			name:      ".env file risky",
			path:      "/home/testuser/project/.env",
			wantRisky: &MountRisk{Source: "/home/testuser/project/.env", Tier: 2, Reason: "contains sensitive path .env"},
		},
		{
			name:      "credentials risky",
			path:      "/home/testuser/.config/credentials",
			wantRisky: &MountRisk{Source: "/home/testuser/.config/credentials", Tier: 2, Reason: "contains sensitive path credentials"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked, risky := classifyPath(tt.path, homeDir)
			if blocked != nil {
				t.Errorf("classifyPath(%q) blocked = %v, want nil", tt.path, blocked)
			}
			if diff := cmp.Diff(tt.wantRisky, risky); diff != "" {
				t.Errorf("classifyPath(%q) risky mismatch (-want +got):\n%s", tt.path, diff)
			}
		})
	}
}

func TestClassifyPath_Tier2BroadDirectories(t *testing.T) {
	homeDir := "/home/testuser"

	tests := []struct {
		name      string
		path      string
		wantRisky *MountRisk
	}{
		{
			name:      "/opt is broad directory",
			path:      "/opt",
			wantRisky: &MountRisk{Source: "/opt", Tier: 2, Reason: "broad directory mount; larger scope than typically needed"},
		},
		{
			name:      "/usr/local is broad directory",
			path:      "/usr/local",
			wantRisky: &MountRisk{Source: "/usr/local", Tier: 2, Reason: "broad directory mount; larger scope than typically needed"},
		},
		{
			name: "/opt/tool is not broad",
			path: "/opt/tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked, risky := classifyPath(tt.path, homeDir)
			if blocked != nil {
				t.Errorf("classifyPath(%q) blocked = %v, want nil", tt.path, blocked)
			}
			if diff := cmp.Diff(tt.wantRisky, risky); diff != "" {
				t.Errorf("classifyPath(%q) risky mismatch (-want +got):\n%s", tt.path, diff)
			}
		})
	}
}

func TestClassifyPath_Tier3Allowed(t *testing.T) {
	homeDir := "/home/testuser"

	tests := []struct {
		name string
		path string
	}{
		{name: "home subdirectory allowed", path: "/home/testuser/.config/git"},
		{name: "workspace subfolder allowed", path: "/home/testuser/projects/myapp/data"},
		{name: "workspace allowed", path: "/home/testuser/projects/myapp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked, risky := classifyPath(tt.path, homeDir)
			if blocked != nil {
				t.Errorf("classifyPath(%q) blocked = %v, want nil", tt.path, blocked)
			}
			if risky != nil {
				t.Errorf("classifyPath(%q) risky = %v, want nil", tt.path, risky)
			}
		})
	}
}

// TestValidateMounts_Classification tests ValidateMounts path classification
// using cleanResolve (no filesystem access). Verifies mount source extraction,
// path normalization, and classification integration.
func TestValidateMounts_Classification(t *testing.T) {
	homeDir := "/home/testuser"

	t.Run("root mount blocked", func(t *testing.T) {
		blocked, _ := ValidateMounts("/home/testuser/project", []string{
			"type=bind,source=/,target=/host",
		}, homeDir, cleanResolve)

		if len(blocked) != 1 {
			t.Fatalf("ValidateMounts() blocked count = %d, want 1; blocked = %v", len(blocked), blocked)
		}
		if blocked[0].Reason != "exposes entire host filesystem" {
			t.Errorf("ValidateMounts() blocked[0].Reason = %q, want root reason", blocked[0].Reason)
		}
	})

	t.Run("workspace as home blocked", func(t *testing.T) {
		blocked, _ := ValidateMounts("/home/testuser", nil, homeDir, cleanResolve)
		if len(blocked) != 1 {
			t.Fatalf("ValidateMounts() blocked count = %d, want 1; blocked = %v", len(blocked), blocked)
		}
		if blocked[0].Reason != "exposes user home directory" {
			t.Errorf("ValidateMounts() blocked[0].Reason = %q, want home directory reason", blocked[0].Reason)
		}
	})

	t.Run("path normalization with trailing slashes", func(t *testing.T) {
		blocked, _ := ValidateMounts("/home/testuser/project", []string{
			"type=bind,source=///,target=/host",
		}, homeDir, cleanResolve)
		if len(blocked) != 1 {
			t.Errorf("ValidateMounts() blocked count = %d, want 1; blocked = %v", len(blocked), blocked)
		}
	})

	t.Run("dot-dot segments normalized", func(t *testing.T) {
		blocked, _ := ValidateMounts("/home/testuser/project", []string{
			"type=bind,source=/home/testuser/../testuser,target=/home",
		}, homeDir, cleanResolve)
		if len(blocked) != 1 {
			t.Errorf("ValidateMounts() blocked count = %d, want 1; blocked = %v", len(blocked), blocked)
		}
	})

	t.Run("volume mounts skipped", func(t *testing.T) {
		blocked, risky := ValidateMounts("/home/testuser/project", []string{
			"type=volume,source=myvolume,target=/data",
			"source=namedvol,target=/data",
		}, homeDir, cleanResolve)
		if len(blocked) > 0 {
			t.Errorf("ValidateMounts() blocked = %v, want empty", blocked)
		}
		if len(risky) > 0 {
			t.Errorf("ValidateMounts() risky = %v, want empty", risky)
		}
	})

	t.Run("tier 1 unconditional even with safe workspace", func(t *testing.T) {
		blocked, _ := ValidateMounts("/home/testuser/project", []string{
			"type=bind,source=/,target=/host",
		}, homeDir, cleanResolve)
		if len(blocked) == 0 {
			t.Error("ValidateMounts() blocked is empty, want tier 1 for root mount")
		}
	})

	t.Run("multiple violations", func(t *testing.T) {
		blocked, risky := ValidateMounts("/home/testuser/project", []string{
			"type=bind,source=/,target=/host",
			"type=bind,source=/etc,target=/etc",
			"type=bind,source=/home/testuser/.ssh,target=/ssh",
			"type=bind,source=/opt,target=/opt",
		}, homeDir, cleanResolve)

		if len(blocked) != 2 {
			t.Errorf("ValidateMounts() blocked count = %d, want 2; blocked = %v", len(blocked), blocked)
		}
		if len(risky) != 2 {
			t.Errorf("ValidateMounts() risky count = %d, want 2; risky = %v", len(risky), risky)
		}
	})
}

// TestValidateMounts_SymlinkResolution tests symlink handling with real
// filesystem paths and filepath.EvalSymlinks.
func TestValidateMounts_SymlinkResolution(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir() error: %v", err)
	}

	t.Run("symlink to blocked path is blocked", func(t *testing.T) {
		workspace := t.TempDir()
		linkPath := filepath.Join(workspace, "link-to-etc")
		if err := os.Symlink("/etc", linkPath); err != nil {
			t.Fatal(err)
		}

		blocked, _ := ValidateMounts(workspace, []string{
			"type=bind,source=" + linkPath + ",target=/data",
		}, homeDir, filepath.EvalSymlinks)

		found := false
		for _, b := range blocked {
			if b.Reason == "exposes host system configuration" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ValidateMounts() did not block symlink to /etc; blocked = %v", blocked)
		}
	})

	t.Run("symlink to allowed path is allowed", func(t *testing.T) {
		workspace := t.TempDir()
		target := filepath.Join(workspace, "real-data")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		linkPath := filepath.Join(workspace, "link-to-data")
		if err := os.Symlink(target, linkPath); err != nil {
			t.Fatal(err)
		}

		blocked, risky := ValidateMounts(workspace, []string{
			"type=bind,source=" + linkPath + ",target=/data",
		}, homeDir, filepath.EvalSymlinks)
		if len(blocked) > 0 {
			t.Errorf("ValidateMounts() blocked = %v, want empty", blocked)
		}
		if len(risky) > 0 {
			t.Errorf("ValidateMounts() risky = %v, want empty", risky)
		}
	})

	t.Run("broken symlink blocked as tier 1", func(t *testing.T) {
		workspace := t.TempDir()
		linkPath := filepath.Join(workspace, "broken-link")
		if err := os.Symlink("/nonexistent/target", linkPath); err != nil {
			t.Fatal(err)
		}

		blocked, _ := ValidateMounts(workspace, []string{
			"type=bind,source=" + linkPath + ",target=/data",
		}, homeDir, filepath.EvalSymlinks)

		found := false
		for _, b := range blocked {
			if b.Source == linkPath && b.Tier == 1 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ValidateMounts() did not block broken symlink; blocked = %v", blocked)
		}
	})

	t.Run("unresolvable path blocked as tier 1", func(t *testing.T) {
		workspace := t.TempDir()

		blocked, _ := ValidateMounts(workspace, []string{
			"type=bind,source=/nonexistent/broken/path,target=/data",
		}, homeDir, filepath.EvalSymlinks)

		found := false
		for _, b := range blocked {
			if b.Source == "/nonexistent/broken/path" && b.Tier == 1 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ValidateMounts() did not block unresolvable path; blocked = %v", blocked)
		}
	})
}

// TestBlockedPathsInitResolvesSymlinks verifies that init() populates
// blockedPaths with both canonical and symlink-resolved forms so that
// platforms with symlinked system paths (e.g., macOS /etc → /private/etc)
// are correctly blocked.
func TestBlockedPathsInitResolvesSymlinks(t *testing.T) {
	// Every canonical path from blockedPathDefs must be in blockedPaths.
	for _, def := range blockedPathDefs {
		reason, ok := blockedPaths[def.path]
		if !ok {
			t.Errorf("blockedPaths missing canonical path %q", def.path)
			continue
		}
		if reason != def.reason {
			t.Errorf("blockedPaths[%q] = %q, want %q", def.path, reason, def.reason)
		}
	}

	// If any canonical path is a symlink, the resolved form must also be present.
	for _, def := range blockedPathDefs {
		resolved, err := filepath.EvalSymlinks(def.path)
		if err != nil {
			// Path does not exist on this platform (e.g., docker.sock); skip.
			continue
		}
		if resolved == def.path {
			continue
		}
		reason, ok := blockedPaths[resolved]
		if !ok {
			t.Errorf("blockedPaths missing resolved path %q (symlink from %q)", resolved, def.path)
			continue
		}
		if reason != def.reason {
			t.Errorf("blockedPaths[%q] = %q, want %q", resolved, reason, def.reason)
		}
	}
}

func TestIsParentOf(t *testing.T) {
	tests := []struct {
		name   string
		parent string
		child  string
		want   bool
	}{
		{name: "root is parent of everything", parent: "/", child: "/home", want: true},
		{name: "parent of nested path", parent: "/home", child: "/home/user", want: true},
		{name: "equal paths are not parent", parent: "/home", child: "/home", want: false},
		{name: "sibling is not parent", parent: "/home/alice", child: "/home/bob", want: false},
		{name: "prefix but not parent", parent: "/home/us", child: "/home/user", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isParentOf(tt.parent, tt.child)
			if got != tt.want {
				t.Errorf("isParentOf(%q, %q) = %v, want %v", tt.parent, tt.child, got, tt.want)
			}
		})
	}
}

func TestProbeWorkspaceRisks(t *testing.T) {
	t.Run("workspace with sensitive files", func(t *testing.T) {
		dir := t.TempDir()

		// Create sensitive files/directories.
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=val"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, ".ssh"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, ".gnupg"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, ".aws"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte("creds"), 0o644); err != nil {
			t.Fatal(err)
		}

		risks := probeWorkspaceRisks(dir)
		if len(risks) != 5 {
			t.Errorf("probeWorkspaceRisks() found %d risks, want 5; risks = %v", len(risks), risks)
		}

		for _, r := range risks {
			if r.Tier != 2 {
				t.Errorf("probeWorkspaceRisks() risk tier = %d, want 2; risk = %v", r.Tier, r)
			}
		}
	})

	t.Run("clean workspace", func(t *testing.T) {
		dir := t.TempDir()

		risks := probeWorkspaceRisks(dir)
		if len(risks) != 0 {
			t.Errorf("probeWorkspaceRisks() found %d risks, want 0; risks = %v", len(risks), risks)
		}
	})

	t.Run("partial sensitive files", func(t *testing.T) {
		dir := t.TempDir()

		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("val"), 0o644); err != nil {
			t.Fatal(err)
		}

		risks := probeWorkspaceRisks(dir)
		if len(risks) != 1 {
			t.Errorf("probeWorkspaceRisks() found %d risks, want 1; risks = %v", len(risks), risks)
		}
	})

	t.Run("nonexistent workspace returns empty", func(t *testing.T) {
		risks := probeWorkspaceRisks("/nonexistent/workspace/path")
		if len(risks) != 0 {
			t.Errorf("probeWorkspaceRisks() found %d risks, want 0", len(risks))
		}
	})
}
