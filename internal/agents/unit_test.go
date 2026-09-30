package agents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

func TestNormalizeModel(t *testing.T) {
	cases := map[string]string{
		"claude-sonnet-4-5-20250929":          "claude-sonnet-4-5",
		"anthropic/claude-sonnet-4.5[1m]":     "claude-sonnet-4-5",
		"claude-opus-4-1@20250805":            "claude-opus-4-1",
		"gpt-5.3-codex":                       "gpt-5-3-codex",
		"models/gemini-2.5-pro-preview-06-05": "gemini-2-5-pro",
		"GROK-4-latest":                       "grok-4",
		"claude-3-7-sonnet-thinking":          "claude-3-7-sonnet",
		"":                                    "",
	}
	for in, want := range cases {
		if got := normalizeModel(in); got != want {
			t.Errorf("normalizeModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPriceLookupAndCost(t *testing.T) {
	pt, err := loadPrices(pricesJSON)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		u         usageRec
		usd       float64
		estimated bool
	}{
		{"exact with cache", usageRec{Model: "claude-sonnet-4-5-20250929", Input: 1_000_000, Output: 1_000_000, CacheRead: 1_000_000, CacheWrite: 1_000_000},
			3 + 15 + 0.3 + 3.75, false},
		{"prefix match", usageRec{Model: "gpt-5-codex-high", Input: 2_000_000}, 2.5, false},
		{"family fallback is estimated", usageRec{Model: "claude-opus-9", Output: 1_000_000}, 20, true},
		{"unknown model is estimated at 0", usageRec{Model: "mystery-1", Input: 5}, 0, true},
		{"unknown model uses native cost", usageRec{Model: "mystery-1", Input: 5, NativeCost: 0.42}, 0.42, false},
		{"family + native cost prefers native", usageRec{Model: "claude-opus-9", Output: 1_000_000, NativeCost: 1.5}, 1.5, false},
		{"known model ignores native cost", usageRec{Model: "gpt-5", Input: 1_000_000, NativeCost: 99}, 1.25, false},
	}
	for _, c := range cases {
		usd, est := pt.cost(c.u)
		if abs(usd-c.usd) > 1e-9 || est != c.estimated {
			t.Errorf("%s: cost = %v (est %v), want %v (est %v)", c.name, usd, est, c.usd, c.estimated)
		}
	}
	if _, err := loadPrices([]byte(`{"models":{},"families":[{"match":"x","model":"y"}]}`)); err == nil {
		t.Error("family pointing at an unknown model must fail")
	}
}

func TestAggregate(t *testing.T) {
	s := &Service{byID: map[string]*Adapter{"claude": newClaude(), "codex": newCodex()}}
	s.prices, _ = loadPrices(pricesJSON)
	loc := time.FixedZone("X", 2*3600)
	day1 := time.Date(2026, 1, 1, 21, 30, 0, 0, time.UTC) // 23:30 local
	day2 := time.Date(2026, 1, 1, 22, 30, 0, 0, time.UTC) // 00:30 next local day
	recs := []usageRow{
		{Session: "claude:a", At: day1, usageRec: usageRec{Model: "claude-sonnet-4-5", Input: 1_000_000}},
		{Session: "claude:a", At: day2, usageRec: usageRec{Model: "claude-sonnet-4-5", Output: 1_000_000}},
		{Session: "codex:b", At: day2, usageRec: usageRec{Model: "gpt-5", Input: 1_000_000}},
		{Session: "codex:c", At: day2, usageRec: usageRec{Model: "", Input: 10}},
	}
	sum := s.aggregate("7d", recs, loc)
	if sum.Totals.Sessions != 3 || sum.Totals.Messages != 4 || abs(sum.Totals.CostUSD-(3+15+1.25)) > 1e-9 {
		t.Errorf("totals = %+v", sum.Totals)
	}
	if len(sum.Daily) != 2 || sum.Daily[0].Date != "2026-01-01" || sum.Daily[1].Date != "2026-01-02" || abs(sum.Daily[1].ByAgent["claude"]-15) > 1e-9 {
		t.Errorf("daily = %+v", sum.Daily)
	}
	if sum.ByAgent[0].Key != "claude" || sum.ByAgent[0].Label != "Claude Code" || sum.ByAgent[0].Sessions != 1 {
		t.Errorf("byAgent = %+v", sum.ByAgent)
	}
	var unknown *api.UsageSlice
	for i := range sum.ByModel {
		if sum.ByModel[i].Key == "unknown" {
			unknown = &sum.ByModel[i]
		}
	}
	if unknown == nil || !unknown.Estimated {
		t.Errorf("unknown model slice = %+v", sum.ByModel)
	}
}

func TestRangeStart(t *testing.T) {
	now := time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC)
	for r, want := range map[string]time.Time{
		"today": time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC),
		"7d":    time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC),
		"30d":   time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC),
		"all":   {},
	} {
		if got, err := rangeStart(r, now); err != nil || !got.Equal(want) {
			t.Errorf("%s: %v %v", r, got, err)
		}
	}
	if _, err := rangeStart("1y", now); err == nil {
		t.Error("bad range accepted")
	}
}

func TestUsageDedupeInIndex(t *testing.T) {
	ts := newTestService(t)
	// The same API message streamed on two lines, in two separate
	// incremental passes: stored once, with the larger counters.
	id := "dup1"
	path := filepath.Join(ts.home, ".claude", "projects", "-d", id+".jsonl")
	line := func(out int) string {
		return `{"type":"assistant","uuid":"x` + itoa64(int64(out)) + `","requestId":"req","timestamp":"2026-01-02T03:00:00Z","message":{"id":"msg","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":10,"output_tokens":` + itoa64(int64(out)) + `}}}`
	}
	writeFile(t, path, jl(`{"type":"user","uuid":"u","timestamp":"2026-01-02T03:00:00Z","message":{"role":"user","content":"q"}}`, line(5)))
	ts.indexOnce(t)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(line(40) + "\n")
	f.Close()
	ts.indexOnce(t)
	var n, out int64
	ts.ix.db.QueryRow(`SELECT count(*), sum(output) FROM agent_usage WHERE session='claude:dup1'`).Scan(&n, &out)
	if n != 1 || out != 40 {
		t.Errorf("usage rows=%d output=%d, want 1 row with 40", n, out)
	}
}

func TestPairTerminals(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	term := func(id, agent, cwd string, at time.Time) api.TerminalSession {
		return api.TerminalSession{ID: id, Agent: agent, Cwd: cwd, CreatedAt: at, Activity: api.ActivityWorking}
	}
	cand := func(id, agent, cwd string, at time.Time) pairCand {
		return pairCand{ID: id, Agent: agent, Cwd: cwd, Started: at}
	}
	cases := []struct {
		name  string
		terms []api.TerminalSession
		hints map[string]string
		cands []pairCand
		want  map[string]string
	}{
		{"explicit id wins", []api.TerminalSession{{ID: "T", Agent: "claude", AgentSessionID: "n1", Activity: api.ActivityIdle}},
			nil, nil, map[string]string{"T": "claude:n1"}},
		{"hook hint", []api.TerminalSession{term("T", "codex", "/w", base)}, map[string]string{"T": "n2"}, nil,
			map[string]string{"T": "codex:n2"}},
		{"heuristic match", []api.TerminalSession{term("T", "codex", "/w", base)}, nil,
			[]pairCand{cand("codex:a", "codex", "/w", base.Add(30*time.Second))}, map[string]string{"T": "codex:a"}},
		{"too late", []api.TerminalSession{term("T", "codex", "/w", base)}, nil,
			[]pairCand{cand("codex:a", "codex", "/w", base.Add(3*time.Minute))}, map[string]string{}},
		{"too early", []api.TerminalSession{term("T", "codex", "/w", base)}, nil,
			[]pairCand{cand("codex:a", "codex", "/w", base.Add(-time.Minute))}, map[string]string{}},
		{"other cwd or agent", []api.TerminalSession{term("T", "codex", "/w", base)}, nil,
			[]pairCand{cand("codex:a", "codex", "/x", base), cand("claude:b", "claude", "/w", base)}, map[string]string{}},
		{"ambiguous candidates: none borrowed", []api.TerminalSession{term("T", "codex", "/w", base)}, nil,
			[]pairCand{cand("codex:a", "codex", "/w", base.Add(10*time.Second)), cand("codex:b", "codex", "/w", base.Add(12*time.Second))},
			map[string]string{}},
		{"two terminals, two sessions, clear order", []api.TerminalSession{term("T1", "codex", "/w", base), term("T2", "codex", "/w", base.Add(time.Minute))}, nil,
			[]pairCand{cand("codex:a", "codex", "/w", base.Add(2*time.Second)), cand("codex:b", "codex", "/w", base.Add(62*time.Second))},
			map[string]string{"T1": "codex:a", "T2": "codex:b"}},
		{"claimed by explicit id is not reused", []api.TerminalSession{
			{ID: "T1", Agent: "codex", AgentSessionID: "a", Cwd: "/w", CreatedAt: base}, term("T2", "codex", "/w", base)}, nil,
			[]pairCand{cand("codex:a", "codex", "/w", base.Add(time.Second))}, map[string]string{"T1": "codex:a"}},
		{"exited and shell terminals ignored", []api.TerminalSession{
			{ID: "T1", Agent: "codex", Cwd: "/w", CreatedAt: base, Activity: api.ActivityExited}, {ID: "T2", Cwd: "/w", CreatedAt: base}}, nil,
			[]pairCand{cand("codex:a", "codex", "/w", base)}, map[string]string{}},
	}
	for _, c := range cases {
		got := pairTerminals(c.terms, c.hints, c.cands)
		if mustJSON(got) != mustJSON(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCodexQuota(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	now := time.Unix(1767330000, 0)
	writeFile(t, path, jl(
		`{"timestamp":"2026-01-02T03:04:05Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":1,"window_minutes":300,"resets_at":1767340000}}}}`,
		`{"timestamp":"2026-01-02T03:05:05Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":9,"window_minutes":300,"resets_at":1767340000},"secondary":{"used_percent":75,"window_minutes":10080,"resets_at":1767000000},"plan_type":"pro"}}}`,
		`{"timestamp":"2026-01-02T03:06:05Z","type":"response_item","payload":{"type":"message"}}`,
	))
	q := codexQuotaFromFile(path, now)
	if q == nil || q.Plan != "pro" || len(q.Windows) != 2 || q.Source != "transcript" {
		t.Fatalf("quota = %+v", q)
	}
	if w := q.Windows[0]; w.Label != "5h" || w.UsedPct != 9 || !w.ResetsAt.Equal(time.Unix(1767340000, 0)) {
		t.Errorf("primary = %+v", w)
	}
	// The weekly window already reset: reported as 0 % and stale.
	if w := q.Windows[1]; w.Label != "Weekly" || w.UsedPct != 0 || !q.Stale {
		t.Errorf("secondary = %+v stale=%v", w, q.Stale)
	}
	// Older CLIs: resets_in_seconds relative to the event.
	at := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	q = codexQuota(&codexRateLimits{Primary: &codexWindow{UsedPercent: 5, WindowMinutes: 60 * 24, ResetsInSeconds: 60}}, at, at)
	if q.Windows[0].Label != "1d" || !q.Windows[0].ResetsAt.Equal(at.Add(time.Minute)) {
		t.Errorf("relative reset = %+v", q.Windows[0])
	}
	if codexQuota(&codexRateLimits{}, at, at) != nil {
		t.Error("no windows must give no quota")
	}
}

func TestClaudeQuota(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"test-token-not-real","expiresAt":`+itoa64(time.Now().Add(time.Hour).UnixMilli())+`,"subscriptionType":"max"}}`)
	var gotAuth, gotBeta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotBeta = r.Header.Get("Authorization"), r.Header.Get("anthropic-beta")
		w.Write([]byte(`{"five_hour":{"utilization":12.5,"resets_at":"2026-01-02T05:00:00.123+00:00"},"seven_day":{"utilization":40,"resets_at":"2026-01-05T00:00:00Z"},"seven_day_opus":null}`))
	}))
	defer srv.Close()
	q, err := fetchClaudeQuota(context.Background(), srv.Client(), srv.URL, home)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer test-token-not-real" || gotBeta == "" {
		t.Errorf("headers: %q %q", gotAuth, gotBeta)
	}
	if q.Plan != "max" || len(q.Windows) != 2 || q.Windows[0].Label != "5h" || q.Windows[0].UsedPct != 12.5 || q.Windows[0].ResetsAt.IsZero() {
		t.Errorf("quota = %+v", q)
	}
	// Errors never echo the token.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv2.Close()
	if _, err := fetchClaudeQuota(context.Background(), srv2.Client(), srv2.URL, home); err == nil || strings.Contains(err.Error(), "test-token") {
		t.Errorf("err = %v", err)
	}
	if _, err := fetchClaudeQuota(context.Background(), srv.Client(), srv.URL, t.TempDir()); err == nil {
		t.Error("missing credentials must fail")
	}
}

func TestClaudeQuotaOptIn(t *testing.T) {
	ts := newTestService(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"five_hour":{"utilization":1}}`))
	}))
	defer srv.Close()
	writeFile(t, filepath.Join(ts.home, ".claude", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"x"}}`)
	ts.quota.claudeURL, ts.quota.client = srv.URL, srv.Client()
	if qs := ts.quotas(context.Background()); len(qs) != 0 || calls != 0 {
		t.Fatalf("claude quota must be opt-in: %+v calls=%d", qs, calls)
	}
	ts.d.Cfg.Usage.ClaudeQuota = true
	ts.quotas(context.Background())
	ts.quotas(context.Background())
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (cached)", calls)
	}
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"<system-reminder>x</system-reminder>  Fix\n\nthe bug":                    "Fix the bug",
		"<command-name>/review</command-name> <command-args>pr 12</command-args>": "/review pr 12",
		"Caveat: The messages below were generated":                               "",
		"\x1b[31mred\x1b[0m text":                                                 "red text",
		strings.Repeat("é", 100):                                                  strings.Repeat("é", 40) + "…",
	} {
		if got := cleanTitle(in); got != want {
			t.Errorf("cleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
	if got := truncate(strings.Repeat("a", 5000), maxToolText); len(got) > maxToolText+4 {
		t.Errorf("truncate len %d", len(got))
	}
	for out, want := range map[string]string{"claude 2.1.3 (Claude Code)\n": "2.1.3", "codex-cli 0.44.0": "0.44.0", "v1.2": "1.2", "nothing": ""} {
		if got := parseVersion(out); got != want {
			t.Errorf("parseVersion(%q) = %q", out, got)
		}
	}
	for _, m := range []string{"", "opus", "anthropic/claude-sonnet-4.5", "gpt-5[1m]"} {
		if !validModel(m) {
			t.Errorf("model %q rejected", m)
		}
	}
	for _, m := range []string{"-x", "--dangerously-skip-permissions", "a;b", strings.Repeat("a", 200)} {
		if validModel(m) {
			t.Errorf("model %q accepted", m)
		}
	}
	if validNativeID("-r") || validNativeID("a b") || !validNativeID("0199-abc_DEF.1") {
		t.Error("validNativeID")
	}
	if safeArg("-p") != " -p" || safeArg("ok") != "ok" {
		t.Error("safeArg")
	}
	for in, want := range map[int]string{300: "5h", 10080: "Weekly", 1440: "1d", 90: "90m", 0: "Window"} {
		if got := windowLabel(in); got != want {
			t.Errorf("windowLabel(%d) = %q", in, got)
		}
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(2, time.Second)
	l.now = func() time.Time { return now }
	if !l.allow() || !l.allow() || l.allow() {
		t.Fatal("burst of 2")
	}
	now = now.Add(600 * time.Millisecond)
	if !l.allow() || l.allow() {
		t.Error("refill")
	}
}

func TestDetector(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin", "claude")
	writeFile(t, bin, "#!/bin/sh\n")
	os.Chmod(bin, 0o755)
	d := newDetector(home)
	d.lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	runs := 0
	d.run = func(context.Context, string, ...string) ([]byte, error) {
		runs++
		return []byte("2.0.1 (Claude Code)"), nil
	}
	det := d.detect(context.Background(), newClaude())
	if det.Binary != bin || det.Version != "2.0.1" {
		t.Fatalf("detect = %+v", det)
	}
	d.detect(context.Background(), newClaude())
	d.cache = map[string]detection{} // expire detection, version stays cached by mtime
	d.detect(context.Background(), newClaude())
	if runs != 1 {
		t.Errorf("--version ran %d times, want 1", runs)
	}
	if det := d.detect(context.Background(), newAider()); det.Binary != "" {
		t.Errorf("aider should not be found: %+v", det)
	}
}

func TestAdapterCommands(t *testing.T) {
	for _, a := range builtinAdapters() {
		if a.ID == "" || a.Name == "" || !strings.HasPrefix(a.Color, "#") || len(a.Binaries) == 0 || a.Interactive == nil {
			t.Errorf("%s: incomplete adapter", a.ID)
		}
		argv := a.Interactive("/bin/x", "-p hi", "m1")
		if argv[0] != "/bin/x" {
			t.Errorf("%s: argv[0] = %q", a.ID, argv[0])
		}
		for _, arg := range argv[1:] {
			if arg == "-p hi" {
				t.Errorf("%s: prompt passed unescaped as a flag", a.ID)
			}
		}
		if a.Caps.Resume != (a.Resume != nil) || a.Caps.Fork != (a.Fork != nil) || a.Caps.Headless != (a.Headless != nil) ||
			a.Caps.Hooks != (a.Hooks != nil) || a.Caps.History != (a.History != nil) {
			t.Errorf("%s: capabilities disagree with implementation: %+v", a.ID, a.Caps)
		}
		if a.Headless != nil {
			h := a.Headless("/bin/x", "--rm -rf", "")
			if h[len(h)-1] == "--rm -rf" || !strings.Contains(strings.Join(h, "\x00"), " --rm -rf") {
				t.Errorf("%s: headless prompt not neutralised: %q", a.ID, h)
			}
		}
		if a.Resume != nil {
			if r := a.Resume("/bin/x", "abc"); r[len(r)-1] != "abc" && !contains(r, "abc") {
				t.Errorf("%s: resume argv %q", a.ID, r)
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
