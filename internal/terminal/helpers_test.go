package terminal

import (
	"bytes"
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
	"sync"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/ptyd"
	"github.com/aduthekaddu/relay/internal/server"
)

// tokenAuth authenticates every request as a token caller (no Origin
// check), which is what API clients and the CLI look like.
type tokenAuth struct{}

func (tokenAuth) Identify(*http.Request) *server.Principal {
	return &server.Principal{User: "owner", Method: "token"}
}

// fakeNotifier records notifications.
type fakeNotifier struct {
	mu   sync.Mutex
	reqs []api.NotifyRequest
}

func (f *fakeNotifier) Notify(_ context.Context, req api.NotifyRequest) (*api.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	return &api.Notification{ID: "n1", Kind: req.Kind, Title: req.Title}, nil
}

func (f *fakeNotifier) all() []api.NotifyRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]api.NotifyRequest(nil), f.reqs...)
}

type env struct {
	t     *testing.T
	svc   *Service
	d     *core.Deps
	srv   *httptest.Server
	paths config.Paths
	files string // files root
}

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	// Unix socket paths are short-limited: keep the root short.
	root, err := os.MkdirTemp("", "rt")
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

// newEnv starts a real ptyd on a private socket and serves the terminal
// routes on an httptest server. withDaemon=false leaves ptyd down.
func newEnv(t *testing.T, withDaemon bool, mutate func(*config.Config)) *env {
	t.Helper()
	paths := testPaths(t)
	cfg := config.Defaults()
	cfg.Terminal.Shell = "/bin/sh"
	cfg.Terminal.DefaultCwd = paths.Home
	cfg.Terminal.Record = "off"
	cfg.Terminal.ImportTmux = false
	cfg.Terminal.UploadMaxMB = 16
	cfg.Desktop.Enabled = false
	cfg.Agents.IdleSeconds = 1
	files := filepath.Join(paths.Home, "files")
	if err := os.MkdirAll(files, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg.Files.Root = files
	if mutate != nil {
		mutate(cfg)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if withDaemon {
		off := false
		dm, err := ptyd.New(ptyd.Options{Cfg: cfg, Paths: paths, Log: log, LoginEnv: &off})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- dm.Run(ctx) }()
		c := ptyclient.New(paths.PtydSocket)
		deadline := time.Now().Add(5 * time.Second)
		for c.Health(context.Background()) != nil {
			if time.Now().After(deadline) {
				t.Fatal("ptyd did not start")
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Cleanup(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("ptyd did not stop")
			}
		})
	}
	d := &core.Deps{
		Cfg:    cfg,
		Paths:  paths,
		Bus:    events.New(),
		Log:    log,
		Pty:    ptyclient.New(paths.PtydSocket),
		Search: core.NewSearchRegistry(),
	}
	svc, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	rt := server.NewRouter(tokenAuth{}, func() []string { return nil })
	svc.Routes(rt)
	var ln net.Listener
	for port := 47700; port <= 47799; port++ {
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal("no reserved loopback test port available")
	}
	srv := httptest.NewUnstartedServer(rt)
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return &env{t: t, svc: svc, d: d, srv: srv, paths: paths, files: files}
}

// call sends a request and decodes a JSON answer into out when non-nil.
// It returns the status code.
func (e *env) call(method, path string, body any, out any) int {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if out != nil && res.StatusCode < 300 && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			e.t.Fatalf("%s %s: decode %q: %v", method, path, data, err)
		}
	}
	if res.StatusCode >= 300 && out != nil {
		if eb, ok := out.(*api.ErrorBody); ok {
			_ = json.Unmarshal(data, eb)
		}
	}
	return res.StatusCode
}

func (e *env) create(req api.CreateTerminalRequest) *api.TerminalSession {
	e.t.Helper()
	var s api.TerminalSession
	if code := e.call("POST", "/api/v1/terminals", req, &s); code != http.StatusCreated {
		e.t.Fatalf("create: status %d", code)
	}
	return &s
}

// waitSnapshot polls the snapshot until it contains want.
func (e *env) waitSnapshot(id, want string) string {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var snap api.TerminalSnapshot
	for time.Now().Before(deadline) {
		if e.call("GET", "/api/v1/terminals/"+id+"/snapshot?lines=50", nil, &snap) == 200 && strings.Contains(snap.Text, want) {
			return snap.Text
		}
		time.Sleep(30 * time.Millisecond)
	}
	e.t.Fatalf("snapshot never contained %q; last: %q", want, snap.Text)
	return ""
}

func (e *env) waitExit(id string) *api.TerminalSession {
	e.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var s api.TerminalSession
		if e.call("GET", "/api/v1/terminals/"+id, nil, &s) == 200 && s.Activity == api.ActivityExited {
			return &s
		}
		time.Sleep(30 * time.Millisecond)
	}
	e.t.Fatalf("session %s did not exit", id)
	return nil
}

func sh(script string) api.CreateTerminalRequest {
	return api.CreateTerminalRequest{Command: []string{"/bin/sh", "-c", script}}
}
