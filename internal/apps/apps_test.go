package apps

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/server"
)

// TestHelperProcess is the fake app binary: the test executable re-run
// with GO_APPS_HELPER=1 and a mode after "--".
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_APPS_HELPER") != "1" {
		return
	}
	mode := ""
	for i, a := range os.Args {
		if a == "--" && i+1 < len(os.Args) {
			mode = os.Args[i+1]
		}
	}
	switch mode {
	case "serve":
		ln, err := net.Listen("unix", os.Getenv("RELAY_APP_SOCKET"))
		if err != nil {
			os.Exit(2)
		}
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Upstream-Path", r.URL.Path)
			w.Header().Set("X-Saw-Cookie", r.Header.Get("Cookie"))
			fmt.Fprintf(w, "hello from %s", r.URL.Path)
		}))
	case "exit":
		fmt.Fprintln(os.Stderr, "fatal: bad config")
		os.Exit(3)
	case "hang":
		select {}
	}
	os.Exit(0)
}

func helperArgv(mode string) []string {
	return []string{os.Args[0], "-test.run=^TestHelperProcess$", "--", mode}
}

func helperEnv(extra ...string) []string {
	return append(append(os.Environ(), "GO_APPS_HELPER=1"), extra...)
}

func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ra")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestProcLifecycle(t *testing.T) {
	sock := filepath.Join(shortDir(t), "a.sock")
	p := newProc(procSpec{
		Name: "fake", Argv: helperArgv("serve"), Env: helperEnv("RELAY_APP_SOCKET=" + sock),
		Ready: socketReady(sock), ReadyTimeout: 10 * time.Second, StopGrace: time.Second,
	}, nil)
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	pid := p.PID()
	if st, _, _ := p.Status(); st != stateRunning || pid == 0 {
		t.Fatalf("state %s pid %d", st, pid)
	}
	if err := p.Start(ctx); err != nil || p.PID() != pid {
		t.Fatal("second Start must reuse the running child")
	}
	if err := p.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := p.Status(); st != stateStopped {
		t.Fatalf("state after stop = %s", st)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("pid %d still alive after Stop", pid)
	}
}

func TestProcFailures(t *testing.T) {
	sock := filepath.Join(shortDir(t), "never.sock")
	tests := []struct {
		name, mode, want string
	}{
		{"exits before ready", "exit", "fatal: bad config"},
		{"never ready", "hang", "did not become ready"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newProc(procSpec{
				Name: "fake", Argv: helperArgv(tt.mode), Env: helperEnv(),
				Ready: socketReady(sock), ReadyTimeout: 700 * time.Millisecond,
			}, nil)
			err := p.Start(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if st, _, _ := p.Status(); st != stateError {
				t.Fatalf("state = %s", st)
			}
		})
	}
}

func TestProcMissingBinary(t *testing.T) {
	p := newProc(procSpec{Name: "fake", Argv: []string{"/nonexistent/relay-test-bin"}}, nil)
	if err := p.Start(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}

// ---------------------------------------------------------------------------
// Service with a user app and the /apps proxy

type fakeAuth struct{}

func (fakeAuth) Identify(r *http.Request) *server.Principal {
	if r.Header.Get("X-Test-Auth") == "" {
		return nil
	}
	return &server.Principal{User: "me", Method: r.Header.Get("X-Test-Auth")}
}

func newTestService(t *testing.T, mut func(*config.Config)) (*Service, *server.Router) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Code.Enabled = false
	cfg.Desktop.Enabled = false
	cfg.Apps = []config.AppConfig{{
		ID: "web", Name: "Web", Command: helperArgv("serve"), Socket: "web.sock",
		Env: map[string]string{"GO_APPS_HELPER": "1"},
	}}
	if mut != nil {
		mut(cfg)
	}
	root := shortDir(t)
	d := &core.Deps{
		Cfg: cfg, Bus: events.New(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Paths: config.Paths{Home: root, DataDir: filepath.Join(root, "data"), RuntimeDir: filepath.Join(root, "run")},
	}
	s, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	rt := server.NewRouter(fakeAuth{}, func() []string { return []string{cfg.Origin()} })
	s.Routes(rt)
	return s, rt
}

func do(rt http.Handler, method, path, auth string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if auth != "" {
		r.Header.Set("X-Test-Auth", auth)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)
	return w
}

func TestAppProxyStartsOnDemand(t *testing.T) {
	s, rt := newTestService(t, nil)
	nav := map[string]string{"Accept": "text/html", "Sec-Fetch-Mode": "navigate"}

	if w := do(rt, "GET", "/apps/web/hello", "", nav); w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/login?next=") {
		t.Fatalf("anonymous: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := do(rt, "GET", "/apps/web", "token", nil); w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != "/apps/web/" {
		t.Fatalf("redirect: %d %q", w.Code, w.Header().Get("Location"))
	}
	var w *httptest.ResponseRecorder
	deadline := time.Now().Add(15 * time.Second)
	for {
		w = do(rt, "GET", "/apps/web/hello?x=1", "token", map[string]string{"Accept": "text/html", "Sec-Fetch-Mode": "navigate", "Cookie": "relay_session=secret"})
		if w.Code != http.StatusServiceUnavailable || time.Now().After(deadline) {
			break
		}
		if !strings.Contains(w.Body.String(), "Starting Web") {
			t.Fatalf("503 without the starting page: %s", w.Body.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if w.Code != http.StatusOK || w.Header().Get("X-Upstream-Path") != "/hello" {
		t.Fatalf("proxied: %d path=%q body=%q", w.Code, w.Header().Get("X-Upstream-Path"), w.Body.String())
	}
	if strings.Contains(w.Header().Get("X-Saw-Cookie"), "relay_session") {
		t.Error("Relay session cookie leaked to the app")
	}
	if got := s.appState(s.apps["web"]).State; got != stateRunning {
		t.Fatalf("state = %s", got)
	}

	// Cookie-authenticated unsafe request from another origin is refused.
	if w := do(rt, "POST", "/apps/web/x", "cookie", map[string]string{"Origin": "https://evil.example"}); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST: %d", w.Code)
	}

	// Idle stop: never touched since the probe above for longer than idle_stop.
	s.apps["web"].idle = func() time.Duration { return time.Millisecond }
	s.checkIdle(context.Background(), time.Now().Add(time.Minute))
	if got := s.appState(s.apps["web"]).State; got != stateStopped {
		t.Fatalf("after idle check: %s", got)
	}
}

func TestAppsAPI(t *testing.T) {
	s, rt := newTestService(t, nil)
	if w := do(rt, "GET", "/api/v1/apps", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: %d", w.Code)
	}
	list := s.List()
	if len(list) != 2 || list[0].ID != "desktop" || list[1].ID != "web" || list[0].State != "unavailable" {
		t.Fatalf("list = %+v", list)
	}
	if w := do(rt, "POST", "/api/v1/apps/nope/start", "token", nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown app: %d", w.Code)
	}
	if w := do(rt, "POST", "/api/v1/apps/web/start", "token", nil); w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	a := s.apps["web"]
	waitFor(t, func() bool { st, _, _ := a.proc.Status(); return st == stateRunning })
	if w := do(rt, "POST", "/api/v1/apps/web/stop", "token", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"stopped"`) {
		t.Fatalf("stop: %d %s", w.Code, w.Body.String())
	}
	if w := do(rt, "POST", "/api/v1/desktop/start", "token", nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled desktop start: %d", w.Code)
	}
}

func TestUserAppValidation(t *testing.T) {
	tests := []struct {
		name string
		app  config.AppConfig
	}{
		{"reserved id", config.AppConfig{ID: "code", Port: 9000}},
		{"bad id", config.AppConfig{ID: "../x", Port: 9000}},
		{"no target", config.AppConfig{ID: "a"}},
		{"both targets", config.AppConfig{ID: "a", Port: 9000, Socket: "a.sock"}},
		{"bad port", config.AppConfig{ID: "a", Port: 70000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestService(t, func(c *config.Config) { c.Apps = []config.AppConfig{tt.app} })
			if len(s.apps) != 0 {
				t.Fatalf("invalid app accepted: %+v", tt.app)
			}
		})
	}
}
