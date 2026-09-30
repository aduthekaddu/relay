package app

import (
	"context"

	"github.com/aduthekaddu/relay/internal/auth"
)

// wireAuth is owned by the auth feature. It must run first: every other
// authenticated route depends on the authenticator it installs.
func wireAuth(ctx context.Context, a *App) error {
	svc, err := auth.New(a.D)
	if err != nil {
		return err
	}
	svc.SetOrigins(a.Origins)
	svc.Routes(a.Router)
	a.Router.SetAuthenticator(svc)
	a.OnStart("auth", svc.Start)
	return nil
}
