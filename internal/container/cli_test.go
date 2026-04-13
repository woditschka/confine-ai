package container

import (
	"errors"
	"os"
	goexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewCLIExecutor(t *testing.T) {
	exec := NewCLIExecutor("/usr/bin/docker")
	if exec.path != "/usr/bin/docker" {
		t.Errorf("NewCLIExecutor(%q).path = %q, want %q", "/usr/bin/docker", exec.path, "/usr/bin/docker")
	}
}

func TestCLIExecutor_Output(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses shell script as fake binary")
	}

	// Create a fake binary that echoes its arguments, one per line.
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fake-runtime")
	script := "#!/bin/sh\nfor arg in \"$@\"; do echo \"$arg\"; done\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", fakeBin, err)
	}

	exec := NewCLIExecutor(fakeBin)
	got, err := exec.Output(t.Context(), "ps", "--all", "--filter", "label=key=value", "--format", "{{.ID}}")
	if err != nil {
		t.Fatalf("CLIExecutor.Output() unexpected error: %v", err)
	}

	want := "ps\n--all\n--filter\nlabel=key=value\n--format\n{{.ID}}\n"
	if got != want {
		t.Errorf("CLIExecutor.Output() = %q, want %q", got, want)
	}
}

func TestCLIExecutor_Output_Error(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses shell script as fake binary")
	}

	// Create a fake binary that exits with an error.
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fake-runtime")
	script := "#!/bin/sh\necho 'something went wrong' >&2\nexit 1\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", fakeBin, err)
	}

	exec := NewCLIExecutor(fakeBin)
	_, err := exec.Output(t.Context(), "ps")
	if err == nil {
		t.Fatal("CLIExecutor.Output() = nil error, want error for non-zero exit")
	}

	if !strings.Contains(err.Error(), "run ") {
		t.Errorf("CLIExecutor.Output() error = %q, want error containing runtime path context", err.Error())
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("CLIExecutor.Output() error = %q, want error containing stderr output", err.Error())
	}
}

func TestCLIExecutor_Run(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses shell script as fake binary")
	}

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fake-runtime")
	script := "#!/bin/sh\necho \"out: $@\"\necho \"err: $@\" >&2\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", fakeBin, err)
	}

	exec := NewCLIExecutor(fakeBin)

	var stdout, stderr strings.Builder
	err := exec.Run(t.Context(), &stdout, &stderr, "start", "abc123")
	if err != nil {
		t.Fatalf("CLIExecutor.Run() unexpected error: %v", err)
	}

	if !strings.Contains(stdout.String(), "start abc123") {
		t.Errorf("CLIExecutor.Run() stdout = %q, want containing %q", stdout.String(), "start abc123")
	}
	if !strings.Contains(stderr.String(), "start abc123") {
		t.Errorf("CLIExecutor.Run() stderr = %q, want containing %q", stderr.String(), "start abc123")
	}
}

func TestCLIExecutor_Run_Error(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses shell script as fake binary")
	}

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fake-runtime")
	script := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", fakeBin, err)
	}

	exec := NewCLIExecutor(fakeBin)
	err := exec.Run(t.Context(), nil, nil, "start", "abc123")
	if err == nil {
		t.Fatal("CLIExecutor.Run() = nil error, want error for non-zero exit")
	}

	if !strings.Contains(err.Error(), "run ") {
		t.Errorf("CLIExecutor.Run() error = %q, want containing runtime path context", err.Error())
	}
}

func TestCLIExecutor_Run_ErrorWithStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses shell script as fake binary")
	}

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fake-runtime")
	script := "#!/bin/sh\necho 'iptables: command not found' >&2\nexit 127\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", fakeBin, err)
	}

	exec := NewCLIExecutor(fakeBin)
	err := exec.Run(t.Context(), nil, nil, "exec", "abc123", "iptables")
	if err == nil {
		t.Fatal("CLIExecutor.Run() = nil error, want error for non-zero exit")
	}

	if !strings.Contains(err.Error(), "iptables: command not found") {
		t.Errorf("CLIExecutor.Run() error = %q, want containing stderr output", err.Error())
	}

	var exitErr *goexec.ExitError
	if !errors.As(err, &exitErr) {
		t.Errorf("CLIExecutor.Run() error does not wrap *exec.ExitError")
	}
}

func TestCLIExecutor_Run_NilWriters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses shell script as fake binary")
	}

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fake-runtime")
	script := "#!/bin/sh\necho output\necho error >&2\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", fakeBin, err)
	}

	exec := NewCLIExecutor(fakeBin)
	// Nil writers should not panic.
	err := exec.Run(t.Context(), nil, nil, "build")
	if err != nil {
		t.Fatalf("CLIExecutor.Run() with nil writers unexpected error: %v", err)
	}
}

func TestCLIExecutor_RunInteractive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses shell script as fake binary")
	}

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fake-runtime")
	// Script that reads one line from stdin and echoes it to stdout.
	script := "#!/bin/sh\nread line\necho \"got: $line\"\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", fakeBin, err)
	}

	exec := NewCLIExecutor(fakeBin)

	stdin := strings.NewReader("hello from stdin\n")
	var stdout, stderr strings.Builder
	err := exec.RunInteractive(t.Context(), stdin, &stdout, &stderr, "exec", "-it", "abc123")
	if err != nil {
		t.Fatalf("CLIExecutor.RunInteractive() unexpected error: %v", err)
	}

	if !strings.Contains(stdout.String(), "got: hello from stdin") {
		t.Errorf("CLIExecutor.RunInteractive() stdout = %q, want containing %q", stdout.String(), "got: hello from stdin")
	}
}

func TestCLIExecutor_RunInteractive_NilStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses shell script as fake binary")
	}

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fake-runtime")
	script := "#!/bin/sh\necho ok\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", fakeBin, err)
	}

	exec := NewCLIExecutor(fakeBin)

	var stdout, stderr strings.Builder
	err := exec.RunInteractive(t.Context(), nil, &stdout, &stderr, "exec", "abc123")
	if err != nil {
		t.Fatalf("CLIExecutor.RunInteractive() with nil stdin unexpected error: %v", err)
	}

	if !strings.Contains(stdout.String(), "ok") {
		t.Errorf("CLIExecutor.RunInteractive() stdout = %q, want containing %q", stdout.String(), "ok")
	}
}

func TestCLIExecutor_RunInteractive_Error(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses shell script as fake binary")
	}

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fake-runtime")
	script := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", fakeBin, err)
	}

	exec := NewCLIExecutor(fakeBin)
	err := exec.RunInteractive(t.Context(), nil, nil, nil, "exec", "abc123")
	if err == nil {
		t.Fatal("CLIExecutor.RunInteractive() = nil error, want error for non-zero exit")
	}

	if !strings.Contains(err.Error(), "run ") {
		t.Errorf("CLIExecutor.RunInteractive() error = %q, want containing runtime path context", err.Error())
	}
}

func TestCLIExecutor_Output_ErrorNoStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses shell script as fake binary")
	}

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "fake-runtime")
	script := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", fakeBin, err)
	}

	exec := NewCLIExecutor(fakeBin)
	_, err := exec.Output(t.Context(), "ps")
	if err == nil {
		t.Fatal("CLIExecutor.Output() = nil error, want error for non-zero exit")
	}

	if !strings.Contains(err.Error(), "run ") {
		t.Errorf("CLIExecutor.Output() error = %q, want error containing runtime path context", err.Error())
	}
}
