package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/workspaces"
)

// wireWorkspaces is owned by the workspaces feature. It runs before
// wireAgents, which uses a.D.Workspaces for worktree launches.
func wireWorkspaces(ctx context.Context, a *App) error {
	svc, err := workspaces.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	a.D.Workspaces = svc
	if a.D.Search != nil {
		a.D.Search.Add(svc.SearchProvider())
	}
	return nil
}
