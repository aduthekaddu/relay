package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/setup"
	"github.com/aduthekaddu/relay/internal/version"
)

// statusReport is `relay status --json`.
type statusReport struct {
	Version  string            `json:"version"`
	URL      string            `json:"url"`
	Listen   string            `json:"listen"`
	Server   componentStatus   `json:"server"`
	Ptyd     componentStatus   `json:"ptyd"`
	Services map[string]string `json:"services"`
	Config   string            `json:"config"`
}

type componentStatus struct {
	Running  bool   `json:"running"`
	Version  string `json:"version,omitempty"`
	Sessions *int   `json:"sessions,omitempty"`
	Error    string `json:"error,omitempty"`
}

func init() {
	Register(&Command{
		Name:    "status",
		Group:   "Server",
		Summary: "Show whether Relay is running and where to open it",
		Usage:   "relay status [--json]",
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("json", false, "print as JSON")
		},
		Run: runStatus,
	})
}

func runStatus(ctx context.Context, fs *flag.FlagSet, _ []string) error {
	paths, err := config.ResolvePaths()
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	sys := setup.OSSystem{}
	rep := statusReport{
		Version: version.Version,
		URL:     cfg.Origin(),
		Listen:  cfg.Server.Listen,
		Config:  paths.ConfigFile,
		Server:  probe(ctx, paths.CtlSocket, "/api/v1/health", false),
		Ptyd:    probe(ctx, paths.PtydSocket, "/v1/health", true),
	}
	m := setup.Manager{Sys: sys, UnitsDir: setup.DefaultUnitsDir(sys.GOOS(), paths.Home, os.Getenv)}
	if m.Installed(setup.SvcServe) || m.Installed(setup.SvcPtyd) {
		rep.Services = map[string]string{}
		for _, svc := range []string{setup.SvcPtyd, setup.SvcServe} {
			rep.Services[svc] = m.State(ctx, svc)
		}
	}
	if fs.Lookup("json").Value.String() == "true" {
		return setup.WriteJSON(os.Stdout, rep)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "relay\t%s\n", rep.Version)
	fmt.Fprintf(tw, "server\t%s\n", describe(rep.Server))
	fmt.Fprintf(tw, "ptyd\t%s\n", describe(rep.Ptyd))
	fmt.Fprintf(tw, "url\t%s\n", rep.URL)
	fmt.Fprintf(tw, "listen\t%s\n", rep.Listen)
	if rep.Services == nil {
		fmt.Fprintf(tw, "services\tnot installed (run `relay setup`)\n")
	} else {
		fmt.Fprintf(tw, "services\tptyd %s, serve %s\n", rep.Services[setup.SvcPtyd], rep.Services[setup.SvcServe])
	}
	fmt.Fprintf(tw, "config\t%s\n", rep.Config)
	if err := tw.Flush(); err != nil {
		return err
	}
	if !rep.Server.Running {
		return &ExitError{Code: 3}
	}
	return nil
}

func probe(ctx context.Context, socket, path string, sessions bool) componentStatus {
	h, err := setup.SocketHealth(ctx, socket, path)
	if setup.Answered(err) {
		return componentStatus{Running: true, Version: "(health endpoint unavailable)"}
	}
	if err != nil {
		return componentStatus{Error: "not running"}
	}
	st := componentStatus{Running: true, Version: h.Version}
	if sessions {
		n := h.Sessions
		st.Sessions = &n
	}
	return st
}

func describe(c componentStatus) string {
	if !c.Running {
		return "not running"
	}
	s := "running " + c.Version
	if c.Sessions != nil {
		s += fmt.Sprintf(", %d session(s)", *c.Sessions)
	}
	return s
}
