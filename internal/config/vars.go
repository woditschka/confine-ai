package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// substContext holds the precomputed values for variable resolution.
type substContext struct {
	workspaceFolder         string
	workspaceFolderBasename string
	devcontainerID          string
	lookupEnv               func(string) (string, bool)
}

// Substitute resolves variable patterns in all string fields of cfg.
// workspaceFolder is the absolute path to the project on the host.
// lookupEnv returns the value and presence of a host environment variable
// (matches the signature of os.LookupEnv).
//
// Substitute returns a new Config with all variables resolved. It returns
// an error if any variable pattern cannot be resolved (e.g., localEnv
// without a default for an unset variable).
func Substitute(cfg Config, workspaceFolder string, lookupEnv func(string) (string, bool)) (Config, error) {
	ctx := &substContext{
		workspaceFolder:         workspaceFolder,
		workspaceFolderBasename: filepath.Base(workspaceFolder),
		devcontainerID:          devcontainerID(workspaceFolder),
		lookupEnv:               lookupEnv,
	}

	var err error
	out := Config{}

	// Direct string fields.
	substituteInto(&out.Name, cfg.Name, "name", ctx, &err)
	substituteInto(&out.Image, cfg.Image, "image", ctx, &err)
	substituteInto(&out.WorkspaceFolder, cfg.WorkspaceFolder, "workspaceFolder", ctx, &err)
	substituteInto(&out.RemoteUser, cfg.RemoteUser, "remoteUser", ctx, &err)
	substituteInto(&out.ContainerUser, cfg.ContainerUser, "containerUser", ctx, &err)
	if err != nil {
		return Config{}, err
	}

	// ContainerEnv: substitute values, preserve keys.
	out.ContainerEnv, err = substituteMapValues(cfg.ContainerEnv, "containerEnv", ctx)
	if err != nil {
		return Config{}, err
	}

	// Mounts: substitute each entry.
	if cfg.Mounts != nil {
		out.Mounts = make([]string, len(cfg.Mounts))
		for i, m := range cfg.Mounts {
			resolved, err := substituteString(m, ctx)
			if err != nil {
				return Config{}, fmt.Errorf("substitute config \"mounts\"[%d]: %w", i, err)
			}
			out.Mounts[i] = resolved
		}
	}

	// Customizations: copy through unchanged. Resource limit values are
	// not subject to variable substitution.
	out.Customizations = cfg.Customizations

	// Build: substitute all string fields and args values.
	if cfg.Build != nil {
		b := &Build{}
		substituteInto(&b.Dockerfile, cfg.Build.Dockerfile, "build.dockerfile", ctx, &err)
		substituteInto(&b.Context, cfg.Build.Context, "build.context", ctx, &err)
		if err != nil {
			return Config{}, err
		}

		b.Args, err = substituteMapValues(cfg.Build.Args, "build.args", ctx)
		if err != nil {
			return Config{}, err
		}

		out.Build = b
	}

	return out, nil
}

// substituteField resolves variable patterns in a single config field value
// and wraps errors with the field name for context.
func substituteField(s, field string, ctx *substContext) (string, error) {
	resolved, err := substituteString(s, ctx)
	if err != nil {
		return "", fmt.Errorf("substitute config %q: %w", field, err)
	}
	return resolved, nil
}

// substituteInto resolves variable patterns in s and writes the result to dst.
// Errors are accumulated via errp so callers can chain multiple calls.
func substituteInto(dst *string, s, field string, ctx *substContext, errp *error) {
	if *errp != nil {
		return
	}
	*dst, *errp = substituteField(s, field, ctx)
}

// substituteMapValues resolves variable patterns in each value of m.
// Returns a new map with the same keys and resolved values.
func substituteMapValues(m map[string]string, field string, ctx *substContext) (map[string]string, error) {
	if m == nil {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		resolved, err := substituteString(v, ctx)
		if err != nil {
			return nil, fmt.Errorf("substitute config %q: %w", field, err)
		}
		out[k] = resolved
	}
	return out, nil
}

// substituteString resolves all ${...} patterns in a single string.
// It scans left-to-right, collecting literal segments and resolved values.
// No recursive expansion: a resolved value containing ${...} is not re-expanded.
func substituteString(s string, ctx *substContext) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}

	var b strings.Builder
	b.Grow(len(s))

	i := 0
	for i < len(s) {
		// Find the next "${".
		idx := strings.Index(s[i:], "${")
		if idx == -1 {
			// No more patterns; append the rest.
			b.WriteString(s[i:])
			break
		}

		// Append the literal text before "${".
		b.WriteString(s[i : i+idx])
		start := i + idx

		// Find the closing "}".
		end := strings.Index(s[start:], "}")
		if end == -1 {
			return "", errors.New("substitute: unclosed variable reference")
		}
		end += start // Convert to absolute index.

		// Extract the content between ${ and }.
		content := s[start+2 : end]

		resolved, err := resolvePattern(content, ctx)
		if err != nil {
			return "", err
		}

		b.WriteString(resolved)
		i = end + 1 // Move past the closing '}'.
	}

	return b.String(), nil
}

// resolvePattern resolves a single variable pattern (the content between ${ and }).
func resolvePattern(content string, ctx *substContext) (string, error) {
	switch {
	case content == "localWorkspaceFolder":
		return ctx.workspaceFolder, nil

	case content == "localWorkspaceFolderBasename":
		return ctx.workspaceFolderBasename, nil

	case content == "devcontainerId":
		return ctx.devcontainerID, nil

	case strings.HasPrefix(content, "localEnv:"):
		return resolveLocalEnv(content[len("localEnv:"):], ctx.lookupEnv)

	default:
		return "", fmt.Errorf("substitute: unknown variable pattern %q; supported: ${localWorkspaceFolder}, ${localWorkspaceFolderBasename}, ${devcontainerId}, ${localEnv:VAR} or ${localEnv:VAR:default}", content)
	}
}

// resolveLocalEnv resolves a localEnv variable reference.
// The input is the portion after "localEnv:", e.g., "HOME" or "MISSING:fallback".
// Split on the first colon to separate variable name from default value.
func resolveLocalEnv(ref string, lookupEnv func(string) (string, bool)) (string, error) {
	// Split on the first colon: variable name vs. default.
	varName, defaultVal, hasDefault := strings.Cut(ref, ":")

	val, ok := lookupEnv(varName)
	if ok {
		return val, nil
	}

	if hasDefault {
		return defaultVal, nil
	}

	return "", fmt.Errorf("substitute: environment variable %q is not set; use ${localEnv:%s:default} to provide a fallback", varName, varName)
}

// devcontainerID returns a deterministic identifier derived from the workspace
// folder path. It is the lowercase hex encoding of the SHA-256 hash of the
// folder path encoded as UTF-8 (64 hex characters).
func devcontainerID(workspaceFolder string) string {
	hash := sha256.Sum256([]byte(workspaceFolder))
	return hex.EncodeToString(hash[:])
}
