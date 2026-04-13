package config

import (
	"fmt"
	"io"
)

// LoadFromWorkspace runs the full configuration pipeline for a workspace:
// discover (or honour the explicit override path), parse the JSONC source,
// apply the typed Load step, write any load warnings to stderr, and then
// perform variable substitution. It returns the resolved Config together
// with the config file path that was used.
//
// configPathOverride takes precedence over auto-discovery when non-empty.
// lookupEnv is passed to Substitute, allowing callers to inject a custom
// environment lookup for testing.
func LoadFromWorkspace(
	workspaceFolder string,
	configPathOverride string,
	stderr io.Writer,
	lookupEnv func(string) (string, bool),
) (Config, string, error) {
	cfgPath := configPathOverride
	if cfgPath == "" {
		p, err := Find(workspaceFolder)
		if err != nil {
			return Config{}, "", fmt.Errorf("config discovery: %w", err)
		}
		cfgPath = p
	}

	raw, err := Parse(cfgPath)
	if err != nil {
		return Config{}, "", fmt.Errorf("config parse: %w", err)
	}

	cfg, warnings, err := Load(raw)
	if err != nil {
		return Config{}, "", fmt.Errorf("config load: %w", err)
	}

	for _, w := range warnings {
		fmt.Fprintf(stderr, "warning: %s\n", w)
	}

	cfg, err = Substitute(cfg, workspaceFolder, lookupEnv)
	if err != nil {
		return Config{}, "", fmt.Errorf("config substitute: %w", err)
	}

	return cfg, cfgPath, nil
}
