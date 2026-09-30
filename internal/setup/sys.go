package setup

import (
	"context"
	"io"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"time"
)

// System is every side effect setup and the ops commands perform on the
// machine, so tests can substitute a fake.
type System interface {
	GOOS() string
	LookPath(name string) (string, error)
	// Output runs a command and returns its combined output.
	Output(ctx context.Context, name string, args ...string) (string, error)
	// Interactive runs a command attached to the user's terminal (sudo
	// password prompts, installer progress).
	Interactive(ctx context.Context, env []string, name string, args ...string) error
	Getuid() int
	Username() string
}

// OSSystem is the real System.
type OSSystem struct {
	Stdin  io.Reader // defaults to os.Stdin
	Stdout io.Writer // defaults to os.Stdout
	Stderr io.Writer // defaults to os.Stderr
}

// GOOS returns runtime.GOOS.
func (OSSystem) GOOS() string { return runtime.GOOS }

// LookPath wraps exec.LookPath.
func (OSSystem) LookPath(name string) (string, error) { return exec.LookPath(name) }

// Output runs name with a 2 minute ceiling and returns combined output.
func (OSSystem) Output(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Interactive runs name attached to the terminal.
func (s OSSystem) Interactive(ctx context.Context, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = s.Stdin, s.Stdout, s.Stderr
	if cmd.Stdin == nil {
		cmd.Stdin = os.Stdin
	}
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd.Run()
}

// Getuid returns os.Getuid.
func (OSSystem) Getuid() int { return os.Getuid() }

// Username returns the login name of the current user.
func (OSSystem) Username() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if v := os.Getenv("USER"); v != "" {
		return v
	}
	return "relay"
}
