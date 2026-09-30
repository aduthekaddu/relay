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
	svc.Routes(a.Router)
	a.OnStart("system", svc.Start)
	a.D.Search.Add(svc.SearchProvider())
	return nil
}
