package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/system"
)

// wireSystem is owned by the system feature.
func wireSystem(ctx context.Context, a *App) error {
	svc, err := system.New(a.D)
	if err != nil {
		return err
	}
	// wireLive runs before system and installs the canonical Presence owner.
	// Capture its visibility-aware subscription query before any starter runs.
	if a.D.Presence != nil {
		svc.Subscribed = a.D.Presence.Subscribed
	}
	svc.Routes(a.Router)
	a.OnStart("system", svc.Start)
	a.D.Search.Add(svc.SearchProvider())
	return nil
}
