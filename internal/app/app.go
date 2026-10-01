// Package app assembles Relay: it builds the shared dependencies, wires
// every feature (one wire_<feature>.go file per feature, owned by that
// feature) and runs the HTTP server plus background loops.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
	"github.com/aduthekaddu/relay/internal/version"
)

// App is a fully wired Relay server.
type App struct {
	D      *core.Deps
	Router *server.Router
	Server *server.Server

	mu       sync.Mutex
	starters []namedFn
	closers  []func() error
	// extraOrigins are additional browser origins allowed for unsafe
	// requests (e.g. the Tailscale name when both are used).
	extraOrigins []string
	hostPolicies []func(host string) bool
}

type namedFn struct {
	name string
	fn   func(ctx context.Context) error
}

// OnStart registers a background loop. fn should block until ctx is done.
func (a *App) OnStart(name string, fn func(ctx context.Context) error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.starters = append(a.starters, namedFn{name, fn})
}

// OnClose registers a cleanup function (run in reverse order).
func (a *App) OnClose(fn func() error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closers = append(a.closers, fn)
}

// AllowOrigin adds a browser origin accepted for unsafe requests.
func (a *App) AllowOrigin(origin string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.extraOrigins = append(a.extraOrigins, strings.TrimRight(origin, "/"))
}

// AllowTLSHost extends the automatic-HTTPS host whitelist.
func (a *App) AllowTLSHost(f func(host string) bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hostPolicies = append(a.hostPolicies, f)
}

// Origins returns every allowed browser origin.
func (a *App) Origins() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []string{a.D.Cfg.Origin()}
	out = append(out, a.extraOrigins...)
	// Local development origins for the Vite dev server.
	if os.Getenv("RELAY_DEV") == "1" {
		for port := 47780; port <= 47789; port++ {
			out = append(out, fmt.Sprintf("http://127.0.0.1:%d", port), fmt.Sprintf("http://localhost:%d", port))
		}
	}
	return out
}

// Build loads configuration and wires every feature. It starts no
// goroutines; call Run.
func Build(ctx context.Context, log *slog.Logger) (*App, error) {
	paths, err := config.ResolvePaths()
	if err != nil {
		return nil, err
	}
	if err := paths.Ensure(); err != nil {
		return nil, fmt.Errorf("create directories: %w", err)
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(paths.DB)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	d := &core.Deps{
		Cfg:       cfg,
		Paths:     paths,
		Store:     st,
		Bus:       events.New(),
		Log:       log,
		Pty:       ptyclient.New(paths.PtydSocket),
		Version:   version.Version,
		Commit:    version.Commit,
		StartedAt: time.Now().UTC(),
		Search:    core.NewSearchRegistry(),
	}
	d.InitSettings()
	a := &App{D: d}
	a.Router = server.NewRouter(nil, a.Origins)
	a.OnClose(st.Close)

	// Order matters: later features may use services set on Deps by
	// earlier ones (Notifier, Workspaces, Agents).
	steps := []struct {
		name string
		fn   func(context.Context, *App) error
	}{
		{"auth", wireAuth},
		{"live", wireLive},
		{"notify", wireNotify},
		{"workspaces", wireWorkspaces},
		{"agents", wireAgents},
		{"terminal", wireTerminal},
		{"files", wireFiles},
		{"system", wireSystem},
		{"previews", wirePreviews},
		{"apps", wireApps},
		{"clip", wireClip},
		{"snippets", wireSnippets},
		{"schedule", wireSchedule},
		{"search", wireSearch},
		{"toolbox", wireToolbox},
		{"info", wireInfo},
		{"web", wireWeb}, // last: SPA catch-all
	}
	for _, s := range steps {
		if err := s.fn(ctx, a); err != nil {
			a.Close()
			return nil, fmt.Errorf("wire %s: %w", s.name, err)
		}
	}
	a.Server = server.New(cfg, paths, log, a.Router)
	a.Server.HostPolicy = func(host string) bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, f := range a.hostPolicies {
			if f(host) {
				return true
			}
		}
		return false
	}
	return a, nil
}

// Run starts background loops and serves until ctx is cancelled.
func (a *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for _, s := range a.starters {
		s := s
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.fn(ctx); err != nil && !errors.Is(err, context.Canceled) {
				a.D.Log.Error("background task failed", "task", s.name, "err", err)
			}
		}()
	}
	err := a.Server.Run(ctx)
	cancel()
	wg.Wait()
	a.Close()
	return err
}

// Close runs closers in reverse registration order.
func (a *App) Close() {
	a.mu.Lock()
	cl := a.closers
	a.closers = nil
	a.mu.Unlock()
	for i := len(cl) - 1; i >= 0; i-- {
		_ = cl[i]()
	}
}
