// Package toolbox installs agents and developer tools from embedded
// recipes and wires MCP servers into agents.
//
// Recipes live in recipes/<id>.sh with a "# key: value" metadata header
// (see docs/dev/TOOLBOX.md). The server lists them with their installed
// state and runs installs visibly in a ptyd terminal of kind "toolbox",
// so the user sees every command, types their sudo password there, and
// the install survives a browser disconnect.
package toolbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/server"
)

// cacheTTL is how long a tool list stays fresh.
const cacheTTL = 60 * time.Second

// pollEvery is how often a running install's terminal is checked.
const pollEvery = 2 * time.Second

// ptyAPI is the subset of ptyclient.Client the service uses (fakeable).
type ptyAPI interface {
	Create(ctx context.Context, spec ptyclient.CreateSpec) (*api.TerminalSession, error)
	Get(ctx context.Context, id string) (*api.TerminalSession, error)
}

// publisher is the subset of events.Bus the service uses.
type publisher interface {
	Publish(t string, data any)
}

// Service serves /api/v1/toolbox.
type Service struct {
	cat    *Catalog
	prober *Prober
	mcp    *MCP
	pty    ptyAPI
	bus    publisher
	home   string
	jobDir string
	goos   string
	poll   time.Duration

	mu        sync.Mutex
	cache     []api.Tool
	cachedAt  time.Time
	inflight  chan struct{}     // closed when the running refresh finishes
	jobs      map[string]string // tool id -> terminal id
	runCtx    context.Context
	runCancel context.CancelFunc
	wg        sync.WaitGroup
}

// New constructs the service. It starts no goroutines.
func New(d *core.Deps) (*Service, error) {
	cat, err := DefaultCatalog()
	if err != nil {
		return nil, err
	}
	home := d.Paths.Home
	finder := Finder{Dirs: SearchPath(home)}
	s := newService(cat, finder, home, filepath.Join(d.Paths.CacheDir, "toolbox"))
	if d.Pty != nil {
		s.pty = d.Pty
	}
	if d.Bus != nil {
		s.bus = d.Bus
	}
	return s, nil
}

func newService(cat *Catalog, finder Finder, home, jobDir string) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		cat:       cat,
		prober:    &Prober{Finder: finder, Run: ExecRunner, Timeout: 5 * time.Second, Concurrency: 4},
		mcp:       NewMCP(home, finder, ExecRunner),
		home:      home,
		jobDir:    jobDir,
		goos:      runtime.GOOS,
		poll:      pollEvery,
		jobs:      map[string]string{},
		runCtx:    ctx,
		runCancel: cancel,
	}
}

// Routes registers the toolbox API.
func (s *Service) Routes(rt *server.Router) {
	rt.Handle("GET /api/v1/toolbox", s.handleList)
	rt.Handle("POST /api/v1/toolbox/{id}/install", s.handleInstall)
	rt.Handle("GET /api/v1/toolbox/mcp", s.handleMCP)
	rt.Handle("POST /api/v1/toolbox/mcp/apply", s.handleMCPApply)
}

// Start removes install scripts left behind by a crash, then waits for
// ctx; job watchers are tied to the service lifetime.
func (s *Service) Start(ctx context.Context) error {
	s.cleanStaleScripts(24 * time.Hour)
	<-ctx.Done()
	return nil
}

// Close stops job watchers and waits for them.
func (s *Service) Close() error {
	s.runCancel()
	s.wg.Wait()
	return nil
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tools, err := s.List(r.Context(), httpx.QueryBool(r, "refresh"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, tools)
}

func (s *Service) handleInstall(w http.ResponseWriter, r *http.Request) {
	sess, err := s.Install(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, sess)
}

func (s *Service) handleMCP(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, s.mcp.Matrix(r.Context()))
}

func (s *Service) handleMCPApply(w http.ResponseWriter, r *http.Request) {
	var req api.MCPApplyRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	row, err := s.mcp.Apply(r.Context(), req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, row)
}

// List returns every recipe with its installed state, served from a 60 s
// cache. Concurrent callers share one refresh.
func (s *Service) List(ctx context.Context, refresh bool) ([]api.Tool, error) {
	for {
		s.mu.Lock()
		if !refresh && s.cache != nil && time.Since(s.cachedAt) < cacheTTL {
			out := append([]api.Tool(nil), s.cache...)
			s.mu.Unlock()
			return out, nil
		}
		if wait := s.inflight; wait != nil {
			s.mu.Unlock()
			select {
			case <-wait:
				refresh = false // use what the other caller computed
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		done := make(chan struct{})
		s.inflight = done
		s.mu.Unlock()

		// Probe on the service context so a cancelled request does not
		// poison the shared result.
		pctx, cancel := context.WithTimeout(s.runCtx, 30*time.Second)
		tools := s.probe(pctx)
		cancel()

		s.mu.Lock()
		s.cache, s.cachedAt, s.inflight = tools, time.Now(), nil
		out := append([]api.Tool(nil), tools...)
		s.mu.Unlock()
		close(done)
		return out, nil
	}
}

func (s *Service) probe(ctx context.Context) []api.Tool {
	rs := s.cat.All()
	st := s.prober.ProbeAll(ctx, rs)
	out := make([]api.Tool, len(rs))
	for i, r := range rs {
		out[i] = api.Tool{
			ID:           r.ID,
			Name:         r.Name,
			Category:     r.Category,
			Description:  r.Description,
			Homepage:     r.Homepage,
			Installed:    st[i].Installed,
			Version:      st[i].Version,
			Installable:  r.Supports(s.goos),
			RequiresSudo: r.NeedsSudo(s.goos),
			Size:         r.Size,
			Tags:         r.Tags,
		}
	}
	return out
}

func (s *Service) invalidate() {
	s.mu.Lock()
	s.cachedAt = time.Time{}
	s.mu.Unlock()
}

// Install starts the recipe for id in a new toolbox terminal and returns
// the session. Progress is published as api.EvToolboxJob events.
func (s *Service) Install(ctx context.Context, id string) (*api.TerminalSession, error) {
	r := s.cat.Get(id)
	if r == nil {
		return nil, httpx.NotFound("unknown tool " + id)
	}
	if !r.Supports(s.goos) {
		return nil, httpx.BadRequest(r.Name + " cannot be installed on " + s.goos)
	}
	if s.pty == nil {
		return nil, httpx.Unavailable("the terminal daemon is not running")
	}
	s.mu.Lock()
	if tid, busy := s.jobs[id]; busy {
		s.mu.Unlock()
		return nil, &httpx.Err{Status: 409, Code: "conflict", Message: r.Name + " is already installing in terminal " + tid}
	}
	s.jobs[id] = "" // reserve while the session is created
	s.mu.Unlock()

	sess, err := s.startJob(ctx, r)
	if err != nil {
		s.mu.Lock()
		delete(s.jobs, id)
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Lock()
	s.jobs[id] = sess.ID
	s.mu.Unlock()
	s.publish(api.ToolboxJob{Tool: id, TerminalID: sess.ID, State: api.ToolboxJobRunning})

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.watch(r.ID, sess.ID)
	}()
	return sess, nil
}

// runner wraps the recipe: run it, delete it, print a clear result line.
// Arguments: $1 script path, $2 display name. Nothing is interpolated.
const runner = `bash "$1"; rc=$?; rm -f -- "$1"
if [ "$rc" -eq 0 ]; then printf '\n\033[1;32m✓ %s is installed.\033[0m You can close this terminal.\n' "$2"
else printf '\n\033[1;31m✗ Installing %s failed (exit %s).\033[0m Scroll up for the error.\n' "$2" "$rc"; fi
exit "$rc"`

func (s *Service) startJob(ctx context.Context, r *Recipe) (*api.TerminalSession, error) {
	path, err := s.writeScript(r)
	if err != nil {
		return nil, err
	}
	spec := ptyclient.CreateSpec{
		CreateTerminalRequest: api.CreateTerminalRequest{
			Name:    "Install " + r.Name,
			Command: []string{"bash", "-c", runner, "relay-toolbox", path, r.Name},
			Cwd:     s.home,
			Kind:    api.KindToolbox,
			Meta:    map[string]string{"tool": r.ID},
		},
		ExtraEnv: map[string]string{
			"PATH":          strings.TrimPrefix(s.prober.Finder.Env(), "PATH="),
			"RELAY_TOOLBOX": r.ID,
		},
	}
	sess, err := s.pty.Create(ctx, spec)
	if err != nil {
		_ = os.Remove(path)
		if errors.Is(err, ptyclient.ErrUnavailable) {
			return nil, httpx.Unavailable("the terminal daemon is not running")
		}
		return nil, fmt.Errorf("start install terminal: %w", err)
	}
	return sess, nil
}

// writeScript writes the runnable recipe to a private (0700) file.
func (s *Service) writeScript(r *Recipe) (string, error) {
	if err := os.MkdirAll(s.jobDir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", s.jobDir, err)
	}
	if err := os.Chmod(s.jobDir, 0o700); err != nil {
		return "", fmt.Errorf("secure %s: %w", s.jobDir, err)
	}
	f, err := os.CreateTemp(s.jobDir, r.ID+"-*.sh")
	if err != nil {
		return "", fmt.Errorf("create install script: %w", err)
	}
	name := f.Name()
	_, werr := f.WriteString(s.cat.Script(r))
	cerr := f.Close()
	if err := errors.Join(werr, cerr, os.Chmod(name, 0o700)); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("write install script: %w", err)
	}
	return name, nil
}

// watch polls the install terminal until it exits, then publishes the
// result and refreshes the tool list.
func (s *Service) watch(toolID, termID string) {
	defer func() {
		s.mu.Lock()
		delete(s.jobs, toolID)
		s.mu.Unlock()
		s.invalidate()
	}()
	deadline := time.Now().Add(6 * time.Hour)
	t := time.NewTicker(s.poll)
	defer t.Stop()
	misses := 0
	for time.Now().Before(deadline) {
		select {
		case <-s.runCtx.Done():
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(s.runCtx, 5*time.Second)
		sess, err := s.pty.Get(ctx, termID)
		cancel()
		switch {
		case errors.Is(err, ptyclient.ErrNotFound):
			s.publish(api.ToolboxJob{Tool: toolID, TerminalID: termID, State: api.ToolboxJobFailed})
			return
		case err != nil:
			// ptyd restarting: keep trying for a while.
			if misses++; misses > 30 {
				return
			}
			continue
		}
		misses = 0
		if sess.ExitCode == nil && sess.Activity != api.ActivityExited {
			continue
		}
		state := api.ToolboxJobDone
		if sess.ExitCode == nil || *sess.ExitCode != 0 {
			state = api.ToolboxJobFailed
		}
		s.invalidate()
		s.publish(api.ToolboxJob{Tool: toolID, TerminalID: termID, State: state, ExitCode: sess.ExitCode})
		return
	}
}

func (s *Service) publish(job api.ToolboxJob) {
	if s.bus != nil {
		s.bus.Publish(api.EvToolboxJob, job)
	}
}

// cleanStaleScripts removes install scripts older than age (left behind
// when ptyd died mid-install).
func (s *Service) cleanStaleScripts(age time.Duration) {
	entries, err := os.ReadDir(s.jobDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		if time.Since(info.ModTime()) > age {
			_ = os.Remove(filepath.Join(s.jobDir, e.Name()))
		}
	}
}
