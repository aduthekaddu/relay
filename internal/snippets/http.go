package snippets

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
)

// Routes registers /api/v1/snippets and /api/v1/notes.
func (s *Service) Routes(rt *server.Router) {
	rt.Handle("GET /api/v1/snippets", s.handleListSnippets)
	rt.Handle("POST /api/v1/snippets", s.handleCreateSnippet)
	rt.Handle("GET /api/v1/snippets/{id}", s.handleGetSnippet)
	rt.Handle("PATCH /api/v1/snippets/{id}", s.handleUpdateSnippet)
	rt.Handle("DELETE /api/v1/snippets/{id}", s.handleDeleteSnippet)
	rt.Handle("POST /api/v1/snippets/{id}/use", s.handleUseSnippet)

	rt.Handle("GET /api/v1/notes", s.handleListNotes)
	rt.Handle("POST /api/v1/notes", s.handleCreateNote)
	rt.Handle("GET /api/v1/notes/{id}", s.handleGetNote)
	rt.Handle("PATCH /api/v1/notes/{id}", s.handleUpdateNote)
	rt.Handle("DELETE /api/v1/notes/{id}", s.handleDeleteNote)
}

func respond[T any](w http.ResponseWriter, v T, err error) {
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, v)
}

func (s *Service) handleListSnippets(w http.ResponseWriter, r *http.Request) {
	v, err := s.ListSnippets(r.Context())
	respond(w, v, err)
}

func (s *Service) handleGetSnippet(w http.ResponseWriter, r *http.Request) {
	v, err := s.GetSnippet(r.Context(), r.PathValue("id"))
	respond(w, v, err)
}

func (s *Service) handleCreateSnippet(w http.ResponseWriter, r *http.Request) {
	var p snippetPatch
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Fail(w, err)
		return
	}
	v, err := s.CreateSnippet(r.Context(), p)
	respond(w, v, err)
}

func (s *Service) handleUpdateSnippet(w http.ResponseWriter, r *http.Request) {
	var p snippetPatch
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Fail(w, err)
		return
	}
	v, err := s.UpdateSnippet(r.Context(), r.PathValue("id"), p)
	respond(w, v, err)
}

func (s *Service) handleDeleteSnippet(w http.ResponseWriter, r *http.Request) {
	if err := s.DeleteSnippet(r.Context(), r.PathValue("id")); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Service) handleUseSnippet(w http.ResponseWriter, r *http.Request) {
	v, err := s.UseSnippet(r.Context(), r.PathValue("id"))
	respond(w, v, err)
}

func (s *Service) handleListNotes(w http.ResponseWriter, r *http.Request) {
	v, err := s.ListNotes(r.Context())
	respond(w, v, err)
}

func (s *Service) handleGetNote(w http.ResponseWriter, r *http.Request) {
	v, err := s.GetNote(r.Context(), r.PathValue("id"))
	respond(w, v, err)
}

func (s *Service) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	var p notePatch
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Fail(w, err)
		return
	}
	v, err := s.CreateNote(r.Context(), p)
	respond(w, v, err)
}

func (s *Service) handleUpdateNote(w http.ResponseWriter, r *http.Request) {
	var p notePatch
	if err := httpx.Decode(r, &p); err != nil {
		httpx.Fail(w, err)
		return
	}
	v, err := s.UpdateNote(r.Context(), r.PathValue("id"), p)
	respond(w, v, err)
}

func (s *Service) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	if err := s.DeleteNote(r.Context(), r.PathValue("id")); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

// ---------------------------------------------------------------------------
// Command center providers

// SnippetProvider returns the "snippets" search provider.
func (s *Service) SnippetProvider() core.SearchProvider { return snippetProvider{s} }

// NoteProvider returns the "notes" search provider.
func (s *Service) NoteProvider() core.SearchProvider { return noteProvider{s} }

type snippetProvider struct{ s *Service }

func (snippetProvider) Scope() string { return "snippets" }

// candidateLimit bounds rows scored in Go per query.
const candidateLimit = 200

func (p snippetProvider) Search(ctx context.Context, query string, limit int) []api.SearchResult {
	where, args := likeClause(query, "name", "body", "tags")
	rows, err := p.s.d.Store.DB.QueryContext(ctx, `SELECT `+snippetCols+` FROM snippets`+where+` ORDER BY uses DESC, updated_at DESC LIMIT ?`, append(args, candidateLimit)...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []api.SearchResult
	for rows.Next() {
		sn, err := scanSnippet(rows)
		if err != nil {
			return out
		}
		score := matchScore(query, sn.Name, sn.Body+" "+strings.Join(sn.Tags, " "))
		if score <= 0 {
			continue
		}
		boost := float64(min(sn.Uses, 50)) / 500
		meta := map[string]string{"kind": sn.Kind}
		if vars := Variables(sn.Body); len(vars) > 0 {
			meta["variables"] = strings.Join(vars, ",")
		}
		if sn.Agent != "" {
			meta["agent"] = sn.Agent
		}
		out = append(out, api.SearchResult{
			Scope:    "snippets",
			ID:       sn.ID,
			Title:    sn.Name,
			Subtitle: firstLine(sn.Body, 100),
			Icon:     sn.Kind,
			Score:    min(score+boost, 1),
			At:       sn.UpdatedAt,
			Meta:     meta,
		})
	}
	return topN(out, limit)
}

type noteProvider struct{ s *Service }

func (noteProvider) Scope() string { return "notes" }

func (p noteProvider) Search(ctx context.Context, query string, limit int) []api.SearchResult {
	where, args := likeClause(query, "title", "text")
	rows, err := p.s.d.Store.DB.QueryContext(ctx, `SELECT `+noteCols+` FROM notes`+where+` ORDER BY updated_at DESC LIMIT ?`, append(args, candidateLimit)...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []api.SearchResult
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return out
		}
		score := matchScore(query, n.Title, n.Text)
		if score <= 0 {
			continue
		}
		out = append(out, api.SearchResult{
			Scope:    "notes",
			ID:       n.ID,
			Title:    n.Title,
			Subtitle: excerpt(n.Text, query, 100),
			Icon:     "note",
			Score:    score,
			At:       n.UpdatedAt,
		})
	}
	return topN(out, limit)
}

// likeClause builds " WHERE (c1 LIKE ? OR c2 LIKE ?) AND (...)" requiring
// every query word to appear in some column. Empty query: no filter.
func likeClause(query string, cols ...string) (string, []any) {
	words := strings.Fields(query)
	if len(words) == 0 {
		return "", nil
	}
	if len(words) > 8 {
		words = words[:8]
	}
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	var parts []string
	var args []any
	for _, w := range words {
		var ors []string
		pat := "%" + esc.Replace(w) + "%"
		for _, c := range cols {
			ors = append(ors, c+` LIKE ? ESCAPE '\'`)
			args = append(args, pat)
		}
		parts = append(parts, "("+strings.Join(ors, " OR ")+")")
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

// matchScore rates how well query matches a title (strong) or body (weak),
// in (0, 1]. Empty queries score a small constant so recents still list.
func matchScore(query, title, body string) float64 {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return 0.1
	}
	t := strings.ToLower(title)
	switch {
	case t == q:
		return 1
	case strings.HasPrefix(t, q):
		return 0.9
	case wordBoundary(t, q):
		return 0.75
	case strings.Contains(t, q):
		return 0.6
	}
	words := strings.Fields(q)
	all := strings.ToLower(title + " " + body)
	inTitle := 0
	for _, w := range words {
		if !strings.Contains(all, w) {
			return 0
		}
		if strings.Contains(t, w) {
			inTitle++
		}
	}
	return 0.3 + 0.25*float64(inTitle)/float64(len(words))
}

// wordBoundary reports whether q occurs in s at the start of a word.
func wordBoundary(s, q string) bool {
	for i := strings.Index(s, q); i >= 0; {
		if i == 0 {
			return true
		}
		prev, _ := utf8.DecodeLastRuneInString(s[:i])
		if !unicode.IsLetter(prev) && !unicode.IsDigit(prev) {
			return true
		}
		next := strings.Index(s[i+1:], q)
		if next < 0 {
			return false
		}
		i += 1 + next
	}
	return false
}

func firstLine(s string, n int) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return cut(line, n)
		}
	}
	return ""
}

// excerpt returns text around the first query word, or the first line.
func excerpt(text, query string, n int) string {
	lower := strings.ToLower(text)
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if i := strings.Index(lower, w); i >= 0 {
			start := strings.LastIndex(text[:i], "\n") + 1
			if i-start > n/2 {
				start = i - n/2
				for start < i && !utf8.RuneStart(text[start]) {
					start++
				}
			}
			line := text[start:]
			if j := strings.IndexByte(line, '\n'); j >= 0 {
				line = line[:j]
			}
			return cut(strings.TrimSpace(line), n)
		}
	}
	return firstLine(text, n)
}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// topN keeps the best limit results (stable for equal scores).
func topN(rs []api.SearchResult, limit int) []api.SearchResult {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Score > rs[j].Score })
	if limit > 0 && len(rs) > limit {
		rs = rs[:limit]
	}
	return rs
}
