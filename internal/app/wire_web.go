package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/web"
)

// wireWeb mounts the embedded single-page app as the catch-all route.
func wireWeb(ctx context.Context, a *App) error {
	a.Router.Raw("/", web.Handler())
	return nil
}
