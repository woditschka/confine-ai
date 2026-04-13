package cli

import (
	"errors"
	"flag"
	"io"
)

// NewFlagSet creates a flag.FlagSet for a subcommand, wired to stderr.
func NewFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// ErrHelp is returned by ParseFlags when --help is passed.
var ErrHelp = errors.New("help requested")

// ParseFlags parses the flag set. Returns ErrHelp when --help is passed.
func ParseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ErrHelp
		}
		return err
	}
	return nil
}

// IgnoreHelp converts ErrHelp to nil, passing other errors through.
func IgnoreHelp(err error) error {
	if errors.Is(err, ErrHelp) {
		return nil
	}
	return err
}
