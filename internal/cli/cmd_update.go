package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/setup"
	"github.com/aduthekaddu/relay/internal/version"
)

func init() {
	Register(&Command{
		Name:    "update",
		Group:   "Setup",
		Summary: "Update Relay to the latest release (checksum verified)",
		Usage:   "relay update [--check] [--version v1.2.3] [--all] [--no-restart]",
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("check", false, "only report whether an update is available")
			fs.String("version", "", "install this release instead of the latest (also allows downgrades)")
			fs.Bool("force", false, "reinstall even when already up to date")
			fs.Bool("all", false, "also restart relay-ptyd (ends every terminal session)")
			fs.Bool("no-restart", false, "replace the binary but do not restart services")
		},
		Run: runUpdate,
	})
}

func runUpdate(ctx context.Context, fs *flag.FlagSet, _ []string) error {
	flagStr := func(n string) string { return fs.Lookup(n).Value.String() }
	flagOn := func(n string) bool { return flagStr(n) == "true" }
	ui, done := setup.NewUI(false, os.Stdout)
	defer done()
	up := setup.Updater{API: os.Getenv("RELAY_RELEASES_API"), Token: os.Getenv("GITHUB_TOKEN")}
	want := strings.TrimSpace(flagStr("version"))
	rel, err := up.FetchRelease(ctx, want)
	if err != nil {
		return err
	}
	cur := version.Version
	cmp := setup.CompareVersions(cur, rel.Tag)
	if flagOn("check") {
		switch {
		case cur == "dev":
			ui.Info("This is a development build; the latest release is %s.", rel.Tag)
		case cmp < 0:
			ui.OK("Update available: %s → %s  (relay update)", cur, rel.Tag)
			if rel.HTMLURL != "" {
				ui.Dim("Release notes: %s", rel.HTMLURL)
			}
		default:
			ui.OK("Relay %s is up to date (latest %s)", cur, rel.Tag)
		}
		return nil
	}
	if cmp >= 0 && want == "" && !flagOn("force") {
		ui.OK("Relay %s is up to date", cur)
		return nil
	}
	target, err := setup.Executable()
	if err != nil {
		return err
	}
	sys := setup.OSSystem{}
	hadCap := runtime.GOOS == "linux" && setup.HasBindCap(ctx, sys, target)
	asset := setup.AssetName(runtime.GOOS, runtime.GOARCH)
	ui.Info("Downloading %s %s…", asset, rel.Tag)
	tmp, err := up.Download(ctx, rel, asset, filepath.Dir(target))
	if err != nil {
		return err
	}
	defer os.Remove(tmp) // no-op after a successful rename
	ui.OK("Checksum verified (sha256)")
	if err := setup.VerifyRuns(ctx, tmp, rel.Tag); err != nil {
		return err
	}
	if err := setup.Replace(tmp, target); err != nil {
		return err
	}
	ui.OK("Installed %s at %s", rel.Tag, target)
	if hadCap {
		reapplyCap(ctx, ui, sys, target)
	}
	if flagOn("no-restart") {
		ui.Dim("Not restarting; run `systemctl --user restart relay` when convenient.")
		return nil
	}
	return restartAfterUpdate(ctx, ui, sys, rel.Tag, flagOn("all"))
}

// reapplyCap restores cap_net_bind_service, which replacing the file drops.
func reapplyCap(ctx context.Context, ui *setup.UI, sys setup.OSSystem, bin string) {
	if _, err := sys.Output(ctx, "sudo", "-n", "setcap", "cap_net_bind_service=+ep", bin); err == nil {
		ui.OK("Re-applied cap_net_bind_service (binding :443)")
		return
	}
	ui.Info("The old binary could bind port 443; the new file needs the capability again (sudo):")
	if err := sys.Interactive(ctx, nil, "sudo", "setcap", "cap_net_bind_service=+ep", bin); err != nil {
		ui.Warn("setcap failed: %v. Run: sudo setcap cap_net_bind_service=+ep %s", err, bin)
		return
	}
	ui.OK("Re-applied cap_net_bind_service")
}

func restartAfterUpdate(ctx context.Context, ui *setup.UI, sys setup.OSSystem, tag string, all bool) error {
	paths, err := config.ResolvePaths()
	if err != nil {
		return err
	}
	m := setup.Manager{Sys: sys, UnitsDir: setup.DefaultUnitsDir(sys.GOOS(), paths.Home, os.Getenv)}
	if !m.Installed(setup.SvcServe) || !m.Available(ctx) {
		ui.Dim("No Relay service installed; restart `relay serve` yourself to use %s.", tag)
		return nil
	}
	if all {
		ui.Warn("Restarting relay-ptyd ends every terminal session.")
		if err := m.Restart(ctx, setup.SvcPtyd); err != nil {
			return err
		}
	} else {
		ui.Dim("Terminal sessions keep running (relay-ptyd is not restarted; use --all to include it).")
	}
	if err := m.Restart(ctx, setup.SvcServe); err != nil {
		return err
	}
	h, err := setup.WaitHealthy(ctx, paths.CtlSocket, 20*time.Second)
	if err != nil {
		return fmt.Errorf("relay did not come back after the restart: %w (see `relay logs`)", err)
	}
	ui.OK("Relay %s is running", h.Version)
	return nil
}
