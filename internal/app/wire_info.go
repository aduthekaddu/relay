package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/info"
)

// wireInfo is owned by the info feature.
func wireInfo(ctx context.Context, a *App) error {
	svc, err := info.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	return nil
}
