package app

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"github.com/aduthekaddu/relay/internal/cli"
)

func init() {
	cli.Register(&cli.Command{
		Name:    "serve",
		Group:   "Server",
		Summary: "Run the Relay web server",
		Usage:   "relay serve [--listen addr] [--debug]",
		Flags: func(fs *flag.FlagSet) {
			fs.String("listen", "", "override server.listen (e.g. 127.0.0.1:7777)")
			fs.Bool("debug", false, "verbose logging")
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
			return a.Run(ctx)
		},
	})
}
