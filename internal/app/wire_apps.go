package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/apps"
)

// wireApps is owned by the apps feature: the browser IDE, user apps and
// the remote desktop, with their idle-stop loop and shutdown.
func wireApps(ctx context.Context, a *App) error {
	svc, err := apps.New(a.D)
	if err != nil {
		return err
	}
	a.D.Apps = svc
	svc.Routes(a.Router)
	a.OnStart("apps", svc.Start)
	a.OnClose(svc.Close)
	return nil
}
