package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/server"
)

type rel023MetricsAuth struct{}

func (rel023MetricsAuth) Identify(r *http.Request) *server.Principal {
	if r.Header.Get("Authorization") == "Bearer test-token" {
		return &server.Principal{User: "test", Method: "token", TokenID: "synthetic"}
	}
	return nil
}

type rel023EventReader struct {
	cancel context.CancelFunc
	done   chan struct{}
	events chan api.Event
}

func newREL023EventReader(conn *websocket.Conn) *rel023EventReader {
	ctx, cancel := context.WithCancel(context.Background())
	r := &rel023EventReader{cancel: cancel, done: make(chan struct{}), events: make(chan api.Event, 32)}
	go func() {
		defer func() {
			close(r.events)
			close(r.done)
		}()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var ev api.Event
			if json.Unmarshal(data, &ev) != nil {
				continue
			}
			select {
			case r.events <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return r
}

func closeREL023EventReader(t *testing.T, reader *rel023EventReader, conn *websocket.Conn) {
	t.Helper()
	reader.cancel()
	if conn != nil {
		_ = conn.CloseNow()
	}
	select {
	case <-reader.done:
	case <-time.After(time.Second):
		t.Error("events socket reader did not stop after cancellation")
	}
}

func (r *rel023EventReader) next(t *testing.T, timeout time.Duration) api.Event {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case ev, ok := <-r.events:
		if !ok {
			t.Fatal("events socket closed unexpectedly")
		}
		return ev
	case <-timer.C:
		t.Fatal("timed out waiting for events socket frame")
		return api.Event{}
	}
}

func sendREL023ClientEvent(t *testing.T, conn *websocket.Conn, ev api.ClientEvent) {
	t.Helper()
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write client event: %v", err)
	}
}

func roundTripREL023(t *testing.T, conn *websocket.Conn, reader *rel023EventReader) {
	t.Helper()
	sendREL023ClientEvent(t, conn, api.ClientEvent{Type: "ping"})
	for {
		if ev := reader.next(t, 3*time.Second); ev.Type == "pong" {
			return
		}
	}
}

func waitREL023(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not met before deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSystemMetricsFollowVisibleEventsSocketSubscribers(t *testing.T) {
	d := &core.Deps{
		Cfg:       config.Defaults(),
		Paths:     config.Paths{Home: t.TempDir()},
		Bus:       events.New(),
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version:   "test",
		StartedAt: time.Now().UTC(),
		Search:    core.NewSearchRegistry(),
	}
	d.InitSettings()
	var origin string
	router := server.NewRouter(rel023MetricsAuth{}, func() []string { return []string{origin} })
	a := &App{D: d, Router: router}
	if err := wireLive(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := wireSystem(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	systemStarters := 0
	for _, starter := range a.starters {
		if starter.name == "system" {
			systemStarters++
		}
	}
	if systemStarters != 1 {
		t.Fatalf("system starters = %d, want exactly one", systemStarters)
	}

	srv := httptest.NewServer(router)
	origin = srv.URL
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, len(a.starters))
	shutdownDone := false
	for _, starter := range a.starters {
		starter := starter
		go func() {
			_ = starter.fn(ctx)
			done <- struct{}{}
		}()
	}
	shutdown := func() {
		if shutdownDone {
			return
		}
		shutdownDone = true
		cancel()
		for range a.starters {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("background feature did not stop after cancellation")
				return
			}
		}
	}
	var conn *websocket.Conn
	var reader *rel023EventReader
	t.Cleanup(func() {
		if reader != nil {
			closeREL023EventReader(t, reader, conn)
		} else if conn != nil {
			_ = conn.CloseNow()
		}
		shutdown()
		srv.Close()
		if got := d.Bus.Subscribers(); got != 0 {
			t.Errorf("bus subscriptions after shutdown = %d, want 0", got)
		}
	})
	waitREL023(t, 3*time.Second, func() bool { return d.Bus.Subscribers() == 1 })

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/events"
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer test-token"}},
	})
	if err != nil {
		t.Fatalf("dial events socket: %v", err)
	}
	readCtx, readCancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, hello, err := conn.Read(readCtx)
	readCancel()
	if err != nil {
		t.Fatalf("read hello: %v", err)
	}
	var first api.Event
	if err := json.Unmarshal(hello, &first); err != nil || first.Type != api.EvHello {
		t.Fatalf("first frame = %s (%v), want hello", hello, err)
	}
	reader = newREL023EventReader(conn)

	visible := true
	sendREL023ClientEvent(t, conn, api.ClientEvent{Type: "visibility", Visible: &visible, Path: "/system"})
	sendREL023ClientEvent(t, conn, api.ClientEvent{Type: "subscribe", Topics: []string{"metrics"}})
	roundTripREL023(t, conn, reader)
	waitREL023(t, time.Second, func() bool { return d.Presence.Subscribed("metrics") })

	stamps := make([]time.Time, 0, 3)
	deadline := time.Now().Add(5 * time.Second)
	for len(stamps) < 3 {
		left := time.Until(deadline)
		if left <= 0 {
			t.Fatal("timed out waiting for three live metrics events")
		}
		ev := reader.next(t, left)
		if ev.Type == api.EvMetrics {
			stamps = append(stamps, ev.At)
		}
	}
	for i := 1; i < len(stamps); i++ {
		interval := stamps[i].Sub(stamps[i-1])
		if interval < 500*time.Millisecond || interval > 1800*time.Millisecond {
			t.Fatalf("metrics interval %s, want roughly 1 Hz", interval)
		}
	}

	hidden := false
	sendREL023ClientEvent(t, conn, api.ClientEvent{Type: "visibility", Visible: &hidden})
	roundTripREL023(t, conn, reader)
	if d.Presence.Subscribed("metrics") {
		t.Fatal("hidden browser still counts as an active metrics subscriber")
	}
	// Discard frames already queued before the hidden state reached the sampler.
	drain := time.NewTimer(100 * time.Millisecond)
Drain:
	for {
		select {
		case _, ok := <-reader.events:
			if !ok {
				t.Fatal("events socket closed while hidden")
			}
		case <-drain.C:
			break Drain
		}
	}
	quiet := time.NewTimer(1200 * time.Millisecond)
	defer quiet.Stop()
	for {
		select {
		case ev, ok := <-reader.events:
			if !ok {
				t.Fatal("events socket closed while hidden")
			}
			if ev.Type == api.EvMetrics {
				t.Fatal("hidden browser continued receiving live metrics")
			}
		case <-quiet.C:
			goto HiddenCheckComplete
		}
	}
HiddenCheckComplete:
	closeREL023EventReader(t, reader, conn)
	reader = nil
	conn = nil
	waitREL023(t, 3*time.Second, func() bool { return !d.Presence.Subscribed("metrics") })
	if got := d.Bus.Subscribers(); got != 1 {
		t.Fatalf("live bus subscriptions after socket reconnect = %d, want one service subscription", got)
	}

	dialCtx, dialCancel = context.WithTimeout(context.Background(), 3*time.Second)
	conn, _, err = websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer test-token"}},
	})
	dialCancel()
	if err != nil {
		t.Fatalf("redial events socket: %v", err)
	}
	readCtx, readCancel = context.WithTimeout(context.Background(), 3*time.Second)
	_, hello, err = conn.Read(readCtx)
	readCancel()
	if err != nil {
		t.Fatalf("read reconnect hello: %v", err)
	}
	if err := json.Unmarshal(hello, &first); err != nil || first.Type != api.EvHello {
		t.Fatalf("reconnect first frame = %s (%v), want hello", hello, err)
	}
	reader = newREL023EventReader(conn)
	sendREL023ClientEvent(t, conn, api.ClientEvent{Type: "visibility", Visible: &visible, Path: "/system"})
	sendREL023ClientEvent(t, conn, api.ClientEvent{Type: "subscribe", Topics: []string{"metrics"}})
	roundTripREL023(t, conn, reader)
	waitREL023(t, time.Second, func() bool { return d.Presence.Subscribed("metrics") })
	reconnectStamps := make([]time.Time, 0, 2)
	deadline = time.Now().Add(4 * time.Second)
	for len(reconnectStamps) < 2 {
		left := time.Until(deadline)
		if left <= 0 {
			t.Fatal("metrics did not resume after reconnect")
		}
		ev := reader.next(t, left)
		if ev.Type == api.EvMetrics {
			reconnectStamps = append(reconnectStamps, ev.At)
		}
	}
	interval := reconnectStamps[1].Sub(reconnectStamps[0])
	if interval < 500*time.Millisecond || interval > 1800*time.Millisecond {
		t.Fatalf("reconnected metrics interval %s, want roughly 1 Hz", interval)
	}
	closeREL023EventReader(t, reader, conn)
	reader = nil
	conn = nil
	waitREL023(t, 3*time.Second, func() bool { return !d.Presence.Subscribed("metrics") })
	shutdown()
	waitREL023(t, 3*time.Second, func() bool { return d.Bus.Subscribers() == 0 })
}
