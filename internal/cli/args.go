package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveFolders resolves raw folder paths to absolute paths, validates that
// each exists and is a directory, and detects basename collisions. If rawPaths
// is empty, returns workspaceFolder as the primary with no additional folders.
// The first path is the primary workspace; the rest are additional folders.
func resolveFolders(rawPaths []string, workspaceFolder string) (primary string, additional []string, err error) {
	if len(rawPaths) == 0 {
		return workspaceFolder, nil, nil
	}

	// Resolve all paths to absolute.
	absPaths := make([]string, len(rawPaths))
	for i, p := range rawPaths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", nil, fmt.Errorf("resolve path %q: %w", p, err)
		}
		absPaths[i] = abs
	}

	// Validate each path exists and is a directory.
	for _, p := range absPaths {
		info, err := os.Stat(p)
		if err != nil {
			if os.IsNotExist(err) {
				return "", nil, fmt.Errorf("folder %q does not exist", p)
			}
			return "", nil, fmt.Errorf("folder %q: %w", p, err)
		}
		if !info.IsDir() {
			return "", nil, fmt.Errorf("%q is not a directory", p)
		}
	}

	// Detect basename collisions.
	basenames := make(map[string][]string)
	for _, p := range absPaths {
		base := filepath.Base(p)
		basenames[base] = append(basenames[base], p)
	}
	for base, paths := range basenames {
		if len(paths) > 1 {
			return "", nil, fmt.Errorf("basename collision %q: %s", base, strings.Join(paths, " and "))
		}
	}

	primary = absPaths[0]
	if len(absPaths) > 1 {
		additional = absPaths[1:]
	}
	return primary, additional, nil
}

// extractBoolFlag removes the named flag (e.g., "--shell") from the argument
// list and returns whether it was present. It must run before parseFolderArgs
// so flags are not treated as folder paths.
func extractBoolFlag(args []string, name string) (found bool, remaining []string) {
	for _, arg := range args {
		if arg == name {
			found = true
		} else {
			remaining = append(remaining, arg)
		}
	}
	return found, remaining
}

// extractRepeatedFlag removes all occurrences of the named flag (e.g.,
// "--allowed-hosts") and its value from the argument list. Returns the
// collected values and the remaining arguments. It must run before
// parseFolderArgs so flags are not treated as folder paths.
func extractRepeatedFlag(args []string, name string) (values []string, remaining []string) {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			values = append(values, args[i+1])
			i++ // skip the value
		} else {
			remaining = append(remaining, args[i])
		}
	}
	return values, remaining
}

// parseFolderArgs splits command args at "--" into folder paths and assistant
// passthrough arguments. Arguments before "--" are folder paths. Arguments
// after "--" are assistant args. If no "--" is found, all arguments are treated
// as folder paths.
func parseFolderArgs(args []string) (folders []string, assistantArgs []string) {
	for i, arg := range args {
		if arg == "--" {
			if i > 0 {
				folders = args[:i]
			}
			if i+1 < len(args) {
				assistantArgs = args[i+1:]
			}
			return folders, assistantArgs
		}
	}
	// No "--" found: all args are folder paths.
	if len(args) > 0 {
		folders = args
	}
	return folders, nil
}
