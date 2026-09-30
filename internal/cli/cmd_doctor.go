package cli

import (
	"context"
	"flag"
	"os"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/setup"
)

func init() {
	Register(&Command{
		Name:    "doctor",
		Group:   "Setup",
		Summary: "Check the installation and explain how to fix problems",
		Usage:   "relay doctor [--json]",
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("json", false, "print the report as JSON")
		},
		Run: func(ctx context.Context, fs *flag.FlagSet, args []string) error {
			paths, err := config.ResolvePaths()
			if err != nil {
				return err
			}
			d := &setup.Doctor{Paths: paths}
			r := d.Run(ctx)
			if fs.Lookup("json").Value.String() == "true" {
				if err := setup.WriteJSON(os.Stdout, r); err != nil {
					return err
				}
			} else {
				ui, done := setup.NewUI(false, os.Stdout)
				defer done()
				ui.Banner("Relay doctor", paths.ConfigFile)
				setup.PrintReport(ui, r)
			}
			if !r.OK {
				return &ExitError{Code: 1}
			}
			return nil
		},
	})
}
