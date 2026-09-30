package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/snippets"
)

// wireSnippets is owned by the snippets feature.
func wireSnippets(ctx context.Context, a *App) error {
	svc, err := snippets.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	return nil
}
