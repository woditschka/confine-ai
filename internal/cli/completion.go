package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/woditschka/confine-ai/internal/assistant"
	"github.com/woditschka/confine-ai/internal/completion"
)

// RunCompletion generates a shell completion script for the given shell.
func RunCompletion(stdout, stderr io.Writer, args []string) error {
	completionFlags := NewFlagSet("completion", stderr)

	completionFlags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: confine-ai completion <shell>\n\n")
		fmt.Fprintf(stderr, "Generate a shell completion script.\n")
		fmt.Fprintf(stderr, "Supported shells: bash, zsh\n\n")
		fmt.Fprintf(stderr, "Example:\n")
		fmt.Fprintf(stderr, "  source <(confine-ai completion bash)\n")
	}

	if err := ParseFlags(completionFlags, args); err != nil {
		return IgnoreHelp(err)
	}

	shell := completionFlags.Arg(0)
	if shell == "" {
		completionFlags.Usage()
		return errors.New("completion: shell argument required; supported shells: bash, zsh")
	}

	script, err := completion.Script(shell)
	if err != nil {
		return err
	}

	// Only print setup instructions when stderr is a terminal (interactive use).
	// When sourced via "source <(confine-ai completion zsh)", stderr is still a
	// terminal but stdout is a pipe — however, instructions go to stderr so they
	// appear regardless. Use stdout as the indicator: if stdout is not a terminal,
	// the user is capturing the script, so skip the instructions.
	if f, ok := stdout.(*os.File); ok && isatty(f) {
		_, _ = fmt.Fprint(stderr, completion.Instructions(shell))
	}
	_, err = fmt.Fprint(stdout, script)
	return err
}

// RunComplete handles the hidden __complete callback used by shell completion
// scripts. It writes one suggestion per line to stdout. The caller supplies the
// embedded assistantDockerfiles map so RunComplete can enumerate the built-in
// template names.
func RunComplete(stdout io.Writer, args []string, assistantDockerfiles map[string][]byte) error {
	// The shell completion script passes args as:
	//   confine-ai __complete [preceding-words...] -- <current-word>
	// Split on "--" to separate preceding words from the current prefix.
	var preceding []string
	var prefix string

	dashIdx := -1
	for i, arg := range args {
		if arg == "--" {
			dashIdx = i
			break
		}
	}

	if dashIdx >= 0 {
		preceding = args[:dashIdx]
		if dashIdx+1 < len(args) {
			prefix = args[dashIdx+1]
		}
	} else {
		// No "--" separator: treat all args as preceding, empty prefix.
		preceding = args
	}

	// Build template names from the authoritative assistantDockerfiles map.
	templateNames := make([]string, 0, len(assistantDockerfiles))
	for name := range assistantDockerfiles {
		templateNames = append(templateNames, name)
	}
	slices.Sort(templateNames)

	opts := completion.CompleteOptions{
		DiscoverAssistants: func() []string {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return nil
			}
			return assistant.ListNames(homeDir)
		},
		TemplateNames: templateNames,
	}

	suggestions := completion.Complete(preceding, prefix, opts)
	for _, s := range suggestions {
		fmt.Fprintln(stdout, s)
	}

	return nil
}
