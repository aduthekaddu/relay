package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
)

// fakePty records Create/SetAttention calls and serves List.
type fakePty struct {
	mu        sync.Mutex
	terms     []api.TerminalSession
	created   []ptyclient.CreateSpec
	attention map[string]*api.Attention
}

func (f *fakePty) List(context.Context) ([]api.TerminalSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]api.TerminalSession(nil), f.terms...), nil
}

func (f *fakePty) Create(_ context.Context, spec ptyclient.CreateSpec) (*api.TerminalSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, spec)
	t := api.TerminalSession{ID: "t" + itoa64(int64(len(f.terms)+1)), Name: spec.Name, Kind: spec.Kind, Agent: spec.Agent,
		AgentSessionID: spec.AgentSessionID, Command: spec.Command, Cwd: spec.Cwd, Workspace: spec.Workspace,
		Activity: api.ActivityWorking, CreatedAt: time.Now().UTC()}
	f.terms = append(f.terms, t)
	return &t, nil
}

func (f *fakePty) SetAttention(_ context.Context, id string, a *api.Attention) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.attention == nil {
		f.attention = map[string]*api.Attention{}
	}
	f.attention[id] = a
	return nil
}

type fakeNotifier struct {
	mu   sync.Mutex
	reqs []api.NotifyRequest
}

func (n *fakeNotifier) Notify(_ context.Context, r api.NotifyRequest) (*api.Notification, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reqs = append(n.reqs, r)
	return &api.Notification{}, nil
}

// fakeWorkspaces implements core.WorkspaceService + worktree creation.
type fakeWorkspaces struct {
	root    string
	created []string
}

func (w *fakeWorkspaces) List(context.Context) ([]api.Workspace, error) { return nil, nil }
func (w *fakeWorkspaces) RootOf(p string) string {
	if w.root != "" && within(p, w.root) {
		return w.root
	}
	return ""
}

func (w *fakeWorkspaces) CreateWorktree(_ context.Context, repo, branch, base string) (*api.Worktree, error) {
	dir := filepath.Join(repo, ".worktrees", branch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	w.created = append(w.created, branch+"@"+base)
	return &api.Worktree{Path: dir, Branch: branch}, nil
}

type testSvc struct {
	*Service
	pty    *fakePty
	notify *fakeNotifier
	ws     *fakeWorkspaces
	e      *env
	mux    *server.Router
}

// newTestService builds a Service on an in-memory store and a temp HOME,
// with every agent "installed" at /fake/bin/<name>.
func newTestService(t *testing.T) *testSvc {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	home := t.TempDir()
	ws := &fakeWorkspaces{root: filepath.Join(home, "repo")}
	nt := &fakeNotifier{}
	d := &core.Deps{Cfg: &config.Config{}, Paths: config.Paths{Home: home, CacheDir: t.TempDir()}, Store: st,
		Bus: events.New(), Workspaces: ws, Notifier: nt, Search: core.NewSearchRegistry()}
	s, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	fp := &fakePty{}
	s.pty = fp
	s.relayPath = "/opt/relay/bin/relay"
	s.det.lookPath = func(name string) (string, error) { return "/fake/bin/" + name, nil }
	s.det.run = func(context.Context, string, ...string) ([]byte, error) { return []byte("tool 1.2.3\n"), nil }
	rt := server.NewRouter(allowAll{}, func() []string { return nil })
	s.Routes(rt)
	return &testSvc{Service: s, pty: fp, notify: nt, ws: ws, e: s.env(), mux: rt}
}

// allowAll authenticates every request as the local owner.
type allowAll struct{}

func (allowAll) Identify(r *http.Request) *server.Principal {
	return &server.Principal{User: "owner", Method: "local"}
}

func (ts *testSvc) do(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ts.mux.ServeHTTP(rec, req)
	if out != nil && rec.Code < 300 && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec.Code
}

// indexOnce runs one indexing pass synchronously.
func (ts *testSvc) indexOnce(t *testing.T) {
	t.Helper()
	ts.idx.pass(context.Background(), false)
}

func claudeFixture(t *testing.T, home, id, cwd, started string, prompts ...string) string {
	t.Helper()
	var lines []string
	for i, p := range prompts {
		u := "u" + itoa64(int64(i))
		a := "a" + itoa64(int64(i))
		lines = append(lines,
			`{"type":"user","uuid":"`+u+`","sessionId":"`+id+`","timestamp":"`+started+`","cwd":"`+cwd+`","message":{"role":"user","content":"`+p+`"}}`,
			`{"type":"assistant","uuid":"`+a+`","requestId":"r`+u+`","timestamp":"`+started+`","message":{"id":"m`+u+`","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"answer about `+p+`"}],"usage":{"input_tokens":1000,"output_tokens":100,"cache_read_input_tokens":10000,"cache_creation_input_tokens":0}}}`)
	}
	path := filepath.Join(home, ".claude", "projects", "-x", id+".jsonl")
	writeFile(t, path, jl(lines...))
	return path
}

func TestServiceIndexListAndPatch(t *testing.T) {
	ts := newTestService(t)
	home := ts.home
	repo := filepath.Join(home, "repo")
	os.MkdirAll(repo, 0o755)
	claudeFixture(t, home, "aaaa", repo, "2026-01-02T03:00:00Z", "first task about widgets")
	claudeFixture(t, home, "bbbb", "/tmp/else", "2026-01-03T03:00:00Z", "second task about gadgets", "follow up")
	ts.indexOnce(t)

	var page api.Page[api.AgentSession]
	if code := ts.do(t, "GET", "/api/v1/agents/sessions", nil, &page); code != 200 {
		t.Fatalf("list: %d", code)
	}
	if len(page.Items) != 2 || page.Items[0].ID != "claude:bbbb" {
		t.Fatalf("items = %+v", page.Items)
	}
	s0 := page.Items[0]
	if s0.Messages != 4 || s0.Title != "second task about gadgets" || s0.Tokens == nil || s0.Tokens.Input != 2000 {
		t.Errorf("session = %+v tokens=%+v", s0, s0.Tokens)
	}
	// 2 calls × (1000×3 + 100×15 + 10000×0.3) / 1e6
	if s0.CostUSD == nil || abs(*s0.CostUSD-0.015) > 1e-9 {
		t.Errorf("cost = %v", s0.CostUSD)
	}
	if page.Items[1].Workspace != repo {
		t.Errorf("workspace = %q, want %q", page.Items[1].Workspace, repo)
	}

	// Paging with a cursor.
	if code := ts.do(t, "GET", "/api/v1/agents/sessions?limit=1", nil, &page); code != 200 || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("page1: %d %+v", code, page)
	}
	var page2 api.Page[api.AgentSession]
	ts.do(t, "GET", "/api/v1/agents/sessions?limit=1&cursor="+url.QueryEscape(page.NextCursor), nil, &page2)
	if len(page2.Items) != 1 || page2.Items[0].ID != "claude:aaaa" || page2.NextCursor != "" {
		t.Fatalf("page2 = %+v", page2)
	}
	if code := ts.do(t, "GET", "/api/v1/agents/sessions?cursor=bm90LWEtY3Vyc29y", nil, nil); code != 400 {
		t.Errorf("bad cursor: %d", code)
	}

	// Filters.
	ts.do(t, "GET", "/api/v1/agents/sessions?q=widgets", nil, &page)
	if len(page.Items) != 1 || page.Items[0].ID != "claude:aaaa" {
		t.Errorf("q filter = %+v", page.Items)
	}
	ts.do(t, "GET", "/api/v1/agents/sessions?cwd="+url.QueryEscape(repo), nil, &page)
	if len(page.Items) != 1 {
		t.Errorf("cwd filter = %+v", page.Items)
	}

	// Patch: rename, pin, archive.
	title, yes := "Renamed", true
	var as api.AgentSession
	if code := ts.do(t, "PATCH", "/api/v1/agents/sessions/claude:aaaa", api.UpdateAgentSessionRequest{Title: &title, Pinned: &yes}, &as); code != 200 {
		t.Fatalf("patch: %d", code)
	}
	if as.Title != "Renamed" || !as.Pinned {
		t.Errorf("patched = %+v", as)
	}
	ts.do(t, "PATCH", "/api/v1/agents/sessions/claude:aaaa", api.UpdateAgentSessionRequest{Archived: &yes}, &as)
	ts.do(t, "GET", "/api/v1/agents/sessions", nil, &page)
	if len(page.Items) != 1 {
		t.Errorf("archived sessions must be hidden by default: %+v", page.Items)
	}
	ts.do(t, "GET", "/api/v1/agents/sessions?archived=1", nil, &page)
	if len(page.Items) != 1 || page.Items[0].ID != "claude:aaaa" {
		t.Errorf("archived=1: %+v", page.Items)
	}
	if code := ts.do(t, "GET", "/api/v1/agents/sessions/claude:nope", nil, nil); code != 404 {
		t.Errorf("missing session: %d", code)
	}

	// Agents list carries counts and detection.
	var infos []api.AgentInfo
	ts.do(t, "GET", "/api/v1/agents", nil, &infos)
	if len(infos) != 14 || infos[0].ID != "claude" || infos[0].Sessions != 2 || !infos[0].Installed || infos[0].Version != "1.2.3" {
		t.Errorf("agents = %+v", infos[0])
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func TestTranscriptPaging(t *testing.T) {
	ts := newTestService(t)
	var prompts []string
	for i := 0; i < 5; i++ {
		prompts = append(prompts, "prompt "+itoa64(int64(i)))
	}
	claudeFixture(t, ts.home, "cccc", "/tmp", "2026-01-02T03:00:00Z", prompts...)
	ts.indexOnce(t)
	var tr api.Transcript
	if code := ts.do(t, "GET", "/api/v1/agents/sessions/claude:cccc/transcript?limit=4", nil, &tr); code != 200 {
		t.Fatalf("transcript: %d", code)
	}
	if len(tr.Messages) != 4 || !tr.HasMore || tr.Messages[0].ID != "u3" || tr.Messages[3].ID != "a4" {
		t.Fatalf("newest page = %v hasMore=%v", ids(tr.Messages), tr.HasMore)
	}
	ts.do(t, "GET", "/api/v1/agents/sessions/claude:cccc/transcript?limit=4&before=u3", nil, &tr)
	if len(tr.Messages) != 4 || !tr.HasMore || tr.Messages[0].ID != "u1" || tr.Messages[3].ID != "a2" {
		t.Fatalf("older page = %v hasMore=%v", ids(tr.Messages), tr.HasMore)
	}
	ts.do(t, "GET", "/api/v1/agents/sessions/claude:cccc/transcript?limit=4&before=u1", nil, &tr)
	if len(tr.Messages) != 2 || tr.HasMore {
		t.Fatalf("oldest page = %v hasMore=%v", ids(tr.Messages), tr.HasMore)
	}
}

func ids(ms []api.AgentMessage) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func TestIncrementalIndexAndForget(t *testing.T) {
	ts := newTestService(t)
	path := claudeFixture(t, ts.home, "dddd", "/tmp", "2026-01-02T03:00:00Z", "one")
	ts.indexOnce(t)
	row, _ := ts.ix.session(context.Background(), "claude:dddd")
	if row == nil || row.Messages != 2 {
		t.Fatalf("row = %+v", row)
	}
	// Append a turn: only the new bytes are parsed, counts add up and the
	// streamed usage of the new turn is stored once.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"user","uuid":"u9","timestamp":"2026-01-02T04:00:00Z","message":{"role":"user","content":"two"}}` + "\n")
	f.Close()
	ts.indexOnce(t)
	row, _ = ts.ix.session(context.Background(), "claude:dddd")
	if row.Messages != 3 || row.Title != "one" {
		t.Fatalf("after append: %+v", row)
	}
	var n int
	ts.ix.db.QueryRow(`SELECT count(*) FROM agent_usage WHERE session='claude:dddd'`).Scan(&n)
	if n != 1 {
		t.Errorf("usage rows = %d", n)
	}
	os.Remove(path)
	ts.indexOnce(t)
	if row, _ = ts.ix.session(context.Background(), "claude:dddd"); row != nil {
		t.Error("session of a deleted transcript must be forgotten")
	}
}

func TestSearchFTS(t *testing.T) {
	ts := newTestService(t)
	if !ts.ix.fts {
		t.Log("SQLite without FTS5: exercising the LIKE fallback")
	}
	claudeFixture(t, ts.home, "eeee", "/tmp/p", "2026-01-02T03:00:00Z", "the flux capacitor overheats", "unrelated")
	claudeFixture(t, ts.home, "ffff", "/tmp/q", "2026-01-02T03:00:00Z", "bake bread")
	ts.indexOnce(t)
	var hits []api.SearchHit
	if code := ts.do(t, "GET", "/api/v1/agents/search?q=capacit", nil, &hits); code != 200 {
		t.Fatalf("search: %d", code)
	}
	if len(hits) == 0 || hits[0].SessionID != "claude:eeee" || !strings.Contains(hits[0].Snippet, "«") {
		t.Fatalf("hits = %+v", hits)
	}
	// FTS syntax in user input is neutralised.
	for _, q := range []string{`"`, `a OR`, `NEAR(`, `*`, `x:y`, `-bread`} {
		if code := ts.do(t, "GET", "/api/v1/agents/search?q="+url.QueryEscape(q), nil, &hits); code != 200 {
			t.Errorf("q=%q: %d", q, code)
		}
	}
	ts.do(t, "GET", "/api/v1/agents/search?q=bread&agent=codex", nil, &hits)
	if len(hits) != 0 {
		t.Errorf("agent filter: %+v", hits)
	}
	// Search providers.
	res := historyProvider{ts.Service}.Search(context.Background(), "bread", 5)
	if len(res) != 2 || res[0].Scope != "history" || !strings.HasPrefix(res[0].Link, "/agents/s/claude:ffff?m=") {
		t.Errorf("history provider = %+v", res)
	}
	res = agentsProvider{ts.Service}.Search(context.Background(), "flux", 5)
	if len(res) != 1 || res[0].ID != "claude:eeee" {
		t.Errorf("agents provider = %+v", res)
	}
}

func TestLikeFallback(t *testing.T) {
	ts := newTestService(t)
	claudeFixture(t, ts.home, "gggg", "/tmp", "2026-01-02T03:00:00Z", "Quantum widget")
	ts.indexOnce(t)
	ts.ix.fts = false
	hits, err := ts.searchText(context.Background(), "widget", "", 10)
	if err != nil || len(hits) == 0 || !strings.Contains(hits[0].Snippet, "«widget»") {
		t.Fatalf("like hits = %+v err=%v", hits, err)
	}
}

func TestFtsQueryAndSnippet(t *testing.T) {
	cases := map[string]string{
		"hello world":   `"hello"* "world"*`,
		`a "quoted" OR`: `"a"* "quoted"* "OR"*`,
		"  ":            "",
		"naïve café":    `"naïve"* "café"*`,
	}
	for in, want := range cases {
		if got := ftsQuery(in); got != want {
			t.Errorf("ftsQuery(%q) = %q, want %q", in, got, want)
		}
	}
	if got := likeSnippet("abc Widget def", "widget"); got != "abc «Widget» def" {
		t.Errorf("snippet = %q", got)
	}
}

func TestLaunchAndResume(t *testing.T) {
	ts := newTestService(t)
	repo := filepath.Join(ts.home, "repo")
	os.MkdirAll(repo, 0o755)

	var term api.TerminalSession
	req := api.LaunchAgentRequest{Agent: "claude", Cwd: "~/repo", Prompt: "--dangerously-skip-permissions fix it", Model: "opus"}
	if code := ts.do(t, "POST", "/api/v1/agents/launch", req, &term); code != 200 {
		t.Fatalf("launch: %d", code)
	}
	spec := ts.pty.created[0]
	argv := spec.Command
	if argv[0] != "/fake/bin/claude" || argv[1] != "--session-id" || spec.AgentSessionID == "" || argv[2] != spec.AgentSessionID {
		t.Fatalf("argv = %q", argv)
	}
	if last := argv[len(argv)-1]; last != " --dangerously-skip-permissions fix it" {
		t.Errorf("prompt must be one argv element that cannot parse as a flag: %q", last)
	}
	if spec.Kind != api.KindAgent || spec.Agent != "claude" || spec.Cwd != repo || spec.Workspace != repo {
		t.Errorf("spec = %+v", spec)
	}
	if !strings.HasPrefix(spec.Name, "claude · ") {
		t.Errorf("name = %q", spec.Name)
	}

	// Worktree launch.
	req = api.LaunchAgentRequest{Agent: "codex", Cwd: repo, Worktree: &api.WorktreeOption{Branch: "feat-x", Base: "main"}}
	if code := ts.do(t, "POST", "/api/v1/agents/launch", req, &term); code != 200 {
		t.Fatalf("worktree launch: %d", code)
	}
	if got := ts.pty.created[1].Cwd; got != filepath.Join(repo, ".worktrees", "feat-x") || ts.ws.created[0] != "feat-x@main" {
		t.Errorf("worktree cwd = %q created=%v", got, ts.ws.created)
	}

	// Validation.
	for _, bad := range []api.LaunchAgentRequest{
		{Agent: "nope", Cwd: repo},
		{Agent: "claude", Cwd: "relative/dir"},
		{Agent: "claude", Cwd: "/does/not/exist"},
		{Agent: "claude", Cwd: repo, Model: "--evil"},
		{Agent: "claude", Cwd: "/tmp", Worktree: &api.WorktreeOption{Branch: "x"}},
	} {
		if code := ts.do(t, "POST", "/api/v1/agents/launch", bad, nil); code < 400 {
			t.Errorf("launch %+v: %d, want an error", bad, code)
		}
	}

	// Resume and fork an indexed session.
	claudeFixture(t, ts.home, "hhhh", repo, "2026-01-02T03:00:00Z", "resumable work")
	ts.indexOnce(t)
	if code := ts.do(t, "POST", "/api/v1/agents/sessions/claude:hhhh/resume", api.ResumeAgentRequest{}, &term); code != 200 {
		t.Fatalf("resume: %d", code)
	}
	last := ts.pty.created[len(ts.pty.created)-1]
	if strings.Join(last.Command, " ") != "/fake/bin/claude --resume hhhh" || last.AgentSessionID != "hhhh" || last.Cwd != repo {
		t.Errorf("resume spec = %+v", last)
	}
	// Resuming a live session returns its terminal instead of a second process.
	n := len(ts.pty.created)
	ts.live.repair()
	var again api.TerminalSession
	ts.do(t, "POST", "/api/v1/agents/sessions/claude:hhhh/resume", nil, &again)
	if len(ts.pty.created) != n || again.ID != term.ID {
		t.Errorf("resume of a live session started a new terminal (%d -> %d)", n, len(ts.pty.created))
	}
	ts.do(t, "POST", "/api/v1/agents/sessions/claude:hhhh/resume", api.ResumeAgentRequest{Fork: true}, &term)
	last = ts.pty.created[len(ts.pty.created)-1]
	if strings.Join(last.Command, " ") != "/fake/bin/claude --resume hhhh --fork-session" || last.AgentSessionID != "" {
		t.Errorf("fork spec = %+v", last)
	}
	// Aider cannot resume.
	ts.ix.db.Exec(`INSERT INTO agent_sessions(id, agent, native_id, title, updated) VALUES('aider:x','aider','x','t',1)`)
	if code := ts.do(t, "POST", "/api/v1/agents/sessions/aider:x/resume", nil, nil); code != 409 {
		t.Errorf("aider resume: %d, want 409", code)
	}
}

func TestLivePairingSnapshot(t *testing.T) {
	ts := newTestService(t)
	created := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	claudeFixture(t, ts.home, "iiii", "/tmp/w", "2026-01-02T03:00:30Z", "heuristic pairing")
	ts.indexOnce(t)
	ts.pty.terms = []api.TerminalSession{
		{ID: "T1", Agent: "claude", Kind: api.KindAgent, Cwd: "/tmp/w", CreatedAt: created, Activity: api.ActivityWaiting},
		{ID: "T2", Agent: "codex", Kind: api.KindAgent, Cwd: "/tmp/w", CreatedAt: created, Activity: api.ActivityIdle},
	}
	ts.live.repair()
	var page api.Page[api.AgentSession]
	ts.do(t, "GET", "/api/v1/agents/sessions?status=live", nil, &page)
	if len(page.Items) != 2 {
		t.Fatalf("live = %+v", page.Items)
	}
	byID := map[string]api.AgentSession{}
	for _, it := range page.Items {
		byID[it.ID] = it
	}
	if s := byID["claude:iiii"]; s.Status != api.AgentLive || s.TerminalID != "T1" || s.Activity != api.ActivityWaiting {
		t.Errorf("paired = %+v", s)
	}
	if s := byID["codex:term-T2"]; s.Status != api.AgentLive || s.TerminalID != "T2" {
		t.Errorf("pending live = %+v", s)
	}
}

func TestHookEndpoint(t *testing.T) {
	ts := newTestService(t)
	ts.pty.terms = []api.TerminalSession{{ID: "T1", Name: "claude · x", Agent: "claude", Kind: api.KindAgent, Cwd: "/tmp", CreatedAt: time.Now()}}
	var res api.AgentHookResult
	body := api.AgentHookRequest{Agent: "claude", Event: "notification", SessionID: "T1",
		Payload: json.RawMessage(`{"session_id":"s-123","cwd":"/tmp","hook_event_name":"Notification","message":"Claude needs your permission to use Bash"}`)}
	if code := ts.do(t, "POST", "/api/v1/agents/hook", body, &res); code != 200 {
		t.Fatalf("hook: %d", code)
	}
	if res.Action != "attention" || res.TerminalID != "T1" {
		t.Fatalf("res = %+v", res)
	}
	if a := ts.pty.attention["T1"]; a == nil || a.Reason != "hook" || !strings.Contains(a.Message, "permission") {
		t.Errorf("attention = %+v", a)
	}
	if n := ts.notify.reqs; len(n) != 1 || n[0].Kind != "attention" || n[0].Link != "/terminal/T1" || n[0].Agent != "claude" {
		t.Errorf("notify = %+v", n)
	}
	// The hook taught pairing the native id.
	if ts.live.hints["T1"] != "s-123" {
		t.Errorf("hint = %q", ts.live.hints["T1"])
	}
	body.Event, body.Payload = "stop", json.RawMessage(`{"session_id":"s-123","hook_event_name":"Stop"}`)
	ts.do(t, "POST", "/api/v1/agents/hook", body, &res)
	if res.Action != "done" || ts.pty.attention["T1"] != nil {
		t.Errorf("stop must clear attention: %+v %+v", res, ts.pty.attention["T1"])
	}
	if n := ts.notify.reqs; len(n) != 2 || n[1].Kind != "done" {
		t.Errorf("notify = %+v", n)
	}
	// Unknown events are ignored; unknown agents are 404.
	body.Payload = json.RawMessage(`{"hook_event_name":"PreToolUse"}`)
	ts.do(t, "POST", "/api/v1/agents/hook", body, &res)
	if res.Action != "ignored" {
		t.Errorf("res = %+v", res)
	}
	if code := ts.do(t, "POST", "/api/v1/agents/hook", api.AgentHookRequest{Agent: "zzz", Event: "x"}, nil); code != 404 {
		t.Errorf("unknown agent: %d", code)
	}
	// "done" outside a Relay terminal is not worth a notification.
	before := len(ts.notify.reqs)
	res = api.AgentHookResult{}
	ts.do(t, "POST", "/api/v1/agents/hook", api.AgentHookRequest{Agent: "codex", Event: "notify",
		Payload: json.RawMessage(`{"type":"agent-turn-complete","thread-id":"z","cwd":"/nowhere"}`)}, &res)
	if res.Action != "done" || res.TerminalID != "" || len(ts.notify.reqs) != before {
		t.Errorf("outside relay: %+v notifications %d -> %d", res, before, len(ts.notify.reqs))
	}
}

func TestHookInstallEndpoints(t *testing.T) {
	ts := newTestService(t)
	sub := ts.d.Bus.Subscribe(16, func(ev api.Event) bool { return ev.Type == busAudit })
	defer sub.Close()
	var st api.HookStatus
	if code := ts.do(t, "POST", "/api/v1/agents/claude/hooks", nil, &st); code != 200 || !st.Installed {
		t.Fatalf("install: %d %+v", code, st)
	}
	if !strings.HasPrefix(st.Path, ts.home) {
		t.Fatalf("hooks must be written below HOME (%s): %s", ts.home, st.Path)
	}
	select {
	case ev := <-sub.C:
		if e, ok := ev.Data.(api.AuditEntry); !ok || e.Event != "agent.hooks.install" || e.Actor != "local" {
			t.Errorf("audit = %+v", ev.Data)
		}
	case <-time.After(time.Second):
		t.Error("no audit event")
	}
	if code := ts.do(t, "DELETE", "/api/v1/agents/claude/hooks", nil, &st); code != 200 || st.Installed {
		t.Fatalf("remove: %d %+v", code, st)
	}
	if code := ts.do(t, "POST", "/api/v1/agents/aider/hooks", nil, nil); code != 409 {
		t.Errorf("aider hooks: %d", code)
	}
}

func TestUsageAndQuotaEndpoints(t *testing.T) {
	ts := newTestService(t)
	now := time.Now().UTC().Format(time.RFC3339)
	claudeFixture(t, ts.home, "jjjj", "/tmp", now, "usage today")
	ts.indexOnce(t)
	var sum api.UsageSummary
	if code := ts.do(t, "GET", "/api/v1/agents/usage?range=today", nil, &sum); code != 200 {
		t.Fatalf("usage: %d", code)
	}
	if sum.Totals.Sessions != 1 || len(sum.Daily) != 1 || abs(sum.Totals.CostUSD-0.0075) > 1e-9 || sum.ByAgent[0].Label != "Claude Code" {
		t.Errorf("usage = %+v", sum)
	}
	if code := ts.do(t, "GET", "/api/v1/agents/usage?range=forever", nil, nil); code != 400 {
		t.Errorf("bad range: %d", code)
	}
	var qs []api.Quota
	if code := ts.do(t, "GET", "/api/v1/agents/quotas", nil, &qs); code != 200 || len(qs) != 0 {
		t.Errorf("quotas without data = %d %+v", code, qs)
	}
	if code := ts.do(t, "POST", "/api/v1/agents/reindex", nil, nil); code != 202 {
		t.Errorf("reindex: %d", code)
	}
}

func TestPtyUnavailable(t *testing.T) {
	ts := newTestService(t)
	ts.Service.pty = nil
	code := ts.do(t, "POST", "/api/v1/agents/launch", api.LaunchAgentRequest{Agent: "claude", Cwd: ts.home}, nil)
	if code != 503 {
		t.Errorf("launch without ptyd: %d", code)
	}
	var he interface{ Error() string }
	if he = ptyErr(ptyclient.ErrUnavailable); !strings.Contains(he.Error(), "unavailable") {
		t.Errorf("ptyErr = %v", he)
	}
}
