package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/woditschka/confine-ai/internal/assistant"
)

// RunInit scaffolds assistant configuration and seeds the base Dockerfile.
//
// With no arguments, it seeds ~/.confine-ai/base/Dockerfile from the embedded
// seed if the file does not exist. With an assistant name, it also scaffolds
// the assistant directory (existing REQ-AS-003 behavior).
//
// When the base Dockerfile or the assistant config directory already exists,
// RunInit prompts interactively to overwrite (default yes). The -y flag
// accepts the default without prompting. In non-interactive contexts without
// -y, the existing files are preserved. Assistant overwrite removes only the
// config directory (~/.confine-ai/assistants/<name>); the persistent data
// directory (~/.confine-ai/data/<name>) is left untouched so credentials survive.
//
// baseDockerfile and assistantDockerfiles are the embedded seed bytes flowed
// in from main.go's //go:embed variables.
func RunInit(stdout, stderr io.Writer, args []string, baseDockerfile []byte, assistantDockerfiles map[string][]byte) error {
	initFlags := NewFlagSet("init", stderr)

	var assumeYes bool
	initFlags.BoolVar(&assumeYes, "y", false, "Answer yes to all prompts")
	initFlags.BoolVar(&assumeYes, "yes", false, "Alias for -y")

	initFlags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: confine-ai init [flags] [assistant-name]\n\n")
		fmt.Fprintf(stderr, "Seed the base Dockerfile at ~/.confine-ai/base/Dockerfile.\n")
		fmt.Fprintf(stderr, "When an assistant name is given, also scaffold assistant configuration.\n\n")
		fmt.Fprintf(stderr, "If the base Dockerfile or assistant config already exists, prompts to overwrite.\n")
		fmt.Fprintf(stderr, "Pass -y to accept without prompting. Assistant data directories are preserved.\n\n")
		fmt.Fprintf(stderr, "Known assistants: claude, copilot, opencode\n")
		fmt.Fprintf(stderr, "Unknown assistants get a generic template using localhost/confine-ai-base:latest.\n\n")
		fmt.Fprintf(stderr, "Flags:\n")
		initFlags.PrintDefaults()
	}

	if err := ParseFlags(initFlags, args); err != nil {
		return IgnoreHelp(err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("init: home directory: %w", err)
	}

	return runInitWith(os.Stdin, stdout, stderr, homeDir, initFlags.Arg(0), baseDockerfile, assistantDockerfiles, assumeYes, isatty(os.Stdin))
}

// runInitWith is the injectable core of RunInit. Tests substitute a temp
// homeDir and non-interactive mode to exercise the init logic without
// real terminal interaction.
func runInitWith(stdin io.Reader, stdout, stderr io.Writer, homeDir, assistantName string, baseDockerfile []byte, assistantDockerfiles map[string][]byte, assumeYes, interactive bool) error {
	basePath := assistant.BaseDockerfilePath(homeDir)
	if err := handleBaseDockerfile(stdin, stdout, stderr, homeDir, basePath, baseDockerfile, assumeYes, interactive); err != nil {
		return fmt.Errorf("init: %w", err)
	}

	if assistantName == "" {
		return nil
	}

	if err := handleAssistantInit(stdin, stdout, stderr, homeDir, assistantName, assistantDockerfiles, assumeYes, interactive); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	return nil
}

// handleAssistantInit scaffolds the assistant config directory, prompting to
// overwrite when the directory already exists. The overwrite rule mirrors
// handleBaseDockerfile: assumeYes forces overwrite; otherwise, when
// interactive, prompt [Y/n] with default yes; otherwise, preserve the
// existing config. Overwrite removes only the assistant config directory;
// ~/.confine-ai/data/<name> (credentials, tokens) is left untouched.
func handleAssistantInit(stdin io.Reader, stdout, stderr io.Writer, homeDir, assistantName string, assistantDockerfiles map[string][]byte, assumeYes, interactive bool) error {
	assistantDir := assistant.Dir(homeDir, assistantName)
	dockerfile := assistantDockerfiles[assistantName]

	if assistant.Exists(homeDir, assistantName) {
		overwrite := assumeYes
		if !overwrite && interactive {
			confirm := newConfirm(stdin, stderr)
			overwrite = confirm(fmt.Sprintf("Assistant %q already present at %s. Overwrite? [Y/n] ", assistantName, assistantDir))
		}
		if !overwrite {
			fmt.Fprintf(stdout, "Assistant %q already present at %s\n", assistantName, assistantDir)
			return nil
		}
		if err := os.RemoveAll(assistantDir); err != nil {
			return fmt.Errorf("remove existing assistant dir: %w", err)
		}
	}

	if err := assistant.Init(homeDir, assistantName, dockerfile); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Initialized assistant %q at %s\n", assistantName, assistantDir)

	if hint := assistant.PostInitHint(assistantName); hint != "" {
		fmt.Fprintf(stdout, "\n%s\n", hint)
	}

	return nil
}

// handleBaseDockerfile seeds ~/.confine-ai/base/Dockerfile when absent, or
// overwrites it when present and the caller has opted in. The overwrite
// decision is: assumeYes forces overwrite; otherwise, when interactive,
// prompt [Y/n] with default yes; otherwise, preserve the existing file.
func handleBaseDockerfile(stdin io.Reader, stdout, stderr io.Writer, homeDir, basePath string, seed []byte, assumeYes, interactive bool) error {
	_, statErr := os.Stat(basePath)
	if os.IsNotExist(statErr) {
		wrote, err := assistant.SeedBaseDockerfile(homeDir, seed)
		if err != nil {
			return err
		}
		if wrote {
			fmt.Fprintf(stdout, "Seeded base Dockerfile at %s\n", basePath)
		}
		return nil
	}
	if statErr != nil {
		return fmt.Errorf("stat base dockerfile: %w", statErr)
	}

	overwrite := assumeYes
	if !overwrite && interactive {
		confirm := newConfirm(stdin, stderr)
		overwrite = confirm(fmt.Sprintf("Base Dockerfile already present at %s. Overwrite? [Y/n] ", basePath))
	}

	if !overwrite {
		fmt.Fprintf(stdout, "Base Dockerfile already present at %s\n", basePath)
		return nil
	}

	if err := os.WriteFile(basePath, seed, 0o644); err != nil {
		return fmt.Errorf("overwrite base dockerfile: %w", err)
	}
	fmt.Fprintf(stdout, "Overwrote base Dockerfile at %s\n", basePath)
	return nil
}
