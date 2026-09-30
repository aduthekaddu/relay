package agents

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
)

// Full-text search over indexed user + assistant text.
//
// With FTS5 the query is tokenised and every token becomes a quoted
// prefix term ("tok"*), so user input can never inject FTS syntax. The
// LIKE fallback (SQLite without FTS5) matches the whole query as a
// substring and cuts the snippet itself.
const (
	maxSearchQuery = 200
	snippetTokens  = 14
	likeSnippetCtx = 60
)

// ftsQuery turns free text into a safe FTS5 MATCH expression ("" if no
// searchable tokens).
func ftsQuery(q string) string {
	var terms []string
	for _, tok := range strings.FieldsFunc(q, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
	}) {
		if len(terms) == 12 {
			break
		}
		terms = append(terms, `"`+strings.ReplaceAll(tok, `"`, `""`)+`"*`)
	}
	return strings.Join(terms, " ")
}

// searchText returns FTS hits (best first), optionally for one agent.
func (s *Service) searchText(ctx context.Context, q, agent string, limit int) ([]api.SearchHit, error) {
	q = strings.TrimSpace(q)
	if len(q) > maxSearchQuery {
		q = truncate(q, maxSearchQuery)
	}
	if q == "" {
		return []api.SearchHit{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	if s.ix.fts {
		return s.searchFTS(ctx, q, agent, limit)
	}
	return s.searchLike(ctx, q, agent, limit)
}

const hitCols = `s.id, s.agent, s.title, s.first_prompt, s.summary, s.cwd, u.title, t.role, t.at, t.msg`

func (s *Service) searchFTS(ctx context.Context, q, agent string, limit int) ([]api.SearchHit, error) {
	match := ftsQuery(q)
	if match == "" {
		return []api.SearchHit{}, nil
	}
	where, args := "agent_fts MATCH ? AND s.hidden=0", []any{match}
	if agent != "" {
		where += " AND s.agent=?"
		args = append(args, agent)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`SELECT %s, snippet(agent_fts, 0, '«', '»', '…', %d)
		FROM agent_fts JOIN agent_text t ON t.rowid = agent_fts.rowid
		JOIN agent_sessions s ON s.id = t.session
		LEFT JOIN agent_user u ON u.id = s.id
		WHERE %s ORDER BY bm25(agent_fts) LIMIT ?`, hitCols, snippetTokens, where)
	return s.scanHits(ctx, query, args, nil)
}

func (s *Service) searchLike(ctx context.Context, q, agent string, limit int) ([]api.SearchHit, error) {
	where, args := `t.body LIKE ? ESCAPE '\' AND s.hidden=0`, []any{"%" + escapeLike(q) + "%"}
	if agent != "" {
		where += " AND s.agent=?"
		args = append(args, agent)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`SELECT %s, t.body FROM agent_text t
		JOIN agent_sessions s ON s.id = t.session
		LEFT JOIN agent_user u ON u.id = s.id
		WHERE %s ORDER BY t.at DESC LIMIT ?`, hitCols, where)
	return s.scanHits(ctx, query, args, func(body string) string { return likeSnippet(body, q) })
}

func (s *Service) scanHits(ctx context.Context, query string, args []any, snip func(string) string) ([]api.SearchHit, error) {
	rows, err := s.ix.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()
	out := []api.SearchHit{}
	for rows.Next() {
		var h api.SearchHit
		var title, first, summary, snippet string
		var userTitle *string
		var at int64
		if err := rows.Scan(&h.SessionID, &h.Agent, &title, &first, &summary, &h.Cwd, &userTitle, &h.Role, &at, &h.MessageID, &snippet); err != nil {
			return nil, err
		}
		h.Title = pickTitle(userTitle, title, first, summary)
		h.At = msTime(at)
		if snip != nil {
			snippet = snip(snippet)
		}
		h.Snippet = strings.Join(strings.Fields(snippet), " ")
		out = append(out, h)
	}
	return out, rows.Err()
}

// pickTitle applies the same precedence as scanSession.
func pickTitle(user *string, title, first, summary string) string {
	switch {
	case user != nil && *user != "":
		return *user
	case title != "":
		return title
	case first != "":
		return first
	case summary != "":
		return summary
	}
	return "Untitled session"
}

// likeSnippet cuts context around the first case-insensitive match and
// wraps it in « ».
func likeSnippet(body, q string) string {
	lb, lq := strings.ToLower(body), strings.ToLower(q)
	i := strings.Index(lb, lq)
	if i < 0 || len(lb) != len(body) {
		// Lower-casing changed byte lengths (rare scripts): no highlight.
		return truncate(body, 2*likeSnippetCtx)
	}
	start, end := max(0, i-likeSnippetCtx), min(len(body), i+len(q)+likeSnippetCtx)
	for start > 0 && !utf8Start(body[start]) {
		start--
	}
	for end < len(body) && !utf8Start(body[end]) {
		end++
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	b.WriteString(body[start:i] + "«" + body[i:i+len(q)] + "»" + body[i+len(q):end])
	if end < len(body) {
		b.WriteString("…")
	}
	return b.String()
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// sessionLink is the in-app route of a session (optionally at a message).
func sessionLink(id, msg string) string {
	l := "/agents/s/" + url.PathEscape(id)
	if msg != "" {
		l += "?m=" + url.QueryEscape(msg)
	}
	return l
}

// SearchProviders returns the command-center providers of this feature.
func (s *Service) SearchProviders() []core.SearchProvider {
	return []core.SearchProvider{agentsProvider{s}, historyProvider{s}}
}

// agentsProvider ("agents"): live and recent sessions matching by title.
type agentsProvider struct{ s *Service }

func (agentsProvider) Scope() string { return "agents" }

func (p agentsProvider) Search(ctx context.Context, q string, limit int) []api.SearchResult {
	if limit <= 0 {
		limit = 8
	}
	page, err := p.s.listSessions(ctx, sessionQuery{Q: strings.TrimSpace(q), Status: "all", Limit: limit})
	if err != nil {
		return nil
	}
	out := []api.SearchResult{}
	now := time.Now()
	for i, as := range page.Items {
		if len(out) >= limit {
			break
		}
		r := api.SearchResult{Scope: "agents", ID: as.ID, Title: as.Title, Icon: as.Agent, At: as.UpdatedAt,
			Link: sessionLink(as.ID, ""), Meta: map[string]string{"agent": as.Agent, "status": string(as.Status)}}
		name := as.Agent
		if a, ok := p.s.byID[as.Agent]; ok {
			name = a.Name
		}
		sub := []string{name}
		if as.Cwd != "" {
			sub = append(sub, tildePath(as.Cwd, p.s.home))
		}
		// Rank: live first, then recency, then list order.
		r.Score = 0.5 - float64(i)*0.01
		if as.Status == api.AgentLive {
			r.Score += 0.4
			r.Link = "/terminal/" + as.TerminalID
			r.Meta["terminalId"] = as.TerminalID
			if as.Activity != "" {
				sub = append(sub, string(as.Activity))
			}
		} else if age := now.Sub(as.UpdatedAt); age < 24*time.Hour {
			r.Score += 0.1
		}
		if q != "" && strings.Contains(strings.ToLower(as.Title), strings.ToLower(strings.TrimSpace(q))) {
			r.Score += 0.2
		}
		r.Subtitle = strings.Join(sub, " · ")
		out = append(out, r)
	}
	return out
}

// historyProvider ("history"): full-text hits inside transcripts.
type historyProvider struct{ s *Service }

func (historyProvider) Scope() string { return "history" }

func (p historyProvider) Search(ctx context.Context, q string, limit int) []api.SearchResult {
	if len(strings.TrimSpace(q)) < 2 {
		return nil
	}
	if limit <= 0 {
		limit = 8
	}
	hits, err := p.s.searchText(ctx, q, "", limit)
	if err != nil {
		return nil
	}
	out := make([]api.SearchResult, 0, len(hits))
	for i, h := range hits {
		sub := h.Title
		if h.Cwd != "" {
			sub += " · " + filepath.Base(h.Cwd)
		}
		out = append(out, api.SearchResult{Scope: "history", ID: h.SessionID + "#" + h.MessageID, Title: h.Snippet,
			Subtitle: sub, Icon: h.Agent, Link: sessionLink(h.SessionID, h.MessageID), At: h.At,
			Score: 0.6 - float64(i)*0.02, Meta: map[string]string{"agent": h.Agent, "role": h.Role, "sessionId": h.SessionID}})
	}
	return out
}

// tildePath shortens paths under home to ~/….
func tildePath(p, home string) string {
	if home != "" && within(p, home) {
		if p == home {
			return "~"
		}
		return "~" + strings.TrimPrefix(p, strings.TrimSuffix(home, "/"))
	}
	return p
}
