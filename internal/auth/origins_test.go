package auth

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/server"
)

func TestAuthLocalAliasRequests(t *testing.T) {
	for _, canonical := range []string{"localhost", "127.0.0.1", "::1"} {
		t.Run(canonical, func(t *testing.T) {
			host := canonical
			if host == "::1" {
				host = "127.0.0.1"
			}
			e := newEnv(t, host, func(cfg *config.Config) {
				if canonical == "::1" {
					_, port, _ := net.SplitHostPort(cfg.Server.Listen)
					cfg.Server.PublicURL = "http://[::1]:" + port
				}
			})
			e.setup()
			_, port, _ := net.SplitHostPort(e.d.Cfg.Server.Listen)
			// A second listener exercises an actual IPv6 connection to the same
			// router and port; both listeners are test-owned loopback sockets.
			ln, err := net.Listen("tcp", "[::1]:"+port)
			if err != nil {
				t.Fatalf("IPv6 fixture listener: %v", err)
			}
			v6 := &httptest.Server{Listener: ln, Config: &http.Server{Handler: server.WithRequestInfo(e.trusted, e.rt)}}
			v6.Start()
			t.Cleanup(v6.Close)
			for _, alias := range []string{"localhost", "127.0.0.1", "::1"} {
				t.Run(alias, func(t *testing.T) {
					e.base = "http://" + net.JoinHostPort(alias, port)
					c := e.browser("synthetic-browser")
					c.origin = e.base
					var lr api.LoginResponse
					c.expect(200, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword}).decode(t, &lr)
					wantPasskey := canonical == "localhost" && alias == "localhost"
					if !lr.OK || lr.SetupPasskey != wantPasskey {
						t.Fatalf("login OK=%v setupPasskey=%v; want passkey %v", lr.OK, lr.SetupPasskey, wantPasskey)
					}
					c.expect(200, "POST", "/api/v1/test/whoami", nil)
					var state api.AuthStateResponse
					c.expect(200, "GET", "/api/v1/auth/state", nil).decode(t, &state)
					if !state.Authenticated || state.PasskeysAvailable != wantPasskey {
						t.Fatalf("authenticated=%v passkeysAvailable=%v; want %v", state.Authenticated, state.PasskeysAvailable, wantPasskey)
					}
					wantBegin := 503
					if wantPasskey {
						wantBegin = 200
					}
					c.expect(wantBegin, "POST", "/api/v1/auth/passkeys/begin", api.NameRequest{Name: "synthetic"})
					c.expect(wantBegin, "POST", "/api/v1/auth/passkey/begin", nil)
					for _, origin := range []string{"http://" + net.JoinHostPort(alias, "47739"), "https://unrelated.example.test", "http://localhost.evil.test:" + port, "http://localhost:" + port + "/evil", "null", "*"} {
						c.origin = origin
						c.expect(403, "POST", "/api/v1/test/whoami", nil)
						c.expect(403, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword})
					}
					c.origin = ""
					c.expect(403, "POST", "/api/v1/test/whoami", nil)
					c.expect(200, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword})
					c.header["Sec-Fetch-Site"] = "same-origin"
					c.expect(403, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword})
				})
			}
		})
	}
}

func TestPublicOriginEmptyAndDuplicate(t *testing.T) {
	e := newEnv(t, "localhost")
	e.setup()
	for _, origins := range [][]string{{""}, {e.origin, e.origin}, {e.origin + "/"}, {"http://owner@" + strings.TrimPrefix(e.origin, "http://")}} {
		body, _ := json.Marshal(api.LoginRequest{Username: "owner", Password: testPassword})
		r := httptest.NewRequest("POST", e.origin+"/api/v1/auth/login", bytes.NewReader(body))
		r.Header["Origin"] = origins
		w := httptest.NewRecorder()
		e.rt.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("Origin %q: %d; want 403", origins, w.Code)
		}
	}
}

func TestPasskeyOriginAndProxyMatrix(t *testing.T) {
	cases := []struct {
		name, canonical, domain, host, remote, forwardedHost, proto, rp string
		tls, want                                                       bool
		proxies                                                         []string
	}{
		{name: "HTTP localhost", canonical: "http://localhost:47733", host: "localhost:47733", rp: "localhost", want: true},
		{name: "localhost default HTTP port", canonical: "http://localhost:80", host: "localhost", rp: "localhost", want: true},
		{name: "IPv4 alias", canonical: "http://localhost:47733", host: "127.0.0.1:47733", rp: "localhost"},
		{name: "IPv6 alias", canonical: "http://localhost:47733", host: "[::1]:47733", rp: "localhost"},
		{name: "IPv4 canonical with localhost alias", canonical: "http://127.0.0.1:47733", host: "localhost:47733"},
		{name: "IPv6 canonical with localhost alias", canonical: "http://[::1]:47733", host: "localhost:47733"},
		{name: "HTTPS IP canonical", canonical: "https://192.0.2.1", host: "192.0.2.1", tls: true},
		{name: "HTTPS domain", canonical: "https://relay.example.test:443", host: "relay.example.test", tls: true, rp: "relay.example.test", want: true},
		{name: "HTTPS explicit port", canonical: "https://relay.example.test:47733", host: "relay.example.test:47733", tls: true, rp: "relay.example.test", want: true},
		{name: "configured parent RP", canonical: "https://relay.example.test", domain: "example.test", host: "relay.example.test", tls: true, rp: "example.test", want: true},
		{name: "unrelated configured domain", canonical: "https://relay.example.test", domain: "elsewhere.test", host: "relay.example.test", tls: true, rp: "relay.example.test", want: true},
		{name: "HTTP remote domain", canonical: "http://relay.example.test", host: "relay.example.test"},
		{name: "wrong port", canonical: "http://localhost:47733", host: "localhost:47734", rp: "localhost"},
		{name: "unrelated host", canonical: "https://relay.example.test", host: "evil.example.test", tls: true, rp: "relay.example.test"},
		{name: "untrusted proxy spoof", canonical: "https://relay.example.test", host: "127.0.0.1:47733", remote: "192.0.2.1:47734", forwardedHost: "relay.example.test", proto: "https", rp: "relay.example.test"},
		{name: "public URL trusts loopback proxy", canonical: "https://relay.example.test", host: "127.0.0.1:47733", remote: "127.0.0.1:47734", forwardedHost: "relay.example.test", proto: "https", rp: "relay.example.test", want: true},
		{name: "explicit proxy", canonical: "https://relay.example.test", host: "127.0.0.1:47733", remote: "192.0.2.1:47734", forwardedHost: "relay.example.test", proto: "https", proxies: []string{"192.0.2.1"}, rp: "relay.example.test", want: true},
		{name: "explicit list excludes loopback", canonical: "https://relay.example.test", host: "127.0.0.1:47733", remote: "127.0.0.1:47734", forwardedHost: "relay.example.test", proto: "https", proxies: []string{"192.0.2.1"}, rp: "relay.example.test"},
		{name: "trusted hostile host", canonical: "https://relay.example.test", host: "127.0.0.1:47733", remote: "127.0.0.1:47734", forwardedHost: "relay.example.test/evil", proto: "https", rp: "relay.example.test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Server.PublicURL, cfg.Server.Domain, cfg.Server.TrustedProxies = tc.canonical, tc.domain, tc.proxies
			s := &Service{d: newDepsWith(cfg)}
			s.origins = func() []string { return append([]string{tc.canonical}, server.LocalOriginAliases(tc.canonical)...) }
			r := httptest.NewRequest("GET", "/", nil)
			r.Host, r.RemoteAddr = tc.host, tc.remote
			r.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			r.Header.Set("X-Forwarded-Proto", tc.proto)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			h := server.WithRequestInfo(server.TrustedProxies(cfg), http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				if got := s.passkeysAvailableAt(r); got != tc.want {
					t.Fatalf("available=%v; want %v", got, tc.want)
				}
				wa, err := s.webAuthnAt(r)
				if tc.want {
					if err != nil || wa.Config.RPID != tc.rp {
						t.Fatalf("WebAuthn config = %v, %v; want RP %q", wa, err, tc.rp)
					}
				} else if err == nil {
					t.Fatal("unavailable origin received WebAuthn config")
				}
			}))
			h.ServeHTTP(httptest.NewRecorder(), r)
			if rp, _ := s.rpID(); rp != tc.rp {
				t.Fatalf("RP=%q; want %q", rp, tc.rp)
			}
		})
	}
}

func TestWebAuthnAllowedOriginsFilter(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.PublicURL = "https://relay.example.test:47733"
	cfg.Server.Domain = "example.test"
	s := &Service{d: newDepsWith(cfg)}
	s.origins = func() []string {
		return []string{cfg.Server.PublicURL, "http://relay.example.test:47733", "https://evil.test", "https://owner@relay.example.test", "https://relay.example.test/path", "https://192.0.2.1"}
	}
	wa, err := s.webAuthn()
	if err != nil || len(wa.Config.RPOrigins) != 1 || wa.Config.RPOrigins[0] != cfg.Server.PublicURL {
		t.Fatalf("origins not filtered: %v, %v", wa, err)
	}
}

func TestCookieCanonicalOriginValidation(t *testing.T) {
	for _, tc := range []struct {
		origin string
		secure bool
	}{
		{"HTTPS://Relay.Example.Test:443", true},
		{"https://relay.example.test", true},
		{"https://owner@relay.example.test", false},
		{"https://relay.example.test/path", false},
		{"http://localhost:47733", false},
	} {
		t.Run(tc.origin, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Server.PublicURL = tc.origin
			s := &Service{d: newDepsWith(cfg)}
			r := httptest.NewRequest("GET", "http://localhost:47733", nil)
			if got := s.secureContext(r); got != tc.secure {
				t.Fatalf("secure cookie=%v; want %v", got, tc.secure)
			}
		})
	}
}

func TestPasskeyVirtualAuthenticatorAliasPolicy(t *testing.T) {
	e := newEnv(t, "localhost")
	owner := e.setup()
	va := newVirtualAuthenticator(t, e.origin)
	create := owner.expect(200, "POST", "/api/v1/auth/passkeys/begin", api.NameRequest{Name: "synthetic"})
	owner.expect(201, "POST", "/api/v1/auth/passkeys/finish", va.create(create.body))
	login := e.browser("synthetic")
	get := login.expect(200, "POST", "/api/v1/auth/passkey/begin", nil)
	login.expect(200, "POST", "/api/v1/auth/passkey/finish", va.get(get.body))
	// The router permits an IP alias for password operations, but the
	// WebAuthn clientData origin cannot use the localhost RP ID.
	for _, host := range []string{"127.0.0.1", "::1"} {
		get = login.expect(200, "POST", "/api/v1/auth/passkey/begin", nil)
		u, _ := url.Parse(e.origin)
		va.origin = fmt.Sprintf("http://%s", net.JoinHostPort(host, u.Port()))
		login.expect(401, "POST", "/api/v1/auth/passkey/finish", va.get(get.body))
	}
}
