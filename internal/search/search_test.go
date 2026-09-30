package search

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
)

// fakeProvider returns fixed results after an optional delay.
type fakeProvider struct {
	scope   string
	delay   time.Duration
	results []api.SearchResult
	panics  bool
	gotQ    chan string
}

func (p *fakeProvider) Scope() string { return p.scope }

func (p *fakeProvider) Search(ctx context.Context, q string, limit int) []api.SearchResult {
	if p.gotQ != nil {
		p.gotQ <- q
	}
	if p.panics {
		panic("boom")
	}
	if p.delay > 0 {
		time.Sleep(p.delay) // deliberately ignores ctx: a misbehaving provider
	}
	return p.results
}

func newTestService(t *testing.T, providers ...core.SearchProvider) *Service {
	t.Helper()
	reg := core.NewSearchRegistry()
	for _, p := range providers {
		reg.Add(p)
	}
	s, err := New(&core.Deps{Search: reg}, WithBudget(60*time.Millisecond), WithScriptDirs())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func res(scope, id, title string, score float64) api.SearchResult {
	return api.SearchResult{Scope: scope, ID: id, Title: title, Score: score}
}

func TestSearchBudgetReturnsFinishedProviders(t *testing.T) {
	fast := &fakeProvider{scope: "notes", results: []api.SearchResult{res("notes", "1", "deploy notes", 0.5)}}
	slow := &fakeProvider{scope: "files", delay: 400 * time.Millisecond, results: []api.SearchResult{res("files", "f", "deploy.sh", 1)}}
	bad := &fakeProvider{scope: "agents", panics: true}
	s := newTestService(t, fast, slow, bad)

	start := time.Now()
	got := s.Search(context.Background(), "deploy", nil, 8)
	if el := time.Since(start); el > 250*time.Millisecond {
		t.Fatalf("search took %v, budget not enforced", el)
	}
	if len(got.Results) != 1 || got.Results[0].ID != "1" {
		t.Fatalf("results = %+v, want only the fast provider", got.Results)
	}
	if got.Query != "deploy" {
		t.Fatalf("query = %q", got.Query)
	}
}

func TestSearchScopesAndNormalisation(t *testing.T) {
	a := &fakeProvider{scope: "notes", gotQ: make(chan string, 1), results: []api.SearchResult{res("", "1", "a", 0.1)}}
	b := &fakeProvider{scope: "snippets", results: []api.SearchResult{res("snippets", "2", "b", 0.1)}}
	s := newTestService(t, a, b)
	got := s.Search(context.Background(), "  hello \t  world ", []string{"notes"}, 8)
	if q := <-a.gotQ; q != "hello world" {
		t.Fatalf("provider saw %q", q)
	}
	if len(got.Results) != 1 || got.Results[0].Scope != "notes" {
		t.Fatalf("results = %+v, want notes only with scope filled in", got.Results)
	}
}

func TestSearchPerScopeCapAndDedupe(t *testing.T) {
	var many []api.SearchResult
	for _, id := range []string{"a", "b", "c", "d", "a"} {
		many = append(many, res("notes", id, "note "+id, 0.5))
	}
	many = append(many, api.SearchResult{Scope: "notes", ID: "", Title: "no id"})
	other := &fakeProvider{scope: "snippets", results: []api.SearchResult{res("snippets", "x", "snip", 0.1)}}
	s := newTestService(t, &fakeProvider{scope: "notes", results: many}, other)
	got := s.Search(context.Background(), "", nil, 3)
	counts := map[string]int{}
	for _, r := range got.Results {
		counts[r.Scope]++
	}
	if counts["notes"] != 3 || counts["snippets"] != 1 {
		t.Fatalf("counts = %v", counts)
	}
}

func TestRankOrdering(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	b := batch{scope: "x", results: []api.SearchResult{
		res("x", "contains", "my deploy script", 0.5),
		res("x", "word", "run-deploy", 0.5),
		res("x", "prefix", "deployment", 0.5),
		res("x", "exact", "Deploy", 0.5),
		res("x", "none", "unrelated", 0.5),
	}}
	got := rank("deploy", []batch{b}, 10, now)
	// "contains" and "word" both start "deploy" at a word boundary, so
	// they tie on score and fall back to title order.
	want := []string{"exact", "prefix", "contains", "word", "none"}
	for i, id := range want {
		if got[i].ID != id {
			ids := make([]string, len(got))
			for j, r := range got {
				ids[j] = r.ID
			}
			t.Fatalf("order = %v, want %v", ids, want)
		}
	}
}

func TestMatchBoost(t *testing.T) {
	tests := []struct {
		title, q string
		want     float64
	}{
		{"deploy", "deploy", boostExact},
		{"deployment", "deploy", boostPrefix},
		{"run deploy", "deploy", boostWord},
		{"run_deploy", "deploy", boostWord},
		{"redeploy", "deploy", boostContains},
		{"nothing", "deploy", 0},
		{"git push origin", "git or", boostAllWords},
		{"git push origin", "git push", boostPrefix + boostAllWords},
		{"push to git", "git push", boostAllWords},
	}
	for _, tt := range tests {
		got := matchBoost(tt.title, tt.q, strings.Fields(tt.q))
		if round3(got) != round3(tt.want) {
			t.Errorf("matchBoost(%q, %q) = %v, want %v", tt.title, tt.q, got, tt.want)
		}
	}
}

func TestRecency(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		at   time.Time
		want float64
	}{
		{"zero", time.Time{}, 0},
		{"now", now, boostRecency},
		{"half-life", now.Add(-recencyHalf), boostRecency / 2},
		{"small skew", now.Add(30 * time.Second), boostRecency},
		{"far future", now.Add(time.Hour), 0},
	}
	for _, tt := range tests {
		if got := recency(tt.at, now); round3(got) != round3(tt.want) {
			t.Errorf("%s: recency = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestRankRecencyBreaksTies(t *testing.T) {
	now := time.Now()
	old := res("x", "old", "notes", 0.5)
	old.At = now.Add(-30 * 24 * time.Hour)
	fresh := res("x", "fresh", "notes", 0.5)
	fresh.At = now.Add(-time.Minute)
	got := rank("", []batch{{scope: "x", results: []api.SearchResult{old, fresh}}}, 5, now)
	if got[0].ID != "fresh" {
		t.Fatalf("got %v first", got[0].ID)
	}
}

func TestClampScores(t *testing.T) {
	for in, want := range map[float64]float64{-1: 0, 0.4: 0.4, 7: 1} {
		if got := clamp01(in); got != want {
			t.Errorf("clamp01(%v) = %v", in, got)
		}
	}
}

func TestBucket(t *testing.T) {
	now := time.Unix(0, 0)
	b := newBucket(1, 2, func() time.Time { return now })
	for i := 0; i < 2; i++ {
		if _, ok := b.take(); !ok {
			t.Fatalf("take %d refused", i)
		}
	}
	if wait, ok := b.take(); ok || wait <= 0 {
		t.Fatalf("third take = %v, %v", wait, ok)
	}
	now = now.Add(time.Second)
	if _, ok := b.take(); !ok {
		t.Fatal("refill failed")
	}
}

func TestAskLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := newAskLimiter(2, time.Hour, func() time.Time { return now })
	rel, err := l.acquire()
	if err != nil {
		t.Fatal(err)
	}
	if _, e := l.acquire(); e == nil || !e.busy {
		t.Fatalf("concurrent acquire = %+v, want busy", e)
	}
	rel()
	rel() // idempotent
	rel2, err := l.acquire()
	if err != nil {
		t.Fatal(err)
	}
	rel2()
	if _, e := l.acquire(); e == nil || e.busy || e.wait != time.Hour {
		t.Fatalf("over quota = %+v", e)
	}
	now = now.Add(time.Hour + time.Second)
	if r, e := l.acquire(); e != nil {
		t.Fatalf("after window = %+v", e)
	} else {
		r()
	}
}
