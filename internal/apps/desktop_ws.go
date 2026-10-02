package apps

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
)

const (
	wsReadLimit   = 8 << 20 // largest client message (ClientCutText)
	rfbReadBuf    = 64 << 10
	wsPingEvery   = 25 * time.Second
	wsPingTimeout = 10 * time.Second
	wsWriteWait   = 30 * time.Second
)

// ServeWS bridges a binary WebSocket (noVNC) to the Xvnc unix socket.
// The router has already authenticated the request and checked Origin.
// The desktop is started on demand.
func (k *desktop) ServeWS(w http.ResponseWriter, r *http.Request) {
	startCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	err := k.ensure(startCtx)
	cancel()
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var d net.Dialer
	dialCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	vnc, err := d.DialContext(dialCtx, "unix", k.sock)
	cancel()
	if err != nil {
		httpx.Fail(w, httpx.Unavailable("the desktop is not accepting connections"))
		return
	}
	if !k.addViewer(vnc) {
		vnc.Close()
		httpx.Fail(w, httpx.Conflict("too many desktop viewers"))
		return
	}
	defer k.removeViewer(vnc)
	ws, guard, err := server.AcceptSocket(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{"binary"},
		// Origin was enforced by Router.WS; the Host may be a proxy name.
		InsecureSkipVerify: true,
		// RFB encodings are already compressed; deflate only costs CPU.
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		vnc.Close()
		return // Accept wrote the error response
	}
	defer guard.Stop()
	ws.SetReadLimit(wsReadLimit)
	bridgeRFB(r.Context(), ws, vnc, guard)
}

// bridgeRFB copies binary frames both ways until either side ends, then
// closes both. Backpressure is natural: each direction blocks on its
// writer, so a slow browser stops reads from the VNC socket.
func bridgeRFB(ctx context.Context, ws *websocket.Conn, vnc net.Conn, guards ...*server.SocketGuard) {
	var guard *server.SocketGuard
	if len(guards) > 0 {
		guard = guards[0]
	}
	// done ends the bridge; ctx is only cancelled after the close
	// handshake, because cancelling a coder/websocket read or write
	// context drops the connection without a close frame.
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var closeOnce sync.Once
	status, reason := websocket.StatusNormalClosure, ""
	var reasonMu sync.Mutex
	finish := func(code websocket.StatusCode, why string) {
		closeOnce.Do(func() {
			reasonMu.Lock()
			status, reason = code, why
			reasonMu.Unlock()
			close(done)
			_ = vnc.Close()
		})
	}

	wg.Add(3)
	go func() { // browser -> VNC
		defer wg.Done()
		for {
			typ, rd, err := ws.Reader(ctx)
			if err != nil {
				finish(websocket.StatusNormalClosure, "")
				return
			}
			if typ != websocket.MessageBinary {
				finish(websocket.StatusUnsupportedData, "binary frames only")
				return
			}
			if _, err := io.Copy(&guardedVNCWriter{conn: vnc, guard: guard}, rd); err != nil {
				finish(websocket.StatusGoingAway, "desktop connection lost")
				return
			}
		}
	}()
	go func() { // VNC -> browser
		defer wg.Done()
		buf := make([]byte, rfbReadBuf)
		for {
			n, err := vnc.Read(buf)
			if n > 0 {
				wctx, wcancel := context.WithTimeout(ctx, wsWriteWait)
				werr := ws.Write(wctx, websocket.MessageBinary, buf[:n])
				wcancel()
				if werr != nil {
					finish(websocket.StatusGoingAway, "")
					return
				}
			}
			if err != nil {
				why := "desktop stopped"
				if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					why = "desktop connection lost"
				}
				finish(websocket.StatusGoingAway, why)
				return
			}
		}
	}()
	go func() { // keepalive through idle proxies
		defer wg.Done()
		t := time.NewTicker(wsPingEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				pctx, pcancel := context.WithTimeout(ctx, wsPingTimeout)
				err := ws.Ping(pctx)
				pcancel()
				if err != nil {
					finish(websocket.StatusGoingAway, "ping timeout")
					return
				}
			}
		}
	}()
	select {
	case <-guard.Done():
		finish(websocket.StatusPolicyViolation, "authentication ended")
		guard.Wait()
	case <-done:
	case <-ctx.Done(): // request context: server shutting down
		finish(websocket.StatusGoingAway, "")
	}
	reasonMu.Lock()
	code, why := status, reason
	reasonMu.Unlock()
	if guard.Revoked() {
		guard.Wait()
	}
	_ = ws.Close(code, why) // handshake; unblocks the reader
	cancel()
	wg.Wait()
}

// Check each chunk of a streamed frame, including a frame opened before
// revocation. The monitor closes both readers even when a peer goes idle.
type guardedVNCWriter struct {
	conn  net.Conn
	guard *server.SocketGuard
}

func (w *guardedVNCWriter) Write(b []byte) (int, error) {
	if !w.guard.Check() {
		return 0, context.Canceled
	}
	return w.conn.Write(b)
}
