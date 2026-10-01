// Package live serves the live event WebSocket (/api/v1/events): it streams
// bus events to browsers, tracks what each browser is looking at and
// implements core.Presence from that.
package live

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/server"
)

// Tunables.
const (
	sendQueue     = 256              // queued frames per client before it counts as slow
	pingEvery     = 25 * time.Second // keepalive (API.md)
	pingTimeout   = 15 * time.Second
	writeTimeout  = 10 * time.Second
	readLimit     = 64 << 10
	maxTopics     = 16
	snapshotLimit = 3 * time.Second
)

// gatedTopics are event types delivered only to clients that subscribed
// to the topic of the same name (high-frequency streams).
var gatedTopics = map[string]bool{api.EvMetrics: true}

var topicRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Service is the live events feature.
type Service struct {
	d    *core.Deps
	log  *slog.Logger
	info func(context.Context) api.Info

	mu      sync.RWMutex
	clients map[*client]struct{}
}

// New constructs the service. It starts no goroutines.
func New(d *core.Deps) (*Service, error) {
	log := d.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	s := &Service{d: d, log: log.With("feature", "live"), clients: map[*client]struct{}{}}
	s.info = func(context.Context) api.Info {
		return api.Info{Version: d.Version, Commit: d.Commit, StartedAt: d.StartedAt, PublicURL: d.Cfg.Origin()}
	}
	return s, nil
}

// SetInfo sets the builder for the hello payload (the same one that serves
// /api/v1/info).
func (s *Service) SetInfo(f func(context.Context) api.Info) {
	if f != nil {
		s.info = f
	}
}

// Routes registers GET /api/v1/events.
func (s *Service) Routes(rt *server.Router) {
	rt.WS("GET /api/v1/events", s.handleEvents)
}

// Start fans bus events out to connected clients until ctx is done, then
// closes every connection.
func (s *Service) Start(ctx context.Context) error {
	sub := s.d.Bus.Subscribe(1024, nil)
	defer sub.Close()
	defer s.closeAll(websocket.StatusGoingAway, "server shutting down")
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-sub.C:
			if !ok {
				return nil
			}
			s.dispatch(ev)
		}
	}
}

func (s *Service) dispatch(ev api.Event) {
	if ev.Type == core.BusSessionRevoked {
		s.dropSessions(ev.Data)
		return
	}
	if core.IsBackendTopic(ev.Type) {
		return
	}
	msg, err := json.Marshal(ev)
	if err != nil {
		s.log.Warn("event not serialisable", "type", ev.Type, "err", err)
		return
	}
	gated := gatedTopics[ev.Type]
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.clients {
		if gated && !c.visibleSubscription(ev.Type) {
			continue
		}
		if !c.enqueue(msg) {
			c.drop()
		}
	}
}

func (s *Service) dropSessions(data any) {
	var ids []string
	switch v := data.(type) {
	case core.SessionRevoked:
		ids = v.SessionIDs
	case *core.SessionRevoked:
		if v != nil {
			ids = v.SessionIDs
		}
	}
	if len(ids) == 0 {
		return
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.clients {
		if c.sessionID != "" && set[c.sessionID] {
			c.kill(websocket.StatusPolicyViolation, "signed out")
		}
	}
}

func (s *Service) closeAll(code websocket.StatusCode, reason string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.clients {
		c.kill(code, reason)
	}
}

func (s *Service) add(c *client) {
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
}

func (s *Service) remove(c *client) {
	s.mu.Lock()
	delete(s.clients, c)
	s.mu.Unlock()
}

// Clients returns the number of connected browsers.
func (s *Service) Clients() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

// --- core.Presence -------------------------------------------------------------

// Watching reports whether a visible browser shows /terminal/<terminalID>.
func (s *Service) Watching(terminalID string) bool {
	if terminalID == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.clients {
		if vis, path := c.view(); vis && terminalOf(path) == terminalID {
			return true
		}
	}
	return false
}

// Online reports whether at least one browser has the app open and visible.
func (s *Service) Online() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.clients {
		if vis, _ := c.view(); vis {
			return true
		}
	}
	return false
}

// Subscribed reports whether any visible connected browser subscribed to topic.
func (s *Service) Subscribed(topic string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.clients {
		if c.visibleSubscription(topic) {
			return true
		}
	}
	return false
}

// terminalOf extracts <id> from an app route "/terminal/<id>[/…][?…][#…]".
func terminalOf(path string) string {
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	rest, ok := strings.CutPrefix(path, "/terminal/")
	if !ok {
		return ""
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// --- connection ---------------------------------------------------------------------

type client struct {
	conn      *websocket.Conn
	send      chan []byte
	sessionID string
	cancel    context.CancelFunc

	mu      sync.Mutex
	topics  map[string]bool
	visible bool
	path    string

	killOnce sync.Once
}

func (c *client) enqueue(msg []byte) bool {
	select {
	case c.send <- msg:
		return true
	default:
		return false
	}
}

func (c *client) visibleSubscription(topic string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.visible && c.topics[topic]
}

func (c *client) view() (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.visible, c.path
}

// kill closes the connection with code (asynchronously: the close
// handshake may wait for a slow peer) and stops the client's loops.
func (c *client) kill(code websocket.StatusCode, reason string) {
	c.killOnce.Do(func() {
		go func() {
			_ = c.conn.Close(code, reason)
			c.cancel()
		}()
	})
}

// drop disconnects a client whose queue is full. A peer that is not
// reading cannot complete a close handshake either, so the socket is torn
// down at once; the browser reconnects and receives a fresh snapshot.
func (c *client) drop() {
	c.killOnce.Do(func() {
		c.cancel()
		_ = c.conn.CloseNow()
	})
}

func (s *Service) handleEvents(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	// Origin was already verified by the router for cookie principals;
	// token and local callers are not ambient-credential (CSRF) risks.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return // Accept already wrote the error response
	}
	conn.SetReadLimit(readLimit)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c := &client{conn: conn, send: make(chan []byte, sendQueue), cancel: cancel, topics: map[string]bool{}, visible: true}
	if p != nil {
		c.sessionID = p.SessionID
	}
	// Register first so events published while the snapshot is built are
	// queued behind it (newer state wins on the client).
	s.add(c)
	defer s.remove(c)

	if err := s.greet(ctx, c); err != nil {
		c.kill(websocket.StatusInternalError, "could not send initial state")
		return
	}
	go s.writeLoop(ctx, c)
	err = s.readLoop(ctx, c)
	if err != nil && !errors.Is(err, context.Canceled) && websocket.CloseStatus(err) == -1 {
		s.log.Debug("events socket closed", "err", err)
	}
	c.kill(websocket.StatusNormalClosure, "")
}

// greet writes hello and the terminal snapshot directly, before the writer
// starts draining queued events.
func (s *Service) greet(ctx context.Context, c *client) error {
	now := time.Now().UTC()
	if err := s.write(ctx, c.conn, api.Event{Type: api.EvHello, At: now, Data: s.info(ctx)}); err != nil {
		return err
	}
	if s.d.Pty == nil {
		return nil
	}
	sctx, cancel := context.WithTimeout(ctx, snapshotLimit)
	defer cancel()
	terms, err := s.d.Pty.List(sctx)
	if err != nil {
		s.log.Debug("terminal snapshot unavailable", "err", err)
		return nil
	}
	for i := range terms {
		if err := s.write(ctx, c.conn, api.Event{Type: api.EvTerminalUpdated, At: now, Data: terms[i]}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) write(ctx context.Context, conn *websocket.Conn, ev api.Event) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return writeFrame(ctx, conn, b)
}

func writeFrame(ctx context.Context, conn *websocket.Conn, b []byte) error {
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, b)
}

func (s *Service) writeLoop(ctx context.Context, c *client) {
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-c.send:
			if err := writeFrame(ctx, c.conn, msg); err != nil {
				c.kill(websocket.StatusGoingAway, "write failed")
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, pingTimeout)
			err := c.conn.Ping(pctx)
			cancel()
			if err != nil {
				c.kill(websocket.StatusGoingAway, "ping timeout")
				return
			}
		}
	}
}

func (s *Service) readLoop(ctx context.Context, c *client) error {
	for {
		typ, data, err := c.conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			continue
		}
		var ev api.ClientEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			continue // ignore malformed frames rather than dropping the socket
		}
		s.handleClientEvent(c, ev)
	}
}

func (s *Service) handleClientEvent(c *client, ev api.ClientEvent) {
	switch ev.Type {
	case "subscribe", "unsubscribe":
		c.mu.Lock()
		for _, t := range ev.Topics {
			if !topicRe.MatchString(t) {
				continue
			}
			if ev.Type == "unsubscribe" {
				delete(c.topics, t)
			} else if len(c.topics) < maxTopics {
				c.topics[t] = true
			}
		}
		c.mu.Unlock()
	case "visibility":
		c.mu.Lock()
		if ev.Visible != nil {
			c.visible = *ev.Visible
		}
		if ev.Path != "" || ev.Visible == nil {
			c.path = clipPath(ev.Path)
		}
		c.mu.Unlock()
	case "ping":
		b, _ := json.Marshal(api.Event{Type: "pong", At: time.Now().UTC()})
		if !c.enqueue(b) {
			c.drop()
		}
	}
}

func clipPath(p string) string {
	if len(p) > 1024 {
		return p[:1024]
	}
	return p
}
