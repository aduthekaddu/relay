package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
	"github.com/coder/websocket"
)

func rel030App(t *testing.T) *App {
	t.Helper()
	root, err := os.MkdirTemp("", "r30-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := config.Defaults()
	cfg.Files.Root = root
	cfg.Terminal.ImportTmux = false
	a := &App{D: &core.Deps{Cfg: cfg, Paths: config.Paths{Home: root, DataDir: root, Uploads: filepath.Join(root, "uploads"), PtydSocket: filepath.Join(root, "ptyd.sock")}, Store: st, Bus: events.New(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	a.Router = server.NewRouter(rel023MetricsAuth{}, func() []string { return nil })
	t.Cleanup(a.Close)
	return a
}

func rel030Loops(t *testing.T, a *App) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, len(a.starters))
	for _, starter := range a.starters {
		go func() { done <- starter.fn(ctx) }()
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		for range a.starters {
			select {
			case err := <-done:
				if err != nil && err != context.Canceled {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("wiring loop did not stop")
			}
		}
	}
	t.Cleanup(stop)
	return stop
}

func rel030Listen(t *testing.T) net.Listener {
	t.Helper()
	for port := 47730; port < 47780; port++ {
		if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			return ln
		}
	}
	t.Fatal("no isolated loopback test port available")
	return nil
}

func TestREL030PresenceAndBrowserFiltering(t *testing.T) {
	a := rel030App(t)
	ln := rel030Listen(t)
	base := "http://" + ln.Addr().String()
	deliveries := make(chan api.Notification, 16)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sink", func(w http.ResponseWriter, r *http.Request) {
		var n api.Notification
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			t.Error(err)
		}
		deliveries <- n
		w.WriteHeader(204)
	})
	mux.Handle("/", a.Router)
	srv := httptest.NewUnstartedServer(mux)
	srv.Listener = ln
	a.D.Cfg.Notify.WebhookURL = base + "/sink"
	for _, wire := range []func(context.Context, *App) error{wireLive, wireNotify, wireClip} {
		if err := wire(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	stop := rel030Loops(t, a)
	waitREL023(t, 3*time.Second, func() bool { return a.D.Bus.Subscribers() == 2 })
	conn, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(base, "http")+"/api/v1/events", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer test-token"}}})
	if err != nil {
		t.Fatal(err)
	}
	reader := newREL023EventReader(conn)
	t.Cleanup(func() { closeREL023EventReader(t, reader, conn) })
	if ev := reader.next(t, 3*time.Second); ev.Type != api.EvHello {
		t.Fatalf("first event=%s", ev.Type)
	}
	for _, tt := range []struct {
		name, path, session string
		visible, deliver    bool
	}{
		{"watched", "/terminal/t_fixture", "t_fixture", true, false},
		{"other session", "/terminal/t_fixture", "t_other", true, true},
		{"hidden", "/terminal/t_fixture", "t_fixture_hidden", false, true},
		{"sessionless", "/terminal/t_fixture", "", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path
			if tt.name == "hidden" {
				path = "/terminal/" + tt.session
			}
			sendREL023ClientEvent(t, conn, api.ClientEvent{Type: "visibility", Path: path, Visible: &tt.visible})
			roundTripREL023(t, conn, reader)
			if _, err := a.D.Notifier.Notify(t.Context(), api.NotifyRequest{Kind: "attention", Title: tt.name, SessionID: tt.session}); err != nil {
				t.Fatal(err)
			}
			if tt.deliver {
				select {
				case n := <-deliveries:
					if n.SessionID != tt.session {
						t.Fatalf("delivered wrong notification: %s", n.SessionID)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("expected fixture delivery")
				}
			} else {
				select {
				case <-deliveries:
					t.Fatal("watched terminal delivered externally")
				case <-time.After(100 * time.Millisecond):
				}
			}
		})
	}
	// Explicit subscriptions must not opt a browser into backend-only topics.
	sendREL023ClientEvent(t, conn, api.ClientEvent{Type: "subscribe", Topics: []string{core.BusClipCapture, core.BusAudit, core.BusSessionRevoked}})
	roundTripREL023(t, conn, reader)
	a.D.Bus.Publish(core.BusClipCapture, core.ClipCapture{Text: "canonical fixture", Source: "desktop"})
	a.D.Bus.Publish(core.BusClipCapture, map[string]any{"text": false})
	a.D.Bus.Publish(core.BusAudit, core.AuditEvent{Event: "synthetic.audit"})
	a.D.Bus.Publish(core.BusSessionRevoked, core.SessionRevoked{})
	a.D.Bus.Publish("fixture.barrier", nil)
	publicClip := false
	barrier := false
	for !publicClip || !barrier {
		ev := reader.next(t, 3*time.Second)
		if core.IsBackendTopic(ev.Type) {
			t.Fatalf("browser received backend topic %s", ev.Type)
		}
		if ev.Type == api.EvClip {
			publicClip = true
		}
		if ev.Type == "fixture.barrier" {
			barrier = true
		}
	}
	if err := conn.CloseNow(); err != nil {
		t.Fatal(err)
	}
	waitREL023(t, 3*time.Second, func() bool { return !a.D.Presence.Online() })
	if _, err := a.D.Notifier.Notify(t.Context(), api.NotifyRequest{Kind: "done", Title: "after disconnect", SessionID: "t_fixture"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-deliveries:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect still suppresses")
	}
	stop()
	if n := a.D.Bus.Subscribers(); n != 0 {
		t.Fatalf("subscriptions after shutdown=%d", n)
	}
}

func TestREL030TerminalCanonicalCapture(t *testing.T) {
	t.Setenv("RELAY_NO_PTYD", "1")
	a := rel030App(t)
	ln, err := net.Listen("unix", a.D.Paths.PtydSocket)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/events":
			ws, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer ws.CloseNow()
			data, _ := json.Marshal(ptyclient.PtyEvent{Type: "clip", ID: "t_fixture", Text: "OSC fixture"})
			_ = ws.Write(r.Context(), websocket.MessageText, data)
			_, _, _ = ws.Read(r.Context())
		case "/v1/sessions":
			_, _ = w.Write([]byte("[]"))
		default:
			w.WriteHeader(404)
		}
	}))
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	for _, wire := range []func(context.Context, *App) error{wireTerminal, wireClip} {
		if err := wire(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}
	sub := a.D.Bus.Subscribe(8, func(ev api.Event) bool { return ev.Type == core.BusClipCapture || ev.Type == api.EvClip })
	t.Cleanup(sub.Close)
	stop := rel030Loops(t, a)
	for _, topic := range []string{core.BusClipCapture, api.EvClip} {
		select {
		case ev := <-sub.C:
			if ev.Type != topic {
				t.Fatalf("event=%s, want %s", ev.Type, topic)
			}
			if topic == core.BusClipCapture {
				c, ok := ev.Data.(core.ClipCapture)
				if !ok || c.Text != "OSC fixture" || c.Source != "terminal" || c.SessionID != "t_fixture" {
					t.Fatalf("capture=%+v", ev.Data)
				}
			} else {
				c, ok := ev.Data.(api.Clip)
				if !ok || c.Text != "OSC fixture" || c.Source != "terminal" {
					t.Fatalf("stored=%+v", ev.Data)
				}
			}
		case <-time.After(3 * time.Second):
			t.Fatal("terminal capture did not reach history")
		}
	}
	stop()
	a.Close()
	if n := a.D.Bus.Subscribers(); n != 1 {
		t.Fatalf("capture subscription leaked: %d", n)
	}
}

func TestREL030WiringCloseBeforeRun(t *testing.T) {
	a := rel030App(t)
	if err := wireClip(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	if n := a.D.Bus.Subscribers(); n != 1 {
		t.Fatalf("constructor subscriptions=%d", n)
	}
	a.Close()
	a.Close()
	if n := a.D.Bus.Subscribers(); n != 0 {
		t.Fatalf("wiring failure leaked subscriptions=%d", n)
	}
}
