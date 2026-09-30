package cli

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/setup"
)

func init() {
	Register(&Command{
		Name:    "logs",
		Group:   "Server",
		Summary: "Show the logs of the Relay services",
		Usage:   "relay logs [-f] [-n lines] [--service all|serve|ptyd]",
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("f", false, "follow: keep printing new lines")
			fs.Int("n", 200, "number of recent lines")
			fs.String("service", "all", "which service: all, serve or ptyd")
		},
		Run: runLogs,
	})
}

// LogsCommand returns the argv that shows Relay's logs on goos.
func LogsCommand(goos, logDir, service string, lines int, follow bool) ([]string, error) {
	if lines <= 0 || lines > 1_000_000 {
		return nil, fmt.Errorf("-n must be between 1 and 1000000")
	}
	var units, files []string
	switch service {
	case "all", "":
		units = []string{"relay-ptyd.service", "relay.service"}
		files = []string{"relay-ptyd.log", "relay.log"}
	case "serve", "relay":
		units, files = []string{"relay.service"}, []string{"relay.log"}
	case "ptyd":
		units, files = []string{"relay-ptyd.service"}, []string{"relay-ptyd.log"}
	default:
		return nil, fmt.Errorf("unknown service %q (use all, serve or ptyd)", service)
	}
	n := strconv.Itoa(lines)
	if goos == "darwin" {
		argv := []string{"tail", "-n", n}
		if follow {
			argv = append(argv, "-F")
		}
		for _, f := range files {
			argv = append(argv, filepath.Join(logDir, f))
		}
		return argv, nil
	}
	argv := []string{"journalctl", "--user", "-n", n, "-o", "short-iso"}
	for _, u := range units {
		argv = append(argv, "-u", u)
	}
	if follow {
		argv = append(argv, "-f")
	} else {
		argv = append(argv, "--no-pager")
	}
	return argv, nil
}

func runLogs(ctx context.Context, fs *flag.FlagSet, _ []string) error {
	paths, err := config.ResolvePaths()
	if err != nil {
		return err
	}
	lines, _ := strconv.Atoi(fs.Lookup("n").Value.String())
	sys := setup.OSSystem{}
	argv, err := LogsCommand(sys.GOOS(), filepath.Join(paths.DataDir, "logs"), fs.Lookup("service").Value.String(),
		lines, fs.Lookup("f").Value.String() == "true")
	if err != nil {
		return &ExitError{Code: 2, Msg: err.Error()}
	}
	if _, err := sys.LookPath(argv[0]); err != nil {
		return fmt.Errorf("%s not found; Relay's services log to the %s", argv[0], map[bool]string{true: "files in " + filepath.Join(paths.DataDir, "logs"), false: "systemd journal"}[sys.GOOS() == "darwin"])
	}
	if err := sys.Interactive(ctx, nil, argv[0], argv[1:]...); err != nil && ctx.Err() == nil {
		return fmt.Errorf("%s: %w", argv[0], err)
	}
	return nil
}
