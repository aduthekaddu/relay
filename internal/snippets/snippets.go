// Package snippets stores reusable prompts/commands (snippets, which may
// contain {{variables}}) and scratchpad notes, and contributes both to the
// command center search.
package snippets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/secret"
)

// Limits.
const (
	MaxSnippets   = 2000
	MaxNotes      = 2000
	maxName       = 120
	maxSnippet    = 64 << 10
	maxNoteText   = 512 << 10
	maxTags       = 20
	maxTag        = 32
	maxAgentField = 32
)

// Service implements snippets and notes.
type Service struct {
	d   *core.Deps
	now func() time.Time
}

// New migrates the snippets and notes tables.
func New(d *core.Deps) (*Service, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Store.Migrate(ctx, "snippets", []string{
		`CREATE TABLE snippets (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			body TEXT NOT NULL,
			kind TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '[]',
			agent TEXT NOT NULL DEFAULT '',
			uses INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			used_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE notes (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			text TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX notes_updated ON notes(updated_at DESC)`,
	}); err != nil {
		return nil, fmt.Errorf("snippets: migrate: %w", err)
	}
	return &Service{d: d, now: time.Now}, nil
}

const tsLayout = "2006-01-02T15:04:05.000000000Z07:00"

func ts(t time.Time) string { return t.UTC().Format(tsLayout) }

func parseTS(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

var varRe = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_.-]{0,63})\s*\}\}`)

// Variables returns the distinct {{variable}} names in body, in order of
// first appearance. "{{ name }}" and "{{name}}" are the same variable.
func Variables(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range varRe.FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// Expand replaces {{variables}} with values; unknown variables are kept.
func Expand(body string, values map[string]string) string {
	return varRe.ReplaceAllStringFunc(body, func(m string) string {
		name := varRe.FindStringSubmatch(m)[1]
		if v, ok := values[name]; ok {
			return v
		}
		return m
	})
}

// ---------------------------------------------------------------------------
// Snippets

// snippetPatch is Partial<Snippet>; read-only fields are accepted and ignored.
type snippetPatch struct {
	Name      *string   `json:"name"`
	Body      *string   `json:"body"`
	Kind      *string   `json:"kind"`
	Tags      *[]string `json:"tags"`
	Agent     *string   `json:"agent"`
	ID        *string   `json:"id"`
	UpdatedAt *string   `json:"updatedAt"`
	Uses      *int      `json:"uses"`
}

func (p snippetPatch) apply(sn *api.Snippet) error {
	if p.Name != nil {
		sn.Name = strings.TrimSpace(*p.Name)
	}
	if p.Body != nil {
		sn.Body = *p.Body
	}
	if p.Kind != nil {
		sn.Kind = strings.ToLower(strings.TrimSpace(*p.Kind))
	}
	if p.Tags != nil {
		sn.Tags = *p.Tags
	}
	if p.Agent != nil {
		sn.Agent = strings.TrimSpace(*p.Agent)
	}
	return validateSnippet(sn)
}

func validateSnippet(sn *api.Snippet) error {
	if sn.Name == "" {
		return field("name", "name is required")
	}
	if utf8.RuneCountInString(sn.Name) > maxName {
		return field("name", "name is too long")
	}
	if strings.TrimSpace(sn.Body) == "" {
		return field("body", "body is required")
	}
	if len(sn.Body) > maxSnippet {
		return field("body", "snippets are limited to 64 KiB")
	}
	if sn.Kind == "" {
		sn.Kind = "prompt"
	}
	if sn.Kind != "prompt" && sn.Kind != "command" {
		return field("kind", "kind must be prompt or command")
	}
	if len(sn.Agent) > maxAgentField {
		return field("agent", "agent id is too long")
	}
	tags, err := cleanTags(sn.Tags)
	if err != nil {
		return err
	}
	sn.Tags = tags
	return nil
}

func cleanTags(in []string) ([]string, error) {
	if len(in) > maxTags {
		return nil, field("tags", "at most 20 tags")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		if utf8.RuneCountInString(t) > maxTag {
			return nil, field("tags", "tags are limited to 32 characters")
		}
		seen[t] = true
		out = append(out, t)
	}
	return out, nil
}

func field(name, msg string) *httpx.Err {
	return &httpx.Err{Status: 400, Code: "bad_request", Message: msg, Field: name}
}

const snippetCols = `id,name,body,kind,tags,agent,uses,updated_at`

func scanSnippet(sc interface{ Scan(...any) error }) (api.Snippet, error) {
	var sn api.Snippet
	var tags, updated string
	if err := sc.Scan(&sn.ID, &sn.Name, &sn.Body, &sn.Kind, &tags, &sn.Agent, &sn.Uses, &updated); err != nil {
		return sn, err
	}
	_ = json.Unmarshal([]byte(tags), &sn.Tags)
	sn.UpdatedAt = parseTS(updated)
	return sn, nil
}

// ListSnippets returns snippets, most used first.
func (s *Service) ListSnippets(ctx context.Context) ([]api.Snippet, error) {
	rows, err := s.d.Store.DB.QueryContext(ctx, `SELECT `+snippetCols+` FROM snippets ORDER BY uses DESC, updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("snippets: list: %w", err)
	}
	defer rows.Close()
	out := []api.Snippet{}
	for rows.Next() {
		sn, err := scanSnippet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

// GetSnippet returns one snippet.
func (s *Service) GetSnippet(ctx context.Context, id string) (*api.Snippet, error) {
	sn, err := scanSnippet(s.d.Store.DB.QueryRowContext(ctx, `SELECT `+snippetCols+` FROM snippets WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("snippet not found")
	}
	if err != nil {
		return nil, fmt.Errorf("snippets: get: %w", err)
	}
	return &sn, nil
}

// CreateSnippet validates and stores a new snippet.
func (s *Service) CreateSnippet(ctx context.Context, p snippetPatch) (*api.Snippet, error) {
	sn := api.Snippet{}
	if err := p.apply(&sn); err != nil {
		return nil, err
	}
	var n int
	if err := s.d.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM snippets`).Scan(&n); err != nil {
		return nil, err
	}
	if n >= MaxSnippets {
		return nil, httpx.Conflict("too many snippets")
	}
	id, err := secret.Token("sn_", 8)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	sn.ID, sn.UpdatedAt = id, now
	tags, _ := json.Marshal(sn.Tags)
	if _, err := s.d.Store.DB.ExecContext(ctx, `INSERT INTO snippets(id,name,body,kind,tags,agent,uses,created_at,updated_at) VALUES(?,?,?,?,?,?,0,?,?)`,
		sn.ID, sn.Name, sn.Body, sn.Kind, string(tags), sn.Agent, ts(now), ts(now)); err != nil {
		return nil, fmt.Errorf("snippets: create: %w", err)
	}
	return &sn, nil
}

// UpdateSnippet applies a partial update.
func (s *Service) UpdateSnippet(ctx context.Context, id string, p snippetPatch) (*api.Snippet, error) {
	sn, err := s.GetSnippet(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := p.apply(sn); err != nil {
		return nil, err
	}
	sn.UpdatedAt = s.now().UTC()
	tags, _ := json.Marshal(sn.Tags)
	if _, err := s.d.Store.DB.ExecContext(ctx, `UPDATE snippets SET name=?,body=?,kind=?,tags=?,agent=?,updated_at=? WHERE id=?`,
		sn.Name, sn.Body, sn.Kind, string(tags), sn.Agent, ts(sn.UpdatedAt), id); err != nil {
		return nil, fmt.Errorf("snippets: update: %w", err)
	}
	return sn, nil
}

// DeleteSnippet removes a snippet.
func (s *Service) DeleteSnippet(ctx context.Context, id string) error {
	return deleteRow(ctx, s.d.Store.DB, "snippets", id, "snippet not found")
}

// UseSnippet increments the use counter (ranking) and returns the snippet.
func (s *Service) UseSnippet(ctx context.Context, id string) (*api.Snippet, error) {
	res, err := s.d.Store.DB.ExecContext(ctx, `UPDATE snippets SET uses=uses+1, used_at=? WHERE id=?`, ts(s.now()), id)
	if err != nil {
		return nil, fmt.Errorf("snippets: use: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, httpx.NotFound("snippet not found")
	}
	return s.GetSnippet(ctx, id)
}

func deleteRow(ctx context.Context, db *sql.DB, table, id, notFound string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM `+table+` WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("%s: delete: %w", table, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.NotFound(notFound)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Notes

type notePatch struct {
	Title     *string `json:"title"`
	Text      *string `json:"text"`
	ID        *string `json:"id"`
	UpdatedAt *string `json:"updatedAt"`
}

func (p notePatch) apply(n *api.Note) error {
	if p.Title != nil {
		n.Title = strings.TrimSpace(*p.Title)
	}
	if p.Text != nil {
		n.Text = *p.Text
	}
	if len(n.Text) > maxNoteText {
		return field("text", "notes are limited to 512 KiB")
	}
	if n.Title == "" {
		n.Title = deriveTitle(n.Text)
	}
	if utf8.RuneCountInString(n.Title) > maxName {
		n.Title = string([]rune(n.Title)[:maxName-1]) + "…"
	}
	return nil
}

// deriveTitle uses the first non-empty line (markdown heading marks
// stripped) as a title, or "Untitled".
func deriveTitle(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#>-*"))
		if line != "" {
			if utf8.RuneCountInString(line) > 80 {
				line = string([]rune(line)[:79]) + "…"
			}
			return line
		}
	}
	return "Untitled"
}

const noteCols = `id,title,text,updated_at`

func scanNote(sc interface{ Scan(...any) error }) (api.Note, error) {
	var n api.Note
	var updated string
	if err := sc.Scan(&n.ID, &n.Title, &n.Text, &updated); err != nil {
		return n, err
	}
	n.UpdatedAt = parseTS(updated)
	return n, nil
}

// ListNotes returns notes, most recently edited first.
func (s *Service) ListNotes(ctx context.Context) ([]api.Note, error) {
	rows, err := s.d.Store.DB.QueryContext(ctx, `SELECT `+noteCols+` FROM notes ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("notes: list: %w", err)
	}
	defer rows.Close()
	out := []api.Note{}
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// GetNote returns one note.
func (s *Service) GetNote(ctx context.Context, id string) (*api.Note, error) {
	n, err := scanNote(s.d.Store.DB.QueryRowContext(ctx, `SELECT `+noteCols+` FROM notes WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("note not found")
	}
	if err != nil {
		return nil, fmt.Errorf("notes: get: %w", err)
	}
	return &n, nil
}

// CreateNote stores a new note (an empty note is allowed: a fresh scratchpad).
func (s *Service) CreateNote(ctx context.Context, p notePatch) (*api.Note, error) {
	n := api.Note{}
	if err := p.apply(&n); err != nil {
		return nil, err
	}
	var count int
	if err := s.d.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM notes`).Scan(&count); err != nil {
		return nil, err
	}
	if count >= MaxNotes {
		return nil, httpx.Conflict("too many notes")
	}
	id, err := secret.Token("nt_", 8)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	n.ID, n.UpdatedAt = id, now
	if _, err := s.d.Store.DB.ExecContext(ctx, `INSERT INTO notes(id,title,text,created_at,updated_at) VALUES(?,?,?,?,?)`, n.ID, n.Title, n.Text, ts(now), ts(now)); err != nil {
		return nil, fmt.Errorf("notes: create: %w", err)
	}
	return &n, nil
}

// UpdateNote applies a partial update. An explicit empty title re-derives
// it from the text.
func (s *Service) UpdateNote(ctx context.Context, id string, p notePatch) (*api.Note, error) {
	n, err := s.GetNote(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.Text != nil && p.Title == nil && n.Title == deriveTitle(n.Text) {
		// The title was derived; keep following the first line.
		n.Title = ""
	}
	if err := p.apply(n); err != nil {
		return nil, err
	}
	n.UpdatedAt = s.now().UTC()
	if _, err := s.d.Store.DB.ExecContext(ctx, `UPDATE notes SET title=?, text=?, updated_at=? WHERE id=?`, n.Title, n.Text, ts(n.UpdatedAt), id); err != nil {
		return nil, fmt.Errorf("notes: update: %w", err)
	}
	return n, nil
}

// DeleteNote removes a note.
func (s *Service) DeleteNote(ctx context.Context, id string) error {
	return deleteRow(ctx, s.d.Store.DB, "notes", id, "note not found")
}
