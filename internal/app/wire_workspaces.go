package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/workspaces"
)

// wireWorkspaces is owned by the workspaces feature.
func wireWorkspaces(ctx context.Context, a *App) error {
	svc, err := workspaces.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	return nil
}
