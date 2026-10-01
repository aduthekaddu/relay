package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Plan quotas.
//
//   - Codex writes its rate-limit windows into every token_count event of
//     the rollout (payload.rate_limits.{primary,secondary}); the newest
//     rollout carries the freshest reading. Read from the tail of the file.
//   - Claude Code's plan usage comes from the (undocumented) OAuth usage
//     endpoint, using the token Claude Code stores in
//     ~/.claude/.credentials.json. This only runs when the user opted in
//     with usage.claude_quota = true, is cached for 10 minutes, and the
//     token never leaves the request header (never logged or returned).
const (
	quotaTTL          = time.Minute
	claudeQuotaTTL    = 10 * time.Minute
	claudeUsageURL    = "https://api.anthropic.com/api/oauth/usage"
	quotaTailBytes    = 512 << 10
	codexQuotaFiles   = 6
	claudeHTTPTimeout = 6 * time.Second
)

// quotaCache memoises quota readings.
type quotaCache struct {
	mu       sync.Mutex
	codex    *api.Quota
	codexAt  time.Time
	claude   *api.Quota
	claudeAt time.Time
	// claudeURL and client are overridable in tests.
	claudeURL string
	client    *http.Client
}

func newQuotaCache() *quotaCache {
	return &quotaCache{claudeURL: claudeUsageURL, client: &http.Client{Timeout: claudeHTTPTimeout}}
}

// quotas returns every readable plan quota.
func (s *Service) quotas(ctx context.Context) []api.Quota {
	out := []api.Quota{}
	if _, ok := s.byID["codex"]; ok {
		if q := s.codexQuota(ctx); q != nil {
			out = append(out, *q)
		}
	}
	if _, ok := s.byID["claude"]; ok && s.d.RuntimeConfig().Usage.ClaudeQuota {
		if q := s.claudeQuota(ctx); q != nil && s.d.RuntimeConfig().Usage.ClaudeQuota {
			out = append(out, *q)
		}
	}
	return out
}

// codexQuota reads rate limits from the newest indexed rollouts.
func (s *Service) codexQuota(ctx context.Context) *api.Quota {
	c := s.quota
	c.mu.Lock()
	if time.Since(c.codexAt) < quotaTTL {
		q := c.codex
		c.mu.Unlock()
		return q
	}
	c.mu.Unlock()
	rows, err := s.ix.db.QueryContext(ctx, `SELECT path FROM agent_sources WHERE agent='codex' ORDER BY mtime DESC LIMIT ?`, codexQuotaFiles)
	if err != nil {
		return nil
	}
	var paths []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			paths = append(paths, p)
		}
	}
	rows.Close()
	var q *api.Quota
	for _, p := range paths {
		if q = codexQuotaFromFile(p, time.Now()); q != nil {
			break
		}
	}
	c.mu.Lock()
	c.codex, c.codexAt = q, time.Now()
	c.mu.Unlock()
	return q
}

// codexRateLimits is payload.rate_limits of a token_count event.
type codexRateLimits struct {
	Primary   *codexWindow `json:"primary"`
	Secondary *codexWindow `json:"secondary"`
	PlanType  string       `json:"plan_type"`
}

type codexWindow struct {
	UsedPercent     float64 `json:"used_percent"`
	WindowMinutes   int     `json:"window_minutes"`
	ResetsAt        int64   `json:"resets_at"`         // unix seconds (newer CLIs)
	ResetsInSeconds int64   `json:"resets_in_seconds"` // relative to the event (older CLIs)
}

// codexQuotaFromFile scans the tail of a rollout for the last token_count
// event that carries rate limits.
func codexQuotaFromFile(path string, now time.Time) *api.Quota {
	tail, err := readTail(path, quotaTailBytes)
	if err != nil {
		return nil
	}
	lines := bytes.Split(tail, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if !bytes.Contains(line, []byte(`"rate_limits"`)) {
			continue
		}
		var l struct {
			Timestamp string `json:"timestamp"`
			Payload   struct {
				Type       string           `json:"type"`
				RateLimits *codexRateLimits `json:"rate_limits"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &l) != nil || l.Payload.RateLimits == nil {
			continue
		}
		if q := codexQuota(l.Payload.RateLimits, parseTime(l.Timestamp), now); q != nil {
			return q
		}
	}
	return nil
}

// codexQuota converts rate limits into a Quota (pure; tested).
func codexQuota(rl *codexRateLimits, at, now time.Time) *api.Quota {
	q := &api.Quota{Agent: "codex", Plan: rl.PlanType, UpdatedAt: at, Source: "transcript", Windows: []api.QuotaWindow{}}
	for _, w := range []*codexWindow{rl.Primary, rl.Secondary} {
		if w == nil {
			continue
		}
		win := api.QuotaWindow{Label: windowLabel(w.WindowMinutes), UsedPct: w.UsedPercent}
		switch {
		case w.ResetsAt > 0:
			win.ResetsAt = time.Unix(w.ResetsAt, 0).UTC()
		case w.ResetsInSeconds > 0 && !at.IsZero():
			win.ResetsAt = at.Add(time.Duration(w.ResetsInSeconds) * time.Second)
		}
		if !win.ResetsAt.IsZero() && win.ResetsAt.Before(now) {
			// The window rolled over since this reading: usage restarted.
			win.UsedPct = 0
			q.Stale = true
		}
		q.Windows = append(q.Windows, win)
	}
	if len(q.Windows) == 0 {
		return nil
	}
	return q
}

// windowLabel names a rate-limit window by its length.
func windowLabel(minutes int) string {
	switch {
	case minutes <= 0:
		return "Window"
	case minutes == 10080:
		return "Weekly"
	case minutes%1440 == 0:
		return fmt.Sprintf("%dd", minutes/1440)
	case minutes%60 == 0:
		return fmt.Sprintf("%dh", minutes/60)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

// readTail returns up to n bytes from the end of path, starting at a line
// boundary.
func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := max(0, st.Size()-n)
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, n))
	if err != nil {
		return nil, err
	}
	if off > 0 {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return b, nil
}

// claudeQuota asks the OAuth usage endpoint (opt-in, cached 10 min).
func (s *Service) claudeQuota(ctx context.Context) *api.Quota {
	c := s.quota
	c.mu.Lock()
	if time.Since(c.claudeAt) < claudeQuotaTTL {
		q := c.claude
		c.mu.Unlock()
		return q
	}
	c.mu.Unlock()
	q, err := fetchClaudeQuota(ctx, c.client, c.claudeURL, s.home)
	if err != nil {
		// Never include the error text verbatim if it could echo headers;
		// our errors only name the failing step.
		s.log().Debug("agents: claude quota unavailable", "err", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if q == nil && c.claude != nil {
		stale := *c.claude
		stale.Stale = true
		q = &stale
	}
	c.claude, c.claudeAt = q, time.Now()
	return q
}

// claudeCredentials is the part of ~/.claude/.credentials.json we read.
type claudeCredentials struct {
	OAuth *struct {
		AccessToken      string `json:"accessToken"`
		ExpiresAt        int64  `json:"expiresAt"`
		SubscriptionType string `json:"subscriptionType"`
	} `json:"claudeAiOauth"`
}

var errNoClaudeToken = errors.New("no Claude Code OAuth token")

// fetchClaudeQuota performs one usage request.
func fetchClaudeQuota(ctx context.Context, client *http.Client, url, home string) (*api.Quota, error) {
	var cred claudeCredentials
	if err := readJSONFile(home+"/.claude/.credentials.json", 1<<20, &cred); err != nil {
		return nil, errNoClaudeToken
	}
	if cred.OAuth == nil || cred.OAuth.AccessToken == "" {
		return nil, errNoClaudeToken
	}
	if cred.OAuth.ExpiresAt > 0 && unixAny(float64(cred.OAuth.ExpiresAt)).Before(time.Now()) {
		return nil, errors.New("claude OAuth token expired (run claude once to refresh it)")
	}
	ctx, cancel := context.WithTimeout(ctx, claudeHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.New("build usage request")
	}
	req.Header.Set("Authorization", "Bearer "+cred.OAuth.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "relay")
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("usage request failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage request: HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 256<<10))
	if err != nil {
		return nil, errors.New("read usage response")
	}
	q, err := parseClaudeUsage(body, time.Now())
	if err != nil {
		return nil, err
	}
	q.Plan = cred.OAuth.SubscriptionType
	return q, nil
}

// parseClaudeUsage converts the OAuth usage response (pure; tested):
// {"five_hour":{"utilization":12,"resets_at":"…"},"seven_day":{…},"seven_day_opus":{…}}.
func parseClaudeUsage(body []byte, now time.Time) (*api.Quota, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, errors.New("usage response is not JSON")
	}
	q := &api.Quota{Agent: "claude", UpdatedAt: now.UTC(), Source: "api", Windows: []api.QuotaWindow{}}
	for _, k := range []struct{ key, label string }{
		{"five_hour", "5h"}, {"seven_day", "Weekly"}, {"seven_day_opus", "Opus weekly"},
		{"seven_day_sonnet", "Sonnet weekly"}, {"seven_day_oauth_apps", "Apps weekly"},
	} {
		raw, ok := m[k.key]
		if !ok || string(raw) == "null" {
			continue
		}
		var w struct {
			Utilization *float64 `json:"utilization"`
			ResetsAt    string   `json:"resets_at"`
		}
		if json.Unmarshal(raw, &w) != nil || w.Utilization == nil {
			continue
		}
		q.Windows = append(q.Windows, api.QuotaWindow{Label: k.label, UsedPct: *w.Utilization, ResetsAt: parseTime(strings.TrimSpace(w.ResetsAt))})
	}
	if len(q.Windows) == 0 {
		return nil, errors.New("usage response has no windows")
	}
	return q, nil
}
