package update

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Resolved holds the probe output for one managed group: the new version
// string and the map of arch → sha256 that the rewriter splices into the
// group's ARG lines.
type Resolved struct {
	// Version is the upstream's reported latest version. Written into the
	// group's kind=version ARG line verbatim.
	Version string
	// Sha256 maps each arch in the group to its 64-char lowercase-hex
	// sha256. The rewriter writes the value for each arch present in the
	// group; missing arches are treated as "do not rewrite this line".
	Sha256 map[string]string
}

// Delta is the orchestrator's fully-resolved rewrite plan: one Resolved per
// managed group. A nil or empty Delta means "no changes"; the rewriter still
// produces a copy of the input bytes so callers can treat the output as the
// authoritative new file contents.
type Delta map[*ManagedGroup]Resolved

// Rewrite produces the new file bytes for pd by splicing the Delta values
// into the managed ARG lines. Non-managed lines (including FROM, markers,
// comments, whitespace, RUN, ENV, and everything else) are copied
// byte-identically from pd.Raw. CRLF line endings and trailing-newline state
// are preserved.
//
// Rewrite is a pure function. It does no I/O. The orchestrator passes the
// result to WriteAtomic.
func Rewrite(pd *ParsedDockerfile, delta Delta) []byte {
	// Build a map from line index → (value replacement bytes) so we can
	// decide per-line whether to rewrite.
	replacements := map[int]string{}
	for group, resolved := range delta {
		if group == nil {
			continue
		}
		if resolved.Version != "" {
			replacements[group.VersionLineIdx] = resolved.Version
		}
		for arch, sum := range resolved.Sha256 {
			idx, ok := group.Sha256LinesByArch[arch]
			if !ok {
				continue
			}
			replacements[idx] = sum
		}
	}

	// Preallocate capacity based on the input size plus a small slack for
	// longer replacement values.
	out := make([]byte, 0, len(pd.Raw)+64)
	for i, line := range pd.Lines {
		newVal, rewrite := replacements[i]
		if !rewrite {
			out = append(out, line.Raw...)
			continue
		}
		// Splice: raw[:ArgValueStart] + newVal + raw[ArgValueEnd:]
		out = append(out, line.Raw[:line.ArgValueStart]...)
		out = append(out, newVal...)
		out = append(out, line.Raw[line.ArgValueEnd:]...)
	}
	return out
}

// WriteAtomic writes content to path using a temp-file-plus-rename sequence
// so readers never observe a partial file. The sequence is:
//
//  1. Create a temp file in the same directory as path via os.CreateTemp.
//  2. Write content and close the temp file.
//  3. Chmod the temp file to the target mode (existing file's mode if the
//     target already exists and mode is zero, otherwise the explicit mode).
//  4. Rename the temp file over path.
//
// If any step fails before the rename, the temp file is removed. If the
// rename succeeds, the deferred cleanup is a no-op.
//
// When mode is zero and path already exists, WriteAtomic preserves the
// existing file's mode. When mode is zero and path does not exist, it
// defaults to 0o644 (REQ-AS-008 constraint).
func WriteAtomic(path string, content []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)

	// Resolve the effective mode before creating the temp file.
	effectiveMode := mode
	if effectiveMode == 0 {
		if info, err := os.Stat(path); err == nil {
			effectiveMode = info.Mode().Perm()
		} else {
			effectiveMode = 0o644
		}
	}

	tmp, err := os.CreateTemp(dir, ".Dockerfile.tmp-*")
	if err != nil {
		return fmt.Errorf("write atomic: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	// Defer cleanup; becomes a no-op once the rename succeeds.
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write atomic: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write atomic: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write atomic: close: %w", err)
	}
	if err := os.Chmod(tmpPath, effectiveMode); err != nil {
		return fmt.Errorf("write atomic: chmod: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("write atomic: rename: %w", err)
	}
	renamed = true

	// Best-effort directory fsync. Ignore errors; many platforms do not
	// allow opening a directory for sync.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
