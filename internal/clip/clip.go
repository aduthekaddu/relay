// Package clip is the universal clipboard shared between devices,
// terminals (OSC 52), the desktop and the CLI.
//
// Entries are kept in SQLite (the newest Retain), each at most MaxBytes.
// Adding the same text as the newest entry refreshes that entry instead of
// creating a duplicate. Every stored entry is published as api.EvClip.
package clip

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/secret"
	"github.com/aduthekaddu/relay/internal/server"
)

const (
	// Retain is how many clips are kept.
	Retain = 200
	// MaxBytes caps one clip (SECURITY.md: clipboard entries 256 KiB).
	MaxBytes = 256 << 10
)

// Sources a clip may come from.
var sources = map[string]bool{"terminal": true, "cli": true, "web": true, "osc52": true, "desktop": true}

// Service stores and serves clipboard history.
type Service struct {
	d   *core.Deps
	now func() time.Time
	mu  sync.Mutex  // serialises Add so "dedupe consecutive" is race-free
	sub *events.Sub // installed during construction, before producers start
}

// New migrates the clips table.
func New(d *core.Deps) (*Service, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Store.Migrate(ctx, "clip", []string{
		`CREATE TABLE clips (
			id TEXT PRIMARY KEY,
			text TEXT NOT NULL,
			source TEXT NOT NULL,
			at TEXT NOT NULL,
			size INTEGER NOT NULL
		)`,
		`CREATE INDEX clips_at ON clips(at DESC)`,
	}); err != nil {
		return nil, fmt.Errorf("clip: migrate: %w", err)
	}
	s := &Service{d: d, now: time.Now}
	if d.Bus != nil {
		s.sub = d.Bus.Subscribe(64, func(ev api.Event) bool { return ev.Type == core.BusClipCapture })
	}
	return s, nil
}

// Close releases the capture subscription, including when wiring fails before
// Start. It is safe to call repeatedly or alongside Start.
func (s *Service) Close() error {
	if s.sub != nil {
		s.sub.Close()
	}
	return nil
}

// Start consumes the subscription installed by New, so events can queue during
// startup before this loop is scheduled. Start is called once per service. A nil
// bus disables capture; HTTP Add still works. Cancellation releases the subscription.
func (s *Service) Start(ctx context.Context) error {
	defer s.Close()
	if s.sub == nil {
		<-ctx.Done()
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-s.sub.C:
			if !ok {
				return nil
			}
			text, source, err := decodeCapture(ev.Data)
			if err != nil {
				s.warn("clip: bad capture event", err)
				continue
			}
			if _, err := s.Add(ctx, text, source); err != nil {
				s.warn("clip: store capture", err)
			}
		}
	}
}

func (s *Service) warn(msg string, err error) {
	if s.d.Log != nil {
		s.d.Log.Warn(msg, "err", err)
	}
}

// decodeCapture uses the canonical payload and keeps the generic string,
// byte slice and JSON-compatible struct/map forms used by older producers.
func decodeCapture(data any) (text, source string, err error) {
	var c core.ClipCapture
	switch v := data.(type) {
	case core.ClipCapture:
		c = v
	case *core.ClipCapture:
		if v == nil {
			return "", "", errors.New("nil clipboard capture")
		}
		c = *v
	case string:
		return v, "terminal", nil
	case []byte:
		return string(v), "terminal", nil
	default:
		b, err := json.Marshal(data)
		if err != nil {
			return "", "", err
		}
		// The generic decoder historically consumed only Text and Source.
		// Ignore SessionID even if an older generic producer uses another type.
		var payload struct {
			*core.ClipCapture
			SessionID json.RawMessage
		}
		payload.ClipCapture = &c
		if err := json.Unmarshal(b, &payload); err != nil {
			return "", "", err
		}
	}
	if c.Source == "" {
		c.Source = "terminal"
	}
	return c.Text, c.Source, nil
}

// Add stores text. The newest entry is refreshed (not duplicated) when the
// text is identical.
func (s *Service) Add(ctx context.Context, text, source string) (*api.Clip, error) {
	if text == "" || strings.TrimSpace(text) == "" {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "text is empty", Field: "text"}
	}
	if len(text) > MaxBytes {
		return nil, &httpx.Err{Status: 413, Code: "too_large", Message: "clipboard entries are limited to 256 KiB", Field: "text"}
	}
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "\uFFFD")
		if len(text) > MaxBytes {
			return nil, &httpx.Err{Status: 413, Code: "too_large", Message: "clipboard entries are limited to 256 KiB", Field: "text"}
		}
	}
	source = strings.ToLower(strings.TrimSpace(source))
	if !sources[source] {
		source = "web"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	db := s.d.Store.DB
	now := s.now().UTC()

	var c api.Clip
	var at string
	err := db.QueryRowContext(ctx, `SELECT id,text,source,at,size FROM clips ORDER BY at DESC, rowid DESC LIMIT 1`).Scan(&c.ID, &c.Text, &c.Source, &at, &c.Size)
	switch {
	case err == nil && c.Text == text:
		c.At, c.Source = now, source
		if _, err := db.ExecContext(ctx, `UPDATE clips SET at=?, source=? WHERE id=?`, ts(now), source, c.ID); err != nil {
			return nil, fmt.Errorf("clip: refresh: %w", err)
		}
	case err == nil || errors.Is(err, sql.ErrNoRows):
		id, err := secret.Token("c_", 8)
		if err != nil {
			return nil, err
		}
		c = api.Clip{ID: id, Text: text, Source: source, At: now, Size: len(text)}
		if _, err := db.ExecContext(ctx, `INSERT INTO clips(id,text,source,at,size) VALUES(?,?,?,?,?)`, c.ID, c.Text, c.Source, ts(now), c.Size); err != nil {
			return nil, fmt.Errorf("clip: insert: %w", err)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM clips WHERE id IN (SELECT id FROM clips ORDER BY at DESC, rowid DESC LIMIT -1 OFFSET ?)`, Retain); err != nil {
			return nil, fmt.Errorf("clip: retain: %w", err)
		}
	default:
		return nil, fmt.Errorf("clip: latest: %w", err)
	}
	if s.d.Bus != nil {
		s.d.Bus.Publish(api.EvClip, c)
	}
	return &c, nil
}

// List returns the newest clips first.
func (s *Service) List(ctx context.Context, limit int) ([]api.Clip, error) {
	rows, err := s.d.Store.DB.QueryContext(ctx, `SELECT id,text,source,at,size FROM clips ORDER BY at DESC, rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("clip: list: %w", err)
	}
	defer rows.Close()
	out := []api.Clip{}
	for rows.Next() {
		var c api.Clip
		var at string
		if err := rows.Scan(&c.ID, &c.Text, &c.Source, &at, &c.Size); err != nil {
			return nil, err
		}
		c.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Delete removes one clip.
func (s *Service) Delete(ctx context.Context, id string) error {
	res, err := s.d.Store.DB.ExecContext(ctx, `DELETE FROM clips WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("clip: delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.NotFound("clip not found")
	}
	return nil
}

// Clear removes every clip.
func (s *Service) Clear(ctx context.Context) error {
	if _, err := s.d.Store.DB.ExecContext(ctx, `DELETE FROM clips`); err != nil {
		return fmt.Errorf("clip: clear: %w", err)
	}
	return nil
}

func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z07:00") }

// Routes registers /api/v1/clip.
func (s *Service) Routes(rt *server.Router) {
	rt.Handle("GET /api/v1/clip", s.handleList)
	rt.Handle("POST /api/v1/clip", s.handleAdd)
	rt.Handle("DELETE /api/v1/clip/{id}", s.handleDelete)
	rt.Handle("DELETE /api/v1/clip", s.handleClear)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := s.List(r.Context(), httpx.QueryInt(r, "limit", 50, 1, Retain))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, list)
}

type addRequest struct {
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
}

func (s *Service) handleAdd(w http.ResponseWriter, r *http.Request) {
	var req addRequest
	// JSON escaping can inflate text up to 6x; allow for it, Add enforces MaxBytes.
	if err := httpx.DecodeLimit(r, &req, 6*MaxBytes+1024); err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.Source == "" {
		req.Source = "web"
		if server.IsLocal(r.Context()) {
			req.Source = "cli"
		}
	}
	c, err := s.Add(r.Context(), req.Text, req.Source)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, c)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Delete(r.Context(), r.PathValue("id")); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Service) handleClear(w http.ResponseWriter, r *http.Request) {
	if err := s.Clear(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}
