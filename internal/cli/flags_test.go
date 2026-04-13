package cli

import (
	"bytes"
	"errors"
	"testing"
)

func TestParseFlags(t *testing.T) {
	t.Run("valid flags parsed", func(t *testing.T) {
		var stderr bytes.Buffer
		fs := NewFlagSet("test", &stderr)
		var name string
		fs.StringVar(&name, "name", "", "test flag")

		err := ParseFlags(fs, []string{"--name", "hello"})
		if err != nil {
			t.Fatalf("ParseFlags() unexpected error: %v", err)
		}
		if name != "hello" {
			t.Errorf("ParseFlags() name = %q, want %q", name, "hello")
		}
	})

	t.Run("help flag returns ErrHelp", func(t *testing.T) {
		var stderr bytes.Buffer
		fs := NewFlagSet("test", &stderr)

		err := ParseFlags(fs, []string{"--help"})
		if !errors.Is(err, ErrHelp) {
			t.Errorf("ParseFlags(--help) error = %v, want ErrHelp", err)
		}
	})

	t.Run("unknown flag returns error", func(t *testing.T) {
		var stderr bytes.Buffer
		fs := NewFlagSet("test", &stderr)

		err := ParseFlags(fs, []string{"--nonexistent"})
		if err == nil {
			t.Fatal("ParseFlags(--nonexistent) = nil, want error")
		}
		if errors.Is(err, ErrHelp) {
			t.Error("ParseFlags(--nonexistent) returned ErrHelp, want different error")
		}
	})
}

func TestIgnoreHelp(t *testing.T) {
	t.Run("ErrHelp returns nil", func(t *testing.T) {
		if err := IgnoreHelp(ErrHelp); err != nil {
			t.Errorf("IgnoreHelp(ErrHelp) = %v, want nil", err)
		}
	})

	t.Run("other error passes through", func(t *testing.T) {
		other := errors.New("some error")
		if got := IgnoreHelp(other); !errors.Is(got, other) {
			t.Errorf("IgnoreHelp(other) = %v, want %v", got, other)
		}
	})

	t.Run("nil returns nil", func(t *testing.T) {
		if err := IgnoreHelp(nil); err != nil {
			t.Errorf("IgnoreHelp(nil) = %v, want nil", err)
		}
	})
}
