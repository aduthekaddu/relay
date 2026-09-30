package cli

import (
	"os"

	"golang.org/x/term"
)

// isTTY reports whether f is an interactive terminal.
func isTTY(f *os.File) bool { return f != nil && term.IsTerminal(int(f.Fd())) }
