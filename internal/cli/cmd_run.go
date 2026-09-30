package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

func init() {
	Register(&Command{
		Name:    "run",
		Group:   "Terminal",
		Summary: "Start a command in a new persistent Relay session",
		Usage:   "relay run [--name n] [--cwd dir] [--attach] [--json] -- command [args...]",
		Flags: func(fs *flag.FlagSet) {
			fs.String("name", "", "session name (default: the command name)")
			fs.String("cwd", "", "working directory (default: the current directory)")
			fs.Bool("attach", false, "attach to the session right away")
			fs.Bool("json", false, "print the created session as JSON")
		},
		Run: runRun,
	})
}

func runRun(ctx context.Context, fs *flag.FlagSet, args []string) error {
	if len(args) == 0 {
		fs.Usage()
		return &ExitError{Code: 2}
	}
	cwd := fs.Lookup("cwd").Value.String()
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("current directory: %w", err)
		}
		cwd = wd
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return fmt.Errorf("cwd: %w", err)
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return &ExitError{Code: 1, Msg: fmt.Sprintf("%s is not a directory", cwd)}
	}
	c, err := ptydClient(ctx, true)
	if err != nil {
		return friendly(err)
	}
	spec := ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{
		Name:    fs.Lookup("name").Value.String(),
		Command: args,
		Cwd:     abs,
		Kind:    api.KindTask,
		// Resolve the command with the caller's PATH, as a shell would.
		Env: map[string]string{"PATH": os.Getenv("PATH")},
	}}
	if spec.Env["PATH"] == "" {
		spec.Env = nil
	}
	sess, err := c.Create(ctx, spec)
	if err != nil {
		return friendly(err)
	}
	if fs.Lookup("attach").Value.String() == "true" {
		return attachSession(ctx, c, sess, false, true)
	}
	if fs.Lookup("json").Value.String() == "true" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(sess)
	}
	fmt.Printf("%s\t%s\n", sess.ID, clean(sess.Name))
	fmt.Fprintf(os.Stderr, "attach with: relay attach %s\n", sess.ID)
	return nil
}
