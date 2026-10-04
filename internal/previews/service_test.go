package previews

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
)

// fakeSource returns a fixed socket list.
type fakeSource struct {
	mu    sync.Mutex
	socks []socket
	infos map[int]procInfo
}

func (f *fakeSource) set(s ...socket) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.socks = s
}

func (f *fakeSource) sockets(context.Context) ([]socket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]socket(nil), f.socks...), nil
}

func (f *fakeSource) info(_ context.Context, pid int) procInfo {
	if pi, ok := f.infos[pid]; ok {
		return pi
	}
	return procInfo{PID: pid, Exe: "node"}
}

// fakeAuth accepts relay_session=good (cookie) and Bearer rly_good (token).
type fakeAuth struct{}

func (fakeAuth) Identify(r *http.Request) *server.Principal {
	if c, err := r.Cookie("relay_session"); err == nil && c.Value == "good" {
		return &server.Principal{User: "me", SessionID: "sess1", Method: "cookie"}
	}
	if r.Header.Get("Authorization") == "Bearer rly_good" {
		return &server.Principal{User: "me", TokenID: "tok1", Method: "token"}
	}
	return nil
}

type fakeNotifier struct {
	mu   sync.Mutex
	reqs []api.NotifyRequest
}

func (n *fakeNotifier) Notify(_ context.Context, req api.NotifyRequest) (*api.Notification, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reqs = append(n.reqs, req)
	return &api.Notification{Title: req.Title}, nil
}

func (n *fakeNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.reqs)
}

type fakeWorkspaces struct{}

func (fakeWorkspaces) List(context.Context) ([]api.Workspace, error) { return nil, nil }
func (fakeWorkspaces) RootOf(p string) string {
	if strings.HasPrefix(p, "/work/app") {
		return "/work/app"
	}
	return ""
}

type harness struct {
	svc  *Service
	src  *fakeSource
	note *fakeNotifier
	rt   *server.Router
	h    http.Handler
	bus  *events.Bus
	now  time.Time
}

func newHarness(t *testing.T, mode, host, originURL string) *harness {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Defaults()
	cfg.Server.Listen = "127.0.0.1:47999"
	cfg.Server.PublicURL = originURL
	cfg.Previews.Mode = mode
	cfg.Previews.Host = host
	h := &harness{src: &fakeSource{}, note: &fakeNotifier{}, bus: events.New(), now: time.Unix(1_800_000_000, 0)}
	d := &core.Deps{
		Cfg: cfg, Store: st, Bus: h.bus, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notifier: h.note, Workspaces: fakeWorkspaces{}, Search: core.NewSearchRegistry(),
	}
	svc, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	svc.src = h.src
	svc.now = func() time.Time { return h.now }
	h.svc = svc
	h.rt = server.NewRouter(fakeAuth{}, func() []string { return []string{originURL} })
	svc.Routes(h.rt)
	h.h = h.rt
	t.Cleanup(func() { svc.Close() })
	return h
}

// upstreamServer is a fake dev server that records what it receives.
func upstreamServer(t *testing.T) (port int, reqs chan *http.Request) {
	t.Helper()
	reqs = make(chan *http.Request, 16)
	srv := previewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hmr" {
			reqs <- r.Clone(context.Background())
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			defer c.CloseNow()
			_ = c.Write(r.Context(), websocket.MessageText, []byte(`{"type":"connected"}`))
			_, _, _ = c.Read(r.Context())
			return
		}
		if r.Header.Get("User-Agent") == "relay-preview-probe/1" {
			// The detector's own HTTP check.
		} else {
			select {
			case reqs <- r.Clone(context.Background()):
			default:
			}
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Set-Cookie", "relay_session=stolen; Path=/")
		io.WriteString(w, `<html><head><script type="module" src="/@vite/client"></script><title>Demo</title></head></html>`)
	}))
	t.Cleanup(srv.Close)
	return serverPort(t, srv), reqs
}

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestPathModeProxy(t *testing.T) {
	h := newHarness(t, "path", "", "https://relay.example")
	port, reqs := upstreamServer(t)
	h.src.set(socket{IP: net.IPv4(127, 0, 0, 1), Port: port, PID: 4242, Key: 1})
	h.svc.scan(t.Context())
	front := previewTestServer(t, h.h)
	defer front.Close()
	base := front.URL + "/p/" + strconv.Itoa(port)

	// Unauthenticated navigation → login redirect.
	res, err := noRedirect().Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound || !strings.HasPrefix(res.Header.Get("Location"), "https://relay.example/login?next=") {
		t.Fatalf("anon = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	// Unauthenticated POST → 401.
	res, _ = noRedirect().Post(base+"/x", "text/plain", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("anon POST = %d", res.StatusCode)
	}

	req, _ := http.NewRequest("GET", base+"/index.html?x=1", nil)
	req.Header.Set("Cookie", "relay_session=good; app=1")
	res, err = noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), "Demo") {
		t.Fatalf("proxied = %d %s", res.StatusCode, body)
	}
	if csp := res.Header.Get("Content-Security-Policy"); csp != sandboxCSP {
		t.Errorf("CSP = %q", csp)
	}
	if sc := res.Header.Values("Set-Cookie"); len(sc) != 0 {
		t.Errorf("relay cookie set by upstream leaked: %q", sc)
	}
	up := <-reqs
	if up.URL.RequestURI() != "/index.html?x=1" || up.Header.Get("Cookie") != "app=1" || up.Host != "localhost:"+strconv.Itoa(port) {
		t.Errorf("upstream saw %s cookie=%q host=%q", up.URL.RequestURI(), up.Header.Get("Cookie"), up.Host)
	}

	// Trailing-slash redirect.
	req, _ = http.NewRequest("GET", base+"?a=b", nil)
	res, _ = noRedirect().Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusPermanentRedirect || res.Header.Get("Location") != "/p/"+strconv.Itoa(port)+"/?a=b" {
		t.Errorf("slash redirect = %d %q", res.StatusCode, res.Header.Get("Location"))
	}

	// A port that is not a detected listener is refused.
	req, _ = http.NewRequest("GET", front.URL+"/p/1234/", nil)
	req.Header.Set("Cookie", "relay_session=good")
	res, _ = noRedirect().Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown port = %d", res.StatusCode)
	}
	// Relay's own listen port is never proxied.
	h.src.set(socket{IP: net.IPv4(127, 0, 0, 1), Port: 47999, PID: 4242, Key: 9})
	h.now = h.now.Add(time.Minute)
	req, _ = http.NewRequest("GET", front.URL+"/p/47999/", nil)
	req.Header.Set("Cookie", "relay_session=good")
	res, _ = noRedirect().Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("own port = %d", res.StatusCode)
	}
}

func TestPathModeWebSocket(t *testing.T) {
	h := newHarness(t, "path", "", "http://relay.test")
	port, reqs := upstreamServer(t)
	h.src.set(socket{IP: net.IPv4zero, Port: port, PID: 1, Key: 1})
	h.svc.scan(t.Context())
	front := previewTestServer(t, h.h)
	defer front.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	u := "ws" + strings.TrimPrefix(front.URL, "http") + "/p/" + strconv.Itoa(port) + "/hmr"
	if _, _, err := websocket.Dial(ctx, u, nil); err == nil {
		t.Fatal("unauthenticated websocket accepted")
	}
	for _, origin := range []string{"", "null", "http://other.test"} {
		_, res, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {"relay_session=good"}, "Origin": {origin}}})
		if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
			t.Fatalf("origin %q: response = %v, err = %v", origin, res, err)
		}
	}
	if len(reqs) != 0 {
		t.Fatal("refused upgrade reached upstream")
	}
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {"relay_session=good"}, "Origin": {"http://relay.test"}}})
	if err != nil {
		t.Fatalf("websocket through path proxy: %v", err)
	}
	defer c.CloseNow()
	_, msg, err := c.Read(ctx)
	if err != nil || string(msg) != `{"type":"connected"}` {
		t.Fatalf("read = %q %v", msg, err)
	}
	if up := <-reqs; up.Header.Get("Cookie") != "" {
		t.Fatal("Relay cookie reached upstream")
	}
}

func TestSubdomainHandshake(t *testing.T) {
	h := newHarness(t, "subdomain", "relay.test", "https://relay.test")
	port, reqs := upstreamServer(t)
	h.src.set(socket{IP: net.IPv4(127, 0, 0, 1), Port: port, PID: 4242, Key: 1})
	h.svc.scan(t.Context())
	ps := strconv.Itoa(port)
	serve := func(method, target string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.h.ServeHTTP(w, r)
		return w
	}

	// 1. No cookie on the preview host → sent to the Relay origin.
	w := serve("GET", "https://"+ps+".relay.test/app?x=1", nil)
	loc := w.Header().Get("Location")
	wantAuth := "https://relay.test" + authPath + "?port=" + ps + "&next=" + url.QueryEscape("/app?x=1")
	if w.Code != http.StatusFound || loc != wantAuth {
		t.Fatalf("step1 = %d %q, want %q", w.Code, loc, wantAuth)
	}
	if w.Header().Get("X-Frame-Options") != "" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors") {
		t.Errorf("subdomain headers = %v", w.Header())
	}
	// Non-navigation without a cookie → 401, no redirect.
	if w := serve("POST", "https://"+ps+".relay.test/api", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anon POST = %d", w.Code)
	}

	// 2. Relay origin, not signed in → login.
	w = serve("GET", loc, nil)
	if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/login?next=") {
		t.Fatalf("step2 anon = %d %q", w.Code, w.Header().Get("Location"))
	}
	// Signed in → handshake token to the preview origin.
	w = serve("GET", loc, map[string]string{"Cookie": "relay_session=good"})
	cb := w.Header().Get("Location")
	if w.Code != http.StatusFound || !strings.HasPrefix(cb, "https://"+ps+".relay.test"+callbackPath+"?token=") {
		t.Fatalf("step2 = %d %q", w.Code, cb)
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("token redirect leaks referrer")
	}
	// The token cannot be used on another port's host.
	cbURL, _ := url.Parse(cb)
	other := "https://5999.relay.test" + callbackPath + "?" + cbURL.RawQuery
	h.src.set(socket{IP: net.IPv4(127, 0, 0, 1), Port: port, PID: 4242, Key: 1}, socket{IP: net.IPv4(127, 0, 0, 1), Port: 5999, PID: 1, Key: 2})
	if w := serve("GET", other, nil); w.Code != http.StatusForbidden {
		t.Errorf("token on other port = %d", w.Code)
	}

	// 3. Callback sets the host-only cookie and redirects to next.
	w = serve("GET", cb, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/app?x=1" {
		t.Fatalf("step3 = %d %q", w.Code, w.Header().Get("Location"))
	}
	var pc *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			pc = c
		}
	}
	if pc == nil || !pc.HttpOnly || !pc.Secure || pc.SameSite != http.SameSiteLaxMode || pc.Domain != "" || pc.MaxAge != 43200 {
		t.Fatalf("preview cookie = %+v", pc)
	}
	// Replaying the handshake fails.
	if w := serve("GET", cb, nil); w.Code != http.StatusForbidden {
		t.Errorf("replayed handshake = %d", w.Code)
	}

	// 4. With the cookie the request is proxied, cookie stripped.
	w = serve("GET", "https://"+ps+".relay.test/app?x=1", map[string]string{"Cookie": cookieName + "=" + pc.Value + "; mine=1"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Demo") {
		t.Fatalf("step4 = %d %s", w.Code, w.Body)
	}
	up := <-reqs
	if up.Header.Get("Cookie") != "mine=1" {
		t.Errorf("upstream cookie = %q", up.Header.Get("Cookie"))
	}
	// The cookie is bound to its port.
	if w := serve("GET", "https://5999.relay.test/", map[string]string{"Cookie": cookieName + "=" + pc.Value}); w.Code != http.StatusFound {
		t.Errorf("cookie on other port = %d", w.Code)
	}
	// API tokens work for scripts.
	if w := serve("GET", "https://"+ps+".relay.test/", map[string]string{"Authorization": "Bearer rly_good"}); w.Code != 200 {
		t.Errorf("bearer = %d", w.Code)
	}
	<-reqs
	// Expired cookie → handshake again.
	h.now = h.now.Add(cookieTTL + time.Minute)
	h.svc.tok.now = func() time.Time { return h.now }
	if w := serve("GET", "https://"+ps+".relay.test/", map[string]string{"Cookie": cookieName + "=" + pc.Value}); w.Code != http.StatusFound {
		t.Errorf("expired cookie = %d", w.Code)
	}
	// Hosts that are not preview hosts fall through to normal routing.
	if w := serve("GET", "https://relay.test/api/v1/previews", map[string]string{"Cookie": "relay_session=good"}); w.Code != 200 {
		t.Errorf("main host API = %d", w.Code)
	}
	// TLS policy: only listening ports under the base host.
	if !h.svc.TLSHostAllowed(ps+".relay.test") || h.svc.TLSHostAllowed("4444.relay.test") || h.svc.TLSHostAllowed("relay.test.evil") {
		t.Errorf("TLS host policy wrong")
	}
}

func TestListPatchAndEvents(t *testing.T) {
	h := newHarness(t, "path", "", "https://relay.example")
	sub := h.bus.Subscribe(16, nil)
	defer sub.Close()
	h.src.infos = map[int]procInfo{7: {PID: 7, Exe: "node", Cmdline: "node vite", Cwd: "/work/app/web"}}
	h.src.set(
		socket{IP: net.IPv4(127, 0, 0, 1), Port: 5173, PID: 7, Key: 1},
		socket{IP: net.IPv6unspecified, Port: 5173, PID: 7, Key: 2}, // same port, wider bind wins
		socket{IP: net.IPv4zero, Port: 80, PID: 7, Key: 3},          // below port_min
		socket{IP: net.IPv4zero, Port: 6000, PID: h.svc.selfPID, Key: 4},
	)
	h.svc.d.Cfg.Previews.Ignore = []int{6001}
	h.svc.scan(t.Context())
	list := h.svc.List()
	if len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
	p := list[0]
	if p.Port != 5173 || p.Address != "::" || p.Process != "node" || p.Workspace != "/work/app" || p.Label != "Vite" || p.URL != "https://relay.example/p/5173/" {
		t.Errorf("preview = %+v", p)
	}
	ev := <-sub.C
	if ev.Type != api.EvPreviewsChanged {
		t.Errorf("event = %s", ev.Type)
	}
	// Unchanged scan → no event.
	h.svc.scan(t.Context())
	select {
	case ev := <-sub.C:
		t.Errorf("unexpected event %s", ev.Type)
	default:
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("PATCH", "/api/v1/previews/5173", strings.NewReader(`{"label":"Web app","pinned":true}`))
	r.Header.Set("Authorization", "Bearer rly_good")
	h.h.ServeHTTP(w, r)
	var got api.Preview
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
		t.Fatalf("patch = %d %s", w.Code, w.Body)
	}
	if got.Label != "Web app" || !got.Pinned {
		t.Errorf("patched = %+v", got)
	}
	// Persisted.
	m, _ := h.svc.meta.all(t.Context())
	if m[5173].Label != "Web app" {
		t.Errorf("meta = %+v", m)
	}
	// Unauthenticated API → 401.
	w = httptest.NewRecorder()
	h.h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/previews", nil))
	if w.Code != 401 {
		t.Errorf("anon list = %d", w.Code)
	}
	// Link endpoint.
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/api/v1/previews/5173/link", nil)
	r.Header.Set("Authorization", "Bearer rly_good")
	h.h.ServeHTTP(w, r)
	var link api.PreviewLink
	_ = json.Unmarshal(w.Body.Bytes(), &link)
	if !link.Listening || link.URL != "https://relay.example/p/5173/" || link.Preview == nil || link.Mode != "path" {
		t.Errorf("link = %d %+v", w.Code, link)
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/api/v1/previews/5999/link", nil)
	r.Header.Set("Authorization", "Bearer rly_good")
	h.h.ServeHTTP(w, r)
	link = api.PreviewLink{}
	_ = json.Unmarshal(w.Body.Bytes(), &link)
	if link.Listening || link.Preview != nil || link.URL == "" {
		t.Errorf("idle link = %+v", link)
	}
	// Search provider.
	res := h.svc.SearchProvider().Search(t.Context(), "web", 5)
	if len(res) != 1 || res[0].ID != "5173" || res[0].Meta["url"] == "" {
		t.Errorf("search = %+v", res)
	}
	if res := h.svc.SearchProvider().Search(t.Context(), ":517", 5); len(res) != 1 {
		t.Errorf("port search = %+v", res)
	}
}

func TestNotifications(t *testing.T) {
	h := newHarness(t, "path", "", "https://relay.example")
	port, _ := upstreamServer(t)
	other, _ := upstreamServer(t)
	// Baseline: servers already running at startup never notify.
	h.src.set(socket{IP: net.IPv4(127, 0, 0, 1), Port: other, PID: 2, Key: 20})
	h.svc.scan(t.Context())
	h.now = h.now.Add(10 * time.Second)
	h.svc.scan(t.Context())
	if n := h.note.count(); n != 0 {
		t.Fatalf("baseline notified %d", n)
	}
	// A new server: nothing before 3 s, one notification after.
	h.src.set(socket{IP: net.IPv4(127, 0, 0, 1), Port: other, PID: 2, Key: 20}, socket{IP: net.IPv4(127, 0, 0, 1), Port: port, PID: 3, Key: 30})
	h.svc.scan(t.Context())
	h.now = h.now.Add(2 * time.Second)
	h.svc.scan(t.Context())
	if n := h.note.count(); n != 0 {
		t.Fatalf("notified before stable: %d", n)
	}
	h.now = h.now.Add(2 * time.Second)
	h.svc.scan(t.Context())
	h.now = h.now.Add(2 * time.Second)
	h.svc.scan(t.Context())
	if n := h.note.count(); n != 1 {
		t.Fatalf("notifications = %d, want 1", n)
	}
	req := h.note.reqs[0]
	if req.Kind != "preview" || req.Title != "Vite on :"+strconv.Itoa(port) || !strings.Contains(req.Body, "Demo") {
		t.Errorf("notification = %+v", req)
	}
	// Restart within the cooldown: no second notification.
	h.src.set(socket{IP: net.IPv4(127, 0, 0, 1), Port: other, PID: 2, Key: 20})
	h.svc.scan(t.Context())
	h.src.set(socket{IP: net.IPv4(127, 0, 0, 1), Port: other, PID: 2, Key: 20}, socket{IP: net.IPv4(127, 0, 0, 1), Port: port, PID: 4, Key: 31})
	h.svc.scan(t.Context())
	h.now = h.now.Add(5 * time.Second)
	h.svc.scan(t.Context())
	if n := h.note.count(); n != 1 {
		t.Errorf("cooldown broken: %d", n)
	}
}

func TestProbeSilentServerTimesOut(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var conns []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	defer func() {
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
	}()
	start := time.Now()
	r := newProber().probe(t.Context(), "127.0.0.1", ln.Addr().(*net.TCPAddr).Port)
	if r.HTTP {
		t.Error("silent server reported as HTTP")
	}
	if el := time.Since(start); el < 900*time.Millisecond || el > 1600*time.Millisecond {
		t.Errorf("probe took %v, want ≈1s", el)
	}
}
