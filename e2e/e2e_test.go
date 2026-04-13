//go:build integration

package e2e_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// binaryPath holds the path to the built confine-ai binary.
var binaryPath string

// runtimePath holds the path to the detected container runtime.
var runtimePath string

func TestMain(m *testing.M) {
	for _, name := range []string{"docker", "podman"} {
		p, err := exec.LookPath(name)
		if err == nil {
			runtimePath = p
			break
		}
	}
	if runtimePath == "" {
		fmt.Println("SKIP: no container runtime (docker/podman) found on PATH")
		os.Exit(0)
	}

	// Use .scratch/tmp/ instead of system /tmp per project convention.
	scratchTmp := filepath.Join(mustCwd(), "..", ".scratch", "tmp")
	if err := os.MkdirAll(scratchTmp, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create scratch tmp dir: %v\n", err)
		os.Exit(1)
	}
	tmpDir, err := os.MkdirTemp(scratchTmp, "confine-ai-e2e-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	binaryPath = filepath.Join(tmpDir, "confine-ai")
	cmd := exec.Command("go", "build", "-o", binaryPath, ".")
	cmd.Dir = filepath.Join(mustCwd(), "..")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "build confine-ai: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func mustCwd() string {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "getwd: %v\n", err)
		os.Exit(1)
	}
	return dir
}

// runConfineEnv executes the built binary with the given args and extra
// environment variables merged on top of the inherited environment. Tests use
// this to isolate HOME so they can exercise ~/.confine-ai paths without touching
// real user state. Returns stdout, stderr, and the exit code; does not fail
// the test on non-zero exit.
func runConfineEnv(t *testing.T, extraEnv []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(binaryPath, args...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout, stderr, exitErr.ExitCode()
		}
		t.Fatalf("exec %v: %v", args, err)
	}
	return stdout, stderr, 0
}
