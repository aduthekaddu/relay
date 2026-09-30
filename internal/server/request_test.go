package server

import (
	"bytes"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aduthekaddu/relay/internal/config"
)

func TestTrustedProxies(t *testing.T) {
	cases := []struct {
		name    string
		proxies []string
		public  string
		ip      string
		want    bool
	}{
		{"none configured, no public url", nil, "", "127.0.0.1", false},
		{"public url trusts loopback", nil, "https://relay.example", "127.0.0.1", true},
		{"public url trusts ::1", nil, "https://relay.example", "::1", true},
		{"public url does not trust lan", nil, "https://relay.example", "192.168.1.9", false},
		{"cidr match", []string{"10.0.0.0/8"}, "", "10.2.3.4", true},
		{"cidr miss", []string{"10.0.0.0/8"}, "", "11.2.3.4", false},
		{"bare ip", []string{" 172.16.0.5 "}, "", "172.16.0.5", true},
		{"explicit list disables loopback default", []string{"10.0.0.0/8"}, "https://relay.example", "127.0.0.1", false},
		{"invalid entries ignored", []string{"bogus", "300.1.1.1/8"}, "", "10.0.0.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Server.TrustedProxies = tc.proxies
			cfg.Server.PublicURL = tc.public
			if got := TrustedProxies(cfg)(net.ParseIP(tc.ip)); got != tc.want {
				t.Fatalf("trusted(%s) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
	if TrustedProxies(config.Defaults())(nil) {
		t.Fatal("nil IP must not be trusted")
	}
}

func TestIsSecureRequestAndClientIP(t *testing.T) {
	trustLoopback := func(ip net.IP) bool { return ip != nil && ip.IsLoopback() }
	cases := []struct {
		name     string
		remote   string
		tls      bool
		proto    string
		xff      string
		wantSec  bool
		wantAddr string
	}{
		{"direct tls", "203.0.113.7:5000", true, "", "", true, "203.0.113.7"},
		{"plain direct", "203.0.113.7:5000", false, "https", "198.51.100.1", false, "203.0.113.7"},
		{"trusted proxy https", "127.0.0.1:4000", false, "https", "198.51.100.1", true, "198.51.100.1"},
		{"trusted proxy http", "127.0.0.1:4000", false, "http", "", false, "127.0.0.1"},
		{"trusted proxy chain first hop", "127.0.0.1:4000", false, "https, http", "", true, "127.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sec bool
			var ip string
			h := WithRequestInfo(trustLoopback, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sec, ip = IsSecureRequest(r), ClientIP(r)
			}))
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if tc.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			h.ServeHTTP(httptest.NewRecorder(), r)
			if sec != tc.wantSec || ip != tc.wantAddr {
				t.Fatalf("secure=%v ip=%q, want %v %q", sec, ip, tc.wantSec, tc.wantAddr)
			}
		})
	}
	// Without the middleware only r.TLS counts.
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	if IsSecureRequest(r) {
		t.Fatal("forwarded proto trusted without middleware")
	}
	// Control-socket requests report "local".
	var got string
	h := WithRequestInfo(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = ClientIP(r) }))
	lr := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(httptest.NewRecorder(), lr.WithContext(MarkLocal(lr.Context())))
	if got != "local" {
		t.Fatalf("local ClientIP = %q", got)
	}
}

func TestLimitBodies(t *testing.T) {
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "too big", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := LimitBodies(read)
	cases := []struct {
		name, path, ctype string
		size              int
		want              int
	}{
		{"auth small", "/api/v1/auth/login", "application/json", 1 << 10, 204},
		{"auth over cap", "/api/v1/auth/login", "text/plain", MaxAuthBody + 1, 413},
		{"json under cap", "/api/v1/files/write", "application/json; charset=utf-8", 1 << 20, 204},
		{"json over cap", "/api/v1/files/write", "application/json", MaxJSONRequest + 1, 413},
		{"raw upload uncapped here", "/api/v1/files/upload", "application/octet-stream", MaxJSONRequest + 1, 204},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", tc.path, bytes.NewReader(make([]byte, tc.size)))
			r.Header.Set("Content-Type", tc.ctype)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestRequestLogRedaction(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := RequestLog(log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("body-secret"))
	}))
	for _, u := range []string{
		"/api/v1/auth/passkey/login/finish?challenge=abc123",
		"/api/v1/files/list?token=rly_zzz",
		"/api/v1/files/list?path=%2Ftmp",
	} {
		r := httptest.NewRequest("POST", u, strings.NewReader("password=hunter2hunter2"))
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	out := buf.String()
	for _, leak := range []string{"abc123", "rly_zzz", "hunter2", "body-secret"} {
		if strings.Contains(out, leak) {
			t.Fatalf("log leaked %q:\n%s", leak, out)
		}
	}
	if !strings.Contains(out, "status=418") || !strings.Contains(out, "/api/v1/files/list?path=%2Ftmp") {
		t.Fatalf("expected status and harmless query in log:\n%s", out)
	}

	// Disabled debug: no wrapper at all.
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if got := RequestLog(quiet, inner); got == nil {
		t.Fatal("nil handler")
	}
}

type fixedAuth struct{ p *Principal }

func (a fixedAuth) Identify(*http.Request) *Principal { return a.p }

func TestRouterAuthenticateAndCSRF(t *testing.T) {
	const origin = "https://relay.example"
	origins := func() []string { return []string{origin + "/"} }
	ok := func(w http.ResponseWriter, r *http.Request) {
		if PrincipalFrom(r.Context()) == nil {
			t.Error("principal missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	}
	cookie := &Principal{User: "owner", Method: "cookie", SessionID: "s"}
	token := &Principal{User: "owner", Method: "token", TokenID: "t"}
	cases := []struct {
		name   string
		p      *Principal
		method string
		hdr    map[string]string
		want   int
	}{
		{"anonymous", nil, "GET", nil, 401},
		{"incomplete principal is anonymous", &Principal{User: "owner"}, "GET", nil, 401},
		{"cookie GET needs no origin", cookie, "GET", nil, 204},
		{"cookie POST without origin", cookie, "POST", nil, 403},
		{"cookie POST wrong origin", cookie, "POST", map[string]string{"Origin": "https://evil.example"}, 403},
		{"cookie POST right origin", cookie, "POST", map[string]string{"Origin": origin}, 204},
		{"cookie POST origin case-insensitive", cookie, "POST", map[string]string{"Origin": "HTTPS://Relay.Example"}, 204},
		{"cookie POST cross-site fetch metadata", cookie, "POST", map[string]string{"Origin": origin, "Sec-Fetch-Site": "cross-site"}, 403},
		{"token POST without origin", token, "DELETE", nil, 204},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := NewRouter(fixedAuth{tc.p}, origins)
			rt.Handle("GET /x", ok)
			rt.Handle("POST /x", ok)
			rt.Handle("DELETE /x", ok)
			r := httptest.NewRequest(tc.method, "/x", nil)
			for k, v := range tc.hdr {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			rt.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d", w.Code, tc.want)
			}
		})
	}

	// WS upgrades with a cookie always need the origin, even on GET.
	rt := NewRouter(fixedAuth{cookie}, origins)
	rt.WS("GET /ws", ok)
	r := httptest.NewRequest("GET", "/ws", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("ws without origin: %d", w.Code)
	}

	// No authenticator configured: nil, never a panic.
	if p := NewRouter(nil, origins).Authenticate(httptest.NewRequest("GET", "/", nil)); p != nil {
		t.Fatalf("got %+v", p)
	}
	// Control socket: local principal.
	lr := httptest.NewRequest("GET", "/", nil)
	if p := NewRouter(nil, origins).Authenticate(lr.WithContext(MarkLocal(lr.Context()))); p == nil || p.Method != "local" {
		t.Fatalf("local principal = %+v", p)
	}
}
