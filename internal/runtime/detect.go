// Package runtime provides container runtime detection for Docker-compatible CLIs.
package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// runtimeNames defines the search order for container runtime binaries.
// The first match wins. Docker is preferred over Podman.
var runtimeNames = []string{"docker", "podman"}

// knownRuntimes is the set of recognized runtime basenames for explicit path
// validation.
var knownRuntimes = map[string]bool{
	"docker": true,
	"podman": true,
}

// LookPathFunc matches the signature of exec.LookPath.
// Production callers pass exec.LookPath; tests pass a fake.
type LookPathFunc func(file string) (string, error)

// Runtime identifies a detected container runtime CLI.
// It is an immutable value object with no methods that mutate state.
type Runtime struct {
	// Name is the runtime identifier: "docker" or "podman".
	Name string

	// Path is the absolute path to the runtime binary.
	Path string
}

// DefaultNetwork returns the default network name for the runtime.
// Docker uses "bridge"; Podman uses "podman".
func (r Runtime) DefaultNetwork() string {
	if r.Name == "podman" {
		return "podman"
	}
	return "bridge"
}

// Detect finds a Docker-compatible container runtime on the host.
//
// When explicitPath is non-empty, Detect validates that the file exists, is
// executable, and has a recognized basename ("docker" or "podman"). No PATH
// search occurs.
//
// When explicitPath is empty, Detect searches PATH for runtimes in priority
// order (docker, then podman) using lookPath and returns the first one found.
//
// Returns an error if no runtime is found or the explicit path is invalid.
func Detect(explicitPath string, lookPath LookPathFunc) (Runtime, error) {
	if explicitPath != "" {
		return detectExplicit(explicitPath)
	}
	return detectFromPATH(lookPath)
}

// detectExplicit validates an explicit runtime path.
// Symlinks are resolved before basename validation to prevent a symlink
// named "docker" from bypassing the allowlist check.
func detectExplicit(explicitPath string) (Runtime, error) {
	resolved, err := filepath.EvalSymlinks(explicitPath)
	if err != nil {
		return Runtime{}, fmt.Errorf("%q: %w", explicitPath, err)
	}

	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return Runtime{}, fmt.Errorf("%q: %w", explicitPath, err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return Runtime{}, fmt.Errorf("%q: %w", explicitPath, err)
	}

	if info.Mode()&0o111 == 0 {
		return Runtime{}, fmt.Errorf("%q: not executable", explicitPath)
	}

	name := filepath.Base(resolved)
	if !knownRuntimes[name] {
		return Runtime{}, fmt.Errorf("%q: unrecognized runtime %q; expected docker or podman", explicitPath, name)
	}

	return Runtime{
		Name: name,
		Path: resolved,
	}, nil
}

// detectFromPATH searches PATH for known runtimes in priority order.
func detectFromPATH(lookPath LookPathFunc) (Runtime, error) {
	for _, name := range runtimeNames {
		p, err := lookPath(name)
		if err == nil {
			return Runtime{
				Name: name,
				Path: p,
			}, nil
		}
	}
	return Runtime{}, errors.New("no container runtime found; install docker or podman")
}
