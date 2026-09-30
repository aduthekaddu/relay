package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/previews"
)

// wirePreviews is owned by the previews feature.
func wirePreviews(ctx context.Context, a *App) error {
	svc, err := previews.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	return nil
}
