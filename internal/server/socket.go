package server

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/coder/websocket"
)

const (
	SocketRecheckInterval   = time.Second
	SocketValidationTimeout = time.Second
	SocketCloseTimeout      = time.Second
	// SocketRevocationBound includes one poll, a database check and forced close.
	SocketRevocationBound = SocketRecheckInterval + SocketValidationTimeout + SocketCloseTimeout
)

// SocketAuthenticator adds authoritative validation and optional revocation
// hints to the request authenticator. Local owner sockets are exempt.
type SocketAuthenticator interface {
	SocketValid(context.Context, *http.Request, *Principal) bool
	SubscribeRevocations(*Principal) (<-chan api.Event, func())
}

// socketWriter retains the hijacked transport so revocation can interrupt a
// close handshake or a blocked write within SocketCloseTimeout. CloseNow
// alone does not interrupt a Close already in progress in coder/websocket.
type socketWriter struct {
	http.ResponseWriter
	conn net.Conn
}

func (w *socketWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *socketWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	w.conn = c
	return c, rw, err
}

// AcceptSocket upgrades and guards a Router.WS connection. Call Check before
// applying each input frame, and defer Stop until the handler returns.
func AcceptSocket(w http.ResponseWriter, r *http.Request, opts *websocket.AcceptOptions) (*websocket.Conn, *SocketGuard, error) {
	sw := &socketWriter{ResponseWriter: w}
	c, err := websocket.Accept(sw, r, opts)
	if err != nil {
		return nil, nil, err
	}
	g := &SocketGuard{conn: c, transport: sw.conn, request: r, done: make(chan struct{}), ended: make(chan struct{}), invalid: make(chan struct{})}
	p := PrincipalFrom(r.Context())
	a, ok := r.Context().Value(socketAuthKey).(SocketAuthenticator)
	if ok && p != nil && p.Method != "local" {
		g.auth, g.principal = a, p
		changes, unsubscribe := a.SubscribeRevocations(p)
		if !g.Check() {
			g.Wait()
			unsubscribe()
			return nil, nil, errors.New("authentication ended")
		}
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			defer unsubscribe()
			tick := time.NewTicker(SocketRecheckInterval)
			defer tick.Stop()
			for {
				select {
				case <-g.done:
					return
				case <-r.Context().Done():
					return
				case _, ok := <-changes:
					if !ok {
						changes = nil
					}
				case <-tick.C:
				}
				if !g.Check() {
					return
				}
			}
		}()
	}
	return c, g, nil
}

// SocketGuard never retains a credential in a close reason or event. Checks
// fail closed on database errors and deadlines. Concurrent checks are allowed.
type SocketGuard struct {
	conn      *websocket.Conn
	transport net.Conn
	request   *http.Request
	auth      SocketAuthenticator
	principal *Principal
	revoked   atomic.Bool
	done      chan struct{}
	ended     chan struct{}
	invalid   chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

func (g *SocketGuard) Check() bool {
	if g == nil || g.auth == nil {
		return true
	}
	if g.revoked.Load() {
		return false
	}
	ctx, cancel := context.WithTimeout(g.request.Context(), SocketValidationTimeout)
	valid := g.auth.SocketValid(ctx, g.request, g.principal) && ctx.Err() == nil
	cancel()
	if !valid {
		if g.revoked.CompareAndSwap(false, true) {
			// Reject input and release blocked upstream writers before waiting for
			// the peer. Feature owners wait on ended before sending another close.
			close(g.invalid)
			go func() {
				timer := time.AfterFunc(SocketCloseTimeout, func() { _ = g.transport.Close() })
				_ = g.conn.Close(websocket.StatusPolicyViolation, "authentication ended")
				timer.Stop()
				close(g.ended)
			}()
		}
		return false
	}
	return !g.revoked.Load()
}
func (g *SocketGuard) Stop() {
	g.stopOnce.Do(func() { close(g.done) })
	g.wg.Wait()
	if g.Revoked() {
		g.Wait()
	}
}

func (g *SocketGuard) Managed() bool { return g != nil && g.auth != nil }

// Done closes as soon as authentication fails, before the close handshake.
// Backend bridges use it to interrupt blocked upstream writes and reads.
func (g *SocketGuard) Done() <-chan struct{} {
	if !g.Managed() {
		return nil
	}
	return g.invalid
}
func (g *SocketGuard) Wait() {
	if g.Managed() {
		<-g.ended
	}
}

func (g *SocketGuard) Revoked() bool { return g != nil && g.revoked.Load() }
