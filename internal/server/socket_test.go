package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/coder/websocket"
)

type socketTestAuth struct {
	valid  atomic.Bool
	checks atomic.Int32
	wait   atomic.Bool
}

func (a *socketTestAuth) Identify(*http.Request) *Principal {
	return &Principal{User: "synthetic", Method: "token", TokenID: "fixture"}
}
func (a *socketTestAuth) SocketValid(ctx context.Context, _ *http.Request, _ *Principal) bool {
	a.checks.Add(1)
	if a.wait.Load() {
		<-ctx.Done()
		return false
	}
	return a.valid.Load()
}
func (a *socketTestAuth) SubscribeRevocations(*Principal) (<-chan api.Event, func()) {
	return nil, func() {}
}

func TestSocketGuardForcesTransportClose(t *testing.T) {
	for _, mode := range []string{"unresponsive-peer", "blocked-output", "database-deadline"} {
		t.Run(mode, func(t *testing.T) {
			a := &socketTestAuth{}
			a.valid.Store(true)
			rt := NewRouter(a, nil)
			done := make(chan struct{})
			ready := make(chan struct{})
			rt.WS("GET /ws", func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				c, g, err := AcceptSocket(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
				if err != nil {
					return
				}
				defer g.Stop()
				defer func() { _ = c.CloseNow() }()
				close(ready)
				if mode == "blocked-output" {
					// A peer that never reads leaves a large write in progress. Closing
					// the transport must interrupt both the write and the close handshake.
					_ = c.Write(r.Context(), websocket.MessageBinary, make([]byte, 16<<20))
				} else {
					for {
						if _, _, err := c.Read(r.Context()); err != nil {
							return
						}
					}
				}
			})
			hs := httptest.NewServer(rt)
			defer hs.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http")+"/ws", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.CloseNow() }()
			<-ready
			started := time.Now()
			if mode == "database-deadline" {
				a.wait.Store(true)
			} else {
				a.valid.Store(false)
			}
			// No read runs on the peer, so it cannot acknowledge a close frame.
			select {
			case <-done:
				if elapsed := time.Since(started); elapsed > SocketRevocationBound+300*time.Millisecond {
					t.Fatalf("handler cleanup took %s", elapsed)
				}
				t.Logf("%s transport closed in %s", mode, time.Since(started).Round(time.Millisecond))
			case <-time.After(SocketRevocationBound + 300*time.Millisecond):
				t.Fatal("revoked handler/transport stayed open")
			}
		})
	}
}

func TestSocketGuardValidatesBeforeHandlerAndExemptsLocal(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "revoked-during-upgrade", true: "local-owner"}[local], func(t *testing.T) {
			a := &socketTestAuth{}
			rt := NewRouter(a, nil)
			rt.WS("GET /ws", func(w http.ResponseWriter, r *http.Request) {
				c, g, err := AcceptSocket(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
				if err != nil {
					return
				}
				defer g.Stop()
				defer func() { _ = c.CloseNow() }()
				_ = c.Write(r.Context(), websocket.MessageText, []byte("accepted"))
				ctx := c.CloseRead(r.Context())
				<-ctx.Done()
			})
			var h http.Handler = rt
			if local {
				h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { rt.ServeHTTP(w, r.WithContext(MarkLocal(r.Context()))) })
			}
			hs := httptest.NewServer(h)
			defer hs.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http")+"/ws", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.CloseNow() }()
			_, b, err := c.Read(ctx)
			if local {
				if err != nil || string(b) != "accepted" {
					t.Fatalf("local socket rejected: %v", err)
				}
				if a.checks.Load() != 0 {
					t.Fatal("local socket was revalidated")
				}
			} else if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("revoked upgrade exposed output %q: %v", b, err)
			}
		})
	}
}

// An ordinary failed upgrade must not start a watcher or retain a transport.
func TestSocketGuardFailedUpgrade(t *testing.T) {
	a := &socketTestAuth{}
	a.valid.Store(true)
	rt := NewRouter(a, nil)
	rt.WS("GET /ws", func(w http.ResponseWriter, r *http.Request) { _, _, _ = AcceptSocket(w, r, nil) })
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ws", nil)
	rt.ServeHTTP(w, r)
	if w.Code == http.StatusSwitchingProtocols || a.checks.Load() != 0 {
		t.Fatal("failed upgrade started validation")
	}
	_, _ = io.Copy(io.Discard, w.Result().Body)
}

func TestSocketGuardRejectsInputBeforeCloseHandshakeCompletes(t *testing.T) {
	a := &socketTestAuth{}
	a.valid.Store(true)
	rt := NewRouter(a, nil)
	trigger := make(chan struct{})
	ready := make(chan struct{})
	checked := make(chan bool, 1)
	released := make(chan struct{})
	rt.WS("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		c, g, err := AcceptSocket(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer g.Stop()
		defer func() { _ = c.CloseNow() }()
		close(ready)
		<-trigger
		checked <- g.Check()
		<-g.Done()
		close(released)
	})
	hs := httptest.NewServer(rt)
	defer hs.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	<-ready
	a.valid.Store(false)
	close(trigger)
	// The peer never runs a read or acknowledges the close. Input rejection
	// and backend release must finish without waiting the one-second grace.
	select {
	case valid := <-checked:
		if valid {
			t.Fatal("revoked input accepted")
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("input check waited for close handshake")
	}
	select {
	case <-released:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("backend release waited for close handshake")
	}
}
