package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

const (
	// browserReadLimit caps one browser frame (input bursts, pastes).
	browserReadLimit = 1 << 20
	// pingInterval keeps proxies from idling out a quiet terminal and
	// detects phones that vanished without closing the socket.
	pingInterval = 30 * time.Second
	// bridgeWriteTimeout bounds a single write towards either side.
	bridgeWriteTimeout = 30 * time.Second
)

// hAttach upgrades the browser connection and bridges it to the ptyd
// attach socket frame by frame. Authentication and the Origin check were
// done by the router (rt.WS).
func (s *Service) hAttach(w http.ResponseWriter, r *http.Request) {
	id, ok := sessionID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	cols, _ := strconv.Atoi(q.Get("cols"))
	rows, _ := strconv.Atoi(q.Get("rows"))
	if cols < 0 || rows < 0 || cols > 1000 || rows > 500 {
		cols, rows = 0, 0
	}
	opts := ptyclient.AttachOptions{
		Cols:     cols,
		Rows:     rows,
		Replay:   q.Get("replay") != "0",
		ReadOnly: httpx.QueryBool(r, "readonly"),
	}
	// Dial ptyd before upgrading so that an unknown session or a dead
	// daemon is a proper HTTP error the client can show.
	up, err := s.pty.Attach(r.Context(), id, opts)
	if err != nil {
		fail(w, err)
		return
	}
	down, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The router already enforced the Origin policy for cookie
		// callers; token callers have no Origin to check.
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionNoContextTakeover,
	})
	if err != nil {
		up.CloseNow()
		return
	}
	down.SetReadLimit(browserReadLimit)
	bridge(r.Context(), down, up, opts.ReadOnly)
}

// bridge copies frames between the browser (down) and ptyd (up) until
// either side closes, then closes the other with a matching status.
func bridge(parent context.Context, down, up *websocket.Conn, readOnly bool) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	upDone := make(chan error, 1)
	downDone := make(chan error, 1)
	go func() { upDone <- pumpUp(ctx, down, up) }()
	go func() { downDone <- pumpDown(ctx, down, up, readOnly) }()
	go keepAlive(ctx, down)

	select {
	case err := <-upDone:
		// ptyd ended the stream (session exited, lagging, daemon gone):
		// tell the browser why.
		code, reason := closeStatus(err)
		_ = down.Close(code, reason)
		up.CloseNow()
	case <-downDone:
		_ = up.Close(websocket.StatusNormalClosure, "")
		down.CloseNow()
	}
	cancel()
}

// closeStatus maps a ptyd read error to the status forwarded to the
// browser. An unexpected drop (daemon restart) asks the client to retry.
func closeStatus(err error) (websocket.StatusCode, string) {
	var ce websocket.CloseError
	if errors.As(err, &ce) {
		switch ce.Code {
		case websocket.StatusNormalClosure, websocket.StatusTryAgainLater, websocket.StatusGoingAway:
			return ce.Code, ce.Reason
		}
	}
	return websocket.StatusTryAgainLater, "terminal daemon disconnected"
}

// pumpUp forwards ptyd output (binary) and control messages (text) to the
// browser unchanged; ack-based flow control is end to end.
func pumpUp(ctx context.Context, down, up *websocket.Conn) error {
	for {
		typ, data, err := up.Read(ctx)
		if err != nil {
			return err
		}
		wctx, cancel := context.WithTimeout(ctx, bridgeWriteTimeout)
		err = down.Write(wctx, typ, data)
		cancel()
		if err != nil {
			return err
		}
	}
}

// pumpDown forwards browser input to ptyd. Read-only clients never reach
// the pty with input or size changes; control messages are re-encoded so
// only the documented fields pass.
func pumpDown(ctx context.Context, down, up *websocket.Conn, readOnly bool) error {
	for {
		typ, data, err := down.Read(ctx)
		if err != nil {
			return err
		}
		if typ == websocket.MessageBinary {
			if readOnly || len(data) == 0 {
				continue
			}
		} else {
			var ok bool
			if data, ok = filterClientMsg(data, readOnly); !ok {
				continue
			}
		}
		wctx, cancel := context.WithTimeout(ctx, bridgeWriteTimeout)
		err = up.Write(wctx, typ, data)
		cancel()
		if err != nil {
			return err
		}
	}
}

// filterClientMsg validates a TermClientMsg from the browser and returns
// its canonical encoding, or false to drop it.
func filterClientMsg(data []byte, readOnly bool) ([]byte, bool) {
	var m api.TermClientMsg
	if json.Unmarshal(data, &m) != nil {
		return nil, false
	}
	out := api.TermClientMsg{T: m.T}
	switch m.T {
	case "resize":
		if readOnly || m.Cols < 1 || m.Rows < 1 || m.Cols > 1000 || m.Rows > 500 {
			return nil, false
		}
		out.Cols, out.Rows = m.Cols, m.Rows
	case "focus":
		if m.Visible == nil {
			return nil, false
		}
		out.Visible = m.Visible
	case "ack":
		if m.Bytes < 0 {
			return nil, false
		}
		out.Bytes = m.Bytes
	case "ping":
	default:
		return nil, false
	}
	b, err := json.Marshal(out)
	return b, err == nil
}

// keepAlive pings the browser; a missing pong closes the connection,
// which ends the bridge.
func keepAlive(ctx context.Context, c *websocket.Conn) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					c.CloseNow()
				}
				return
			}
		}
	}
}
