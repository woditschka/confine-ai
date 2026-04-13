package completion

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestComplete(t *testing.T) {
	// stubAssistants returns a discoverAssistants function that returns fixed names.
	stubAssistants := func(names []string) func() []string {
		return func() []string { return names }
	}

	templateNames := []string{"claude", "copilot", "opencode"}

	tests := []struct {
		name               string
		args               []string
		prefix             string
		discoverAssistants func() []string
		templateNames      []string
		want               []string
	}{
		// AC REQ-SC-002 #1: First positional argument returns subcommands + assistant names.
		{
			name:               "empty args returns subcommands and assistant names",
			args:               nil,
			prefix:             "",
			discoverAssistants: stubAssistants([]string{"claude", "copilot"}),
			templateNames:      templateNames,
			want:               []string{"claude", "completion", "copilot", "init", "rm", "status", "update"},
		},

		// AC REQ-SC-002 #2: Prefix filtering.
		{
			name:               "prefix u filters to update",
			args:               nil,
			prefix:             "u",
			discoverAssistants: stubAssistants(nil),
			templateNames:      templateNames,
			want:               []string{"update"},
		},
		{
			name:               "prefix c filters to completion and assistants",
			args:               nil,
			prefix:             "c",
			discoverAssistants: stubAssistants([]string{"claude", "copilot"}),
			templateNames:      templateNames,
			want:               []string{"claude", "completion", "copilot"},
		},

		// AC REQ-SC-002 #4: completion positional argument.
		{
			name:               "completion returns bash and zsh",
			args:               []string{"completion"},
			prefix:             "",
			discoverAssistants: stubAssistants(nil),
			templateNames:      templateNames,
			want:               []string{"bash", "zsh"},
		},
		{
			name:               "completion with b prefix returns bash",
			args:               []string{"completion"},
			prefix:             "b",
			discoverAssistants: stubAssistants(nil),
			templateNames:      templateNames,
			want:               []string{"bash"},
		},

		// AC REQ-SC-002 #5: init positional argument.
		{
			name:               "init returns template names",
			args:               []string{"init"},
			prefix:             "",
			discoverAssistants: stubAssistants(nil),
			templateNames:      templateNames,
			want:               []string{"claude", "copilot", "opencode"},
		},
		{
			name:               "init with cl prefix returns claude",
			args:               []string{"init"},
			prefix:             "cl",
			discoverAssistants: stubAssistants(nil),
			templateNames:      templateNames,
			want:               []string{"claude"},
		},

		// AC REQ-SC-002 #6: Assistant names from discovery in first arg.
		{
			name:               "first arg includes discovered assistant names",
			args:               nil,
			prefix:             "",
			discoverAssistants: stubAssistants([]string{"claude", "copilot"}),
			templateNames:      templateNames,
			want:               []string{"claude", "completion", "copilot", "init", "rm", "status", "update"},
		},

		// AC REQ-SC-002 #7: No assistants directory -- only subcommands.
		{
			name:               "nil assistant discovery returns only subcommands",
			args:               nil,
			prefix:             "",
			discoverAssistants: stubAssistants(nil),
			templateNames:      templateNames,
			want:               []string{"completion", "init", "rm", "status", "update"},
		},

		// AC REQ-SC-002 #8: rm completes assistant names.
		{
			name:               "rm returns assistant names",
			args:               []string{"rm"},
			prefix:             "",
			discoverAssistants: stubAssistants([]string{"claude", "copilot"}),
			templateNames:      templateNames,
			want:               []string{"claude", "copilot"},
		},
		{
			name:               "rm with cl prefix returns claude",
			args:               []string{"rm"},
			prefix:             "cl",
			discoverAssistants: stubAssistants([]string{"claude", "copilot"}),
			templateNames:      templateNames,
			want:               []string{"claude"},
		},

		// Global flags completion.
		{
			name:               "global flags with -- prefix",
			args:               nil,
			prefix:             "--",
			discoverAssistants: stubAssistants(nil),
			templateNames:      templateNames,
			want:               []string{"--config", "--docker-path", "--version", "--workspace-folder"},
		},

		// update flags.
		{
			name:               "update flags",
			args:               []string{"update"},
			prefix:             "--",
			discoverAssistants: stubAssistants(nil),
			templateNames:      templateNames,
			want:               []string{"--dry-run", "--yes"},
		},

		// Assistant shortcut flags (assistant name as subcommand).
		{
			name:               "assistant shortcut flags with dash prefix",
			args:               []string{"claude"},
			prefix:             "--",
			discoverAssistants: stubAssistants([]string{"claude"}),
			templateNames:      templateNames,
			want:               []string{"--no-git-identity", "--shell"},
		},
		{
			name:               "assistant shortcut shows flags without prefix",
			args:               []string{"claude"},
			prefix:             "",
			discoverAssistants: stubAssistants([]string{"claude"}),
			templateNames:      templateNames,
			want:               []string{"--no-git-identity", "--shell"},
		},

		// No completions for status positional.
		{
			name:               "status returns no positional completions",
			args:               []string{"status"},
			prefix:             "",
			discoverAssistants: stubAssistants(nil),
			templateNames:      templateNames,
			want:               nil,
		},

		// REQ-SC-002: update completes "base" plus assistant names.
		{
			name:               "update returns base and assistant names",
			args:               []string{"update"},
			prefix:             "",
			discoverAssistants: stubAssistants([]string{"claude", "copilot"}),
			templateNames:      templateNames,
			want:               []string{"base", "claude", "copilot"},
		},
		{
			name:               "update with b prefix filters to base",
			args:               []string{"update"},
			prefix:             "b",
			discoverAssistants: stubAssistants([]string{"claude", "copilot"}),
			templateNames:      templateNames,
			want:               []string{"base"},
		},

		// Deduplication: assistant names that match subcommands are not duplicated.
		{
			name:               "assistant name matching subcommand is not duplicated",
			args:               nil,
			prefix:             "",
			discoverAssistants: stubAssistants([]string{"rm"}), // unlikely but test dedup
			templateNames:      templateNames,
			want:               []string{"completion", "init", "rm", "status", "update"},
		},

		// No template names provided.
		{
			name:               "nil template names still returns subcommands",
			args:               nil,
			prefix:             "",
			discoverAssistants: stubAssistants(nil),
			templateNames:      nil,
			want:               []string{"completion", "init", "rm", "status", "update"},
		},

		// Assistant shortcut partial prefix filtering.
		{
			name:               "assistant shortcut partial prefix filters flags",
			args:               []string{"claude"},
			prefix:             "--s",
			discoverAssistants: stubAssistants([]string{"claude"}),
			templateNames:      templateNames,
			want:               []string{"--shell"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := CompleteOptions{
				DiscoverAssistants: tt.discoverAssistants,
				TemplateNames:      tt.templateNames,
			}
			got := Complete(tt.args, tt.prefix, opts)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Complete(%v, %q) mismatch (-want +got):\n%s", tt.args, tt.prefix, diff)
			}
		})
	}
}

func TestComplete_AssistantDiscoveryFailure(t *testing.T) {
	// If discoverAssistants is nil, only static completions are returned.
	opts := CompleteOptions{
		DiscoverAssistants: nil,
		TemplateNames:      []string{"claude", "copilot", "opencode"},
	}
	got := Complete(nil, "", opts)

	// Should contain subcommands but no assistant names.
	for _, cmd := range []string{"rm", "init", "status", "update", "completion"} {
		if !slices.Contains(got, cmd) {
			t.Errorf("Complete(nil, \"\") missing subcommand %q in result %v", cmd, got)
		}
	}
}

func TestComplete_ParsesArgsCorrectly(t *testing.T) {
	// Verify that flags in the args don't break subcommand detection.
	opts := CompleteOptions{
		DiscoverAssistants: func() []string { return nil },
		TemplateNames:      []string{"claude", "copilot", "opencode"},
	}

	// "update --dry-run" should still complete update's flags.
	got := Complete([]string{"update", "--dry-run"}, "--", opts)

	// Should include update's flags.
	hasFlags := false
	for _, s := range got {
		if strings.HasPrefix(s, "--") {
			hasFlags = true
		}
	}
	if !hasFlags {
		t.Errorf("Complete(%v, --) = %v, want flags for 'update'", []string{"update", "--dry-run"}, got)
	}
}
