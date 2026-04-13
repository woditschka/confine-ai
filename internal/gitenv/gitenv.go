package gitenv

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ReadIdentity reads the host git user.name and user.email from the global
// git configuration. Both values must be present; if either is missing or
// git is not available, an error is returned.
func ReadIdentity(ctx context.Context) (name, email string, err error) {
	name, err = configValue(ctx, "user.name")
	if err != nil {
		return "", "", errors.New("host git user.name not configured")
	}
	email, err = configValue(ctx, "user.email")
	if err != nil {
		return "", "", errors.New("host git user.email not configured")
	}
	return name, email, nil
}

// configValue runs `git config --global <key>` and returns the trimmed
// output. Returns an error if the command fails or returns empty output.
func configValue(ctx context.Context, key string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "config", "--global", key).Output()
	if err != nil {
		return "", err
	}
	val := strings.TrimSpace(string(out))
	if val == "" {
		return "", fmt.Errorf("git config %s is empty", key)
	}
	return val, nil
}

// MergeInto sets GIT_AUTHOR_NAME, GIT_COMMITTER_NAME, GIT_AUTHOR_EMAIL, and
// GIT_COMMITTER_EMAIL in env. Existing keys are not overwritten, so values
// already present (e.g., from devcontainer.json containerEnv) take
// precedence. A nil env is allocated and returned.
func MergeInto(env map[string]string, name, email string) map[string]string {
	if env == nil {
		env = make(map[string]string)
	}
	gitVars := map[string]string{
		"GIT_AUTHOR_NAME":     name,
		"GIT_COMMITTER_NAME":  name,
		"GIT_AUTHOR_EMAIL":    email,
		"GIT_COMMITTER_EMAIL": email,
	}
	for k, v := range gitVars {
		if _, exists := env[k]; !exists {
			env[k] = v
		}
	}
	return env
}
