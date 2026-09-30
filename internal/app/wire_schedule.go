package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/schedule"
)

// wireSchedule is owned by the schedule feature.
func wireSchedule(ctx context.Context, a *App) error {
	svc, err := schedule.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	return nil
}
