// Package completion provides shell completion logic for the confine-ai CLI.
// It generates completion scripts for bash and zsh, and implements the callback
// handler that returns suggestions based on the partial command line.
package completion

import (
	"slices"
	"strings"
)

// subcommands is the set of known confine-ai subcommands.
var subcommands = []string{
	"completion",
	"init",
	"rm",
	"status",
	"update",
}

// commandFlags maps subcommands to their flags.
var commandFlags = map[string][]string{
	"update": {
		"--dry-run",
		"--yes",
	},
}

// commandPositional maps subcommands to their positional completion values.
// Absent keys indicate no positional completions.
var commandPositional = map[string][]string{
	"completion": {"bash", "zsh"},
}

// globalFlags is the set of top-level flags.
var globalFlags = []string{
	"--config",
	"--docker-path",
	"--version",
	"--workspace-folder",
}

// assistantShortcutFlags are flags available when using an assistant name as a command.
var assistantShortcutFlags = []string{
	"--no-git-identity",
	"--shell",
}

// CompleteOptions configures the Complete function with dynamic data sources.
type CompleteOptions struct {
	// DiscoverAssistants returns the list of known assistant names. May be nil.
	DiscoverAssistants func() []string

	// TemplateNames is the list of built-in assistant template names (for init completion).
	TemplateNames []string
}

// Complete returns completion suggestions for the given partial command line.
// args contains the words before the current word being completed. prefix is
// the current partial word being typed.
func Complete(args []string, prefix string, opts CompleteOptions) []string {
	// Determine whether we are completing the first argument (subcommand/assistant)
	// or a subsequent argument.
	command, isSubcommand := extractCommand(args)

	if command == "" {
		// Completing the first positional argument.
		if strings.HasPrefix(prefix, "--") {
			return filterPrefix(globalFlags, prefix)
		}
		return filterPrefix(firstArgSuggestions(opts), prefix)
	}

	// We have a command. Complete based on it.
	if strings.HasPrefix(prefix, "--") {
		return completeFlags(command, isSubcommand, opts, prefix)
	}

	positional := completePositional(command, isSubcommand, opts, prefix)
	if isSubcommand {
		return positional
	}
	// Assistant shortcut: show flags even without a "--" prefix so that
	// "confine-ai <assistant> <TAB>" suggests --shell and --no-git-identity.
	flags := completeFlags(command, isSubcommand, opts, prefix)
	return append(positional, flags...)
}

// extractCommand determines the command from the args. It returns the command
// name and whether it is a known subcommand (vs an assistant name).
func extractCommand(args []string) (string, bool) {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		// First non-flag argument is the command.
		if slices.Contains(subcommands, arg) {
			return arg, true
		}
		// Not a subcommand; treat as assistant name.
		return arg, false
	}
	return "", false
}

// firstArgSuggestions builds the list of first-argument suggestions:
// subcommands + discovered assistant names (deduplicated).
func firstArgSuggestions(opts CompleteOptions) []string {
	suggestions := slices.Clone(subcommands)
	if opts.DiscoverAssistants != nil {
		for _, name := range opts.DiscoverAssistants() {
			if !slices.Contains(subcommands, name) {
				suggestions = append(suggestions, name)
			}
		}
	}
	slices.Sort(suggestions)
	return suggestions
}

// completeFlags returns flag suggestions for the given command.
func completeFlags(command string, isSubcommand bool, _ CompleteOptions, prefix string) []string {
	if isSubcommand {
		if flags, ok := commandFlags[command]; ok {
			return filterPrefix(flags, prefix)
		}
		return nil
	}
	// Assistant shortcut: return assistant shortcut flags.
	return filterPrefix(assistantShortcutFlags, prefix)
}

// completePositional returns positional suggestions for the given command.
func completePositional(command string, isSubcommand bool, opts CompleteOptions, prefix string) []string {
	if isSubcommand {
		// Check for static positional completions.
		if values, ok := commandPositional[command]; ok {
			return filterPrefix(values, prefix)
		}

		// init: complete with template names.
		if command == "init" {
			return filterPrefix(opts.TemplateNames, prefix)
		}

		// rm: complete with assistant names.
		if command == "rm" {
			return filterPrefix(discoverAssistantNames(opts), prefix)
		}

		// update: complete with "base" + discovered assistant names.
		if command == "update" {
			values := append([]string{"base"}, discoverAssistantNames(opts)...)
			return filterPrefix(values, prefix)
		}

		// status: no positional completions here.
		return nil
	}

	// Assistant shortcut: no positional completions.
	return nil
}

// discoverAssistantNames returns assistant names from the discovery function, or nil.
func discoverAssistantNames(opts CompleteOptions) []string {
	if opts.DiscoverAssistants == nil {
		return nil
	}
	return opts.DiscoverAssistants()
}

// filterPrefix returns only the suggestions that start with the given prefix.
// Returns nil if no suggestions match.
func filterPrefix(suggestions []string, prefix string) []string {
	if prefix == "" {
		if len(suggestions) == 0 {
			return nil
		}
		result := make([]string, len(suggestions))
		copy(result, suggestions)
		return result
	}

	var filtered []string
	for _, s := range suggestions {
		if strings.HasPrefix(s, prefix) {
			filtered = append(filtered, s)
		}
	}
	return filtered
}
