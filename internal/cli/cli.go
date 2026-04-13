// Package cli implements the confine-ai subcommand handlers and CLI-layer
// helpers (flag parsing, folder argument parsing, confirmation prompts,
// terminal detection). Each Run* entry point is called from main.go's
// dispatch switch; main.go passes embedded sample Dockerfiles as function
// parameters so //go:embed can stay at the repository root.
package cli
