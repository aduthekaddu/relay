package terminal

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// searchProvider serves command-center results for scope "terminals".
type searchProvider struct{ s *Service }

func (p *searchProvider) Scope() string { return "terminals" }

// Search matches the query against session name, title, cwd and agent.
// An empty query lists live sessions, most recently active first.
func (p *searchProvider) Search(ctx context.Context, query string, limit int) []api.SearchResult {
	if limit <= 0 {
		limit = 20
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	list, err := p.s.pty.List(ctx)
	if err != nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var out []api.SearchResult
	for i := range list {
		t := &list[i]
		score := matchScore(t, q)
		if score <= 0 {
			continue
		}
		out = append(out, toResult(t, score))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].At.After(out[j].At)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// matchScore ranks a session for q (lower-cased); 0 = no match.
func matchScore(t *api.TerminalSession, q string) float64 {
	live := t.Activity != api.ActivityExited
	if q == "" {
		if !live {
			return 0
		}
		return 0.5
	}
	name := strings.ToLower(t.Name)
	cwd := strings.ToLower(t.CurrentCwd)
	if cwd == "" {
		cwd = strings.ToLower(t.Cwd)
	}
	var score float64
	switch {
	case name == q:
		score = 1
	case strings.HasPrefix(name, q):
		score = 0.9
	case strings.Contains(name, q):
		score = 0.75
	case strings.Contains(strings.ToLower(t.Title), q):
		score = 0.6
	case t.Agent != "" && strings.Contains(strings.ToLower(t.Agent), q):
		score = 0.55
	case strings.Contains(strings.ToLower(filepath.Base(cwd)), q):
		score = 0.5
	case strings.Contains(cwd, q):
		score = 0.4
	case t.ID == q:
		score = 0.8
	default:
		return 0
	}
	if live {
		score += 0.05
	}
	if t.Attention != nil {
		score += 0.05
	}
	return score
}

func toResult(t *api.TerminalSession, score float64) api.SearchResult {
	icon := "terminal"
	if t.Agent != "" {
		icon = t.Agent
	}
	cwd := t.CurrentCwd
	if cwd == "" {
		cwd = t.Cwd
	}
	sub := cwd
	if t.Title != "" && t.Title != t.Name {
		sub = t.Title + " · " + cwd
	}
	at := t.LastOutputAt
	if at.IsZero() {
		at = t.CreatedAt
	}
	return api.SearchResult{
		Scope:    "terminals",
		ID:       t.ID,
		Title:    t.Name,
		Subtitle: sub,
		Icon:     icon,
		Link:     "/terminal/" + t.ID,
		Score:    score,
		At:       at,
		Meta:     map[string]string{"activity": string(t.Activity), "kind": string(t.Kind)},
	}
}
