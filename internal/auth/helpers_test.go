package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
)

const testPassword = "correct horse battery"

// testEnv is a running auth service behind a real router.
type testEnv struct {
	t      *testing.T
	svc    *Service
	d      *core.Deps
	rt     *server.Router
	srv    *httptest.Server
	base   string // URL clients use
	origin string // allowed browser origin
	// trusted proxies predicate used by the test server
	trusted func(net.IP) bool
	// Records rejected browser origins without exposing cookies or bodies.
	rejections chan string
}

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

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

type envOpt func(cfg *config.Config)

// newEnv starts the service. host is "127.0.0.1" or "localhost" (passkeys
// need a host name).
func newEnv(t *testing.T, host string, opts ...envOpt) *testEnv {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &testEnv{t: t, rejections: make(chan string, 64)}
	srv := &httptest.Server{Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := &authFixtureStatus{ResponseWriter: w}
		server.WithRequestInfo(e.trusted, e.rt).ServeHTTP(status, r)
		if status.code == http.StatusForbidden && r.URL.Path == "/api/v1/test/whoami" {
			select {
			case e.rejections <- r.Header.Get("Origin"):
			default:
			}
		}
	})}}
	// Keep integration fixtures inside Relay's shared-machine test range.
	for port := 47740; port <= 47779; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			srv.Listener = ln
			break
		}
		if port == 47779 {
			t.Fatalf("no free auth fixture port in 47740–47779: %v", err)
		}
	}
	port := srv.Listener.Addr().String()[strings.LastIndexByte(srv.Listener.Addr().String(), ':'):]
	cfg := config.Defaults()
	cfg.Server.TLS = "off"
	cfg.Server.Listen = "127.0.0.1" + port
	e.origin = "http://" + host + port
	if host != "127.0.0.1" {
		cfg.Server.PublicURL = e.origin
	}
	for _, o := range opts {
		o(cfg)
	}
	e.d = &core.Deps{
		Cfg: cfg, Store: st, Bus: events.New(),
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Search: core.NewSearchRegistry(),
	}
	svc, err := New(e.d)
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc
	e.rt = server.NewRouter(nil, func() []string {
		canonical, ok := server.NormalizeOrigin(cfg.Origin())
		if !ok {
			return nil
		}
		return append([]string{canonical}, server.LocalOriginAliases(canonical)...)
	})
	svc.SetOrigins(e.rt.AllowedOrigins)
	svc.Routes(e.rt)
	e.rt.SetAuthenticator(svc)
	// Probe routes for CSRF and principal checks.
	whoami := func(w http.ResponseWriter, r *http.Request) { httpx.OK(w, server.PrincipalFrom(r.Context())) }
	e.rt.Handle("GET /api/v1/test/whoami", whoami)
	e.rt.Handle("POST /api/v1/test/whoami", whoami)
	srv.Start()
	t.Cleanup(srv.Close)
	e.srv = srv
	e.base = e.origin
	return e
}

type authFixtureStatus struct {
	http.ResponseWriter
	code int
}

func (w *authFixtureStatus) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

// client is a browser-like client with a cookie jar that sends Origin on
// unsafe requests.
type client struct {
	e      *testEnv
	hc     *http.Client
	ua     string
	origin string            // "" = send no Origin
	header map[string]string // extra headers
}

func (e *testEnv) browser(ua string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, hc: &http.Client{Jar: jar, Timeout: 20 * time.Second}, ua: ua, origin: e.origin, header: map[string]string{}}
}

type resp struct {
	status int
	body   []byte
	header http.Header
}

func (r resp) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (r resp) errCode(t *testing.T) string {
	t.Helper()
	var eb api.ErrorBody
	r.decode(t, &eb)
	return eb.Error.Code
}

func (c *client) do(method, path string, body any) resp {
	c.e.t.Helper()
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case []byte:
			rd = bytes.NewReader(b)
		default:
			j, _ := json.Marshal(body)
			rd = bytes.NewReader(j)
		}
	}
	req, err := http.NewRequest(method, c.e.base+path, rd)
	if err != nil {
		c.e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.ua != "" {
		req.Header.Set("User-Agent", c.ua)
	}
	if c.origin != "" && httpx.IsUnsafeMethod(method) {
		req.Header.Set("Origin", c.origin)
	}
	for k, v := range c.header {
		req.Header.Set(k, v)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{status: res.StatusCode, body: b, header: res.Header}
}

func (c *client) expect(status int, method, path string, body any) resp {
	c.e.t.Helper()
	r := c.do(method, path, body)
	if r.status != status {
		c.e.t.Fatalf("%s %s: status %d, want %d: %s", method, path, r.status, status, r.body)
	}
	return r
}

const (
	uaMacChrome  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
	uaIPhone     = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
	uaAndroidFox = "Mozilla/5.0 (Android 14; Mobile; rv:130.0) Gecko/130.0 Firefox/130.0"
)

// setup creates the account through the API and returns the signed-in client.
func (e *testEnv) setup() *client {
	e.t.Helper()
	c := e.browser(uaMacChrome)
	c.expect(200, "POST", "/api/v1/auth/setup", api.SetupRequest{Username: "owner", Password: testPassword, Remember: true})
	return c
}

func (e *testEnv) login(ua string) *client {
	e.t.Helper()
	c := e.browser(ua)
	c.expect(200, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword})
	return c
}

func newDepsWith(cfg *config.Config) *core.Deps { return &core.Deps{Cfg: cfg} }
