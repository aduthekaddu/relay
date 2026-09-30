package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/notify"
)

// wireNotify is owned by the notify feature.
func wireNotify(ctx context.Context, a *App) error {
	svc, err := notify.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	return nil
}
