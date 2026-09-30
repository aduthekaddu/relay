package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/clip"
)

// wireClip is owned by the clip feature.
func wireClip(ctx context.Context, a *App) error {
	svc, err := clip.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	a.OnStart("clip", svc.Start)
	return nil
}
