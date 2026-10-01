package live

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/server"
)

// cookieAuth accepts the cookie "sid=<id>" and the bearer token "tok".
type cookieAuth struct{}

func (cookieAuth) Identify(r *http.Request) *server.Principal {
	if r.Header.Get("Authorization") == "Bearer tok" {
		return &server.Principal{User: "owner", Method: "token", TokenID: "t1"}
	}
	if c, err := r.Cookie("sid"); err == nil && c.Value != "" {
		return &server.Principal{User: "owner", Method: "cookie", SessionID: c.Value}
	}
	return nil
}

type harness struct {
	svc    *Service
	bus    *events.Bus
	srv    *httptest.Server
	cancel context.CancelFunc
	done   chan struct{}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cfg := config.Defaults()
	d := &core.Deps{
		Cfg:       cfg,
		Bus:       events.New(),
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Pty:       ptyclient.New(filepath.Join(t.TempDir(), "missing.sock")), // List fails: snapshot skipped
		Version:   "test",
		StartedAt: time.Now().UTC(),
	}
	svc, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{svc: svc, bus: d.Bus, done: make(chan struct{})}
	rt := server.NewRouter(cookieAuth{}, func() []string { return []string{h.origin()} })
	svc.Routes(rt)
	h.srv = httptest.NewServer(rt)
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { _ = svc.Start(ctx); close(h.done) }()
	// Start subscribes asynchronously; wait until it is listening.
	waitFor(t, func() bool { return d.Bus.Subscribers() == 1 })
	t.Cleanup(func() {
		cancel()
		<-h.done
		h.srv.Close()
	})
	return h
}

func (h *harness) origin() string {
	if h.srv == nil {
		return ""
	}
	return h.srv.URL
}

func (h *harness) dial(t *testing.T, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u := "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/api/v1/events"
	return websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: hdr})
}

func (h *harness) connect(t *testing.T, sid string) *websocket.Conn {
	t.Helper()
	hdr := http.Header{}
	hdr.Set("Cookie", "sid="+sid)
	hdr.Set("Origin", h.srv.URL)
	c, _, err := h.dial(t, hdr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	if ev := readEvent(t, c); ev.Type != api.EvHello {
		t.Fatalf("first event = %q, want hello", ev.Type)
	}
	waitFor(t, func() bool { return h.svc.Clients() > 0 })
	return c
}

type rawEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func readEvent(t *testing.T, c *websocket.Conn) rawEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var ev rawEvent
	if err := json.Unmarshal(b, &ev); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return ev
}

func send(t *testing.T, c *websocket.Conn, ev api.ClientEvent) {
	t.Helper()
	b, _ := json.Marshal(ev)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// roundTrip sends a ping and waits for the pong, which proves every earlier
// client event has been processed (the read loop is sequential).
func roundTrip(t *testing.T, c *websocket.Conn) {
	t.Helper()
	send(t, c, api.ClientEvent{Type: "ping"})
	for {
		if ev := readEvent(t, c); ev.Type == "pong" {
			return
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHelloAndPublishedEvent(t *testing.T) {
	h := newHarness(t)
	c := h.connect(t, "s1")
	h.bus.Publish(api.EvTerminalUpdated, map[string]string{"id": "abc"})
	ev := readEvent(t, c)
	if ev.Type != api.EvTerminalUpdated || !strings.Contains(string(ev.Data), `"abc"`) {
		t.Fatalf("got %s %s", ev.Type, ev.Data)
	}
}

func TestHelloCarriesInfo(t *testing.T) {
	h := newHarness(t)
	h.svc.SetInfo(func(context.Context) api.Info { return api.Info{Version: "v-hello", Hostname: "box"} })
	hdr := http.Header{"Cookie": {"sid=s1"}, "Origin": {h.srv.URL}}
	c, _, err := h.dial(t, hdr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	ev := readEvent(t, c)
	var info api.Info
	if err := json.Unmarshal(ev.Data, &info); err != nil || info.Version != "v-hello" {
		t.Fatalf("hello data = %s (%v)", ev.Data, err)
	}
}

func TestBackendTopicsAndGatedMetrics(t *testing.T) {
	h := newHarness(t)
	c := h.connect(t, "s1")

	// Backend-only topics and unsubscribed metrics never reach the browser.
	h.bus.Publish(core.BusAudit, core.AuditEvent{Event: "x"})
	h.bus.Publish(api.EvMetrics, map[string]int{"cpu": 1})
	h.bus.Publish("marker.one", nil)
	if ev := readEvent(t, c); ev.Type != "marker.one" {
		t.Fatalf("got %q, want marker.one (audit/metrics must be filtered)", ev.Type)
	}

	send(t, c, api.ClientEvent{Type: "subscribe", Topics: []string{"metrics", "Bad Topic!"}})
	roundTrip(t, c)
	if !h.svc.Subscribed("metrics") || h.svc.Subscribed("Bad Topic!") {
		t.Fatal("subscription state wrong")
	}
	h.bus.Publish(api.EvMetrics, map[string]int{"cpu": 2})
	if ev := readEvent(t, c); ev.Type != api.EvMetrics {
		t.Fatalf("got %q, want metrics after subscribe", ev.Type)
	}

	send(t, c, api.ClientEvent{Type: "unsubscribe", Topics: []string{"metrics"}})
	roundTrip(t, c)
	if h.svc.Subscribed("metrics") {
		t.Fatal("still subscribed")
	}
}

func TestSubscribedRequiresVisibleMetricsClientsAndResetsOnReconnect(t *testing.T) {
	h := newHarness(t)
	first := h.connect(t, "s1")
	second := h.connect(t, "s2")
	if h.svc.Subscribed("metrics") {
		t.Fatal("connected clients without topic subscriptions must not activate metrics")
	}

	send(t, first, api.ClientEvent{Type: "subscribe", Topics: []string{"metrics"}})
	roundTrip(t, first)
	send(t, second, api.ClientEvent{Type: "subscribe", Topics: []string{"metrics"}})
	roundTrip(t, second)
	if !h.svc.Subscribed("metrics") {
		t.Fatal("two visible subscribers should activate metrics")
	}

	hidden := false
	send(t, first, api.ClientEvent{Type: "visibility", Visible: &hidden})
	roundTrip(t, first)
	if !h.svc.Subscribed("metrics") {
		t.Fatal("one remaining visible subscriber should keep metrics active")
	}
	h.bus.Publish(api.EvMetrics, map[string]int{"cpu": 3})
	h.bus.Publish("visible.marker", nil)
	if ev := readEvent(t, second); ev.Type != api.EvMetrics {
		t.Fatalf("visible subscriber got %q, want metrics", ev.Type)
	}
	if ev := readEvent(t, second); ev.Type != "visible.marker" {
		t.Fatalf("visible subscriber got %q after metrics, want marker", ev.Type)
	}
	if ev := readEvent(t, first); ev.Type != "visible.marker" {
		t.Fatalf("hidden subscriber got %q, want only marker", ev.Type)
	}
	send(t, second, api.ClientEvent{Type: "visibility", Visible: &hidden})
	roundTrip(t, second)
	if h.svc.Subscribed("metrics") {
		t.Fatal("hidden subscribers must not activate metrics")
	}

	visible := true
	send(t, first, api.ClientEvent{Type: "visibility", Visible: &visible})
	roundTrip(t, first)
	if !h.svc.Subscribed("metrics") {
		t.Fatal("a visible subscribed client should reactivate metrics")
	}
	send(t, first, api.ClientEvent{Type: "unsubscribe", Topics: []string{"metrics"}})
	roundTrip(t, first)
	if h.svc.Subscribed("metrics") {
		t.Fatal("unsubscribed visible client must not activate metrics")
	}

	_ = first.CloseNow()
	_ = second.CloseNow()
	waitFor(t, func() bool { return h.svc.Clients() == 0 })
	reconnected := h.connect(t, "s3")
	if h.svc.Subscribed("metrics") {
		t.Fatal("reconnected client inherited a stale metrics subscription")
	}
	send(t, reconnected, api.ClientEvent{Type: "subscribe", Topics: []string{"metrics"}})
	roundTrip(t, reconnected)
	if !h.svc.Subscribed("metrics") {
		t.Fatal("reconnected visible subscriber did not activate metrics")
	}
}

func TestVisibilityDrivesPresence(t *testing.T) {
	h := newHarness(t)
	c := h.connect(t, "s1")
	if !h.svc.Online() {
		t.Fatal("a fresh connection counts as online")
	}
	vis, hidden := true, false
	send(t, c, api.ClientEvent{Type: "visibility", Visible: &vis, Path: "/terminal/t42?x=1"})
	roundTrip(t, c)
	if !h.svc.Watching("t42") || h.svc.Watching("t4") || h.svc.Watching("") {
		t.Fatal("Watching(t42) should be the only match")
	}
	send(t, c, api.ClientEvent{Type: "visibility", Visible: &hidden})
	roundTrip(t, c)
	if h.svc.Watching("t42") || h.svc.Online() {
		t.Fatal("hidden page must not count as watching/online")
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
	waitFor(t, func() bool { return h.svc.Clients() == 0 })
}

func TestOriginRequiredForCookieUpgrade(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		name string
		hdr  http.Header
		want int
	}{
		{"no credentials", http.Header{"Origin": {h.srv.URL}}, http.StatusUnauthorized},
		{"cookie without origin", http.Header{"Cookie": {"sid=s1"}}, http.StatusForbidden},
		{"cookie wrong origin", http.Header{"Cookie": {"sid=s1"}, "Origin": {"https://evil.example"}}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, res, err := h.dial(t, tc.hdr)
			if err == nil {
				t.Fatal("dial succeeded")
			}
			if res == nil || res.StatusCode != tc.want {
				t.Fatalf("status = %v, want %d", res, tc.want)
			}
		})
	}
	// Tokens are not ambient credentials: no Origin needed.
	c, _, err := h.dial(t, http.Header{"Authorization": {"Bearer tok"}})
	if err != nil {
		t.Fatalf("token dial: %v", err)
	}
	defer c.CloseNow()
	if ev := readEvent(t, c); ev.Type != api.EvHello {
		t.Fatalf("got %q", ev.Type)
	}
}

func TestRevokedSessionIsDisconnected(t *testing.T) {
	h := newHarness(t)
	keep := h.connect(t, "keep")
	drop := h.connect(t, "gone")
	h.bus.Publish(core.BusSessionRevoked, core.SessionRevoked{SessionIDs: []string{"gone"}})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := drop.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("revoked socket: %v, want policy violation close", err)
	}
	h.bus.Publish("still.here", nil)
	if ev := readEvent(t, keep); ev.Type != "still.here" {
		t.Fatalf("other session got %q", ev.Type)
	}
}

func TestShutdownClosesClients(t *testing.T) {
	h := newHarness(t)
	c := h.connect(t, "s1")
	h.cancel()
	<-h.done
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("got %v, want going-away close", err)
	}
}

func TestSlowClientDropped(t *testing.T) {
	h := newHarness(t)
	_ = h.connect(t, "slow") // never reads
	// Dispatch directly: the bus itself drops events for a lagging fan-out
	// loop, which would hide the per-client queue under test. Frames are
	// large so kernel socket buffers fill quickly.
	payload := strings.Repeat("x", 64<<10)
	deadline := time.Now().Add(5 * time.Second)
	for h.svc.Clients() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("slow client was never dropped")
		}
		h.svc.dispatch(api.Event{Type: "flood", Data: payload})
	}
}

func TestTerminalOf(t *testing.T) {
	cases := map[string]string{
		"/terminal/abc":       "abc",
		"/terminal/abc/split": "abc",
		"/terminal/abc#x":     "abc",
		"/terminal/":          "",
		"/files/terminal/abc": "",
		"":                    "",
	}
	for in, want := range cases {
		if got := terminalOf(in); got != want {
			t.Errorf("terminalOf(%q) = %q, want %q", in, got, want)
		}
	}
}
