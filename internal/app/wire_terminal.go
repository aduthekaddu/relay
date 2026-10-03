package app

import (
	"context"
	"os"
	"time"

	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/terminal"
)

// wireTerminal is owned by the terminal feature. It makes sure ptyd runs
// (unless RELAY_NO_PTYD=1, e.g. under systemd where relay-ptyd.service
// owns it), registers the routes and search provider, and starts the
// event relay and upload sweeper.
func wireTerminal(ctx context.Context, a *App) error {
	if os.Getenv("RELAY_NO_PTYD") != "1" {
		ectx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := ptyclient.EnsureDaemon(ectx, a.D.Paths, "")
		cancel()
		if err != nil {
			// Not fatal: the event relay reconnects once ptyd is up and
			// terminal routes answer 503 meanwhile.
			a.D.Log.Warn("terminal daemon unavailable", "err", err)
		}
	}
	svc, err := terminal.New(a.D)
	if err != nil {
		return err
	}
	bus := a.D.Bus
	svc.OnClip = func(_ context.Context, sessionID, text string) {
		if bus != nil {
			bus.Publish(core.BusClipCapture, core.ClipCapture{Text: text, Source: "terminal", SessionID: sessionID})
		}
	}
	svc.Routes(a.Router)
	if a.D.Search != nil {
		a.D.Search.Add(svc.SearchProvider())
	}
	a.OnStart("terminal", svc.Start)
	return nil
}
