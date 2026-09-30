// Package search powers the command center backend: federated search over
// every registered core.SearchProvider, Raycast-compatible script commands
// and Quick AI (a one-shot headless agent run streamed as NDJSON).
//
// See docs/dev/COMMAND_CENTER.md.
package search

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/server"
)

// Defaults for the federated search and the runners.
const (
	// DefaultBudget is how long a search waits for providers.
	DefaultBudget = 150 * time.Millisecond
	// DefaultLimit is the per-scope result cap when ?limit= is absent.
	DefaultLimit = 8
	// MaxLimit bounds ?limit=.
	MaxLimit = 50
	// MaxQuery bounds the query length (runes) passed to providers.
	MaxQuery = 200

	// ScriptTimeout bounds inline and silent script runs.
	ScriptTimeout = 30 * time.Second
	// ScriptOutputMax caps captured script stdout.
	ScriptOutputMax = 64 << 10

	// AskTimeout bounds a Quick AI run.
	AskTimeout = 120 * time.Second
	// AskPerHour is the Quick AI hourly quota.
	AskPerHour = 20
)

// Pty is the subset of the ptyd client used for terminal-mode scripts.
type Pty interface {
	Create(ctx context.Context, spec ptyclient.CreateSpec) (*api.TerminalSession, error)
}

// Service implements the command-center routes.
type Service struct {
	d      *core.Deps
	pty    Pty
	now    func() time.Time
	budget time.Duration

	scriptDirs    []string
	scriptTimeout time.Duration
	scripts       scriptCache
	scriptSlots   chan struct{}

	askTimeout time.Duration
	ask        *askLimiter
	search     *bucket
}

// Option customises the service (tests).
type Option func(*Service)

// WithPty replaces the pty client.
func WithPty(p Pty) Option { return func(s *Service) { s.pty = p } }

// WithClock replaces the clock.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithBudget overrides the federated search budget.
func WithBudget(d time.Duration) Option { return func(s *Service) { s.budget = d } }

// WithScriptDirs replaces the script command directories.
func WithScriptDirs(dirs ...string) Option { return func(s *Service) { s.scriptDirs = dirs } }

// WithScriptTimeout overrides the inline/silent script timeout.
func WithScriptTimeout(d time.Duration) Option { return func(s *Service) { s.scriptTimeout = d } }

// WithAskTimeout overrides the Quick AI timeout.
func WithAskTimeout(d time.Duration) Option { return func(s *Service) { s.askTimeout = d } }

// New builds the service. It starts no goroutines.
func New(d *core.Deps, opts ...Option) (*Service, error) {
	s := &Service{
		d:             d,
		now:           time.Now,
		budget:        DefaultBudget,
		scriptDirs:    defaultScriptDirs(d.Paths.Home, d.Paths.ConfigDir),
		scriptTimeout: ScriptTimeout,
		scriptSlots:   make(chan struct{}, 4),
		askTimeout:    AskTimeout,
	}
	if d.Pty != nil {
		s.pty = d.Pty
	}
	for _, o := range opts {
		o(s)
	}
	s.ask = newAskLimiter(AskPerHour, time.Hour, s.now)
	// Typing in the palette issues a request per keystroke (debounced by
	// the client); 20/s with a burst of 40 is far above that and still
	// stops a runaway loop from pinning every provider.
	s.search = newBucket(20, 40, s.now)
	return s, nil
}

// defaultScriptDirs returns ~/.config/relay/commands plus
// <ConfigDir>/commands when that differs (RELAY_HOME or XDG_CONFIG_HOME).
func defaultScriptDirs(home, configDir string) []string {
	var dirs []string
	add := func(p string) {
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		for _, d := range dirs {
			if d == p {
				return
			}
		}
		dirs = append(dirs, p)
	}
	if home != "" {
		add(filepath.Join(home, ".config", "relay", "commands"))
	}
	if configDir != "" {
		add(filepath.Join(configDir, "commands"))
	}
	return dirs
}

// Routes registers the command-center routes.
func (s *Service) Routes(rt *server.Router) {
	rt.Handle("GET /api/v1/search", s.handleSearch)
	rt.Handle("GET /api/v1/scripts", s.handleScripts)
	rt.Handle("POST /api/v1/scripts/{id}/run", s.handleRunScript)
	rt.Handle("POST /api/v1/ask", s.handleAsk)
}

func (s *Service) handleSearch(w http.ResponseWriter, r *http.Request) {
	if wait, ok := s.search.take(); !ok {
		httpx.Fail(w, tooMany("slow down: too many searches", wait))
		return
	}
	q := r.URL.Query()
	limit := httpx.QueryInt(r, "limit", DefaultLimit, 1, MaxLimit)
	var scopes []string
	if v := q.Get("scopes"); v != "" {
		for _, sc := range strings.Split(v, ",") {
			if sc = strings.TrimSpace(sc); sc != "" {
				scopes = append(scopes, sc)
			}
		}
	}
	httpx.OK(w, s.Search(r.Context(), q.Get("q"), scopes, limit))
}

// Search runs every provider (or only those in scopes) concurrently under
// the time budget, then normalises, ranks and caps the results per scope.
// Providers that miss the budget are left out of this response.
func (s *Service) Search(ctx context.Context, query string, scopes []string, limit int) api.SearchResponse {
	start := s.now()
	query = normalizeQuery(query)
	if limit <= 0 {
		limit = DefaultLimit
	}
	providers := s.providers(scopes)
	batches := s.gather(ctx, providers, query, min(limit*2, MaxLimit))
	results := rank(query, batches, limit, s.now())
	return api.SearchResponse{Query: query, Results: results, TookMs: s.now().Sub(start).Milliseconds()}
}

// providers returns the registered providers, filtered by scopes.
func (s *Service) providers(scopes []string) []core.SearchProvider {
	if s.d.Search == nil {
		return nil
	}
	all := s.d.Search.Providers()
	if len(scopes) == 0 {
		return all
	}
	want := make(map[string]bool, len(scopes))
	for _, sc := range scopes {
		want[sc] = true
	}
	out := all[:0:0]
	for _, p := range all {
		if want[p.Scope()] {
			out = append(out, p)
		}
	}
	return out
}

type batch struct {
	scope   string
	results []api.SearchResult
}

// gather queries providers concurrently and returns the batches that
// arrived before the budget ran out. Late providers see a cancelled
// context; the buffered channel means they never block on send.
func (s *Service) gather(ctx context.Context, providers []core.SearchProvider, query string, limit int) []batch {
	if len(providers) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.budget)
	defer cancel()
	ch := make(chan batch, len(providers))
	var wg sync.WaitGroup
	for _, p := range providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch <- batch{scope: p.Scope(), results: s.safeSearch(ctx, p, query, limit)}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	out := make([]batch, 0, len(providers))
	for range providers {
		select {
		case b := <-ch:
			out = append(out, b)
		case <-ctx.Done():
			return out
		}
	}
	<-done
	return out
}

// safeSearch shields the request from a panicking provider.
func (s *Service) safeSearch(ctx context.Context, p core.SearchProvider, query string, limit int) (res []api.SearchResult) {
	defer func() {
		if v := recover(); v != nil {
			if s.d.Log != nil {
				s.d.Log.Warn("search provider panicked", "scope", p.Scope(), "panic", fmt.Sprint(v))
			}
			res = nil
		}
	}()
	return p.Search(ctx, query, limit)
}

// normalizeQuery trims, collapses whitespace and bounds the query.
func normalizeQuery(q string) string {
	q = strings.Join(strings.Fields(q), " ")
	if r := []rune(q); len(r) > MaxQuery {
		q = string(r[:MaxQuery])
	}
	return q
}

func tooMany(msg string, wait time.Duration) *httpx.Err {
	secs := int((wait + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return &httpx.Err{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: msg, RetryIn: secs}
}

// homeDir returns the user's home directory.
func (s *Service) homeDir() string {
	if s.d.Paths.Home != "" {
		return s.d.Paths.Home
	}
	h, _ := os.UserHomeDir()
	return h
}
