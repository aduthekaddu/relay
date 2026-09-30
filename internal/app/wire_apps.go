package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/apps"
)

// wireApps is owned by the apps feature.
func wireApps(ctx context.Context, a *App) error {
	svc, err := apps.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	return nil
}
