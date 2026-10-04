package server

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/aduthekaddu/relay/internal/config"
)

func TestNormalizeOrigin(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"http://localhost:47733", "http://localhost:47733"},
		{"HTTP://LOCALHOST:80", "http://localhost"},
		{"https://relay.example.test:443", "https://relay.example.test"},
		{"https://relay.example.test:47733", "https://relay.example.test:47733"},
		{"http://127.0.0.1:80", "http://127.0.0.1"},
		{"http://[::1]:47733", "http://[::1]:47733"},
		{"http://[0:0:0:0:0:0:0:1]:80", "http://[::1]"},
		{"https://[2001:db8::1]:443", "https://[2001:db8::1]"},
		{"https://192.0.2.1", "https://192.0.2.1"},
		{"http://192.0.2.1", ""},
		{"http://[2001:db8::1]", ""},
		{"http://0.0.0.0", ""},
		{"http://[::]", ""},
		{"http://[::ffff:127.0.0.1]", ""},
		{"http://[::1%25lo]", ""},
		{"http://::1", ""},
		{"http://[127.0.0.1]", ""},
		{"http://[localhost]:47733", ""},
		{"http://localhost:", ""},
		{"http://localhost:0", ""},
		{"http://localhost:65536", ""},
		{"http://localhost:047733", ""},
		{"http://localhost:bad", ""},
		{"http://localhost:47733/", ""},
		{"http://localhost:47733/path", ""},
		{"http://localhost:47733?", ""},
		{"http://localhost:47733#", ""},
		{"http://owner@localhost:47733", ""},
		{"http://localhost:47733\\evil", ""},
		{" http://localhost:47733", ""},
		{"http://localhost:47733\n", ""},
		{"http://*.localhost:47733", ""},
		{"http://localhost.:47733", ""},
		{"http://local_host:47733", ""},
		{"http://127.1:47733", ""},
		{"http://127.000.0.1:47733", ""},
		{"http://2130706433:47733", ""},
		{"http://0x7f000001:47733", ""},
		{"http://0x7f.0x0.0x0.0x1:47733", ""},
		{"http://%6cocalhost:47733", ""},
		{"file://localhost", ""},
		{"null", ""},
		{"*", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, ok := NormalizeOrigin(tc.raw)
			if got != tc.want || ok != (tc.want != "") {
				t.Fatalf("NormalizeOrigin(%q) = %q, %v; want %q", tc.raw, got, ok, tc.want)
			}
		})
	}
}

func TestLocalOriginAliases(t *testing.T) {
	cases := []struct {
		canonical string
		want      []string
	}{
		{"http://127.0.0.1:47733", []string{"http://localhost:47733", "http://[::1]:47733"}},
		{"http://localhost:80", []string{"http://127.0.0.1", "http://[::1]"}},
		{"http://[::1]", []string{"http://localhost", "http://127.0.0.1"}},
		{"https://localhost:47733", nil},
		{"https://relay.example.test", nil},
		{"http://other.localhost:47733", nil},
		{"http://127.0.0.2:47733", nil},
		{"http://192.0.2.1:47733", nil},
	}
	for _, tc := range cases {
		t.Run(tc.canonical, func(t *testing.T) {
			if got := LocalOriginAliases(tc.canonical); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("aliases = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestOriginHeaderBoundary(t *testing.T) {
	allowed := func() []string {
		return []string{"http://localhost:47733", "http://127.0.0.1:47733", "http://[::1]:47733", "https://relay.example.test:443/"}
	}
	cases := []struct {
		name    string
		origins []string
		sites   []string
		want    int
	}{
		{"localhost", []string{"http://localhost:47733"}, nil, 204},
		{"IPv4", []string{"http://127.0.0.1:47733"}, nil, 204},
		{"IPv6", []string{"http://[::1]:47733"}, nil, 204},
		{"default port", []string{"https://relay.example.test"}, nil, 204},
		{"explicit default port", []string{"https://relay.example.test:443"}, nil, 204},
		{"unexpected port", []string{"http://localhost:47734"}, nil, 403},
		{"unrelated", []string{"https://evil.example.test"}, nil, 403},
		{"missing", nil, nil, 403},
		{"empty", []string{""}, nil, 403},
		{"duplicate", []string{"http://localhost:47733", "http://localhost:47733"}, nil, 403},
		{"list", []string{"http://localhost:47733 http://127.0.0.1:47733"}, nil, 403},
		{"path", []string{"http://localhost:47733/evil"}, nil, 403},
		{"userinfo", []string{"http://evil@localhost:47733"}, nil, 403},
		{"suffix", []string{"http://localhost.evil.test:47733"}, nil, 403},
		{"cross-site", []string{"http://localhost:47733"}, []string{"cross-site"}, 403},
		{"duplicate metadata", []string{"http://localhost:47733"}, []string{"same-origin", "cross-site"}, 403},
		{"malformed metadata", []string{"http://localhost:47733"}, []string{"cross-site, same-origin"}, 403},
		{"same-site", []string{"http://localhost:47733"}, []string{"same-site"}, 204},
	}
	for _, tc := range cases {
		for _, ws := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/unsafe", true: "/ws"}[ws], func(t *testing.T) {
				rt := NewRouter(fixedAuth{&Principal{User: "fixture", Method: "cookie"}}, allowed)
				h := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }
				method := "POST"
				if ws {
					method = "GET"
					rt.WS("GET /x", h)
				} else {
					rt.Handle("POST /x", h)
				}
				r := httptest.NewRequest(method, "/x", nil)
				r.Header["Origin"], r.Header["Sec-Fetch-Site"] = tc.origins, tc.sites
				w := httptest.NewRecorder()
				rt.ServeHTTP(w, r)
				if w.Code != tc.want {
					t.Fatalf("status = %d; want %d", w.Code, tc.want)
				}
			})
		}
	}
}

func TestExternalOriginTrustedProxyBoundary(t *testing.T) {
	cases := []struct {
		name, host, remote, forwardedHost, proto, public, want string
		proxies                                                []string
		tls                                                    bool
	}{
		{name: "direct localhost", host: "localhost:47733", remote: "127.0.0.1:47734", want: "http://localhost:47733"},
		{name: "direct IPv6", host: "[::1]:47733", remote: "[::1]:47734", want: "http://[::1]:47733"},
		{name: "direct TLS", host: "relay.example.test:443", remote: "192.0.2.1:47734", tls: true, want: "https://relay.example.test"},
		{name: "untrusted spoof", host: "127.0.0.1:47733", remote: "192.0.2.1:47734", forwardedHost: "relay.example.test", proto: "https", want: "http://127.0.0.1:47733"},
		{name: "loopback without public URL", host: "127.0.0.1:47733", remote: "127.0.0.1:47734", forwardedHost: "relay.example.test", proto: "https", want: "http://127.0.0.1:47733"},
		{name: "trusted explicit", host: "127.0.0.1:47733", remote: "192.0.2.1:47734", forwardedHost: "relay.example.test:443", proto: "https", proxies: []string{"192.0.2.1"}, want: "https://relay.example.test"},
		{name: "public URL trusts IPv6 loopback", host: "[::1]:47733", remote: "[::1]:47734", forwardedHost: "relay.example.test", proto: "https", public: "https://relay.example.test", want: "https://relay.example.test"},
		{name: "explicit list disables loopback default", host: "localhost:47733", remote: "127.0.0.1:47734", forwardedHost: "relay.example.test", proto: "https", public: "https://relay.example.test", proxies: []string{"192.0.2.1"}, want: "http://localhost:47733"},
		{name: "first hop", host: "127.0.0.1:47733", remote: "127.0.0.1:47734", forwardedHost: "relay.example.test:47735, proxy.example.test", proto: "https, http", public: "https://relay.example.test:47735", want: "https://relay.example.test:47735"},
		{name: "host preserved by proxy", host: "relay.example.test", remote: "127.0.0.1:47734", proto: "https", public: "https://relay.example.test", want: "https://relay.example.test"},
		{name: "hostile trusted host", host: "127.0.0.1:47733", remote: "127.0.0.1:47734", forwardedHost: "relay.example.test/evil", proto: "https", public: "https://relay.example.test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Server.PublicURL, cfg.Server.TrustedProxies = tc.public, tc.proxies
			r := httptest.NewRequest("GET", "/", nil)
			r.Host, r.RemoteAddr = tc.host, tc.remote
			r.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			r.Header.Set("X-Forwarded-Proto", tc.proto)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			h := WithRequestInfo(TrustedProxies(cfg), http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got, ok := ExternalOrigin(r)
				if got != tc.want || ok != (tc.want != "") {
					t.Fatalf("origin = %q,%v; want %q", got, ok, tc.want)
				}
			}))
			h.ServeHTTP(httptest.NewRecorder(), r)
			if !tc.tls && tc.forwardedHost != "" {
				got, _ := ExternalOrigin(r)
				if want, _ := NormalizeOrigin("http://" + tc.host); got != want {
					t.Fatalf("forwarded host trusted without middleware: %q", got)
				}
			}
		})
	}
}
