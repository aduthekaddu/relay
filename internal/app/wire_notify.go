package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/notify"
)

// wireNotify runs after wireLive installs core.Presence. notify reads that
// interface at notification time; nil means no visible watcher. Deps service
// fields are finalized during wiring, before any background loops start.
func wireNotify(ctx context.Context, a *App) error {
	svc, err := notify.New(a.D)
	if err != nil {
		return err
	}
	a.D.Notifier = svc
	svc.Routes(a.Router)
	a.OnStart("notify", svc.Start)
	return nil
}
