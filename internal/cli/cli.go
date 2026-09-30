// Package cli is the `relay` command line. Each subcommand lives in its own
// file (cmd_<name>.go) and registers itself in init(), so features can add
// commands without touching a shared switch statement.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/aduthekaddu/relay/internal/version"
)

// Command is one `relay <name>` subcommand.
type Command struct {
	Name    string
	Aliases []string
	Group   string // "Server", "Terminal", "Setup", "Account" — for help output
	Summary string // one line
	Usage   string // e.g. "relay notify [-t title] <message>"
	Hidden  bool
	// Run receives the flag set (already created, named after the command)
	// and the remaining args. Define flags in Flags, parse happens for you.
	Flags func(fs *flag.FlagSet)
	Run   func(ctx context.Context, fs *flag.FlagSet, args []string) error
}

var commands = map[string]*Command{}

// Register adds a command. Call from init().
func Register(c *Command) {
	if _, dup := commands[c.Name]; dup {
		panic("duplicate command " + c.Name)
	}
	commands[c.Name] = c
	for _, a := range c.Aliases {
		commands[a] = c
	}
}

// ExitError carries an exit status without printing a stack.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// Run executes the command line and returns the process exit code.
func Run(argv []string) int {
	if len(argv) < 2 {
		printHelp(os.Stdout)
		return 0
	}
	name := argv[1]
	switch name {
	case "-h", "--help", "help":
		if len(argv) > 2 {
			if c, ok := commands[argv[2]]; ok {
				printCommandHelp(os.Stdout, c)
				return 0
			}
		}
		printHelp(os.Stdout)
		return 0
	case "-v", "--version":
		name = "version"
	}
	c, ok := commands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "relay: unknown command %q\n\nRun 'relay help' for usage.\n", name)
		return 2
	}
	fs := flag.NewFlagSet("relay "+c.Name, flag.ContinueOnError)
	fs.Usage = func() { printCommandHelp(os.Stderr, c) }
	if c.Flags != nil {
		c.Flags(fs)
	}
	if err := fs.Parse(argv[2:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := c.Run(ctx, fs, fs.Args()); err != nil {
		if ee, ok := err.(*ExitError); ok {
			if ee.Msg != "" {
				fmt.Fprintln(os.Stderr, "relay:", ee.Msg)
			}
			return ee.Code
		}
		fmt.Fprintln(os.Stderr, "relay:", err)
		return 1
	}
	return 0
}

func printHelp(w io.Writer) {
	fmt.Fprintf(w, "Relay %s — your machine, from any browser.\n\nUsage:\n  relay <command> [flags]\n", version.Version)
	groups := map[string][]*Command{}
	seen := map[*Command]bool{}
	for _, c := range commands {
		if c.Hidden || seen[c] {
			continue
		}
		seen[c] = true
		g := c.Group
		if g == "" {
			g = "Other"
		}
		groups[g] = append(groups[g], c)
	}
	order := []string{"Setup", "Server", "Terminal", "Account", "Other"}
	for _, g := range order {
		cs := groups[g]
		if len(cs) == 0 {
			continue
		}
		sort.Slice(cs, func(i, j int) bool { return cs[i].Name < cs[j].Name })
		fmt.Fprintf(w, "\n%s:\n", g)
		tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
		for _, c := range cs {
			fmt.Fprintf(tw, "  %s\t%s\n", c.Name, c.Summary)
		}
		tw.Flush()
	}
	fmt.Fprintln(w, "\nRun 'relay help <command>' for details. Docs: https://github.com/aduthekaddu/relay")
}

func printCommandHelp(w io.Writer, c *Command) {
	usage := c.Usage
	if usage == "" {
		usage = "relay " + c.Name
	}
	fmt.Fprintf(w, "%s\n\nUsage:\n  %s\n", c.Summary, usage)
	if len(c.Aliases) > 0 {
		fmt.Fprintf(w, "\nAliases: %s\n", strings.Join(c.Aliases, ", "))
	}
	fs := flag.NewFlagSet(c.Name, flag.ContinueOnError)
	if c.Flags != nil {
		c.Flags(fs)
		fmt.Fprintln(w, "\nFlags:")
		fs.SetOutput(w)
		fs.PrintDefaults()
	}
}
