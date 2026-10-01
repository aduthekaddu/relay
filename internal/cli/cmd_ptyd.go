package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/ptyd"
)

func init() {
	Register(&Command{
		Name:    "ptyd",
		Group:   "Server",
		Summary: "Run the terminal session daemon in the foreground",
		Usage:   "relay ptyd [--socket path] [--debug]",
		Flags: func(fs *flag.FlagSet) {
			fs.String("socket", "", "listen on this unix socket instead of $RUNTIME/ptyd.sock")
			fs.Bool("debug", false, "verbose logging")
		},
		Run: runPtyd,
	})
}

func runPtyd(ctx context.Context, fs *flag.FlagSet, _ []string) error {
	level := slog.LevelInfo
	if fs.Lookup("debug").Value.String() == "true" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	paths, err := config.ResolvePaths()
	if err != nil {
		return err
	}
	if err := paths.Ensure(); err != nil {
		return fmt.Errorf("create directories: %w", err)
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return err
	}
	d, err := ptyd.New(ptyd.Options{Cfg: cfg, Paths: paths, Socket: fs.Lookup("socket").Value.String(), Log: log,
		LoadTerminal: func(ctx context.Context) (config.TerminalConfig, error) {
			if err := ctx.Err(); err != nil {
				return config.TerminalConfig{}, err
			}
			cfg, err := config.Load(paths)
			if err != nil {
				return config.TerminalConfig{}, err
			}
			return cfg.Terminal, nil
		}})
	if err != nil {
		return err
	}
	if err := d.Run(ctx); err != nil {
		if errors.Is(err, ptyd.ErrAlreadyRunning) {
			return &ExitError{Code: 1, Msg: "ptyd is already running (" + d.Socket() + ")"}
		}
		return err
	}
	return nil
}
