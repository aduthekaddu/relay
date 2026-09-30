package app

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/aduthekaddu/relay/internal/cli"
)

func init() {
	cli.Register(&cli.Command{
		Name:    "serve",
		Group:   "Server",
		Summary: "Run the Relay web server",
		Usage:   "relay serve [--listen addr] [--debug] [--open]",
		Flags: func(fs *flag.FlagSet) {
			fs.String("listen", "", "override server.listen (e.g. 127.0.0.1:7777)")
			fs.Bool("debug", false, "verbose logging")
			fs.Bool("open", false, "print the URL to open in a browser once the server is set up")
		},
		Run: func(ctx context.Context, fs *flag.FlagSet, args []string) error {
			level := slog.LevelInfo
			if fs.Lookup("debug").Value.String() == "true" {
				level = slog.LevelDebug
			}
			if l := fs.Lookup("listen").Value.String(); l != "" {
				os.Setenv("RELAY_LISTEN", l)
			}
			log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
			a, err := Build(ctx, log)
			if err != nil {
				return err
			}
			if fs.Lookup("open").Value.String() == "true" {
				// Printed on stdout (logs go to stderr) so scripts can
				// capture it: relay serve --open | head -1
				fmt.Println("Open " + a.D.Cfg.Origin() + "/")
			}
			return a.Run(ctx)
		},
	})
}
