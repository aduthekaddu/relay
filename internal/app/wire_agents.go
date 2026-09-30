package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/agents"
)

// wireAgents is owned by the agents feature. It runs after wireWorkspaces,
// so a.D.Workspaces is already set (used for worktree launches).
func wireAgents(ctx context.Context, a *App) error {
	svc, err := agents.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	a.D.Agents = svc
	a.OnStart("agents", svc.Start)
	a.OnClose(svc.Close)
	if a.D.Search != nil {
		for _, p := range svc.SearchProviders() {
			a.D.Search.Add(p)
		}
	}
	return nil
}
