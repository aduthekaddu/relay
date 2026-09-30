package cli

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"os"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/setup"
)

func init() {
	Register(&Command{
		Name:    "uninstall",
		Group:   "Setup",
		Summary: "Stop and remove the Relay services (and, with --purge, all Relay data)",
		Usage:   "relay uninstall [--purge] [--keep-binary] [--yes]",
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("purge", false, "also delete relay.toml, the database, recordings and uploads")
			fs.Bool("keep-binary", false, "leave the relay binary in place")
			fs.Bool("yes", false, "do not ask for confirmation (with --purge this deletes data without asking)")
			fs.String("units-dir", "", "unit directory used at setup (default: the user unit directory)")
		},
		Run: runUninstall,
	})
}

func runUninstall(ctx context.Context, fs *flag.FlagSet, _ []string) error {
	on := func(n string) bool { return fs.Lookup(n).Value.String() == "true" }
	yes, purge := on("yes"), on("purge")
	paths, err := config.ResolvePaths()
	if err != nil {
		return err
	}
	ui, done := setup.NewUI(!yes, os.Stdout)
	defer done()
	sys := setup.OSSystem{}
	dir := fs.Lookup("units-dir").Value.String()
	if dir == "" {
		dir = setup.DefaultUnitsDir(sys.GOOS(), paths.Home, os.Getenv)
	}
	ui.Banner("Uninstall Relay", "Terminal sessions running under relay-ptyd end when it stops.")
	if ok, err := ui.Confirm("Stop and remove the Relay services?", true); err != nil || !ok {
		return &ExitError{Code: 1, Msg: "nothing changed"}
	}
	m := setup.Manager{Sys: sys, UnitsDir: dir}
	removed, rmErr := m.Remove(ctx)
	for _, r := range removed {
		ui.OK("Removed %s", r)
	}
	if len(removed) == 0 && rmErr == nil {
		ui.Dim("No Relay service units in %s", dir)
	}
	if rmErr != nil {
		ui.Warn("%v", rmErr)
	}
	if !on("keep-binary") {
		removeBinary(ui, yes)
	}
	if purge {
		if err := purgeData(ui, paths, yes); err != nil {
			return err
		}
	} else {
		ui.Dim("Your configuration and data are kept in %s and %s (relay uninstall --purge deletes them).", paths.ConfigDir, paths.DataDir)
	}
	ui.Dim("If you published Relay with `tailscale serve`, undo it with `tailscale serve reset`.")
	return rmErr
}

func removeBinary(ui *setup.UI, yes bool) {
	bin, err := setup.Executable()
	if err != nil {
		ui.Warn("%v", err)
		return
	}
	if !yes {
		ok, err := ui.Confirm("Delete the relay binary at "+bin+"?", true)
		if err != nil || !ok {
			return
		}
	}
	if err := os.Remove(bin); err != nil {
		ui.Warn("could not delete %s: %v", bin, err)
		return
	}
	ui.OK("Deleted %s", bin)
}

func purgeData(ui *setup.UI, paths config.Paths, yes bool) error {
	targets, err := setup.PurgeTargets(paths, os.Getenv("RELAY_HOME"))
	if err != nil {
		return err
	}
	ui.Warn("This permanently deletes your Relay account, settings, notifications, recordings and uploads:")
	for _, t := range targets {
		ui.Code(t)
	}
	ui.Dim("Your own files and agent transcripts are not touched.")
	if !yes {
		answer, err := ui.Ask("Type 'purge' to confirm", "", nil)
		if err != nil {
			return err
		}
		if answer != "purge" {
			return &ExitError{Code: 1, Msg: "data kept (confirmation did not match)"}
		}
	}
	var errs []error
	for _, t := range targets {
		if _, err := os.Lstat(t); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err := os.RemoveAll(t); err != nil {
			errs = append(errs, err)
			continue
		}
		ui.OK("Deleted %s", t)
	}
	return errors.Join(errs...)
}
