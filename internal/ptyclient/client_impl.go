package ptyclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/api"
)

const (
	// defaultTimeout applies to REST calls whose context has no deadline.
	defaultTimeout = 15 * time.Second
	// attachReadLimit bounds one frame read from an attach socket (ptyd
	// sends output in chunks of at most 64 KiB).
	attachReadLimit = 4 << 20
	// maxResponse caps JSON responses from the daemon.
	maxResponse = 16 << 20
	baseURL     = "http://ptyd"
)

// httpImpl speaks the daemon protocol over its unix socket.
type httpImpl struct {
	socket string
	hc     *http.Client // REST, per-request timeouts through contexts
	stream *http.Client // WebSocket upgrades and streaming bodies
}

func newImpl(socket string) *httpImpl {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}
	return &httpImpl{
		socket: socket,
		hc: &http.Client{Transport: &http.Transport{
			DialContext:         dial,
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     60 * time.Second,
		}},
		stream: &http.Client{Transport: &http.Transport{DialContext: dial, DisableKeepAlives: true}},
	}
}

func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, defaultTimeout)
}

// do sends a request and decodes a JSON answer into out (may be nil).
func (c *httpImpl) do(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer res.Body.Close()
	if err := statusError(res); err != nil {
		return err
	}
	if out == nil || res.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxResponse)).Decode(out); err != nil {
		return fmt.Errorf("ptyd: decode %s %s: %w", method, path, err)
	}
	return nil
}

// statusError maps a non-2xx response to ErrNotFound or *StatusError.
func statusError(res *http.Response) error {
	if res.StatusCode < 300 {
		return nil
	}
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	var eb api.ErrorBody
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	_ = json.Unmarshal(b, &eb)
	return &StatusError{Status: res.StatusCode, Code: eb.Error.Code, Message: eb.Error.Message}
}

func (c *httpImpl) doJSON(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	ct := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, ct = bytes.NewReader(b), "application/json"
	}
	return c.do(ctx, method, path, body, ct, out)
}

func sessionPath(id, suffix string) string {
	return "/v1/sessions/" + url.PathEscape(id) + suffix
}

func (c *httpImpl) Health(ctx context.Context) error {
	var h struct {
		OK bool `json:"ok"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v1/health", nil, &h); err != nil {
		return err
	}
	if !h.OK {
		return ErrUnavailable
	}
	return nil
}

func (c *httpImpl) List(ctx context.Context) ([]api.TerminalSession, error) {
	var out []api.TerminalSession
	err := c.doJSON(ctx, http.MethodGet, "/v1/sessions", nil, &out)
	return out, err
}

func (c *httpImpl) Get(ctx context.Context, id string) (*api.TerminalSession, error) {
	var out api.TerminalSession
	if err := c.doJSON(ctx, http.MethodGet, sessionPath(id, ""), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *httpImpl) Create(ctx context.Context, spec CreateSpec) (*api.TerminalSession, error) {
	var out api.TerminalSession
	if err := c.doJSON(ctx, http.MethodPost, "/v1/sessions", spec, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *httpImpl) Restore(ctx context.Context, id string) (*api.TerminalSession, error) {
	var out api.TerminalSession
	if err := c.doJSON(ctx, http.MethodPost, sessionPath(id, "/restore"), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// patch mirrors the daemon's PATCH body.
type patch struct {
	Name   *string           `json:"name,omitempty"`
	Pinned *bool             `json:"pinned,omitempty"`
	Meta   map[string]string `json:"meta,omitempty"`
}

func (c *httpImpl) Update(ctx context.Context, id string, req api.UpdateTerminalRequest) (*api.TerminalSession, error) {
	var out api.TerminalSession
	if err := c.doJSON(ctx, http.MethodPatch, sessionPath(id, ""), patch{Name: req.Name, Pinned: req.Pinned}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *httpImpl) SetMeta(ctx context.Context, id string, meta map[string]string) error {
	if len(meta) == 0 {
		return nil
	}
	return c.doJSON(ctx, http.MethodPatch, sessionPath(id, ""), patch{Meta: meta}, nil)
}

func (c *httpImpl) Kill(ctx context.Context, id, signal string) error {
	q := ""
	if signal != "" {
		q = "?signal=" + url.QueryEscape(signal)
	}
	return c.do(ctx, http.MethodDelete, sessionPath(id, q), nil, "", nil)
}

func (c *httpImpl) Remove(ctx context.Context, id string) error {
	// Forgetting a running session waits for the kill escalation (≤ 5 s).
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return c.do(ctx, http.MethodDelete, sessionPath(id, "?forget=1"), nil, "", nil)
}

func (c *httpImpl) Input(ctx context.Context, id string, data []byte, paste bool) error {
	q := ""
	if paste {
		q = "?paste=1"
	}
	return c.do(ctx, http.MethodPost, sessionPath(id, "/input"+q), bytes.NewReader(data), "application/octet-stream", nil)
}

func (c *httpImpl) Resize(ctx context.Context, id string, cols, rows int) error {
	return c.doJSON(ctx, http.MethodPost, sessionPath(id, "/resize"), map[string]int{"cols": cols, "rows": rows}, nil)
}

func (c *httpImpl) Snapshot(ctx context.Context, id string, lines int) (*api.TerminalSnapshot, error) {
	var out api.TerminalSnapshot
	q := ""
	if lines > 0 {
		q = "?lines=" + strconv.Itoa(lines)
	}
	if err := c.doJSON(ctx, http.MethodGet, sessionPath(id, "/snapshot"+q), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *httpImpl) SetAttention(ctx context.Context, id string, a *api.Attention) error {
	b := []byte("null")
	if a != nil {
		var err error
		if b, err = json.Marshal(a); err != nil {
			return err
		}
	}
	return c.do(ctx, http.MethodPost, sessionPath(id, "/attention"), bytes.NewReader(b), "application/json", nil)
}

func (c *httpImpl) Recording(ctx context.Context, id string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+sessionPath(id, "/recording"), nil)
	if err != nil {
		return nil, err
	}
	res, err := c.stream.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := statusError(res); err != nil {
		res.Body.Close()
		return nil, err
	}
	return res.Body, nil
}

func (c *httpImpl) dialWS(ctx context.Context, path string) (*websocket.Conn, error) {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, res, err := websocket.Dial(dctx, "ws://ptyd"+path, &websocket.DialOptions{HTTPClient: c.stream})
	if err != nil {
		if res != nil {
			if serr := statusError(res); serr != nil {
				return nil, serr
			}
		}
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return conn, nil
}

func (c *httpImpl) Attach(ctx context.Context, id string, opts AttachOptions) (*websocket.Conn, error) {
	q := url.Values{}
	if opts.Cols > 0 && opts.Rows > 0 {
		q.Set("cols", strconv.Itoa(opts.Cols))
		q.Set("rows", strconv.Itoa(opts.Rows))
	}
	if opts.Replay {
		q.Set("replay", "1")
	} else {
		q.Set("replay", "0")
	}
	if opts.ReadOnly {
		q.Set("readonly", "1")
	}
	conn, err := c.dialWS(ctx, sessionPath(id, "/attach?"+q.Encode()))
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(attachReadLimit)
	return conn, nil
}

func (c *httpImpl) Events(ctx context.Context) (<-chan PtyEvent, error) {
	conn, err := c.dialWS(ctx, "/v1/events")
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(maxResponse)
	ch := make(chan PtyEvent, 256)
	go func() {
		defer close(ch)
		defer conn.CloseNow()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var ev PtyEvent
			if json.Unmarshal(data, &ev) != nil {
				continue
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}
