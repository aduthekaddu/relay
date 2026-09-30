package toolbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/server"
)

type fakePty struct {
	mu       sync.Mutex
	specs    []ptyclient.CreateSpec
	sessions map[string]*api.TerminalSession
	fail     error
}

func (f *fakePty) Create(_ context.Context, spec ptyclient.CreateSpec) (*api.TerminalSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	f.specs = append(f.specs, spec)
	s := &api.TerminalSession{ID: "t_" + spec.Meta["tool"], Name: spec.Name, Kind: spec.Kind, Command: spec.Command, Activity: api.ActivityWorking}
	if f.sessions == nil {
		f.sessions = map[string]*api.TerminalSession{}
	}
	f.sessions[s.ID] = s
	cp := *s
	return &cp, nil
}

func (f *fakePty) Get(_ context.Context, id string) (*api.TerminalSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return nil, ptyclient.ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (f *fakePty) exit(id string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[id].ExitCode = &code
	f.sessions[id].Activity = api.ActivityExited
}

type fakeBus struct {
	mu   sync.Mutex
	jobs []api.ToolboxJob
}

func (b *fakeBus) Publish(t string, data any) {
	if t != api.EvToolboxJob {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.jobs = append(b.jobs, data.(api.ToolboxJob))
}

func (b *fakeBus) states() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, j := range b.jobs {
		out = append(out, j.Tool+":"+j.State)
	}
	return out
}

func testService(t *testing.T) (*Service, *fakePty, *fakeBus) {
	t.Helper()
	cat, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExe(t, bin, "rg", `echo "ripgrep 14.1.1"`)
	s := newService(cat, Finder{Dirs: []string{bin}}, home, filepath.Join(home, "cache", "toolbox"))
	s.goos = "linux"
	s.poll = 10 * time.Millisecond
	pty, bus := &fakePty{}, &fakeBus{}
	s.pty, s.bus = pty, bus
	t.Cleanup(func() { _ = s.Close() })
	return s, pty, bus
}

func TestListAndCache(t *testing.T) {
	s, _, _ := testService(t)
	var runs atomic.Int32
	inner := s.prober.Run
	s.prober.Run = func(ctx context.Context, env, argv []string) (string, error) {
		runs.Add(1)
		return inner(ctx, env, argv)
	}
	tools, err := s.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	var rg *api.Tool
	for i := range tools {
		if tools[i].ID == "ripgrep" {
			rg = &tools[i]
		}
	}
	if rg == nil || !rg.Installed || rg.Version != "14.1.1" || !rg.Installable || !rg.RequiresSudo {
		t.Fatalf("ripgrep row: %+v", rg)
	}
	if runs.Load() != 1 {
		t.Fatalf("version commands run = %d, want 1 (only installed tools)", runs.Load())
	}
	// Concurrent callers share the cache.
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.List(context.Background(), false) }()
	}
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatalf("cache not used: %d runs", runs.Load())
	}
	if _, err := s.List(context.Background(), true); err != nil || runs.Load() != 2 {
		t.Fatalf("refresh did not re-probe: %v %d", err, runs.Load())
	}
}

func TestInstallLifecycle(t *testing.T) {
	s, pty, bus := testService(t)
	sess, err := s.Install(context.Background(), "jq")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Kind != api.KindToolbox || sess.Name != "Install jq" {
		t.Fatalf("session: %+v", sess)
	}
	spec := pty.specs[0]
	if spec.Command[0] != "bash" || spec.Command[1] != "-c" || spec.Command[5] != "jq" {
		t.Fatalf("command: %q", spec.Command)
	}
	script := spec.Command[4]
	st, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("script mode %v, want 0700", st.Mode().Perm())
	}
	if dst, _ := os.Stat(filepath.Dir(script)); dst.Mode().Perm() != 0o700 {
		t.Fatalf("script dir mode %v", dst.Mode().Perm())
	}
	body, _ := os.ReadFile(script)
	if !strings.Contains(string(body), "pkg apt=") || !strings.Contains(string(body), "as_root()") {
		t.Fatal("script lacks recipe body or helper library")
	}
	if spec.Meta["tool"] != "jq" || spec.ExtraEnv["RELAY_TOOLBOX"] != "jq" {
		t.Fatalf("meta/env: %v %v", spec.Meta, spec.ExtraEnv)
	}

	if _, err := s.Install(context.Background(), "jq"); err == nil || !strings.Contains(err.Error(), "already installing") {
		t.Fatalf("second install while running: %v", err)
	}
	pty.exit(sess.ID, 0)
	waitFor(t, func() bool { return len(bus.states()) == 2 })
	if got := strings.Join(bus.states(), ","); got != "jq:running,jq:done" {
		t.Fatalf("events = %s", got)
	}
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.jobs) == 0 })

	// A failing install reports failed with its exit code.
	sess, err = s.Install(context.Background(), "fzf")
	if err != nil {
		t.Fatal(err)
	}
	pty.exit(sess.ID, 3)
	waitFor(t, func() bool { return len(bus.states()) == 4 })
	bus.mu.Lock()
	last := bus.jobs[3]
	bus.mu.Unlock()
	if last.State != api.ToolboxJobFailed || last.ExitCode == nil || *last.ExitCode != 3 {
		t.Fatalf("last job: %+v", last)
	}
}

func TestInstallErrors(t *testing.T) {
	s, pty, _ := testService(t)
	if _, err := s.Install(context.Background(), "nope"); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("unknown: %v", err)
	}
	s.goos = "darwin"
	if _, err := s.Install(context.Background(), "desktop"); err == nil || !strings.Contains(err.Error(), "cannot be installed") {
		t.Fatalf("unsupported platform: %v", err)
	}
	s.goos = "linux"
	pty.fail = ptyclient.ErrUnavailable
	if _, err := s.Install(context.Background(), "jq"); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("ptyd down: %v", err)
	}
	entries, _ := os.ReadDir(s.jobDir)
	if len(entries) != 0 {
		t.Fatalf("failed start left scripts behind: %v", entries)
	}
	s.mu.Lock()
	n := len(s.jobs)
	s.mu.Unlock()
	if n != 0 {
		t.Fatal("failed start left a job reservation")
	}
}

func TestRoutes(t *testing.T) {
	s, _, _ := testService(t)
	rt := server.NewRouter(nil, func() []string { return nil })
	s.Routes(rt)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req = req.WithContext(server.MarkLocal(req.Context()))
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("GET", "/api/v1/toolbox", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"id":"ripgrep"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String()[:min(200, rec.Body.Len())])
	}
	if rec := do("POST", "/api/v1/toolbox/jq/install", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("install: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/v1/toolbox/nope/install", ""); rec.Code != 404 {
		t.Fatalf("install unknown: %d", rec.Code)
	}
	rec := do("GET", "/api/v1/toolbox/mcp", "")
	var rows []api.MCPServer
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &rows) != nil || len(rows) != 5 {
		t.Fatalf("mcp: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/v1/toolbox/mcp/apply", `{"server":"playwright","agents":["claude"],"extra":1}`); rec.Code != 400 {
		t.Fatalf("apply with unknown field: %d", rec.Code)
	}
	if rec := do("POST", "/api/v1/toolbox/mcp/apply", `{"server":"playwright","agents":["claude"]}`); rec.Code != 400 {
		t.Fatalf("apply to missing agent: %d %s", rec.Code, rec.Body)
	}
	// Unauthenticated requests are refused.
	req := httptest.NewRequest("GET", "/api/v1/toolbox", nil)
	rec = httptest.NewRecorder()
	rt.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("anonymous list: %d", rec.Code)
	}
}

func TestCleanStaleScripts(t *testing.T) {
	s, _, _ := testService(t)
	if err := os.MkdirAll(s.jobDir, 0o700); err != nil {
		t.Fatal(err)
	}
	old, fresh := filepath.Join(s.jobDir, "a-1.sh"), filepath.Join(s.jobDir, "b-2.sh")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(old, past, past)
	s.cleanStaleScripts(24 * time.Hour)
	if _, err := os.Stat(old); err == nil {
		t.Error("stale script kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh script removed")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
