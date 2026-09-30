package revproxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type seen struct {
	path, host, cookie, auth, xff, xfh, xfp, xfprefix, origin string
}

func upstream(t *testing.T, got chan<- seen) (*httptest.Server, int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			defer c.CloseNow()
			for {
				typ, b, err := c.Read(r.Context())
				if err != nil {
					return
				}
				if err := c.Write(r.Context(), typ, append([]byte("echo:"), b...)); err != nil {
					return
				}
			}
		}
		got <- seen{
			path: r.URL.RequestURI(), host: r.Host, cookie: r.Header.Get("Cookie"),
			auth: r.Header.Get("Authorization"), xff: r.Header.Get("X-Forwarded-For"),
			xfh: r.Header.Get("X-Forwarded-Host"), xfp: r.Header.Get("X-Forwarded-Proto"),
			xfprefix: r.Header.Get("X-Forwarded-Prefix"), origin: r.Header.Get("Origin"),
		}
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/login?x=1", http.StatusFound)
		case "/abs-redirect":
			w.Header().Set("Location", "http://localhost:"+strconv.Itoa(portOf(r.Host))+"/abs")
			w.WriteHeader(http.StatusFound)
		case "/cookies":
			w.Header().Add("Set-Cookie", "sid=1; Path=/; Domain=example.com; HttpOnly")
			w.Header().Add("Set-Cookie", "relay_session=evil; Path=/")
			w.Header().Add("Set-Cookie", "__Host-relay_session=evil; Path=/; Secure")
			w.Header().Add("Set-Cookie", "pref=dark")
			w.Header().Set("Clear-Site-Data", `"cookies"`)
			w.Header().Set("Strict-Transport-Security", "max-age=1; includeSubDomains")
			w.Header().Set("Service-Worker-Allowed", "/")
			io.WriteString(w, "ok")
		default:
			io.WriteString(w, "hello")
		}
	}))
	t.Cleanup(srv.Close)
	_, ps, _ := net.SplitHostPort(srv.Listener.Addr().String())
	p, _ := strconv.Atoi(ps)
	return srv, p
}

func portOf(host string) int {
	_, ps, _ := net.SplitHostPort(host)
	p, _ := strconv.Atoi(ps)
	return p
}

func TestProxyRequestHygiene(t *testing.T) {
	got := make(chan seen, 4)
	_, port := upstream(t, got)
	p := New(Target{Dial: LoopbackTCP(port, time.Second), Host: "localhost:" + strconv.Itoa(port)},
		Options{StripPrefix: "/p/" + strconv.Itoa(port) + "/", Proto: "https", PublicOrigin: "https://preview.example"})
	defer p.Close()

	pre := "/p/" + strconv.Itoa(port)
	req := httptest.NewRequest("POST", "https://relay.example"+pre+"/api/x?q=1", strings.NewReader("{}"))
	req.RemoteAddr = "203.0.113.9:5555"
	req.Header.Set("Cookie", "relay_session=SECRET; theme=dark; __Host-relay_session=SECRET2")
	req.Header.Add("Cookie", "relay_preview=TOKEN; app=1")
	req.Header.Set("Authorization", "Bearer rly_secret")
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	req.Header.Set("X-Forwarded-Host", "spoofed")
	req.Header.Set("Origin", "https://preview.example")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	s := <-got
	checks := []struct{ name, got, want string }{
		{"path", s.path, "/api/x?q=1"},
		{"host", s.host, "localhost:" + strconv.Itoa(port)},
		{"cookie", s.cookie, "theme=dark; app=1"},
		{"authorization", s.auth, ""},
		{"x-forwarded-for", s.xff, "203.0.113.9"},
		{"x-forwarded-host", s.xfh, "relay.example"},
		{"x-forwarded-proto", s.xfp, "https"},
		{"x-forwarded-prefix", s.xfprefix, pre},
		{"origin", s.origin, "http://localhost:" + strconv.Itoa(port)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestProxyResponseRewrites(t *testing.T) {
	got := make(chan seen, 8)
	_, port := upstream(t, got)
	pre := "/p/" + strconv.Itoa(port)
	p := New(Target{Dial: LoopbackTCP(port, time.Second), Host: "localhost:" + strconv.Itoa(port)}, Options{StripPrefix: pre})
	defer p.Close()
	do := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest("GET", "http://relay.example"+pre+path, nil))
		<-got
		return rec
	}
	if loc := do("/redirect").Header().Get("Location"); loc != pre+"/login?x=1" {
		t.Errorf("Location = %q", loc)
	}
	if loc := do("/abs-redirect").Header().Get("Location"); loc != pre+"/abs" {
		t.Errorf("absolute Location = %q", loc)
	}
	rec := do("/cookies")
	cookies := rec.Header().Values("Set-Cookie")
	want := []string{"sid=1; Path=" + pre + "/; HttpOnly", "pref=dark"}
	if strings.Join(cookies, "\n") != strings.Join(want, "\n") {
		t.Errorf("Set-Cookie = %q, want %q", cookies, want)
	}
	for _, h := range []string{"Clear-Site-Data", "Strict-Transport-Security", "Service-Worker-Allowed"} {
		if rec.Header().Get(h) != "" {
			t.Errorf("%s leaked through", h)
		}
	}
}

func TestProxyWebSocket(t *testing.T) {
	got := make(chan seen, 1)
	_, port := upstream(t, got)
	pre := "/p/" + strconv.Itoa(port)
	act := &Activity{}
	p := New(Target{Dial: LoopbackTCP(port, time.Second), Host: "localhost:" + strconv.Itoa(port)}, Options{StripPrefix: pre, Activity: act})
	defer p.Close()
	front := httptest.NewServer(p)
	defer front.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(front.URL, "http")+pre+"/ws", nil)
	if err != nil {
		t.Fatalf("dial through proxy: %v", err)
	}
	defer c.CloseNow()
	if act.Active() != 1 {
		t.Errorf("active = %d during websocket, want 1", act.Active())
	}
	if act.IdleFor(time.Now().Add(time.Hour)) != 0 {
		t.Errorf("idle while websocket open")
	}
	for _, msg := range []string{"one", "two"} {
		if err := c.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			t.Fatal(err)
		}
		_, b, err := c.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != "echo:"+msg {
			t.Errorf("got %q", b)
		}
	}
	c.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(2 * time.Second)
	for act.Active() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if act.Active() != 0 {
		t.Errorf("active = %d after close", act.Active())
	}
}

func TestProxyErrorHandler(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // nothing listens now
	called := false
	p := New(Target{Dial: LoopbackTCP(port, 200*time.Millisecond), Host: "localhost"}, Options{
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			called = true
			w.WriteHeader(599)
		},
	})
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest("GET", "http://x/", nil))
	if !called || rec.Code != 599 {
		t.Errorf("error handler not used: %d", rec.Code)
	}
}

func TestRewriteLocation(t *testing.T) {
	tests := []struct{ loc, prefix, want string }{
		{"/a", "/p/1", "/p/1/a"},
		{"/p/1/a", "/p/1", "/p/1/a"},
		{"/p/1", "/p/1", "/p/1"},
		{"/p/10/a", "/p/1", "/p/1/p/10/a"},
		{"rel/x", "/p/1", "rel/x"},
		{"http://localhost:1/x?y", "/p/1", "/p/1/x?y"},
		{"http://127.0.0.1:1/x", "", "/x"},
		{"http://127.0.0.1:2/x", "/p/1", "http://127.0.0.1:2/x"},
		{"https://github.com/login", "/p/1", "https://github.com/login"},
		{"//evil.example/x", "/p/1", "//evil.example/x"},
	}
	for _, tt := range tests {
		if got := RewriteLocation(tt.loc, "localhost:1", tt.prefix); got != tt.want {
			t.Errorf("RewriteLocation(%q, %q) = %q, want %q", tt.loc, tt.prefix, got, tt.want)
		}
	}
}

func TestRewriteSetCookie(t *testing.T) {
	tests := []struct {
		in, prefix, want string
		ok               bool
	}{
		{"a=1; Path=/; Domain=.example.com; Secure", "/p/1", "a=1; Path=/p/1/; Secure", true},
		{"a=1; path=/x", "/p/1", "a=1; Path=/p/1/x", true},
		{"a=1; Path=/p/1/x", "/p/1", "a=1; Path=/p/1/x", true},
		{"a=1; Domain=example.com", "", "a=1", true},
		{"a=1; Path=/", "", "a=1; Path=/", true},
		{"relay_session=x", "", "", false},
		{"__Secure-relay_session=x; Secure", "", "", false},
		{"relay_preview=x", "", "", false},
		{"=novalue", "", "", false},
		{"garbage", "", "", false},
	}
	for _, tt := range tests {
		got, ok := RewriteSetCookie(tt.in, tt.prefix)
		if got != tt.want || ok != tt.ok {
			t.Errorf("RewriteSetCookie(%q) = %q,%v want %q,%v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestSafeNext(t *testing.T) {
	tests := map[string]string{
		"":                    "/",
		"/":                   "/",
		"/a?b=c#d":            "/a?b=c#d",
		"//evil.com":          "/",
		"/\\evil.com":         "/",
		"https://evil.com/":   "/",
		"javascript:alert(1)": "/",
		"/ok\r\nSet-Cookie:":  "/",
	}
	for in, want := range tests {
		if got := SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsWebSocket(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Upgrade", "WebSocket")
	r.Header.Set("Connection", "keep-alive, Upgrade")
	if !IsWebSocket(r) {
		t.Error("expected websocket")
	}
	r.Header.Set("Connection", "keep-alive")
	if IsWebSocket(r) {
		t.Error("not a websocket")
	}
}
