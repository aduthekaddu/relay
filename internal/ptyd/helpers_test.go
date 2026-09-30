package ptyd

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

// testEnv is a daemon on a private socket plus a client for it.
type testEnv struct {
	t      *testing.T
	d      *Daemon
	c      *ptyclient.Client
	paths  config.Paths
	cfg    *config.Config
	cancel context.CancelFunc
	done   chan error
}

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	// Unix socket paths are limited to ~104 bytes: keep the root short.
	root, err := os.MkdirTemp("", "ptyd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	p := config.Paths{
		Home:       root,
		ConfigDir:  filepath.Join(root, "config"),
		DataDir:    filepath.Join(root, "data"),
		RuntimeDir: filepath.Join(root, "run"),
		CacheDir:   filepath.Join(root, "cache"),
	}
	p.PtydSocket = filepath.Join(p.RuntimeDir, "ptyd.sock")
	p.CtlSocket = filepath.Join(p.RuntimeDir, "relay.sock")
	p.Uploads = filepath.Join(p.DataDir, "uploads")
	p.Recordings = filepath.Join(p.DataDir, "recordings")
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	return p
}

func testConfig(p config.Paths) *config.Config {
	cfg := config.Defaults()
	cfg.Terminal.Shell = "/bin/sh"
	cfg.Terminal.DefaultCwd = p.Home
	cfg.Terminal.Record = "off"
	cfg.Desktop.Enabled = false
	cfg.Agents.IdleSeconds = 1
	return cfg
}

func startEnv(t *testing.T, paths config.Paths, mutate func(*config.Config)) *testEnv {
	t.Helper()
	cfg := testConfig(paths)
	if mutate != nil {
		mutate(cfg)
	}
	off := false
	d, err := New(Options{Cfg: cfg, Paths: paths, Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})), LoginEnv: &off})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &testEnv{t: t, d: d, c: ptyclient.New(paths.PtydSocket), paths: paths, cfg: cfg, cancel: cancel, done: make(chan error, 1)}
	go func() { e.done <- d.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for e.c.Health(context.Background()) != nil {
		if time.Now().After(deadline) {
			t.Fatal("daemon did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(e.stop)
	return e
}

func (e *testEnv) stop() {
	if e.cancel == nil {
		return
	}
	e.cancel()
	e.cancel = nil
	select {
	case err := <-e.done:
		if err != nil {
			e.t.Errorf("daemon: %v", err)
		}
	case <-time.After(10 * time.Second):
		e.t.Error("daemon did not stop")
	}
}

func (e *testEnv) create(spec ptyclient.CreateSpec) *api.TerminalSession {
	e.t.Helper()
	s, err := e.c.Create(context.Background(), spec)
	if err != nil {
		e.t.Fatalf("create: %v", err)
	}
	return s
}

func shSpec(script string) ptyclient.CreateSpec {
	return ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Command: []string{"/bin/sh", "-c", script}}}
}

// waitExit polls until the session exited and returns it.
func (e *testEnv) waitExit(id string, timeout time.Duration) *api.TerminalSession {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s, err := e.c.Get(context.Background(), id)
		if err != nil {
			e.t.Fatal(err)
		}
		if s.Activity == api.ActivityExited {
			return s
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("session %s did not exit (activity %s)", id, s.Activity)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitSession polls until cond holds.
func (e *testEnv) waitSession(id string, timeout time.Duration, what string, cond func(*api.TerminalSession) bool) *api.TerminalSession {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s, err := e.c.Get(context.Background(), id)
		if err != nil {
			e.t.Fatal(err)
		}
		if cond(s) {
			return s
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("timeout waiting for %s: %+v", what, s)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// attached is a test WebSocket client collecting output and messages.
type attached struct {
	t    *testing.T
	conn *websocket.Conn

	mu      sync.Mutex
	out     bytes.Buffer
	msgs    []api.TermServerMsg
	replay  []byte
	inRep   bool
	closed  bool
	changed chan struct{}
}

func (e *testEnv) attach(id string, opts ptyclient.AttachOptions) *attached {
	e.t.Helper()
	conn, err := e.c.Attach(context.Background(), id, opts)
	if err != nil {
		e.t.Fatalf("attach: %v", err)
	}
	a := &attached{t: e.t, conn: conn, changed: make(chan struct{}, 1)}
	go a.read()
	e.t.Cleanup(func() { conn.CloseNow() })
	return a
}

func (a *attached) read() {
	for {
		typ, data, err := a.conn.Read(context.Background())
		a.mu.Lock()
		if err != nil {
			a.closed = true
			a.mu.Unlock()
			signal(a.changed)
			return
		}
		if typ == websocket.MessageBinary {
			if a.inRep {
				a.replay = append(a.replay, data...)
			} else {
				a.out.Write(data)
			}
		} else {
			var m api.TermServerMsg
			_ = json.Unmarshal(data, &m)
			switch m.T {
			case "replay-begin":
				a.inRep = true
			case "replay-end":
				a.inRep = false
			}
			a.msgs = append(a.msgs, m)
		}
		a.mu.Unlock()
		signal(a.changed)
	}
}

func (a *attached) waitFor(what string, timeout time.Duration, cond func() bool) {
	a.t.Helper()
	deadline := time.After(timeout)
	for {
		a.mu.Lock()
		ok := cond()
		a.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-a.changed:
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			a.mu.Lock()
			out := a.out.String()
			a.mu.Unlock()
			a.t.Fatalf("timeout waiting for %s; output so far: %q", what, out)
		}
	}
}

func (a *attached) waitOutput(substr string) {
	a.t.Helper()
	a.waitFor("output "+substr, 5*time.Second, func() bool { return strings.Contains(a.out.String(), substr) })
}

func (a *attached) waitMsg(typ string) api.TermServerMsg {
	a.t.Helper()
	var found api.TermServerMsg
	a.waitFor("message "+typ, 5*time.Second, func() bool {
		for _, m := range a.msgs {
			if m.T == typ {
				found = m
				return true
			}
		}
		return false
	})
	return found
}

func (a *attached) send(s string) {
	a.t.Helper()
	if err := a.conn.Write(context.Background(), websocket.MessageBinary, []byte(s)); err != nil {
		a.t.Fatal(err)
	}
}

func (a *attached) control(m api.TermClientMsg) {
	a.t.Helper()
	b, _ := json.Marshal(m)
	if err := a.conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		a.t.Fatal(err)
	}
}

func (a *attached) output() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.out.String()
}

// events subscribes to daemon events and collects them.
type eventLog struct {
	mu  sync.Mutex
	evs []ptyclient.PtyEvent
}

func (e *testEnv) events() *eventLog {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e.t.Cleanup(cancel)
	ch, err := e.c.Events(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	l := &eventLog{}
	go func() {
		for ev := range ch {
			l.mu.Lock()
			l.evs = append(l.evs, ev)
			l.mu.Unlock()
		}
	}()
	return l
}

func (l *eventLog) wait(t *testing.T, typ, id string) ptyclient.PtyEvent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		for _, ev := range l.evs {
			if ev.Type == typ && (id == "" || ev.ID == id) {
				l.mu.Unlock()
				return ev
			}
		}
		l.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no %s event for %s", typ, id)
	return ptyclient.PtyEvent{}
}
