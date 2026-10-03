package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/clip"
)

// wireClip constructs the capture subscriber before App.Run starts terminal
// and desktop producers concurrently. Close also handles a later wiring failure.
func wireClip(ctx context.Context, a *App) error {
	svc, err := clip.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	a.OnStart("clip", svc.Start)
	a.OnClose(svc.Close)
	return nil
}
