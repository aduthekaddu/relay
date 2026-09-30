package previews

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
)

// SearchProvider returns the command-center provider for previews.
func (s *Service) SearchProvider() core.SearchProvider { return searchProvider{s} }

type searchProvider struct{ s *Service }

func (searchProvider) Scope() string { return "previews" }

// Search matches the query against port, label, title, process and
// workspace. An empty query lists every visible preview.
func (p searchProvider) Search(ctx context.Context, query string, limit int) []api.SearchResult {
	q := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), ":")))
	var out []api.SearchResult
	for _, pv := range p.s.List() {
		if pv.Hidden || ctx.Err() != nil {
			continue
		}
		score := matchScore(q, pv)
		if score <= 0 {
			continue
		}
		title := ":" + strconv.Itoa(pv.Port)
		if pv.Label != "" {
			title = pv.Label + " · " + title
		}
		sub := pv.Title
		if pv.Workspace != "" {
			if sub != "" {
				sub += " — "
			}
			sub += lastPathElem(pv.Workspace)
		}
		if sub == "" {
			sub = pv.Process
		}
		out = append(out, api.SearchResult{
			Scope:    "previews",
			ID:       strconv.Itoa(pv.Port),
			Title:    title,
			Subtitle: sub,
			Icon:     "previews",
			Link:     "/previews?port=" + strconv.Itoa(pv.Port),
			Score:    score,
			At:       pv.FirstSeenAt,
			Meta:     map[string]string{"url": pv.URL, "port": strconv.Itoa(pv.Port)},
		})
	}
	sortResults(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// matchScore ranks a preview for q in (0, 1]; 0 means no match.
func matchScore(q string, pv api.Preview) float64 {
	if q == "" {
		return 0.3
	}
	port := strconv.Itoa(pv.Port)
	switch {
	case port == q:
		return 1
	case strings.HasPrefix(port, q):
		return 0.9
	}
	best := 0.0
	for _, f := range []struct {
		v string
		w float64
	}{{pv.Label, 0.85}, {pv.Title, 0.7}, {lastPathElem(pv.Workspace), 0.7}, {pv.Process, 0.6}, {"preview", 0.35}, {"port", 0.35}} {
		v := strings.ToLower(f.v)
		if v == "" {
			continue
		}
		s := 0.0
		switch {
		case v == q:
			s = f.w
		case strings.HasPrefix(v, q):
			s = f.w * 0.9
		case strings.Contains(v, q):
			s = f.w * 0.7
		}
		if s > best {
			best = s
		}
	}
	return best
}

func sortResults(rs []api.SearchResult) {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Score > rs[j].Score })
}
