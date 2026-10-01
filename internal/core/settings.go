package core

import "github.com/aduthekaddu/relay/internal/config"

// InitSettings is construction-only, before any service starts. App wiring
// initializes it before features; feature fixtures may initialize it here.
func (d *Deps) InitSettings() {
	if d.Settings == nil {
		d.Settings = config.NewRuntime(d.Cfg, d.Paths.Home)
	}
}

// RuntimeConfig returns a defensive snapshot for settings consumers.
func (d *Deps) RuntimeConfig() *config.Config {
	if d.Settings != nil {
		return d.Settings.Snapshot()
	}
	return config.Normalized(d.Cfg, d.Paths.Home)
}
