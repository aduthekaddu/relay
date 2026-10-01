package ptyd

import (
	"context"
	"net/http"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

// terminalConfig takes one snapshot per creation. Failure refuses the new
// session; it never reconfigures or stops an existing one.
func (d *Daemon) terminalConfig(ctx context.Context) (config.TerminalConfig, error) {
	if err := ctx.Err(); err != nil {
		return config.TerminalConfig{}, err
	}
	cfg := d.cfg.Terminal
	if d.loadTerminal != nil {
		var err error
		cfg, err = d.loadTerminal(ctx)
		if err != nil {
			return cfg, httpx.Unavailable("Could not read terminal defaults from relay.toml.")
		}
	}
	switch cfg.Record {
	case "", "off", "agents", "all":
	default:
		return cfg, httpx.Unavailable("Unsupported terminal recording mode in relay.toml.")
	}
	return cfg, nil
}

func (d *Daemon) hSettings(w http.ResponseWriter, r *http.Request) {
	cfg, err := d.terminalConfig(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	cwd, err := d.resolveCwd("", cfg.DefaultCwd)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	source := "startup"
	if d.loadTerminal != nil {
		source = "file"
	}
	httpx.OK(w, ptyclient.TerminalSettings{
		TerminalDefaults: api.TerminalDefaults{
			DefaultShell: defaultShell(cfg.Shell, d.sessionEnv(&ptyclient.CreateSpec{})),
			DefaultCwd:   cwd, RecordingMode: cfg.Record, Source: source,
		}, ConfigID: config.SettingsSourceID(d.paths),
	})
}
