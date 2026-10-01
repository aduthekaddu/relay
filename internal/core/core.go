// Package core holds the dependency container passed to every feature
// package, plus the small interfaces features use to talk to each other
// without import cycles.
//
// Wiring happens in internal/app. A feature package exposes:
//
//	func New(d *core.Deps) (*Service, error)   // construct, no goroutines
//	func (s *Service) Routes(rt *server.Router) // register HTTP routes
//	func (s *Service) Start(ctx context.Context) error // optional: background loops, returns when ctx done
//	func (s *Service) Close() error             // optional
package core

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/store"
)

type Deps struct {
	Cfg       *config.Config  // immutable startup configuration
	Settings  *config.Runtime // shared settings snapshots and transactions
	Paths     config.Paths
	Store     *store.Store
	Bus       *events.Bus
	Log       *slog.Logger
	Pty       *ptyclient.Client
	Version   string
	Commit    string
	StartedAt time.Time

	// Set during wiring by the owning feature. May be nil if the feature is
	// disabled; always nil-check.
	Notifier   Notifier
	Agents     AgentService
	Workspaces WorkspaceService
	Search     *SearchRegistry
	Presence   Presence // set by internal/live (see bus.go)
	Previews   PreviewCapabilities
	Apps       AppCapabilities
}

// Notifier delivers a notification to the in-app inbox and every enabled
// channel (Web Push, ntfy, webhook). Owned by internal/notify.
type Notifier interface {
	Notify(ctx context.Context, req api.NotifyRequest) (*api.Notification, error)
}

// AgentService is what other features need from internal/agents.
type AgentService interface {
	// List returns adapter info for every known agent.
	List(ctx context.Context) []api.AgentInfo
	// Command returns argv (+ extra env) to run agent interactively in cwd,
	// optionally with an initial prompt.
	Command(ctx context.Context, agent, prompt, model string) (argv []string, env map[string]string, err error)
	// HeadlessCommand returns argv for a one-shot, non-interactive run that
	// prints the answer to stdout (claude -p, codex exec, gemini -p ...).
	HeadlessCommand(ctx context.Context, agent, prompt, model string) ([]string, error)
}

// WorkspaceService resolves project roots. Owned by internal/workspaces.
type WorkspaceService interface {
	List(ctx context.Context) ([]api.Workspace, error)
	// RootOf returns the workspace (git) root containing path, or "".
	RootOf(path string) string
}

// SearchProvider contributes results to the command center.
type SearchProvider interface {
	Scope() string
	Search(ctx context.Context, query string, limit int) []api.SearchResult
}

type SearchRegistry struct {
	mu        sync.RWMutex
	providers map[string]SearchProvider
}

func NewSearchRegistry() *SearchRegistry {
	return &SearchRegistry{providers: map[string]SearchProvider{}}
}

func (r *SearchRegistry) Add(p SearchProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Scope()] = p
}

// Providers returns providers sorted by scope name.
func (r *SearchRegistry) Providers() []SearchProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SearchProvider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope() < out[j].Scope() })
	return out
}
