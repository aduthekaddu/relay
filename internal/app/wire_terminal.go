package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/terminal"
)

// wireTerminal is owned by the terminal feature.
func wireTerminal(ctx context.Context, a *App) error {
	svc, err := terminal.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	return nil
}
