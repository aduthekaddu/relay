package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/search"
)

// wireSearch is owned by the search feature.
func wireSearch(ctx context.Context, a *App) error {
	svc, err := search.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	return nil
}
