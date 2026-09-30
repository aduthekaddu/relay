package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/auth"
)

// wireAuth is owned by the auth feature.
func wireAuth(ctx context.Context, a *App) error {
	svc, err := auth.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	return nil
}
