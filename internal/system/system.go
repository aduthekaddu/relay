// Package system reports machine health (CPU, memory, disks, network,
// GPU), processes, user services and logs.
//
// On Linux everything is read from /proc and /sys (no cgo, no gopsutil);
// services come from systemd --user. Other platforms get build-tagged
// minimal fallbacks. See docs/dev/FILES_SYSTEM.md.
package system

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
)

const (
	// AuditTopic is the backend bus topic audit events are published on
	// (payload api.AuditEntry) unless Service.Audit is set. It matches
	// core.BusAudit.
	AuditTopic = "audit"

	liveInterval    = time.Second
	historyInterval = 10 * time.Second
	historySize     = 360 // one hour at 10 s
	metricsFresh    = 900 * time.Millisecond
	maxLogStreams   = 8
)

// Service implements the system API.
type Service struct {
	d     *core.Deps
	log   *slog.Logger
	col   *Collector
	procs *ProcLister
	units *Units
	home  string

	journalctl string
	logSem     chan struct{}

	sampleMu sync.Mutex // serialises sampling so rates never use tiny windows
	mu       sync.Mutex
	last     api.Metrics
	lastAt   time.Time
	hist     ring

	// Subscribed reports whether any browser subscribed to a live topic
	// ("metrics"). nil means nobody is subscribed. Wiring points it at
	// core.Deps.Presence.Subscribed.
	Subscribed func(topic string) bool
	// Audit, when set, receives audited actions instead of the default
	// publication on AuditTopic.
	Audit func(ctx context.Context, e api.AuditEntry)
	// ManageAllUnits allows start/stop/restart of every user unit, not
	// only Relay's own ("relay*"). Off by default.
	ManageAllUnits bool
}

// New constructs the service. It starts no goroutines.
func New(d *core.Deps) (*Service, error) {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	home := d.Paths.Home
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = h
	}
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	s := &Service{
		d:      d,
		log:    log.With("feature", "system"),
		col:    NewCollector(),
		procs:  NewProcLister(),
		home:   home,
		logSem: make(chan struct{}, maxLogStreams),
		hist:   ring{buf: make([]api.Metrics, historySize)},
	}
	s.units = NewUnits(s.bootTime)
	if p, err := exec.LookPath("journalctl"); err == nil {
		s.journalctl = p
	}
	return s, nil
}

// Routes registers every system endpoint.
func (s *Service) Routes(rt *server.Router) {
	rt.Handle("GET /api/v1/system/metrics", s.handleMetrics)
	rt.Handle("GET /api/v1/system/metrics/history", s.handleHistory)
	rt.Handle("GET /api/v1/system/processes", s.handleProcesses)
	rt.Handle("POST /api/v1/system/processes/{pid}/signal", s.handleSignal)
	rt.Handle("GET /api/v1/system/services", s.handleServices)
	rt.Handle("POST /api/v1/system/services/{name}/{action}", s.handleServiceAction)
	rt.WS("GET /api/v1/system/logs", s.handleLogs)
}

// Start samples metrics until ctx is done: every second while a browser
// is subscribed to "metrics" (publishing api.EvMetrics), otherwise every
// 10 s for the history ring only.
func (s *Service) Start(ctx context.Context) error {
	t := time.NewTicker(liveInterval)
	defer t.Stop()
	var lastHist time.Time
	for {
		now := time.Now()
		live := s.subscribed("metrics")
		if live || now.Sub(lastHist) >= historyInterval-liveInterval/2 {
			m := s.current(ctx, 0)
			if now.Sub(lastHist) >= historyInterval-liveInterval/2 {
				s.mu.Lock()
				s.hist.push(historySample(m))
				s.mu.Unlock()
				lastHist = now
			}
			// Recheck after sampling so a page hidden during a slow sample
			// does not receive one last high-frequency event.
			if live && s.d.Bus != nil && s.subscribed("metrics") {
				s.d.Bus.Publish(api.EvMetrics, m)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (s *Service) subscribed(topic string) bool {
	return s.Subscribed != nil && s.Subscribed(topic)
}

// current returns a sample no older than maxAge (0 = always resample
// unless another caller sampled within metricsFresh).
func (s *Service) current(ctx context.Context, maxAge time.Duration) api.Metrics {
	if maxAge < metricsFresh {
		maxAge = metricsFresh
	}
	s.sampleMu.Lock()
	defer s.sampleMu.Unlock()
	s.mu.Lock()
	if !s.lastAt.IsZero() && time.Since(s.lastAt) < maxAge {
		m := s.last
		s.mu.Unlock()
		return m
	}
	s.mu.Unlock()
	m := platformMetrics(ctx, s.col)
	s.mu.Lock()
	s.last, s.lastAt = m, time.Now()
	s.mu.Unlock()
	return m
}

// historySample drops fields that do not change over the hour so the
// history payload stays small.
func historySample(m api.Metrics) api.Metrics {
	m.Host = api.HostInfo{}
	m.CPU.Model = ""
	return m
}

func (s *Service) bootTime() time.Time {
	b, err := os.ReadFile(filepath.Join(s.col.Proc, "stat"))
	if err != nil {
		return time.Time{}
	}
	_, _, bt := parseProcStat(b)
	if bt <= 0 {
		return time.Time{}
	}
	return time.Unix(bt, 0)
}

func (s *Service) handleMetrics(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, s.current(r.Context(), 2*time.Second))
}

func (s *Service) handleHistory(w http.ResponseWriter, r *http.Request) {
	minutes := httpx.QueryInt(r, "minutes", 60, 1, 60)
	since := time.Now().Add(-time.Duration(minutes) * time.Minute)
	s.mu.Lock()
	out := s.hist.since(since)
	s.mu.Unlock()
	httpx.OK(w, out)
}

func (s *Service) handleProcesses(w http.ResponseWriter, r *http.Request) {
	o := ListOptions{
		Sort:     r.URL.Query().Get("sort"),
		Limit:    httpx.QueryInt(r, "limit", 100, 1, 2000),
		Query:    r.URL.Query().Get("q"),
		Sessions: s.sessionRoots(r.Context()),
	}
	switch o.Sort {
	case "", "cpu", "mem", "pid", "name":
	default:
		httpx.Fail(w, &httpx.Err{Status: 400, Code: "bad_request", Message: "sort must be cpu, mem, pid or name", Field: "sort"})
		return
	}
	httpx.OK(w, s.listProcs(r.Context(), o))
}

func (s *Service) listProcs(ctx context.Context, o ListOptions) []api.Process {
	if s.col.Available() {
		return s.procs.List(ctx, o)
	}
	return psFallback(ctx, s.procs.self, s.procs.parent, s.procs.uid, o)
}

// sessionRoots maps each live Relay terminal's shell pid to its id.
func (s *Service) sessionRoots(ctx context.Context) map[int]string {
	if s.d.Pty == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	list, err := s.d.Pty.List(ctx)
	if err != nil {
		return nil
	}
	out := make(map[int]string, len(list))
	for _, t := range list {
		if t.Pid > 0 {
			out[t.Pid] = t.ID
		}
	}
	return out
}

func (s *Service) handleSignal(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.Atoi(r.PathValue("pid"))
	if err != nil || pid <= 0 {
		httpx.Fail(w, &httpx.Err{Status: 400, Code: "bad_request", Message: "invalid pid", Field: "pid"})
		return
	}
	var req api.SignalRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	p, name, err := s.procs.Signal(pid, req.Signal)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "process.signal", "SIG"+name+" "+strconv.Itoa(pid)+" ("+p.Name+")")
	httpx.NoContent(w)
}

func (s *Service) handleServices(w http.ResponseWriter, r *http.Request) {
	list, err := s.units.List(r.Context())
	if err != nil {
		if err == ErrNoSystemd {
			httpx.OK(w, []api.Service{})
			return
		}
		s.log.Warn("list services", "err", err)
		httpx.Fail(w, httpx.Unavailable("the user service manager did not answer"))
		return
	}
	httpx.OK(w, list)
}

func (s *Service) handleServiceAction(w http.ResponseWriter, r *http.Request) {
	name, err := NormalizeUnit(r.PathValue("name"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	action := r.PathValue("action")
	switch action {
	case "start", "stop", "restart":
	default:
		httpx.Fail(w, httpx.NotFound("unknown action"))
		return
	}
	if !Managed(name) && !s.ManageAllUnits {
		httpx.Fail(w, httpx.Forbidden("only Relay's own services can be controlled"))
		return
	}
	if s.units.Systemctl == "" {
		httpx.Fail(w, httpx.Unavailable("systemd --user is not available"))
		return
	}
	svc, err := s.units.Act(r.Context(), name, action)
	if err != nil {
		if _, ok := err.(*httpx.Err); !ok {
			s.log.Warn("service action", "unit", name, "action", action, "err", err)
			err = &httpx.Err{Status: 502, Code: "service_failed", Message: "systemctl " + action + " failed"}
		}
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "service."+action, name)
	httpx.OK(w, svc)
}

// audit records a destructive or security-relevant action.
func (s *Service) audit(r *http.Request, event, detail string) {
	e := api.AuditEntry{At: time.Now().UTC(), Event: event, Detail: detail}
	if p := server.PrincipalFrom(r.Context()); p != nil {
		e.Actor = p.User
		if p.Method != "cookie" {
			e.Device = p.Method
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		e.IP = host
	}
	if s.Audit != nil {
		s.Audit(r.Context(), e)
		return
	}
	if s.d.Bus != nil {
		s.d.Bus.Publish(AuditTopic, e)
	}
}

// SearchProvider returns the command-center provider for processes.
func (s *Service) SearchProvider() *ProcessSearchProvider { return &ProcessSearchProvider{s: s} }

// ProcessSearchProvider implements core.SearchProvider (scope "processes").
type ProcessSearchProvider struct{ s *Service }

// Scope implements core.SearchProvider.
func (p *ProcessSearchProvider) Scope() string { return "processes" }

// Search implements core.SearchProvider: processes whose name, command,
// pid or terminal id match query, busiest first.
func (p *ProcessSearchProvider) Search(ctx context.Context, query string, limit int) []api.SearchResult {
	query = strings.TrimSpace(query)
	if len([]rune(query)) < 2 && !isDigits(query) {
		return nil
	}
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	procs := p.s.listProcs(ctx, ListOptions{Sort: "cpu", Limit: limit, Query: query, Sessions: p.s.sessionRoots(ctx)})
	q := strings.ToLower(query)
	out := make([]api.SearchResult, 0, len(procs))
	for _, pr := range procs {
		score := 0.4
		switch name := strings.ToLower(pr.Name); {
		case strconv.Itoa(pr.PID) == query, name == q:
			score = 1
		case strings.HasPrefix(name, q):
			score = 0.8
		case strings.Contains(name, q):
			score = 0.6
		}
		sub := pr.Cmd
		if len(sub) > 120 {
			sub = sub[:120] + "…"
		}
		r := api.SearchResult{
			Scope: "processes", ID: strconv.Itoa(pr.PID),
			Title: pr.Name + " · " + strconv.Itoa(pr.PID), Subtitle: sub, Icon: "process",
			Link: "/system/processes?pid=" + strconv.Itoa(pr.PID), Score: score, At: pr.StartedAt,
			Meta: map[string]string{"user": pr.User, "cpu": strconv.FormatFloat(pr.CPU, 'f', 1, 64)},
		}
		if pr.Terminal != "" {
			r.Meta["terminal"] = pr.Terminal
		}
		if pr.Protected {
			r.Meta["protected"] = "1"
		}
		out = append(out, r)
	}
	return out
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ring is a fixed-size ring buffer of metrics samples.
type ring struct {
	buf  []api.Metrics
	next int
	full bool
}

func (r *ring) push(m api.Metrics) {
	r.buf[r.next] = m
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// since returns samples taken at or after t, oldest first.
func (r *ring) since(t time.Time) []api.Metrics {
	n := r.next
	start := 0
	if r.full {
		n, start = len(r.buf), r.next
	}
	out := make([]api.Metrics, 0, n)
	for i := 0; i < n; i++ {
		m := r.buf[(start+i)%len(r.buf)]
		if !m.At.Before(t) {
			out = append(out, m)
		}
	}
	return out
}
