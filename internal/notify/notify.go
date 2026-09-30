// Package notify delivers notifications: the in-app inbox, Web Push, ntfy
// and generic webhooks.
//
// Notify stores the notification in the inbox, publishes it on the event
// bus and returns immediately; external delivery happens on a background
// worker fed by a bounded queue, so callers (terminal events, hooks, the
// CLI) never wait on the network. See docs/dev/NOTIFY.md.
package notify

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/secret"
)

// Retention and size limits.
const (
	// InboxRetain is how many notifications the inbox keeps.
	InboxRetain = 500
	// DedupeWindow suppresses repeats of the same session+kind.
	DedupeWindow = 30 * time.Second
	// QueueSize bounds the delivery queue; overflow is dropped (and logged)
	// rather than blocking Notify.
	QueueSize = 256

	maxTitle = 200
	maxBody  = 4000
	maxLink  = 2048
)

// Known notification kinds. Unknown kinds are stored as "custom".
var knownKinds = []string{"attention", "done", "exited", "preview", "security", "system", "schedule", "custom"}

// Presence reports whether some device is currently looking at a terminal
// session. It is implemented by internal/live; nil means "nobody".
type Presence interface {
	Watching(sessionID string) bool
}

// Service is the notifier. It implements core.Notifier.
type Service struct {
	d        *core.Deps
	presence func() Presence
	now      func() time.Time
	client   *http.Client // outbound (push, ntfy, webhook); per-request timeouts via ctx

	queue chan job

	mu     sync.Mutex
	recent map[string]recentEntry // dedupe key -> last notification
	cached *settings              // nil until first load

	subs atomic.Int64 // push subscription count (hot-path check)

	vapidPublic  string
	vapidPrivate string
}

type recentEntry struct {
	at time.Time
	n  api.Notification
}

// job is one notification queued for external delivery.
type job struct {
	n        api.Notification
	settings settings
}

// Option customises a Service (tests, wiring).
type Option func(*Service)

// WithPresence sets the presence lookup used for smart suppression. The
// function is called on every notification, so late-wired services work.
func WithPresence(f func() Presence) Option { return func(s *Service) { s.presence = f } }

// WithClock overrides the clock (tests).
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithHTTPClient overrides the outbound HTTP client (tests).
func WithHTTPClient(c *http.Client) Option { return func(s *Service) { s.client = c } }

// New migrates the notify tables and loads (or generates) the VAPID keys.
// It starts no goroutines; call Start for delivery.
func New(d *core.Deps, opts ...Option) (*Service, error) {
	s := &Service{
		d:      d,
		now:    time.Now,
		client: &http.Client{Timeout: 15 * time.Second},
		queue:  make(chan job, QueueSize),
		recent: map[string]recentEntry{},
	}
	for _, o := range opts {
		o(s)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Store.Migrate(ctx, "notify", migrations); err != nil {
		return nil, fmt.Errorf("notify: migrate: %w", err)
	}
	if err := s.loadVAPID(ctx); err != nil {
		return nil, fmt.Errorf("notify: vapid keys: %w", err)
	}
	if _, err := s.countSubscriptions(ctx); err != nil {
		return nil, err
	}
	if _, err := s.loadSettings(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

var migrations = []string{
	`CREATE TABLE notifications (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		title TEXT NOT NULL,
		body TEXT NOT NULL DEFAULT '',
		at TEXT NOT NULL,
		read INTEGER NOT NULL DEFAULT 0,
		link TEXT NOT NULL DEFAULT '',
		session_id TEXT NOT NULL DEFAULT '',
		agent TEXT NOT NULL DEFAULT '',
		severity TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX notifications_at ON notifications(at DESC)`,
	`CREATE TABLE push_subscriptions (
		endpoint TEXT PRIMARY KEY,
		p256dh TEXT NOT NULL,
		auth TEXT NOT NULL,
		device TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		last_ok_at TEXT NOT NULL DEFAULT '',
		failures INTEGER NOT NULL DEFAULT 0
	)`,
}

// Start runs the delivery worker until ctx is done.
func (s *Service) Start(ctx context.Context) error {
	prune := time.NewTicker(time.Minute)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case j := <-s.queue:
			s.deliver(ctx, j)
		case <-prune.C:
			s.pruneRecent()
		}
	}
}

// Notify stores req in the inbox, publishes it and queues external
// delivery. It never waits on the network.
func (s *Service) Notify(ctx context.Context, req api.NotifyRequest) (*api.Notification, error) {
	n, err := normalize(req)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	key := dedupeKey(n)
	s.mu.Lock()
	if prev, ok := s.recent[key]; ok && now.Sub(prev.at) < DedupeWindow {
		s.mu.Unlock()
		p := prev.n
		return &p, nil
	}
	id, err := secret.Token("n_", 8)
	if err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("notify: id: %w", err)
	}
	n.ID = id
	n.At = now
	s.recent[key] = recentEntry{at: now, n: n}
	s.mu.Unlock()

	if err := s.insert(ctx, n); err != nil {
		s.mu.Lock()
		delete(s.recent, key)
		s.mu.Unlock()
		return nil, err
	}
	if s.d.Bus != nil {
		s.d.Bus.Publish(api.EvNotification, n)
	}

	st, err := s.loadSettings(ctx)
	if err != nil {
		s.log().Warn("notify: load settings", "err", err)
		st = s.defaultSettings()
	}
	if s.shouldDeliver(n, st, now) {
		s.enqueue(job{n: n, settings: st})
	}
	return &n, nil
}

// shouldDeliver applies per-kind rules, quiet hours and presence
// suppression. The inbox always receives the notification regardless.
func (s *Service) shouldDeliver(n api.Notification, st settings, now time.Time) bool {
	if on, ok := st.Rules[n.Kind]; ok && !on {
		return false
	}
	if n.Kind != "attention" && n.Kind != "security" && st.inQuietHours(now) {
		return false
	}
	if n.SessionID != "" && s.presence != nil {
		if p := s.presence(); p != nil && p.Watching(n.SessionID) {
			return false
		}
	}
	return st.hasChannel(s.hasSubscriptions())
}

func (s *Service) enqueue(j job) bool {
	select {
	case s.queue <- j:
		return true
	default:
		s.log().Warn("notify: delivery queue full, dropping", "kind", j.n.Kind)
		return false
	}
}

func (s *Service) pruneRecent() {
	cut := s.now().Add(-DedupeWindow)
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.recent {
		if v.at.Before(cut) {
			delete(s.recent, k)
		}
	}
}

func (s *Service) log() logger {
	if s.d.Log != nil {
		return s.d.Log
	}
	return nopLogger{}
}

type logger interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Warn(string, ...any) {}
func (nopLogger) Info(string, ...any) {}

// dedupeKey identifies "the same notification again": same session and
// kind, or — without a session — same kind, title and body.
func dedupeKey(n api.Notification) string {
	if n.SessionID != "" {
		return n.Kind + "\x00s\x00" + n.SessionID
	}
	return n.Kind + "\x00t\x00" + n.Title + "\x00" + n.Body
}

// normalize validates and fills defaults.
func normalize(req api.NotifyRequest) (api.Notification, error) {
	title := strings.TrimSpace(req.Title)
	body := strings.TrimSpace(req.Body)
	if title == "" {
		if body == "" {
			return api.Notification{}, &httpx.Err{Status: 400, Code: "bad_request", Message: "title or body is required", Field: "title"}
		}
		title, body, _ = strings.Cut(body, "\n")
		title, body = strings.TrimSpace(title), strings.TrimSpace(body)
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if !isKnownKind(kind) {
		kind = "custom"
	}
	link := strings.TrimSpace(req.Link)
	sid := strings.TrimSpace(req.SessionID)
	if len(sid) > 128 {
		return api.Notification{}, &httpx.Err{Status: 400, Code: "bad_request", Message: "session id too long", Field: "sessionId"}
	}
	if link == "" && sid != "" {
		link = "/terminal/" + url.PathEscape(sid)
	}
	if link != "" && !validLink(link) {
		return api.Notification{}, &httpx.Err{Status: 400, Code: "bad_request", Message: "link must be an in-app path starting with /", Field: "link"}
	}
	sev := strings.ToLower(strings.TrimSpace(req.Severity))
	switch sev {
	case "info", "success", "warning", "danger":
	case "":
		sev = defaultSeverity(kind)
	default:
		return api.Notification{}, &httpx.Err{Status: 400, Code: "bad_request", Message: "severity must be info, success, warning or danger", Field: "severity"}
	}
	return api.Notification{
		Kind:      kind,
		Title:     truncate(title, maxTitle),
		Body:      truncate(body, maxBody),
		Link:      link,
		SessionID: sid,
		Agent:     truncate(strings.TrimSpace(req.Agent), 64),
		Severity:  sev,
	}, nil
}

func isKnownKind(k string) bool {
	for _, x := range knownKinds {
		if x == k {
			return true
		}
	}
	return false
}

func defaultSeverity(kind string) string {
	switch kind {
	case "attention":
		return "warning"
	case "security":
		return "danger"
	case "done":
		return "success"
	}
	return "info"
}

// validLink accepts in-app paths only: "/x", never "//host" or "/\host".
func validLink(l string) bool {
	if len(l) > maxLink || !strings.HasPrefix(l, "/") || strings.HasPrefix(l, "//") || strings.ContainsAny(l, "\\\r\n\t") {
		return false
	}
	u, err := url.Parse(l)
	return err == nil && u.Scheme == "" && u.Host == ""
}

// truncate cuts s to at most n runes, appending an ellipsis when cut.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// ---------------------------------------------------------------------------
// Inbox storage

const tsLayout = "2006-01-02T15:04:05.000000000Z07:00"

func ts(t time.Time) string { return t.UTC().Format(tsLayout) }

func parseTS(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func (s *Service) insert(ctx context.Context, n api.Notification) error {
	db := s.d.Store.DB
	if _, err := db.ExecContext(ctx, `INSERT INTO notifications(id,kind,title,body,at,read,link,session_id,agent,severity) VALUES(?,?,?,?,?,0,?,?,?,?)`,
		n.ID, n.Kind, n.Title, n.Body, ts(n.At), n.Link, n.SessionID, n.Agent, n.Severity); err != nil {
		return fmt.Errorf("notify: insert: %w", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM notifications WHERE id IN (SELECT id FROM notifications ORDER BY at DESC, rowid DESC LIMIT -1 OFFSET ?)`, InboxRetain); err != nil {
		return fmt.Errorf("notify: retain: %w", err)
	}
	return nil
}

// List returns the newest notifications first.
func (s *Service) List(ctx context.Context, limit int, unreadOnly bool) ([]api.Notification, error) {
	q := `SELECT id,kind,title,body,at,read,link,session_id,agent,severity FROM notifications`
	if unreadOnly {
		q += ` WHERE read=0`
	}
	q += ` ORDER BY at DESC, rowid DESC LIMIT ?`
	rows, err := s.d.Store.DB.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("notify: list: %w", err)
	}
	defer rows.Close()
	out := []api.Notification{}
	for rows.Next() {
		var n api.Notification
		var at string
		var read int
		if err := rows.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &at, &read, &n.Link, &n.SessionID, &n.Agent, &n.Severity); err != nil {
			return nil, fmt.Errorf("notify: scan: %w", err)
		}
		n.At = parseTS(at)
		n.Read = read != 0
		out = append(out, n)
	}
	return out, rows.Err()
}

// Unread returns the number of unread notifications.
func (s *Service) Unread(ctx context.Context) (int, error) {
	var n int
	err := s.d.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE read=0`).Scan(&n)
	return n, err
}

// MarkRead marks ids (or every notification when all) as read and returns
// the ids that changed.
func (s *Service) MarkRead(ctx context.Context, ids []string, all bool) ([]string, error) {
	db := s.d.Store.DB
	var changed []string
	if all {
		rows, err := db.QueryContext(ctx, `SELECT id FROM notifications WHERE read=0`)
		if err != nil {
			return nil, fmt.Errorf("notify: read: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			changed = append(changed, id)
		}
		rows.Close()
		if _, err := db.ExecContext(ctx, `UPDATE notifications SET read=1 WHERE read=0`); err != nil {
			return nil, fmt.Errorf("notify: read: %w", err)
		}
	} else {
		for _, id := range ids {
			res, err := db.ExecContext(ctx, `UPDATE notifications SET read=1 WHERE id=? AND read=0`, id)
			if err != nil {
				return nil, fmt.Errorf("notify: read: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				changed = append(changed, id)
			}
		}
	}
	if changed == nil {
		changed = []string{}
	}
	if s.d.Bus != nil && len(changed) > 0 {
		s.d.Bus.Publish(api.EvNotificationRead, map[string]any{"ids": changed, "all": all})
	}
	return changed, nil
}

// Delete removes one notification.
func (s *Service) Delete(ctx context.Context, id string) error {
	res, err := s.d.Store.DB.ExecContext(ctx, `DELETE FROM notifications WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("notify: delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.NotFound("notification not found")
	}
	return nil
}
