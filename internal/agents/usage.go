package agents

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

// usageRow is one stored billed call.
type usageRow struct {
	Session string
	At      time.Time
	usageRec
}

// sessionCost is the priced total of one session.
type sessionCost struct {
	Tokens    api.TokenUsage
	Cost      float64
	Estimated bool
}

// sessionCosts prices the usage of the given sessions.
func (s *Service) sessionCosts(ctx context.Context, ids []string) map[string]sessionCost {
	out := map[string]sessionCost{}
	if len(ids) == 0 {
		return out
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.ix.db.QueryContext(ctx, `SELECT session, model, sum(input), sum(output), sum(cache_read), sum(cache_write),
		sum(reasoning), sum(native_cost) FROM agent_usage WHERE session IN (`+ph+`) GROUP BY session, model`, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		var u usageRec
		if rows.Scan(&sid, &u.Model, &u.Input, &u.Output, &u.CacheRead, &u.CacheWrite, &u.Reasoning, &u.NativeCost) != nil {
			continue
		}
		c := out[sid]
		addTokens(&c.Tokens, u)
		usd, est := s.prices.cost(u)
		c.Cost += usd
		c.Estimated = c.Estimated || est
		out[sid] = c
	}
	return out
}

func addTokens(t *api.TokenUsage, u usageRec) {
	t.Input += u.Input
	t.Output += u.Output
	t.CacheRead += u.CacheRead
	t.CacheWrite += u.CacheWrite
	t.Reasoning += u.Reasoning
}

func totalTokens(u usageRec) int64 { return u.Input + u.Output + u.CacheRead + u.CacheWrite }

// rangeStart returns the first instant of a usage range in loc.
func rangeStart(r string, now time.Time) (time.Time, error) {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch r {
	case "today":
		return day, nil
	case "", "7d":
		return day.AddDate(0, 0, -6), nil
	case "30d":
		return day.AddDate(0, 0, -29), nil
	case "all":
		return time.Time{}, nil
	}
	return time.Time{}, httpx.BadRequest("range must be today, 7d, 30d or all")
}

// usageCache memoises summaries for a short time.
type usageCache struct {
	mu sync.Mutex
	m  map[string]*api.UsageSummary
}

func newUsageCache() *usageCache { return &usageCache{m: map[string]*api.UsageSummary{}} }

func (c *usageCache) get(k string) *api.UsageSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.m[k]; ok && time.Since(v.Updated) < 30*time.Second {
		return v
	}
	return nil
}

func (c *usageCache) put(k string, v *api.UsageSummary) {
	c.mu.Lock()
	c.m[k] = v
	c.mu.Unlock()
}

func (c *usageCache) invalidate() {
	c.mu.Lock()
	c.m = map[string]*api.UsageSummary{}
	c.mu.Unlock()
}

// usageSummary aggregates usage for range r (local days).
func (s *Service) usageSummary(ctx context.Context, r string) (*api.UsageSummary, error) {
	if r == "" {
		r = "7d"
	}
	if v := s.usage.get(r); v != nil {
		return v, nil
	}
	now := time.Now()
	since, err := rangeStart(r, now)
	if err != nil {
		return nil, err
	}
	rows, err := s.ix.db.QueryContext(ctx, `SELECT session, at, model, input, output, cache_read, cache_write, reasoning, native_cost
		FROM agent_usage WHERE at >= ?`, toMS(since))
	if err != nil {
		return nil, fmt.Errorf("usage: %w", err)
	}
	defer rows.Close()
	var recs []usageRow
	for rows.Next() {
		var u usageRow
		var at int64
		if err := rows.Scan(&u.Session, &at, &u.Model, &u.Input, &u.Output, &u.CacheRead, &u.CacheWrite, &u.Reasoning, &u.NativeCost); err != nil {
			return nil, err
		}
		u.At = msTime(at)
		recs = append(recs, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sum := s.aggregate(r, recs, now.Location())
	s.usage.put(r, sum)
	return sum, nil
}

// aggregate folds usage rows into a summary (pure; tested).
func (s *Service) aggregate(r string, recs []usageRow, loc *time.Location) *api.UsageSummary {
	sum := &api.UsageSummary{Range: r, ByAgent: []api.UsageSlice{}, ByModel: []api.UsageSlice{}, Daily: []api.UsageDay{}, Updated: time.Now().UTC()}
	type acc struct {
		slice    api.UsageSlice
		sessions map[string]bool
	}
	agents, models := map[string]*acc{}, map[string]*acc{}
	days := map[string]*api.UsageDay{}
	allSessions := map[string]bool{}
	bump := func(m map[string]*acc, key, label, session string, usd float64, tok int64, est bool) {
		a, ok := m[key]
		if !ok {
			a = &acc{slice: api.UsageSlice{Key: key, Label: label}, sessions: map[string]bool{}}
			m[key] = a
		}
		a.slice.CostUSD += usd
		a.slice.Tokens += tok
		a.slice.Estimated = a.slice.Estimated || est
		a.sessions[session] = true
	}
	for _, u := range recs {
		agent, _, _ := strings.Cut(u.Session, ":")
		usd, est := s.prices.cost(u.usageRec)
		tok := totalTokens(u.usageRec)
		sum.Totals.CostUSD += usd
		addTokens(&sum.Totals.Tokens, u.usageRec)
		sum.Totals.Messages++
		allSessions[u.Session] = true
		label := agent
		if a, ok := s.byID[agent]; ok {
			label = a.Name
		}
		bump(agents, agent, label, u.Session, usd, tok, est)
		model := u.Model
		if model == "" {
			model = "unknown"
		}
		bump(models, model, model, u.Session, usd, tok, est)
		date := u.At.In(loc).Format("2006-01-02")
		d, ok := days[date]
		if !ok {
			d = &api.UsageDay{Date: date, ByAgent: map[string]float64{}}
			days[date] = d
		}
		d.CostUSD += usd
		d.Tokens += tok
		d.ByAgent[agent] += usd
	}
	sum.Totals.Sessions = len(allSessions)
	flatten := func(m map[string]*acc) []api.UsageSlice {
		out := make([]api.UsageSlice, 0, len(m))
		for _, a := range m {
			a.slice.Sessions = len(a.sessions)
			out = append(out, a.slice)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].CostUSD != out[j].CostUSD {
				return out[i].CostUSD > out[j].CostUSD
			}
			return out[i].Tokens > out[j].Tokens
		})
		return out
	}
	sum.ByAgent, sum.ByModel = flatten(agents), flatten(models)
	for _, d := range days {
		sum.Daily = append(sum.Daily, *d)
	}
	sort.Slice(sum.Daily, func(i, j int) bool { return sum.Daily[i].Date < sum.Daily[j].Date })
	return sum
}
