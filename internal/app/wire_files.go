package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/files"
)

// wireFiles is owned by the files feature.
func wireFiles(ctx context.Context, a *App) error {
	svc, err := files.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	a.OnStart("files", svc.Start)
	a.OnClose(svc.Close)
	a.D.Search.Add(svc.SearchProvider())
	return nil
}
