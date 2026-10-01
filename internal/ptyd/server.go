package ptyd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/version"
)

// maxInputBody caps one input request (a paste).
const maxInputBody = 1 << 20

// patchRequest is the body of PATCH /v1/sessions/{id}.
type patchRequest struct {
	Name   *string           `json:"name,omitempty"`
	Pinned *bool             `json:"pinned,omitempty"`
	Meta   map[string]string `json:"meta,omitempty"`
}

// Handler returns the daemon's HTTP routes (without the peer check that
// Run adds; tests use it directly).
func (d *Daemon) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", d.hHealth)
	mux.HandleFunc("GET /v1/settings", d.hSettings)
	mux.HandleFunc("GET /v1/sessions", d.hList)
	mux.HandleFunc("POST /v1/sessions", d.hCreate)
	mux.HandleFunc("GET /v1/sessions/{id}", d.withSession(d.hGet))
	mux.HandleFunc("PATCH /v1/sessions/{id}", d.hPatch)
	mux.HandleFunc("DELETE /v1/sessions/{id}", d.hDelete)
	mux.HandleFunc("POST /v1/sessions/{id}/input", d.withSession(d.hInput))
	mux.HandleFunc("POST /v1/sessions/{id}/resize", d.withSession(d.hResize))
	mux.HandleFunc("POST /v1/sessions/{id}/attention", d.withSession(d.hAttention))
	mux.HandleFunc("POST /v1/sessions/{id}/restore", d.hRestore)
	mux.HandleFunc("GET /v1/sessions/{id}/snapshot", d.withSession(d.hSnapshot))
	mux.HandleFunc("GET /v1/sessions/{id}/recording", d.withSession(d.hRecording))
	mux.HandleFunc("GET /v1/sessions/{id}/attach", d.withSession(d.hAttach))
	mux.HandleFunc("GET /v1/events", d.hEvents)
	return mux
}

func (d *Daemon) withSession(h func(http.ResponseWriter, *http.Request, *Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := d.get(r.PathValue("id"))
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		h(w, r, s)
	}
}

func (d *Daemon) hHealth(w http.ResponseWriter, _ *http.Request) {
	d.mu.RLock()
	n := len(d.sessions)
	d.mu.RUnlock()
	httpx.OK(w, map[string]any{"ok": true, "version": version.Version, "sessions": n})
}

func (d *Daemon) hList(w http.ResponseWriter, _ *http.Request) { httpx.OK(w, d.List()) }

func (d *Daemon) hCreate(w http.ResponseWriter, r *http.Request) {
	var spec ptyclient.CreateSpec
	if err := httpx.Decode(r, &spec); err != nil {
		httpx.Fail(w, err)
		return
	}
	info, err := d.Create(spec)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, info)
}

func (d *Daemon) hGet(w http.ResponseWriter, _ *http.Request, s *Session) { httpx.OK(w, s.Info()) }

func (d *Daemon) hPatch(w http.ResponseWriter, r *http.Request) {
	var req patchRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	info, err := d.Update(r.PathValue("id"), req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, info)
}

func (d *Daemon) hDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if httpx.QueryBool(r, "forget") {
		if err := d.Remove(r.Context(), id); err != nil {
			httpx.Fail(w, err)
			return
		}
		httpx.NoContent(w)
		return
	}
	s, err := d.get(id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	sig, ok := parseSignal(r.URL.Query().Get("signal"))
	if !ok {
		httpx.Fail(w, httpx.BadRequest("unknown signal"))
		return
	}
	s.Kill(sig)
	httpx.NoContent(w)
}

func (d *Daemon) hInput(w http.ResponseWriter, r *http.Request, s *Session) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxInputBody))
	if err != nil {
		httpx.Fail(w, &httpx.Err{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: "input too large"})
		return
	}
	if httpx.QueryBool(r, "paste") {
		err = s.Paste(nil, data)
	} else {
		err = s.Input(nil, data)
	}
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

type sizeRequest struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

func (d *Daemon) hResize(w http.ResponseWriter, r *http.Request, s *Session) {
	var req sizeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.Cols <= 0 || req.Rows <= 0 {
		httpx.Fail(w, httpx.BadRequest("cols and rows must be positive"))
		return
	}
	_ = s.Resize(nil, req.Cols, req.Rows)
	httpx.NoContent(w)
}

func (d *Daemon) hAttention(w http.ResponseWriter, r *http.Request, s *Session) {
	var a *api.Attention
	if err := httpx.Decode(r, &a); err != nil {
		httpx.Fail(w, err)
		return
	}
	if a != nil && a.Reason == "" {
		a.Reason = "hook"
	}
	s.SetAttention(a)
	httpx.NoContent(w)
}

func (d *Daemon) hRestore(w http.ResponseWriter, r *http.Request) {
	info, err := d.Restore(r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, info)
}

func (d *Daemon) hSnapshot(w http.ResponseWriter, r *http.Request, s *Session) {
	httpx.OK(w, s.Snapshot(httpx.QueryInt(r, "lines", 40, 1, screenScrollback+500)))
}

func (d *Daemon) hRecording(w http.ResponseWriter, r *http.Request, s *Session) {
	s.flushRecording()
	rc, err := openRecording(d.recordDir, s.id())
	if err != nil {
		httpx.Fail(w, httpx.NotFound("no recording for this session"))
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/x-asciicast")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, rc)
}

func (d *Daemon) hAttach(w http.ResponseWriter, r *http.Request, s *Session) {
	q := r.URL.Query()
	cols, _ := strconv.Atoi(q.Get("cols"))
	rows, _ := strconv.Atoi(q.Get("rows"))
	replay := q.Get("replay") != "0"
	readOnly := httpx.QueryBool(r, "readonly")
	// The socket is owner-only and peer-checked; there is no browser
	// origin to verify here (relay serve checks it on its side).
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(maxInputBody + 1024)
	serveAttach(r.Context(), s, conn, newClient(conn, readOnly, cols, rows), replay)
}

// serveAttach runs one attached client until it or the session goes away.
func serveAttach(parent context.Context, s *Session, conn *websocket.Conn, c *client, replay bool) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	nudge, _ := s.attach(c, replay)
	if nudge {
		go s.nudge()
	}
	writeErr := make(chan error, 1)
	go func() { writeErr <- c.writeLoop(ctx) }()
	readErr := make(chan error, 1)
	go func() { readErr <- readClient(ctx, s, c) }()

	var err error
	select {
	case err = <-writeErr:
	case err = <-readErr:
	}
	s.detach(c)
	switch {
	case err == nil:
		conn.Close(websocket.StatusNormalClosure, "exited")
	case errors.Is(err, errLagging):
		conn.Close(websocket.StatusTryAgainLater, "lagging")
	default:
		conn.CloseNow()
	}
	cancel()
}

// readClient handles input and control messages from one client.
func readClient(ctx context.Context, s *Session, c *client) error {
	for {
		typ, data, err := c.conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ == websocket.MessageBinary {
			if err := s.Input(c, data); err != nil && !errors.Is(err, errReadOnly) {
				c.pushMsg(api.TermServerMsg{T: "error", Message: "input failed"})
			}
			continue
		}
		var m api.TermClientMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.T {
		case "resize":
			if m.Cols > 0 && m.Rows > 0 {
				_ = s.Resize(c, m.Cols, m.Rows)
			}
		case "focus":
			if m.Visible != nil {
				s.mu.Lock()
				c.visible = *m.Visible
				s.mu.Unlock()
			}
		case "ping":
			c.pushMsg(api.TermServerMsg{T: "pong"})
		case "ack":
			c.ack(m.Bytes)
		}
	}
}

func (d *Daemon) hEvents(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	sub := d.subscribe()
	defer d.unsubscribe(sub)
	ctx := conn.CloseRead(r.Context())
	for {
		select {
		case <-ctx.Done():
			conn.CloseNow()
			return
		case ev, ok := <-sub.ch:
			if !ok {
				conn.Close(websocket.StatusGoingAway, "")
				return
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err = conn.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
