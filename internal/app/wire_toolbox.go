package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/toolbox"
)

// wireToolbox is owned by the toolbox feature.
func wireToolbox(ctx context.Context, a *App) error {
	svc, err := toolbox.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	return nil
}
