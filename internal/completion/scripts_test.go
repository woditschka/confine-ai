package completion

import (
	"strings"
	"testing"
)

func TestScript(t *testing.T) {
	t.Run("bash script contains callback command", func(t *testing.T) {
		script, err := Script("bash")
		if err != nil {
			t.Fatalf("Script(bash) error: %v", err)
		}
		if !strings.Contains(script, "confine-ai") {
			t.Error("Script(bash) does not contain 'confine-ai'")
		}
		if !strings.Contains(script, "__complete") {
			t.Error("Script(bash) does not contain '__complete'")
		}
	})

	t.Run("zsh script contains callback command", func(t *testing.T) {
		script, err := Script("zsh")
		if err != nil {
			t.Fatalf("Script(zsh) error: %v", err)
		}
		if !strings.Contains(script, "confine-ai") {
			t.Error("Script(zsh) does not contain 'confine-ai'")
		}
		if !strings.Contains(script, "__complete") {
			t.Error("Script(zsh) does not contain '__complete'")
		}
	})

	t.Run("unsupported shell returns error", func(t *testing.T) {
		_, err := Script("fish")
		if err == nil {
			t.Fatal("Script(fish) = nil error, want error")
		}
		if !strings.Contains(err.Error(), "bash") {
			t.Errorf("Script(fish) error = %q, want containing 'bash'", err.Error())
		}
		if !strings.Contains(err.Error(), "zsh") {
			t.Errorf("Script(fish) error = %q, want containing 'zsh'", err.Error())
		}
	})

	t.Run("empty shell returns error", func(t *testing.T) {
		_, err := Script("")
		if err == nil {
			t.Fatal("Script('') = nil error, want error")
		}
	})
}

func TestInstructions(t *testing.T) {
	tests := []struct {
		shell    string
		wantHint string
	}{
		{"bash", "~/.bashrc"},
		{"zsh", "~/.zshrc"},
		{"fish", ""},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			got := Instructions(tt.shell)
			if tt.wantHint == "" {
				if got != "" {
					t.Errorf("Instructions(%q) = %q, want empty", tt.shell, got)
				}
				return
			}
			if !strings.Contains(got, tt.wantHint) {
				t.Errorf("Instructions(%q) = %q, want containing %q", tt.shell, got, tt.wantHint)
			}
			if !strings.Contains(got, "source <(confine-ai completion "+tt.shell+")") {
				t.Errorf("Instructions(%q) missing source command", tt.shell)
			}
		})
	}
}
