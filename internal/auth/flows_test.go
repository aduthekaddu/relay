package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/secret"
	"github.com/aduthekaddu/relay/internal/server"
)

func TestSetupLoginCookieLogout(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	anon := e.browser(uaMacChrome)

	var st api.AuthStateResponse
	anon.expect(200, "GET", "/api/v1/auth/state", nil).decode(t, &st)
	if !st.SetupRequired || st.Authenticated || st.SetupCodeRequired {
		t.Fatalf("fresh state = %+v (direct local request needs no code)", st)
	}
	anon.expect(401, "GET", "/api/v1/test/whoami", nil)
	anon.expect(409, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword})

	// Weak password refused with a field.
	r := anon.expect(400, "POST", "/api/v1/auth/setup", api.SetupRequest{Username: "owner", Password: "short"})
	if !strings.Contains(string(r.body), `"field":"password"`) {
		t.Fatalf("weak password error lacks field: %s", r.body)
	}

	c := e.setup()
	c.expect(409, "POST", "/api/v1/auth/setup", api.SetupRequest{Username: "x", Password: testPassword})

	var p server.Principal
	c.expect(200, "GET", "/api/v1/test/whoami", nil).decode(t, &p)
	if p.User != "owner" || p.Method != "cookie" || !strings.HasPrefix(p.SessionID, "s_") {
		t.Fatalf("principal = %+v", p)
	}
	c.expect(200, "GET", "/api/v1/auth/state", nil).decode(t, &st)
	if !st.Authenticated || st.User != "owner" || st.SetupRequired || !st.Methods.Password || st.SessionID != p.SessionID {
		t.Fatalf("state after setup = %+v", st)
	}

	// Plain HTTP on loopback: relay_session, HttpOnly, SameSite=Lax, not Secure.
	l := e.browser(uaIPhone)
	res := l.expect(200, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword, Remember: true})
	var lr api.LoginResponse
	res.decode(t, &lr)
	if !lr.OK || lr.NeedTOTP {
		t.Fatalf("login response %+v", lr)
	}
	sc := findCookie(res.header, sessionCookie)
	if sc == nil || !sc.HttpOnly || sc.SameSite != http.SameSiteLaxMode || sc.Secure || sc.Path != "/" || sc.MaxAge != int((30*24*time.Hour)/time.Second) {
		t.Fatalf("session cookie = %+v", sc)
	}
	if len(sc.Value) < 50 {
		t.Fatalf("session secret too short: %d chars", len(sc.Value))
	}
	// Only the hash is stored.
	var n int
	if err := e.d.Store.DB.QueryRow(`SELECT COUNT(*) FROM auth_sessions WHERE token_hash=?`, secret.HashToken(sc.Value)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("hashed session lookup: n=%d err=%v", n, err)
	}
	if err := e.d.Store.DB.QueryRow(`SELECT COUNT(*) FROM auth_sessions WHERE token_hash=? OR id=?`, sc.Value, sc.Value).Scan(&n); err != nil || n != 0 {
		t.Fatalf("plaintext session id stored: n=%d err=%v", n, err)
	}

	l.expect(204, "POST", "/api/v1/auth/logout", nil)
	l.expect(401, "GET", "/api/v1/test/whoami", nil)
	c.expect(200, "GET", "/api/v1/test/whoami", nil) // other session unaffected

	acts := activity(t, c)
	for _, ev := range []string{"account.create", "login.ok", "logout"} {
		if !hasEvent(acts, ev) {
			t.Errorf("activity lacks %s", ev)
		}
	}
}

func TestWrongPasswordIsGenericAndAudited(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	c := e.setup()
	for _, req := range []api.LoginRequest{
		{Username: "owner", Password: "wrong password!"},
		{Username: "nobody", Password: testPassword},
		{Username: "", Password: ""},
	} {
		r := e.browser(uaIPhone).expect(401, "POST", "/api/v1/auth/login", req)
		if !strings.Contains(string(r.body), msgBadCredentials) {
			t.Fatalf("message not generic: %s", r.body)
		}
	}
	acts := activity(t, c)
	fails := 0
	for _, a := range acts {
		if a.Event == "login.fail" {
			fails++
			if strings.Contains(a.Detail, "wrong password!") || strings.Contains(a.Actor, "nobody") {
				t.Fatalf("audit leaks attempted credentials: %+v", a)
			}
		}
	}
	if fails != 3 {
		t.Fatalf("login.fail entries = %d, want 3", fails)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	e.setup()
	b := e.browser(uaIPhone)
	for i := 0; i < 5; i++ {
		b.expect(401, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: "nope nope nope"})
	}
	r := b.expect(429, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword})
	if r.errCode(t) != "rate_limited" || r.header.Get("Retry-After") == "" {
		t.Fatalf("429 without rate_limited/Retry-After: %s %v", r.body, r.header)
	}
	var eb api.ErrorBody
	r.decode(t, &eb)
	if eb.Error.RetryIn <= 0 || eb.Error.RetryIn > 60 {
		t.Fatalf("retryIn = %d", eb.Error.RetryIn)
	}
}

func TestRevokeSession(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	a := e.setup()
	b := e.login(uaIPhone)

	var list []api.DeviceSession
	a.expect(200, "GET", "/api/v1/auth/sessions", nil).decode(t, &list)
	if len(list) != 2 {
		t.Fatalf("sessions = %d, want 2", len(list))
	}
	var other api.DeviceSession
	for _, s := range list {
		if !s.Current {
			other = s
		}
	}
	if other.Device != "iPhone" || other.Browser != "Safari" || other.OS != "iOS" || other.Method != "password" || other.IP == "" {
		t.Fatalf("device session = %+v", other)
	}
	a.expect(204, "DELETE", "/api/v1/auth/sessions/"+other.ID, nil)
	b.expect(401, "GET", "/api/v1/test/whoami", nil) // immediately after the database mutation
	a.expect(404, "DELETE", "/api/v1/auth/sessions/"+other.ID, nil)

	// revoke-others
	e.login(uaAndroidFox)
	c := e.login(uaAndroidFox)
	var rc api.RevokedCount
	a.expect(200, "POST", "/api/v1/auth/sessions/revoke-others", nil).decode(t, &rc)
	if rc.Revoked != 2 {
		t.Fatalf("revoked %d, want 2", rc.Revoked)
	}
	c.expect(401, "GET", "/api/v1/test/whoami", nil)
	a.expect(200, "GET", "/api/v1/test/whoami", nil)
}

func TestSessionRevokedEventPublished(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	a := e.setup()
	sub := e.d.Bus.Subscribe(8, func(ev api.Event) bool { return ev.Type == core.BusSessionRevoked })
	defer sub.Close()
	a.expect(204, "POST", "/api/v1/auth/logout", nil)
	select {
	case ev := <-sub.C:
		sr, ok := ev.Data.(core.SessionRevoked)
		if !ok || len(sr.SessionIDs) != 1 {
			t.Fatalf("payload %#v", ev.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("no session revoked event")
	}
}

func TestTokenAuth(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	c := e.setup()
	var ct api.CreatedToken
	c.expect(201, "POST", "/api/v1/auth/tokens", api.NameRequest{Name: "ci"}).decode(t, &ct)
	if !strings.HasPrefix(ct.Token, TokenPrefix) || ct.Prefix != ct.Token[:8] || ct.ID == "" {
		t.Fatalf("created token = %+v", ct)
	}

	tc := e.browser("")
	tc.origin = "" // scripts send no Origin
	tc.header["Authorization"] = "Bearer " + ct.Token
	var p server.Principal
	tc.expect(200, "POST", "/api/v1/test/whoami", map[string]string{}).decode(t, &p)
	if p.Method != "token" || p.TokenID != ct.ID || p.User != "owner" {
		t.Fatalf("token principal = %+v", p)
	}
	// Tokens can read but not change sign-in settings.
	tc.expect(200, "GET", "/api/v1/auth/tokens", nil)
	tc.expect(403, "POST", "/api/v1/auth/tokens", api.NameRequest{Name: "more"})
	tc.expect(403, "POST", "/api/v1/auth/totp/setup", nil)

	var list []api.APIToken
	c.expect(200, "GET", "/api/v1/auth/tokens", nil).decode(t, &list)
	if len(list) != 1 || list[0].LastUsedAt.IsZero() {
		t.Fatalf("token list = %+v (lastUsedAt should be set)", list)
	}
	if strings.Contains(string(c.do("GET", "/api/v1/auth/tokens", nil).body), ct.Token) {
		t.Fatal("token list leaks the secret")
	}

	bad := e.browser("")
	bad.header["Authorization"] = "Bearer rly_doesnotexist"
	bad.expect(401, "GET", "/api/v1/test/whoami", nil)
	bad.header["Authorization"] = "Basic Zm9vOmJhcg=="
	bad.expect(401, "GET", "/api/v1/test/whoami", nil)

	c.expect(204, "DELETE", "/api/v1/auth/tokens/"+ct.ID, nil)
	tc.expect(401, "GET", "/api/v1/test/whoami", nil)
	if !hasEvent(activity(t, c), "token.revoke") {
		t.Fatal("token.revoke not audited")
	}
}

func TestCSRFOriginChecks(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	c := e.setup()

	tests := []struct {
		name   string
		origin string
		site   string
		want   int
	}{
		{"same origin", e.origin, "", 200},
		{"missing origin", "", "", 403},
		{"foreign origin", "https://evil.example", "", 403},
		{"cross-site fetch metadata", e.origin, "cross-site", 403},
		{"null origin", "null", "", 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c.origin = tt.origin
			delete(c.header, "Sec-Fetch-Site")
			if tt.site != "" {
				c.header["Sec-Fetch-Site"] = tt.site
			}
			c.expect(tt.want, "POST", "/api/v1/test/whoami", map[string]string{})
		})
	}
	c.origin, c.header = "", map[string]string{}
	c.expect(200, "GET", "/api/v1/test/whoami", nil) // safe methods need no Origin

	// Public sign-in routes refuse foreign browser origins too.
	evil := e.browser(uaMacChrome)
	evil.origin = "https://evil.example"
	evil.expect(403, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword})
	// …but scripts without Origin may sign in.
	script := e.browser("curl/8.5.0")
	script.origin = ""
	script.expect(200, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword})
}

func TestWebSocketUpgradeOriginCheck(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	c := e.setup()
	e.rt.WS("GET /api/v1/test/ws", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusSwitchingProtocols) })
	upgrade := map[string]string{"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13", "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ=="}
	for k, v := range upgrade {
		c.header[k] = v
	}
	c.origin = ""
	c.expect(403, "GET", "/api/v1/test/ws", nil) // GET, but WS: Origin is mandatory
	c.header["Origin"] = "https://evil.example"
	c.expect(403, "GET", "/api/v1/test/ws", nil)
	c.header["Origin"] = e.origin
	c.expect(101, "GET", "/api/v1/test/ws", nil)
}

func TestTOTPFlow(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	c := e.setup()
	clk := &fakeClock{t: time.Now().UTC()}
	e.svc.now = clk.now

	var setup api.TOTPSetup
	c.expect(200, "POST", "/api/v1/auth/totp/setup", nil).decode(t, &setup)
	if setup.Secret == "" || !strings.HasPrefix(setup.OtpauthURL, "otpauth://totp/Relay:owner@") {
		t.Fatalf("setup = %+v", setup)
	}
	key, _ := decodeTOTPSecret(setup.Secret)
	code := func() string { return hotp(key, uint64(totpStep(clk.t)), 6) }

	c.expect(400, "POST", "/api/v1/auth/totp/enable", api.CodeRequest{Code: "000000"})
	c.expect(204, "POST", "/api/v1/auth/totp/enable", api.CodeRequest{Code: code()})
	var ts api.TOTPStatus
	c.expect(200, "GET", "/api/v1/auth/totp", nil).decode(t, &ts)
	if !ts.Enabled {
		t.Fatal("TOTP not enabled")
	}
	c.expect(409, "POST", "/api/v1/auth/totp/setup", nil)

	b := e.browser(uaIPhone)
	var lr api.LoginResponse
	b.expect(200, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword}).decode(t, &lr)
	if lr.OK || !lr.NeedTOTP {
		t.Fatalf("expected needTotp, got %+v", lr)
	}
	b.expect(401, "GET", "/api/v1/test/whoami", nil)
	// The code used to enable is already spent (replay protection).
	b.expect(401, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword, TOTP: code()})
	clk.advance(30 * time.Second)
	cur := code()
	b.expect(200, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword, TOTP: cur})
	b.expect(200, "GET", "/api/v1/test/whoami", nil)
	// Replaying the same code from another browser fails.
	e.browser(uaAndroidFox).expect(401, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword, TOTP: cur})

	var st api.AuthStateResponse
	b.expect(200, "GET", "/api/v1/auth/state", nil).decode(t, &st)
	if !st.Methods.TOTP {
		t.Fatal("state should report TOTP")
	}
	clk.advance(30 * time.Second)
	c.expect(400, "POST", "/api/v1/auth/totp/disable", api.CodeRequest{Code: "123"})
	c.expect(204, "POST", "/api/v1/auth/totp/disable", api.CodeRequest{Code: code()})
	e.login(uaAndroidFox) // password alone works again
}

func TestPasswordChangeRevokesOthers(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	a := e.setup()
	b := e.login(uaIPhone)
	a.expect(400, "POST", "/api/v1/auth/password", api.ChangePasswordRequest{Current: "wrong wrong", Next: "another good passphrase"})
	r := a.expect(400, "POST", "/api/v1/auth/password", api.ChangePasswordRequest{Current: testPassword, Next: "short"})
	if !strings.Contains(string(r.body), `"field":"next"`) {
		t.Fatalf("weak new password: %s", r.body)
	}
	a.expect(204, "POST", "/api/v1/auth/password", api.ChangePasswordRequest{Current: testPassword, Next: "another good passphrase"})
	a.expect(200, "GET", "/api/v1/test/whoami", nil)
	b.expect(401, "GET", "/api/v1/test/whoami", nil)
	e.browser("").expect(401, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword})
	e.browser("").expect(200, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: "another good passphrase"})
}

func TestRemoteSetupNeedsCode(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	b := e.browser(uaIPhone)
	b.header["X-Forwarded-For"] = "203.0.113.7" // arrives through a tunnel
	var st api.AuthStateResponse
	b.expect(200, "GET", "/api/v1/auth/state", nil).decode(t, &st)
	if !st.SetupRequired || !st.SetupCodeRequired {
		t.Fatalf("state = %+v", st)
	}
	r := b.expect(403, "POST", "/api/v1/auth/setup", api.SetupRequest{Username: "owner", Password: testPassword, Code: "WRONG-CODE"})
	if !strings.Contains(string(r.body), `"field":"code"`) {
		t.Fatalf("missing field: %s", r.body)
	}
	code := e.svc.setupCode
	if len(code) != 9 {
		t.Fatalf("setup code %q", code)
	}
	b.expect(200, "POST", "/api/v1/auth/setup", api.SetupRequest{Username: "owner", Password: testPassword, Code: strings.ToLower(strings.ReplaceAll(code, "-", ""))})
	if e.svc.setupCode != "" {
		t.Fatal("setup code should be cleared")
	}
}

func TestPlainHTTPOffLoopbackRefused(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	e.setup()
	b := e.browser(uaIPhone)
	b.origin = ""
	req, _ := http.NewRequest("POST", e.base+"/api/v1/auth/login", strings.NewReader(`{"username":"owner","password":"`+testPassword+`"}`))
	req.Host = "relay.lan:7777"
	res, err := b.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("plain HTTP sign-in on a LAN host: %d, want 403", res.StatusCode)
	}
	e.d.Cfg.Auth.InsecureCookies = true
	req, _ = http.NewRequest("POST", e.base+"/api/v1/auth/login", strings.NewReader(`{"username":"owner","password":"`+testPassword+`"}`))
	req.Host = "relay.lan:7777"
	res, err = b.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("insecure_cookies sign-in: %d", res.StatusCode)
	}
}

func TestSecureCookieBehindTrustedProxy(t *testing.T) {
	e := newEnv(t, "127.0.0.1", func(cfg *config.Config) { cfg.Server.TrustedProxies = []string{"127.0.0.1"} })
	e.setup()
	// Re-wrap the router with the configured trusted proxies.
	e.trusted = server.TrustedProxies(e.d.Cfg)
	b := e.browser(uaIPhone)
	b.header["X-Forwarded-Proto"] = "https"
	b.header["X-Forwarded-For"] = "198.51.100.4"
	r := b.expect(200, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword})
	sc := findCookie(r.header, hostPrefix+sessionCookie)
	if sc == nil || !sc.Secure || !sc.HttpOnly || sc.Path != "/" || sc.Domain != "" {
		t.Fatalf("secure cookie = %+v (all: %v)", sc, r.header.Values("Set-Cookie"))
	}
	var list []api.DeviceSession
	req, _ := http.NewRequest("GET", e.base+"/api/v1/auth/sessions", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.AddCookie(&http.Cookie{Name: sc.Name, Value: sc.Value})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("__Host- cookie not accepted: %d", res.StatusCode)
	}
	_ = list
	// A plain relay_session cookie is ignored on HTTPS requests.
	req2, _ := http.NewRequest("GET", e.base+"/api/v1/test/whoami", nil)
	req2.Header.Set("X-Forwarded-Proto", "https")
	req2.AddCookie(&http.Cookie{Name: sessionCookie, Value: sc.Value})
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != 401 {
		t.Fatalf("relay_session accepted over HTTPS: %d", res2.StatusCode)
	}
	// The session records the forwarded client IP.
	var ip string
	_ = e.d.Store.DB.QueryRow(`SELECT ip FROM auth_sessions ORDER BY created_at DESC LIMIT 1`).Scan(&ip)
	if ip != "198.51.100.4" {
		t.Fatalf("session ip = %q", ip)
	}
}

func TestBootstrapImportsConfigHash(t *testing.T) {
	hash, err := secret.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, "127.0.0.1", func(cfg *config.Config) {
		cfg.Auth.User = "owner"
		cfg.Auth.PasswordHash = hash
	})
	var st api.AuthStateResponse
	e.browser("").expect(200, "GET", "/api/v1/auth/state", nil).decode(t, &st)
	if st.SetupRequired {
		t.Fatal("hash from relay.toml not imported")
	}
	e.login(uaIPhone)
}

func TestNewDeviceNotification(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	n := &fakeNotifier{}
	e.d.Notifier = n // wired after auth, read at call time
	a := e.setup()
	if n.count() != 0 {
		t.Fatal("first device must not notify")
	}
	// Same browser signing in again: known device.
	a.expect(200, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword})
	if n.count() != 0 {
		t.Fatal("known device notified")
	}
	b := e.login(uaIPhone)
	if n.count() != 1 {
		t.Fatalf("notifications = %d, want 1", n.count())
	}
	if got := n.reqs[0]; got.Kind != "security" || !strings.Contains(got.Body, "Safari on iPhone") {
		t.Fatalf("notification = %+v", got)
	}
	b.expect(200, "POST", "/api/v1/auth/login", api.LoginRequest{Username: "owner", Password: testPassword})
	if n.count() != 1 {
		t.Fatal("second sign-in from the same browser notified again")
	}
}

func TestBusAuditRecorded(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	c := e.setup()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = e.svc.Start(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	waitFor(t, func() bool { return e.d.Bus.Subscribers() > 0 })
	e.d.Bus.Publish(core.BusAudit, core.AuditEvent{Event: "file.delete", Actor: "owner", Detail: "~/tmp/a.txt"})
	e.d.Bus.Publish(core.BusAudit, &core.AuditEvent{Event: "process.kill", Detail: "pid 42"})
	e.d.Bus.Publish(core.BusAudit, "garbage")
	waitFor(t, func() bool {
		acts := activity(t, c)
		return hasEvent(acts, "file.delete") && hasEvent(acts, "process.kill")
	})
	for _, a := range activity(t, c) {
		if a.Event == "process.kill" && a.Actor != "system" {
			t.Fatalf("default actor = %q", a.Actor)
		}
	}
}

func TestActivityLimit(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	c := e.setup()
	for i := 0; i < 5; i++ {
		_ = e.svc.acc.Record(context.Background(), api.AuditEntry{Event: "x.test", Actor: "t"})
	}
	var list []api.AuditEntry
	c.expect(200, "GET", "/api/v1/auth/activity?limit=3", nil).decode(t, &list)
	if len(list) != 3 || list[0].ID < list[1].ID {
		t.Fatalf("activity = %+v (want 3, newest first)", list)
	}
}

func TestSlidingExpiry(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	clk := &fakeClock{t: time.Now().UTC()}
	e.svc.now = clk.now
	e.svc.acc.now = clk.now
	c := e.setup()
	var exp1 int64
	_ = e.d.Store.DB.QueryRow(`SELECT expires_at FROM auth_sessions`).Scan(&exp1)
	clk.advance(10 * time.Minute)
	c.expect(200, "GET", "/api/v1/test/whoami", nil)
	var exp2 int64
	_ = e.d.Store.DB.QueryRow(`SELECT expires_at FROM auth_sessions`).Scan(&exp2)
	if exp2-exp1 < int64(9*time.Minute/time.Millisecond) {
		t.Fatalf("expiry did not slide: %d → %d", exp1, exp2)
	}
	// Past expiry the session is gone.
	clk.advance(31 * 24 * time.Hour)
	c.expect(401, "GET", "/api/v1/test/whoami", nil)
}

// --- helpers ---

func findCookie(h http.Header, name string) *http.Cookie {
	for _, c := range (&http.Response{Header: h}).Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func activity(t *testing.T, c *client) []api.AuditEntry {
	t.Helper()
	var list []api.AuditEntry
	c.expect(200, "GET", "/api/v1/auth/activity?limit=500", nil).decode(t, &list)
	return list
}

func hasEvent(list []api.AuditEntry, ev string) bool {
	for _, a := range list {
		if a.Event == ev {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
