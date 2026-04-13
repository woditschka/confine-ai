package update

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewrite_EmptyDeltaRoundTrip(t *testing.T) {
	// AC-17 preservation contract: parse → rewrite with no deltas → the
	// output equals the input byte-for-byte. This runs against every
	// fixture so future parser changes cannot silently drift.
	fixtures := []string{
		"valid.Dockerfile",
		"orphan-marker.Dockerfile",
		"unmarked-arg.Dockerfile",
		"user-edited-from.Dockerfile",
		"crlf.Dockerfile",
		"no-trailing-newline.Dockerfile",
	}
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			input := readFixture(t, name)
			pd := ParseDockerfile(input)
			got := Rewrite(pd, nil)
			if !bytes.Equal(got, input) {
				t.Errorf("Rewrite(empty delta) != input\ngot:  %q\nwant: %q", got, input)
			}
		})
	}
}

func TestRewrite_SyntheticDelta(t *testing.T) {
	// AC-1 + AC-17: a synthetic delta rewrites only the ARG value bytes,
	// leaving every other byte (markers, FROM, whitespace, RUN) identical.
	input := readFixture(t, "valid.Dockerfile")
	pd := ParseDockerfile(input)

	goGroup := findGroup(pd, "go", "")
	if goGroup == nil {
		t.Fatal("no go group")
	}
	javaGroup := findGroup(pd, "java", "corretto")
	if javaGroup == nil {
		t.Fatal("no java group")
	}

	const newGoVersion = "1.27.1"
	const newGoAMD = "1111111111111111111111111111111111111111111111111111111111111111"
	const newGoARM = "2222222222222222222222222222222222222222222222222222222222222222"
	const newJavaVersion = "25.0.3.11.1"
	const newJavaAMD = "3333333333333333333333333333333333333333333333333333333333333333"
	const newJavaARM = "4444444444444444444444444444444444444444444444444444444444444444"

	delta := Delta{
		goGroup: Resolved{
			Version: newGoVersion,
			Sha256:  map[string]string{"amd64": newGoAMD, "arm64": newGoARM},
		},
		javaGroup: Resolved{
			Version: newJavaVersion,
			Sha256:  map[string]string{"amd64": newJavaAMD, "arm64": newJavaARM},
		},
	}

	got := Rewrite(pd, delta)
	gotStr := string(got)

	// Values present.
	for _, want := range []string{newGoVersion, newGoAMD, newGoARM, newJavaVersion, newJavaAMD, newJavaARM} {
		if !strings.Contains(gotStr, want) {
			t.Errorf("rewrite missing %q", want)
		}
	}
	// Old values gone.
	for _, stale := range []string{"1.26.0", "aac1b08a0fb0c4e0a7c1555beb7b59180b05dfc5a3d62e40e9de90cd42f88235", "25.0.2.10.1"} {
		if strings.Contains(gotStr, stale) {
			t.Errorf("rewrite still contains stale value %q", stale)
		}
	}
	// FROM line preserved verbatim.
	if !strings.Contains(gotStr, "FROM debian:bookworm-slim\n") {
		t.Errorf("FROM line not preserved")
	}
	// Markers preserved verbatim.
	for _, marker := range []string{
		"# confine-ai:managed tool=go kind=version",
		"# confine-ai:managed tool=go kind=sha256 arch=amd64",
		"# confine-ai:managed tool=java kind=version distribution=corretto",
		"# confine-ai:managed tool=base-image kind=image",
	} {
		if !strings.Contains(gotStr, marker) {
			t.Errorf("marker %q not preserved", marker)
		}
	}
	// Non-managed lines untouched.
	if !strings.Contains(gotStr, "RUN echo hello") {
		t.Errorf("non-managed RUN line lost")
	}
	// Trailing newline preserved.
	if got[len(got)-1] != '\n' {
		t.Errorf("trailing newline lost")
	}
}

func TestRewrite_CRLFPreserved(t *testing.T) {
	// AC-17: CRLF line endings must survive a synthetic rewrite.
	input := readFixture(t, "crlf.Dockerfile")
	pd := ParseDockerfile(input)
	goGroup := findGroup(pd, "go", "")
	if goGroup == nil {
		t.Fatal("no go group")
	}

	delta := Delta{
		goGroup: Resolved{
			Version: "1.99.9",
			Sha256: map[string]string{
				"amd64": "5555555555555555555555555555555555555555555555555555555555555555",
			},
		},
	}
	got := Rewrite(pd, delta)

	if !bytes.Contains(got, []byte("ARG GO_VERSION=1.99.9\r\n")) {
		t.Errorf("rewritten GO_VERSION line does not end in CRLF: %q", got)
	}
	// The marker lines, which were not rewritten, must still end in CRLF.
	if !bytes.Contains(got, []byte("# confine-ai:managed tool=go kind=version\r\n")) {
		t.Errorf("marker line CRLF lost")
	}
}

func TestRewrite_NoTrailingNewlinePreserved(t *testing.T) {
	// AC-17: a file without a trailing newline stays without one.
	input := readFixture(t, "no-trailing-newline.Dockerfile")
	pd := ParseDockerfile(input)
	goGroup := findGroup(pd, "go", "")
	if goGroup == nil {
		t.Fatal("no go group")
	}
	delta := Delta{goGroup: Resolved{Version: "9.9.9"}}
	got := Rewrite(pd, delta)

	if len(got) == 0 {
		t.Fatal("empty rewrite")
	}
	if got[len(got)-1] == '\n' {
		t.Errorf("rewrite added a trailing newline that was not present")
	}
}

func TestRewrite_EditedFromPreserved(t *testing.T) {
	// AC-25: a user-edited FROM line must not be touched even when a
	// rewrite runs over the file.
	input := readFixture(t, "user-edited-from.Dockerfile")
	pd := ParseDockerfile(input)

	goGroup := findGroup(pd, "go", "")
	if goGroup == nil {
		t.Fatal("no go group")
	}
	delta := Delta{
		goGroup: Resolved{
			Version: "1.27.1",
			Sha256: map[string]string{
				"amd64": "6666666666666666666666666666666666666666666666666666666666666666",
				"arm64": "7777777777777777777777777777777777777777777777777777777777777777",
			},
		},
	}
	got := Rewrite(pd, delta)
	if !bytes.Contains(got, []byte("FROM ubuntu:24.04\n")) {
		t.Errorf("user-edited FROM line was not preserved in rewrite: %q", got)
	}
	if bytes.Contains(got, []byte("FROM debian:bookworm-slim")) {
		t.Errorf("rewrite introduced an unexpected FROM line")
	}
}

func TestWriteAtomic_ModePreserved(t *testing.T) {
	// The atomic rewrite preserves the existing file mode (REQ-AS-008
	// constraint: file mode 0o644).
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := WriteAtomic(path, []byte("new\n"), 0); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "new\n" {
		t.Errorf("content = %q, want %q", got, "new\n")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want existing 0o600", info.Mode().Perm())
	}

	// No leftover temp files alongside the target.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "Dockerfile" {
			t.Errorf("stray file in dir after atomic write: %q", e.Name())
		}
	}
}

func TestWriteAtomic_ExplicitMode(t *testing.T) {
	// When the target file does not yet exist, WriteAtomic uses the
	// explicit mode parameter (caller passes 0o644 for REQ-AS-008).
	dir := t.TempDir()
	path := filepath.Join(dir, "NewDockerfile")

	if err := WriteAtomic(path, []byte("content\n"), 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0o644", info.Mode().Perm())
	}
}

func TestWriteAtomic_ParentDirMissing(t *testing.T) {
	// WriteAtomic fails cleanly when the parent directory is absent; no
	// temp file is left behind.
	dir := t.TempDir()
	path := filepath.Join(dir, "nosuchdir", "file")
	err := WriteAtomic(path, []byte("x"), 0o644)
	if err == nil {
		t.Fatal("WriteAtomic err = nil, want failure")
	}
	// The exact error type isn't load-bearing (implementations may wrap
	// it). The invariant we care about is that no temp file was left
	// behind in the parent directory.
	var pathErr *fs.PathError
	_ = errors.As(err, &pathErr)
	entries, err2 := os.ReadDir(dir)
	if err2 != nil {
		t.Fatalf("readdir: %v", err2)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty dir after failed WriteAtomic, got %v", entries)
	}
}
