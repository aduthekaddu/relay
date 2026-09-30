package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/info"
	"github.com/aduthekaddu/relay/internal/live"
)

// wireLive is owned by the live feature. It installs core.Presence, so it
// runs before the features that consult it (terminal, notify).
func wireLive(ctx context.Context, a *App) error {
	svc, err := live.New(a.D)
	if err != nil {
		return err
	}
	// The "hello" payload is the same api.Info that /api/v1/info serves.
	inf, err := info.New(a.D)
	if err != nil {
		return err
	}
	svc.SetInfo(inf.Info)
	svc.Routes(a.Router)
	a.D.Presence = svc
	a.OnStart("live", svc.Start)
	return nil
}
