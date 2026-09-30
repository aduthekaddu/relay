package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/live"
)

// wireLive is owned by the live feature.
func wireLive(ctx context.Context, a *App) error {
	svc, err := live.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	return nil
}
