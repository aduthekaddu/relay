package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestDescribe(t *testing.T) {
	tests := map[string]string{
		"* * * * *":            "Every minute",
		"*/15 * * * *":         "Every 15 minutes",
		"*/1 * * * *":          "Every minute",
		"0 * * * *":            "Every hour",
		"30 * * * *":           "Every hour at :30",
		"0 */6 * * *":          "Every 6 hours",
		"15 */2 * * *":         "Every 2 hours at :15",
		"0 2 * * *":            "Every day at 02:00",
		"0 2 * * 1-5":          "Every weekday at 02:00",
		"0 2 * * MON-FRI":      "Every weekday at 02:00",
		"0 10 * * 0,6":         "Every weekend day at 10:00",
		"0 10 * * 6,7":         "Every weekend day at 10:00",
		"30 9 * * 1":           "Every Monday at 09:30",
		"0 9 * * 1,3,5":        "Every Monday, Wednesday and Friday at 09:00",
		"0 9,17 * * *":         "Every day at 09:00 and 17:00",
		"0 9 1 * *":            "Monthly on the 1st at 09:00",
		"0 9 1,15 * *":         "Monthly on the 1st and 15th at 09:00",
		"0 0 22 * *":           "Monthly on the 22nd at 00:00",
		"0 0 11,12,13 * *":     "Monthly on the 11th, 12th and 13th at 00:00",
		"0 8 25 12 *":          "Every year on December 25 at 08:00",
		"0 8 25 dec *":         "Every year on December 25 at 08:00",
		"@daily":               "Every day at 00:00",
		"@hourly":              "Every hour",
		"@weekly":              "Every Sunday at 00:00",
		"@monthly":             "Monthly on the 1st at 00:00",
		"@yearly":              "Every year on January 1 at 00:00",
		"@every 90m":           "Every 90 minutes",
		"@every 2h":            "Every 2 hours",
		"@every 24h":           "Every day",
		"0-30/5 2 * * *":       "Custom schedule (0-30/5 2 * * *)",
		"0 2 1 * 1":            "Custom schedule (0 2 1 * 1)",
		"not a cron":           "Custom schedule (not a cron)",
		"0,15,30,45 1,2 * * *": "Custom schedule (0,15,30,45 1,2 * * *)",
	}
	for expr, want := range tests {
		if got := Describe(expr); got != want {
			t.Errorf("Describe(%q) = %q, want %q", expr, got, want)
		}
	}
}

func TestParseCronAndNextRuns(t *testing.T) {
	for _, bad := range []string{"", "* * *", "61 * * * *", "@every 10s", "TZ=UTC 0 2 * * *", "@sometimes"} {
		if _, err := ParseCron(bad, ""); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := ParseCron("0 2 * * *", "Mars/Olympus"); err == nil {
		t.Error("bad timezone accepted")
	}
	// 02:00 Europe/Berlin on a winter weekday is 01:00 UTC.
	from := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC) // Monday
	next, err := NextRuns("0 2 * * 1-5", "Europe/Berlin", from, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{
		time.Date(2026, 1, 6, 1, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 7, 1, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 8, 1, 0, 0, 0, time.UTC),
	}
	for i := range want {
		if !next[i].Equal(want[i]) {
			t.Fatalf("next[%d] = %s, want %s", i, next[i].UTC(), want[i])
		}
	}
	// Friday → skips the weekend.
	next, _ = NextRuns("0 2 * * 1-5", "UTC", time.Date(2026, 1, 9, 3, 0, 0, 0, time.UTC), 1)
	if !next[0].Equal(time.Date(2026, 1, 12, 2, 0, 0, 0, time.UTC)) {
		t.Fatalf("weekend skip: %s", next[0])
	}
}

// ---------------------------------------------------------------------------
// fakes

type fakePty struct {
	mu       sync.Mutex
	created  []ptyclient.CreateSpec
	sessions map[string]*api.TerminalSession
	polls    map[string]int
	exitAt   int // polls before exiting (0 = never)
	exitCode int
	killed   []string
	fail     error
	snapshot string
	n        int
}

func newFakePty(exitAfter, code int) *fakePty {
	return &fakePty{sessions: map[string]*api.TerminalSession{}, polls: map[string]int{}, exitAt: exitAfter, exitCode: code, snapshot: "building…\nall 42 tests passed\n"}
}

func (f *fakePty) Create(_ context.Context, spec ptyclient.CreateSpec) (*api.TerminalSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	f.n++
	id := "t_fake" + string(rune('a'+f.n))
	f.created = append(f.created, spec)
	ts := &api.TerminalSession{ID: id, Kind: spec.Kind, Command: spec.Command, Cwd: spec.Cwd, Activity: api.ActivityWorking}
	f.sessions[id] = ts
	cp := *ts
	return &cp, nil
}

func (f *fakePty) Get(_ context.Context, id string) (*api.TerminalSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ts, ok := f.sessions[id]
	if !ok {
		return nil, ptyclient.ErrNotFound
	}
	f.polls[id]++
	if f.exitAt > 0 && f.polls[id] >= f.exitAt && ts.ExitCode == nil {
		c := f.exitCode
		ts.ExitCode, ts.Activity = &c, api.ActivityExited
	}
	cp := *ts
	return &cp, nil
}

func (f *fakePty) Kill(_ context.Context, id, sig string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, id+":"+sig)
	return nil
}

func (f *fakePty) Snapshot(_ context.Context, id string, lines int) (*api.TerminalSnapshot, error) {
	return &api.TerminalSnapshot{Text: f.snapshot}, nil
}

type fakeAgents struct{}

func (fakeAgents) List(context.Context) []api.AgentInfo { return nil }
func (fakeAgents) Command(_ context.Context, agent, prompt, model string) ([]string, map[string]string, error) {
	return []string{agent, prompt}, map[string]string{"X": "1"}, nil
}
func (fakeAgents) HeadlessCommand(_ context.Context, agent, prompt, model string) ([]string, error) {
	if agent == "missing" {
		return nil, errors.New("not installed")
	}
	return []string{agent, "-p", prompt}, nil
}

type fakeNotifier struct {
	mu   sync.Mutex
	reqs []api.NotifyRequest
}

func (f *fakeNotifier) Notify(_ context.Context, req api.NotifyRequest) (*api.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	return &api.Notification{ID: "n"}, nil
}

func (f *fakeNotifier) all() []api.NotifyRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]api.NotifyRequest(nil), f.reqs...)
}

type env struct {
	s     *Service
	d     *core.Deps
	pty   *fakePty
	notes *fakeNotifier
	dir   string
}

func newEnv(t *testing.T, pty *fakePty, opts ...Option) env {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dir := t.TempDir()
	notes := &fakeNotifier{}
	d := &core.Deps{Store: st, Bus: events.New(), Cfg: config.Defaults(), Agents: fakeAgents{}, Notifier: notes}
	d.Paths.Home = dir
	opts = append([]Option{WithPty(pty), WithPoll(5 * time.Millisecond)}, opts...)
	s, err := New(d, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return env{s, d, pty, notes, dir}
}

func ptr[T any](v T) *T { return &v }

func waitRun(t *testing.T, s *Service, scheduleID string) api.ScheduleRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runs, _ := s.Runs(context.Background(), scheduleID, 1)
		if len(runs) == 1 && runs[0].Status != "running" {
			return runs[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("run did not finish")
	return api.ScheduleRun{}
}

func TestValidation(t *testing.T) {
	e := newEnv(t, newFakePty(1, 0))
	ctx := context.Background()
	base := func() patch {
		return patch{Name: ptr("Nightly"), Cron: ptr("0 2 * * *"), Command: ptr([]string{"make", "test"})}
	}
	sc, err := e.s.Create(ctx, base())
	if err != nil {
		t.Fatal(err)
	}
	if !sc.Enabled || !sc.Notify || sc.Mode != "headless" || sc.Cwd != e.dir || sc.NextRun.IsZero() {
		t.Fatalf("defaults %+v", sc)
	}
	mut := []func(*patch){
		func(p *patch) { p.Name = ptr("") },
		func(p *patch) { p.Cron = ptr("every night") },
		func(p *patch) { p.Timezone = ptr("Nowhere/City") },
		func(p *patch) { p.Cwd = ptr("relative/dir") },
		func(p *patch) { p.Cwd = ptr("/definitely/not/here") },
		func(p *patch) { p.Command = ptr([]string{}) },
		func(p *patch) { p.Command = ptr([]string{""}) },
		func(p *patch) { p.Agent = ptr("claude") },                  // both agent and command
		func(p *patch) { p.Command = nil; p.Agent = ptr("claude") }, // agent without prompt
		func(p *patch) { p.Mode = ptr("interactive") },              // interactive command
		func(p *patch) { p.Mode = ptr("sometimes") },
		func(p *patch) { p.Command = ptr([]string{"echo", "a\x00b"}) },
	}
	for i, m := range mut {
		p := base()
		m(&p)
		if _, err := e.s.Create(ctx, p); err == nil {
			t.Errorf("invalid schedule %d accepted", i)
		}
	}
	// "~/" cwd expands.
	p := base()
	p.Cwd = ptr("~")
	if sc, err := e.s.Create(ctx, p); err != nil || sc.Cwd != e.dir {
		t.Fatalf("~ cwd: %v %v", sc, err)
	}
	// Switching to an agent run clears the command.
	up, err := e.s.Update(ctx, sc.ID, patch{Agent: ptr("claude"), Prompt: ptr("triage issues")})
	if err != nil || len(up.Command) != 0 || up.Agent != "claude" {
		t.Fatalf("switch to agent: %+v %v", up, err)
	}
	up, err = e.s.Update(ctx, sc.ID, patch{Enabled: ptr(false)})
	if err != nil || up.Enabled || !up.NextRun.IsZero() {
		t.Fatalf("disable: %+v %v", up, err)
	}
	if err := e.s.Delete(ctx, sc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Get(ctx, sc.ID); err == nil {
		t.Fatal("deleted schedule found")
	}
}

func TestRunSuccessAndFailure(t *testing.T) {
	for _, tc := range []struct {
		code   int
		status string
		title  string
		sev    string
	}{
		{0, "ok", "Nightly finished", "success"},
		{2, "failed", "Nightly failed (exit 2)", "danger"},
	} {
		e := newEnv(t, newFakePty(3, tc.code))
		ctx := context.Background()
		evs := e.d.Bus.Subscribe(16, func(ev api.Event) bool { return ev.Type == api.EvScheduleRun })
		sc, err := e.s.Create(ctx, patch{Name: ptr("Nightly"), Cron: ptr("@daily"), Agent: ptr("claude"), Prompt: ptr("fix the flaky test")})
		if err != nil {
			t.Fatal(err)
		}
		run, err := e.s.Run(ctx, sc.ID)
		if err != nil || run.Status != "running" || run.TerminalID == "" {
			t.Fatalf("run %+v %v", run, err)
		}
		spec := e.pty.created[0]
		if spec.Kind != api.KindTask || strings.Join(spec.Command, " ") != "claude -p fix the flaky test" || spec.Cwd != e.dir || spec.Meta["schedule"] != sc.ID {
			t.Fatalf("spec %+v", spec)
		}
		done := waitRun(t, e.s, sc.ID)
		if done.Status != tc.status || done.ExitCode == nil || *done.ExitCode != tc.code || !strings.Contains(done.Output, "42 tests passed") || done.FinishedAt.IsZero() {
			t.Fatalf("finished %+v", done)
		}
		e.s.wg.Wait()
		reqs := e.notes.all()
		if len(reqs) != 1 || reqs[0].Kind != "schedule" || reqs[0].Title != tc.title || reqs[0].Severity != tc.sev || reqs[0].SessionID != run.TerminalID || !strings.Contains(reqs[0].Body, "42 tests passed") {
			t.Fatalf("notify %+v", reqs)
		}
		// running + finished events
		for i, want := range []string{"running", tc.status} {
			select {
			case ev := <-evs.C:
				if ev.Data.(api.ScheduleRun).Status != want {
					t.Fatalf("event %d status %v", i, ev.Data)
				}
			case <-time.After(time.Second):
				t.Fatalf("missing event %d", i)
			}
		}
		evs.Close()
		got, _ := e.s.Get(ctx, sc.ID)
		if got.LastRun == nil || got.LastRun.ID != run.ID {
			t.Fatalf("lastRun %+v", got.LastRun)
		}
	}
}

func TestRunOverlapTimeoutAndErrors(t *testing.T) {
	// Never exits: second run is skipped; the first times out and is killed.
	e := newEnv(t, newFakePty(0, 0), WithMaxRuntime(60*time.Millisecond))
	ctx := context.Background()
	sc, _ := e.s.Create(ctx, patch{Name: ptr("Long"), Cron: ptr("@hourly"), Command: ptr([]string{"sleep", "999"}), Notify: ptr(false)})
	first, err := e.s.Run(ctx, sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.s.Run(ctx, sc.ID)
	if err != nil || second.Status != "skipped" || !strings.Contains(second.Output, first.ID) {
		t.Fatalf("overlap: %+v %v", second, err)
	}
	e.s.wg.Wait()
	runs, _ := e.s.Runs(ctx, sc.ID, 10)
	var timedOut api.ScheduleRun
	for _, r := range runs {
		if r.ID == first.ID {
			timedOut = r
		}
	}
	if timedOut.Status != "failed" || !strings.Contains(timedOut.Output, "timed out") {
		t.Fatalf("timeout run %+v", timedOut)
	}
	if len(e.pty.killed) != 1 || e.pty.killed[0] != first.TerminalID+":TERM" {
		t.Fatalf("killed %v", e.pty.killed)
	}
	if len(e.notes.all()) != 0 {
		t.Fatal("notified although notify=false")
	}
	// After the guard is released a new run starts.
	if r, _ := e.s.Run(ctx, sc.ID); r.Status != "running" {
		t.Fatalf("guard not released: %+v", r)
	}

	// ptyd unavailable → failed run + notification.
	bad := newFakePty(1, 0)
	bad.fail = ptyclient.ErrUnavailable
	e2 := newEnv(t, bad)
	sc2, _ := e2.s.Create(ctx, patch{Name: ptr("Broken"), Cron: ptr("@daily"), Command: ptr([]string{"true"})})
	r, err := e2.s.Run(ctx, sc2.ID)
	if err != nil || r.Status != "failed" || !strings.Contains(r.Output, "could not start") {
		t.Fatalf("unavailable: %+v %v", r, err)
	}
	if n := e2.notes.all(); len(n) != 1 || n[0].Severity != "danger" {
		t.Fatalf("failure not notified: %+v", n)
	}
	// Agent that cannot run headless.
	sc3, _ := e2.s.Create(ctx, patch{Name: ptr("Agentless"), Cron: ptr("@daily"), Agent: ptr("missing"), Prompt: ptr("x")})
	bad.fail = nil
	if r, _ := e2.s.Run(ctx, sc3.ID); r.Status != "failed" || !strings.Contains(r.Output, "not installed") {
		t.Fatalf("missing agent: %+v", r)
	}
	if _, err := e2.s.Run(ctx, "sc_nope"); err == nil {
		t.Fatal("run of unknown schedule")
	}
}

func TestInteractiveRun(t *testing.T) {
	e := newEnv(t, newFakePty(0, 0))
	ctx := context.Background()
	sc, _ := e.s.Create(ctx, patch{Name: ptr("Morning"), Cron: ptr("0 8 * * 1-5"), Agent: ptr("codex"), Prompt: ptr("plan my day"), Mode: ptr("interactive")})
	r, err := e.s.Run(ctx, sc.ID)
	if err != nil || r.Status != "ok" || r.TerminalID == "" {
		t.Fatalf("interactive %+v %v", r, err)
	}
	spec := e.pty.created[0]
	if spec.Kind != api.KindAgent || spec.ExtraEnv["X"] != "1" || spec.Record == nil || !*spec.Record {
		t.Fatalf("interactive spec %+v", spec)
	}
}

func TestFireDue(t *testing.T) {
	now := time.Date(2026, 3, 2, 1, 59, 30, 0, time.UTC)
	e := newEnv(t, newFakePty(1, 0), WithClock(func() time.Time { return now }))
	ctx := context.Background()
	due, _ := e.s.Create(ctx, patch{Name: ptr("Due"), Cron: ptr("0 2 * * *"), Timezone: ptr("UTC"), Command: ptr([]string{"true"})})
	later, _ := e.s.Create(ctx, patch{Name: ptr("Later"), Cron: ptr("0 3 * * *"), Timezone: ptr("UTC"), Command: ptr([]string{"true"})})
	off, _ := e.s.Create(ctx, patch{Name: ptr("Off"), Cron: ptr("0 2 * * *"), Timezone: ptr("UTC"), Command: ptr([]string{"true"}), Enabled: ptr(false)})

	next, err := e.s.nextWake(ctx, now)
	if err != nil || !next.Equal(time.Date(2026, 3, 2, 2, 0, 0, 0, time.UTC)) {
		t.Fatalf("nextWake %s %v", next, err)
	}
	e.s.fireDue(ctx, now, now.Add(time.Minute))
	e.s.wg.Wait()
	for id, want := range map[string]int{due.ID: 1, later.ID: 0, off.ID: 0} {
		if runs, _ := e.s.Runs(ctx, id, 10); len(runs) != want {
			t.Errorf("schedule %s: %d runs, want %d", id, len(runs), want)
		}
	}
}

func TestResumeRunning(t *testing.T) {
	pty := newFakePty(2, 0)
	e := newEnv(t, pty)
	ctx := context.Background()
	sc, _ := e.s.Create(ctx, patch{Name: ptr("Survivor"), Cron: ptr("@daily"), Command: ptr([]string{"true"})})
	term, _ := pty.Create(ctx, ptyclient.CreateSpec{})
	orphan := api.ScheduleRun{ID: "sr_orphan", ScheduleID: sc.ID, StartedAt: time.Now(), Status: "running", TerminalID: term.ID}
	lost := api.ScheduleRun{ID: "sr_lost", ScheduleID: sc.ID, StartedAt: time.Now().Add(-time.Second), Status: "running", TerminalID: "t_gone"}
	for _, r := range []api.ScheduleRun{orphan, lost} {
		if err := e.s.saveRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	e.s.resumeRunning(ctx)
	e.s.wg.Wait()
	e.s.resumeRunning(ctx) // the "lost" one was busy-guarded; resume again
	e.s.wg.Wait()
	runs, _ := e.s.Runs(ctx, sc.ID, 10)
	got := map[string]string{}
	for _, r := range runs {
		got[r.ID] = r.Status
	}
	if got["sr_orphan"] != "ok" || got["sr_lost"] != "failed" {
		t.Fatalf("resumed %v", got)
	}
}

type auth struct{}

func (auth) Identify(*http.Request) *server.Principal {
	return &server.Principal{User: "owner", Method: "token"}
}

func TestHTTP(t *testing.T) {
	e := newEnv(t, newFakePty(1, 0))
	rt := server.NewRouter(auth{}, func() []string { return nil })
	e.s.Routes(rt)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	rec := call("GET", "/api/v1/schedules/describe?cron=0+2+*+*+1-5&timezone=UTC", "")
	var pv api.CronPreview
	_ = json.Unmarshal(rec.Body.Bytes(), &pv)
	if !pv.Valid || pv.Description != "Every weekday at 02:00" || len(pv.Next) != 3 {
		t.Fatalf("describe %s", rec.Body)
	}
	rec = call("GET", "/api/v1/schedules/describe?cron=nope", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &pv)
	if pv.Valid || pv.Error == "" {
		t.Fatalf("describe invalid %s", rec.Body)
	}

	rec = call("POST", "/api/v1/schedules", `{"name":"Lint","cron":"*/30 * * * *","command":["make","lint"]}`)
	var sc api.Schedule
	_ = json.Unmarshal(rec.Body.Bytes(), &sc)
	if rec.Code != 200 || sc.ID == "" {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	if rec := call("POST", "/api/v1/schedules", `{"name":"Bad","cron":"x","command":["a"]}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), `"field":"cron"`) {
		t.Fatalf("invalid create %d %s", rec.Code, rec.Body)
	}
	// Echoing the full object back in a PATCH is fine.
	full, _ := json.Marshal(sc)
	if rec := call("PATCH", "/api/v1/schedules/"+sc.ID, string(full)); rec.Code != 200 {
		t.Fatalf("patch echo %d %s", rec.Code, rec.Body)
	}
	rec = call("POST", "/api/v1/schedules/"+sc.ID+"/run", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"running"`) {
		t.Fatalf("run %d %s", rec.Code, rec.Body)
	}
	waitRun(t, e.s, sc.ID)
	rec = call("GET", "/api/v1/schedules/"+sc.ID+"/runs?limit=5", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("runs %s", rec.Body)
	}
	if rec := call("GET", "/api/v1/schedules", ""); !strings.Contains(rec.Body.String(), `"lastRun"`) {
		t.Fatalf("list %s", rec.Body)
	}
	if rec := call("DELETE", "/api/v1/schedules/"+sc.ID, ""); rec.Code != 204 {
		t.Fatalf("delete %d", rec.Code)
	}
	if rec := call("GET", "/api/v1/schedules/"+sc.ID+"/runs", ""); rec.Code != 404 {
		t.Fatalf("runs of deleted %d", rec.Code)
	}
}
