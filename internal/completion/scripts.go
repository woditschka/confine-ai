package completion

import "fmt"

// bashScript is the bash completion script. It registers a completion handler
// that calls back into the confine-ai binary with the __complete subcommand.
const bashScript = `# bash completion for confine-ai
_confine_ai_completions() {
    local cur="${COMP_WORDS[COMP_CWORD]}"

    # Build args: everything before the current word.
    local args=("${COMP_WORDS[@]:1:COMP_CWORD-1}")

    # Call confine-ai __complete with the partial command line.
    # Use mapfile to safely read newline-delimited output without word splitting.
    mapfile -t COMPREPLY < <(confine-ai __complete "${args[@]}" -- "$cur" 2>/dev/null)
}

complete -o default -F _confine_ai_completions confine-ai
`

// zshScript is the zsh completion script. It registers a completion handler
// that calls back into the confine-ai binary with the __complete subcommand.
const zshScript = `#compdef confine-ai

_confine_ai() {
    local -a completions

    # Build args: everything before the current word.
    local -a args
    args=("${words[@]:1:$((CURRENT-2))}")

    # Call confine-ai __complete with the partial command line.
    # Use ${(@f)...} to split on newlines safely without IFS manipulation.
    completions=("${(@f)$(confine-ai __complete "${args[@]}" -- "${words[CURRENT]}" 2>/dev/null)}")

    compadd -a completions
}

compdef _confine_ai confine-ai
`

// Script returns the shell completion script for the given shell name.
// Supported shells are "bash" and "zsh".
func Script(shell string) (string, error) {
	switch shell {
	case "bash":
		return bashScript, nil
	case "zsh":
		return zshScript, nil
	default:
		return "", fmt.Errorf("completion: unsupported shell %q; supported shells: bash, zsh", shell)
	}
}

const bashInstructions = `# To load completions in your current shell session:
#   source <(confine-ai completion bash)
#
# To load completions for every new session, add to your ~/.bashrc:
#   source <(confine-ai completion bash)
#
# Or write to a file and source it:
#   confine-ai completion bash > ~/.config/confine-ai/completion.bash
#   echo 'source ~/.config/confine-ai/completion.bash' >> ~/.bashrc
`

const zshInstructions = `# Requires zsh's completion system. If not already enabled, add to your ~/.zshrc:
#   autoload -Uz compinit && compinit
#
# To load completions in your current shell session:
#   source <(confine-ai completion zsh)
#
# To load completions for every new session, add to your ~/.zshrc:
#   source <(confine-ai completion zsh)
#
# Or place the script in your fpath:
#   confine-ai completion zsh > "${fpath[1]}/_confine_ai"
#
# You may need to restart your shell or run 'compinit' for changes to take effect.
`

// Instructions returns shell-specific setup instructions for the given shell.
func Instructions(shell string) string {
	switch shell {
	case "bash":
		return bashInstructions
	case "zsh":
		return zshInstructions
	default:
		return ""
	}
}
