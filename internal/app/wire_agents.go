package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/agents"
)

// wireAgents is owned by the agents feature.
func wireAgents(ctx context.Context, a *App) error {
	svc, err := agents.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	return nil
}
