package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/schedule"
)

// wireSchedule is owned by the schedule feature. The loop honours
// schedules.enabled itself (manual "Run now" keeps working when disabled).
func wireSchedule(ctx context.Context, a *App) error {
	svc, err := schedule.New(a.D)
	if err != nil {
		return err
	}
	svc.Routes(a.Router)
	a.OnStart("schedule", svc.Start)
	a.OnClose(svc.Close)
	return nil
}
