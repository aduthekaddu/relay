// Package agents indexes coding-agent sessions (Claude Code, Codex, Gemini
// CLI, OpenCode, Kiro, Cursor, Grok, pi, Hermes, Amp, Copilot, Aider, Qwen
// Code, Crush), pairs them with live terminals, launches and resumes them,
// computes usage and cost, and installs attention hooks.
//
// Agent transcripts are only ever read. The one exception is the hook
// configuration an agent reads, which is edited only when the user asks
// (POST /api/v1/agents/{agent}/hooks), after a timestamped backup.
package agents

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
)

// Service implements the agents feature and core.AgentService.
type Service struct {
	d        *core.Deps
	adapters []*Adapter
	byID     map[string]*Adapter
	det      *detector
	ix       *index
	idx      *indexer
	prices   *priceTable
	dbs      *agentDBs
	live     *liveState
	pty      ptyAPI // nil when ptyd is not configured
	quota    *quotaCache
	usage    *usageCache
	search   *limiter
	home     string
	tmp      string

	// worktrees creates git worktrees for launches (set from
	// d.Workspaces when it implements worktreeCreator).
	worktrees worktreeCreator
	// relayPath is the executable hooks call back into.
	relayPath string

	hookMu sync.Mutex // serialises hook config edits

	wsMu    sync.Mutex
	wsCache []string
	wsAt    time.Time
}

// worktreeCreator is implemented by the workspaces service.
type worktreeCreator interface {
	CreateWorktree(ctx context.Context, repo, branch, base string) (*api.Worktree, error)
}

var _ core.AgentService = (*Service)(nil)

// New builds the service: loads prices, migrates the index and prepares
// adapters. It starts no goroutines.
func New(d *core.Deps) (*Service, error) {
	prices, err := loadPrices(pricesJSON)
	if err != nil {
		return nil, err
	}
	ix, err := openIndex(context.Background(), d.Store)
	if err != nil {
		return nil, err
	}
	home := d.Paths.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	s := &Service{
		d: d, det: newDetector(home), ix: ix, prices: prices, dbs: newAgentDBs(),
		home: home, byID: map[string]*Adapter{},
		quota: newQuotaCache(), usage: newUsageCache(), search: newLimiter(20, time.Second),
	}
	if d.Paths.CacheDir != "" {
		s.tmp = filepath.Join(d.Paths.CacheDir, "agents")
	} else {
		s.tmp = filepath.Join(os.TempDir(), fmt.Sprintf("relay-agents-%d", os.Getuid()))
	}
	disabled := map[string]bool{}
	if d.Cfg != nil {
		ix.noText = !d.Cfg.Agents.IndexHistory
		for _, id := range d.Cfg.Agents.Disabled {
			disabled[id] = true
		}
	}
	for _, a := range builtinAdapters() {
		if disabled[a.ID] {
			continue
		}
		s.adapters = append(s.adapters, a)
		s.byID[a.ID] = a
	}
	if d.Pty != nil {
		s.pty = d.Pty
	}
	if wc, ok := d.Workspaces.(worktreeCreator); ok {
		s.worktrees = wc
	}
	if exe, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		s.relayPath = exe
	}
	s.live = newLiveState(s)
	s.idx = newIndexer(s)
	return s, nil
}

// Routes registers the HTTP API.
func (s *Service) Routes(rt *server.Router) {
	rt.Handle("GET /api/v1/agents", s.handleList)
	rt.Handle("GET /api/v1/agents/sessions", s.handleSessions)
	rt.Handle("GET /api/v1/agents/sessions/{id}", s.handleSession)
	rt.Handle("PATCH /api/v1/agents/sessions/{id}", s.handlePatchSession)
	rt.Handle("GET /api/v1/agents/sessions/{id}/transcript", s.handleTranscript)
	rt.Handle("POST /api/v1/agents/sessions/{id}/resume", s.handleResume)
	rt.Handle("POST /api/v1/agents/launch", s.handleLaunch)
	rt.Handle("GET /api/v1/agents/search", s.handleSearch)
	rt.Handle("GET /api/v1/agents/usage", s.handleUsage)
	rt.Handle("GET /api/v1/agents/quotas", s.handleQuotas)
	rt.Handle("POST /api/v1/agents/reindex", s.handleReindex)
	rt.Handle("POST /api/v1/agents/hook", s.handleHook)
	rt.Handle("POST /api/v1/agents/{agent}/hooks", s.handleInstallHooks)
	rt.Handle("DELETE /api/v1/agents/{agent}/hooks", s.handleRemoveHooks)
}

// Start runs the indexer and the live-terminal pairing loop until ctx ends.
func (s *Service) Start(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		s.live.loop(ctx)
	}()
	go func() {
		defer wg.Done()
		_ = s.idx.loop(ctx)
	}()
	wg.Wait()
	return nil
}

// Close releases database snapshots.
func (s *Service) Close() error {
	s.dbs.closeAll()
	_ = os.RemoveAll(filepath.Join(s.tmp))
	return nil
}

func (s *Service) log() *slog.Logger {
	if s.d.Log != nil {
		return s.d.Log
	}
	return slog.Default()
}

func (s *Service) publish(t string, data any) {
	if s.d.Bus != nil {
		s.d.Bus.Publish(t, data)
	}
}

// env returns the reader environment.
func (s *Service) env() *env {
	return &env{home: s.home, tmp: s.tmp, dbs: s.dbs, workspaces: s.workspaceDirs}
}

// workspaceDirs lists known project directories (cached for a minute) for
// readers that keep history inside projects (Aider, Crush, Gemini hashes).
func (s *Service) workspaceDirs(ctx context.Context) []string {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	if time.Since(s.wsAt) < time.Minute && s.wsCache != nil {
		return s.wsCache
	}
	set := map[string]bool{}
	if wp, ok := s.d.Workspaces.(interface {
		Paths(context.Context) []string
	}); ok {
		// Cheap listing without git briefs.
		for _, p := range wp.Paths(ctx) {
			set[p] = true
		}
	} else if s.d.Workspaces != nil {
		if ws, err := s.d.Workspaces.List(ctx); err == nil {
			for _, w := range ws {
				set[w.Path] = true
			}
		}
	}
	rows, err := s.ix.db.QueryContext(ctx, `SELECT DISTINCT cwd FROM agent_sessions WHERE cwd<>'' LIMIT 2000`)
	if err == nil {
		for rows.Next() {
			var c string
			if rows.Scan(&c) == nil {
				set[c] = true
			}
		}
		rows.Close()
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	s.wsCache, s.wsAt = out, time.Now()
	return out
}

// adapter returns the enabled adapter id or an error.
func (s *Service) adapter(id string) (*Adapter, error) {
	if a, ok := s.byID[id]; ok {
		return a, nil
	}
	return nil, &httpx.Err{Status: 404, Code: "not_found", Message: "unknown agent " + strconv.Quote(id)}
}

// List implements core.AgentService: adapter info for every enabled agent.
func (s *Service) List(ctx context.Context) []api.AgentInfo {
	counts := map[string]int{}
	if rows, err := s.ix.db.QueryContext(ctx, `SELECT agent, count(*) FROM agent_sessions WHERE hidden=0 GROUP BY agent`); err == nil {
		for rows.Next() {
			var a string
			var n int
			if rows.Scan(&a, &n) == nil {
				counts[a] = n
			}
		}
		rows.Close()
	}
	out := make([]api.AgentInfo, len(s.adapters))
	var wg sync.WaitGroup
	for i, a := range s.adapters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			det := s.det.detect(ctx, a)
			info := api.AgentInfo{ID: a.ID, Name: a.Name, Vendor: a.Vendor, Color: a.Color,
				Installed: det.Binary != "", Version: det.Version, Binary: det.Binary,
				Capabilities: a.Caps, InstallHint: a.InstallHint, Sessions: counts[a.ID]}
			if a.Hooks != nil {
				st := a.Hooks.status(s.env())
				info.Hooks = &st
			}
			out[i] = info
		}()
	}
	wg.Wait()
	return out
}

// binary returns the installed binary of a, or a 409-style error.
func (s *Service) binary(ctx context.Context, a *Adapter) (string, error) {
	det := s.det.detect(ctx, a)
	if det.Binary == "" {
		return "", httpx.Conflict(a.Name + " is not installed")
	}
	return det.Binary, nil
}

// Command implements core.AgentService.
func (s *Service) Command(ctx context.Context, agent, prompt, model string) ([]string, map[string]string, error) {
	a, err := s.adapter(agent)
	if err != nil {
		return nil, nil, err
	}
	if !validModel(model) {
		return nil, nil, httpx.BadRequest("invalid model name")
	}
	bin, err := s.binary(ctx, a)
	if err != nil {
		return nil, nil, err
	}
	if !a.Caps.Prompt {
		prompt = ""
	}
	return a.Interactive(bin, prompt, model), map[string]string{}, nil
}

// HeadlessCommand implements core.AgentService.
func (s *Service) HeadlessCommand(ctx context.Context, agent, prompt, model string) ([]string, error) {
	a, err := s.adapter(agent)
	if err != nil {
		return nil, err
	}
	if a.Headless == nil {
		return nil, httpx.Conflict(a.Name + " has no headless mode")
	}
	if !validModel(model) {
		return nil, httpx.BadRequest("invalid model name")
	}
	bin, err := s.binary(ctx, a)
	if err != nil {
		return nil, err
	}
	return a.Headless(bin, prompt, model), nil
}

// announce publishes the current state of one session (after the first
// pass, so the initial bulk index does not flood clients).
func (s *Service) announce(ctx context.Context, id string) {
	if !s.idx.first.Load() || id == "" {
		return
	}
	row, err := s.ix.session(ctx, id)
	if err != nil || row == nil || row.Hidden {
		return
	}
	out := s.decorate(ctx, []*sessionRow{row})
	if len(out) == 1 {
		s.publish(api.EvAgentSession, out[0])
	}
}

// onIndexed runs after every indexing pass.
func (s *Service) onIndexed() {
	s.usage.invalidate()
	s.live.repair()
}

// agentOrder sorts ids by adapter order (for stable output).
func (s *Service) agentOrder(ids []string) {
	pos := map[string]int{}
	for i, a := range s.adapters {
		pos[a.ID] = i
	}
	slices.SortFunc(ids, func(a, b string) int { return pos[a] - pos[b] })
}
