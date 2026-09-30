package workspaces

import (
	"context"
	"net/url"
	"strings"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
)

// SearchProvider returns the command-center provider for workspaces. It
// matches names and paths without running git, so it stays within the
// federated search budget.
func (s *Service) SearchProvider() core.SearchProvider { return searchProvider{s} }

type searchProvider struct{ s *Service }

func (searchProvider) Scope() string { return "workspaces" }

func (p searchProvider) Search(ctx context.Context, q string, limit int) []api.SearchResult {
	q = strings.ToLower(strings.TrimSpace(q))
	if limit <= 0 {
		limit = 8
	}
	out := []api.SearchResult{}
	for i, w := range p.s.collect(ctx) {
		if len(out) >= limit {
			break
		}
		name, path := strings.ToLower(w.Name), strings.ToLower(w.Path)
		score := 0.0
		switch {
		case q == "":
			score = 0.3
		case name == q:
			score = 1
		case strings.HasPrefix(name, q):
			score = 0.85
		case strings.Contains(name, q):
			score = 0.7
		case strings.Contains(path, q):
			score = 0.45
		default:
			continue
		}
		if w.Pinned {
			score += 0.1
		}
		score -= float64(i) * 0.001 // keep recency order among equals
		sub := tilde(w.Path, p.s.home)
		if w.Terminals > 0 {
			sub += " · " + plural(w.Terminals, "terminal")
		}
		out = append(out, api.SearchResult{Scope: "workspaces", ID: w.Path, Title: w.Name, Subtitle: sub,
			Icon: "folder-git", Link: "/workspace?path=" + url.QueryEscape(w.Path), Score: score, At: w.LastUsedAt,
			Meta: map[string]string{"path": w.Path}})
	}
	return out
}

// tilde shortens paths under home to ~/….
func tilde(p, home string) string {
	if home != "" && within(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
