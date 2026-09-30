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
	if a.D.Search != nil {
		a.D.Search.Add(svc.ScriptProvider())
	}
	svc.Routes(a.Router)
	return nil
}
