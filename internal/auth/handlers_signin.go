package auth

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/secret"
	"github.com/aduthekaddu/relay/internal/server"
)

// Generic failure messages (no user enumeration).
const (
	msgBadCredentials = "Wrong username or password."
	msgBadCode        = "That code didn't work. Codes change every 30 seconds, so try the newest one."
)

// Routes registers every auth endpoint (docs/dev/API.md "auth").
func (s *Service) Routes(rt *server.Router) {
	s.rt = rt
	const p = "/api/v1/auth"
	rt.Public("GET "+p+"/state", s.handleState)
	rt.Public("POST "+p+"/setup", s.handleSetup)
	rt.Public("POST "+p+"/login", s.handleLogin)
	rt.Public("POST "+p+"/passkey/begin", s.handlePasskeyLoginBegin)
	rt.Public("POST "+p+"/passkey/finish", s.handlePasskeyLoginFinish)

	rt.Handle("POST "+p+"/logout", s.handleLogout)
	rt.Handle("GET "+p+"/passkeys", s.handlePasskeyList)
	rt.Handle("POST "+p+"/passkeys/begin", s.handlePasskeyRegisterBegin)
	rt.Handle("POST "+p+"/passkeys/finish", s.handlePasskeyRegisterFinish)
	rt.Handle("PATCH "+p+"/passkeys/{id}", s.handlePasskeyRename)
	rt.Handle("DELETE "+p+"/passkeys/{id}", s.handlePasskeyDelete)
	rt.Handle("POST "+p+"/password", s.handlePassword)
	rt.Handle("GET "+p+"/totp", s.handleTOTPStatus)
	rt.Handle("POST "+p+"/totp/setup", s.handleTOTPSetup)
	rt.Handle("POST "+p+"/totp/enable", s.handleTOTPEnable)
	rt.Handle("POST "+p+"/totp/disable", s.handleTOTPDisable)
	rt.Handle("GET "+p+"/sessions", s.handleSessions)
	rt.Handle("DELETE "+p+"/sessions/{id}", s.handleSessionRevoke)
	rt.Handle("POST "+p+"/sessions/revoke-others", s.handleSessionRevokeOthers)
	rt.Handle("GET "+p+"/tokens", s.handleTokens)
	rt.Handle("POST "+p+"/tokens", s.handleTokenCreate)
	rt.Handle("DELETE "+p+"/tokens/{id}", s.handleTokenRevoke)
	rt.Handle("GET "+p+"/activity", s.handleActivity)
}

// --- helpers -----------------------------------------------------------------

// publicOriginOK refuses cross-site posts to the public sign-in endpoints.
// Browsers always send Origin (and Fetch Metadata) on these POSTs; clients
// sending neither (curl, scripts) are not a CSRF vector.
func (s *Service) publicOriginOK(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") == "" && r.Header.Get("Sec-Fetch-Site") == "" {
		return true
	}
	if s.rt != nil && s.rt.OriginAllowed(r) {
		return true
	}
	httpx.Error(w, http.StatusForbidden, "forbidden", "cross-origin request refused")
	return false
}

func rateLimited(wait time.Duration) *httpx.Err {
	secs := int(math.Ceil(wait.Seconds()))
	if secs < 1 {
		secs = 1
	}
	var when string
	switch {
	case secs < 60:
		when = fmt.Sprintf("%d seconds", secs)
	case secs < 120:
		when = "a minute"
	default:
		when = fmt.Sprintf("%d minutes", (secs+59)/60)
	}
	return &httpx.Err{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "Too many attempts. Try again in " + when + ".", RetryIn: secs}
}

func (s *Service) allow(l *limiter, ip string) error {
	if ok, wait := l.Allow(ip); !ok {
		return rateLimited(wait)
	}
	return nil
}

// verifyPassword checks pw against the account hash with bounded
// concurrency (argon2id costs 32 MiB per call). When u is nil a dummy hash
// is checked so timing does not reveal whether an account exists.
func (s *Service) verifyPassword(ctx context.Context, hash, pw string) (ok, rehash bool, err error) {
	select {
	case s.hashSem <- struct{}{}:
	case <-ctx.Done():
		return false, false, ctx.Err()
	}
	defer func() { <-s.hashSem }()
	if hash == "" {
		hash = s.dummyHash
	}
	if len(pw) > maxPasswordLen {
		pw = pw[:maxPasswordLen]
	}
	needs, verr := secret.VerifyPassword(pw, hash)
	return verr == nil, needs, nil
}

func (s *Service) hashPassword(ctx context.Context, pw string) (string, error) {
	select {
	case s.hashSem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-s.hashSem }()
	return secret.HashPassword(pw)
}

// audit records a security event for request r.
func (s *Service) audit(r *http.Request, event, actor, detail string) {
	if actor == "" {
		actor = "anonymous"
	}
	ua := ParseUserAgent(r.UserAgent())
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()
	if err := s.acc.Record(ctx, api.AuditEntry{Event: event, Actor: actor, IP: cleanIP(server.ClientIP(r)), Device: deviceLabel(ua), Detail: detail}); err != nil {
		s.log.Warn("audit write failed", "event", event, "err", err)
	}
}

func actorOf(p *server.Principal) string {
	if p == nil {
		return ""
	}
	if p.Method == "token" {
		return p.User + " (token " + p.TokenID + ")"
	}
	return p.User
}

// requireInteractive keeps account-security changes to browser sessions
// and the local CLI: a leaked API token must not be able to mint more
// tokens, add passkeys or turn off two-factor.
func requireInteractive(p *server.Principal) error {
	if p == nil || p.Method == "token" {
		return httpx.Forbidden("API tokens can't change sign-in settings. Use the web app or the relay CLI.")
	}
	return nil
}

// startSession creates a browser session for username, sets the cookie,
// notes the device and audits the sign-in.
func (s *Service) startSession(w http.ResponseWriter, r *http.Request, username, method string, remember bool) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	// A fresh id on every sign-in (no fixation); end the session this
	// browser had before, if any.
	if old := cookieValue(r, s.cookieName(r, sessionCookie)); old != "" {
		if row, err := s.acc.sessionByHash(ctx, secret.HashToken(old)); err == nil && row != nil {
			if _, err := s.acc.deleteSession(ctx, row.ID); err == nil {
				s.revoked(row.ID)
			}
		}
	}
	value, err := newSessionSecret()
	if err != nil {
		return err
	}
	ua := ParseUserAgent(r.UserAgent())
	now := s.now()
	row := &sessionRow{
		ID: "s_" + randomHex(12), TokenHash: secret.HashToken(value), Username: username, Method: method,
		Remember: remember, IP: cleanIP(server.ClientIP(r)), UserAgent: r.UserAgent(),
		Device: ua.Device, Browser: ua.Browser, OS: ua.OS,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(s.ttl(remember)),
	}
	if err := s.acc.insertSession(ctx, row); err != nil {
		return err
	}
	s.setSessionCookie(w, r, value, remember)
	s.noteDevice(ctx, w, r, ua, row.IP)
	s.audit(r, "login.ok", username, method)
	return nil
}

// noteDevice remembers this browser (long-lived random cookie, hashed at
// rest) and sends a "security" notification the first time a new browser
// signs in, unless it is the very first device.
func (s *Service) noteDevice(ctx context.Context, w http.ResponseWriter, r *http.Request, ua UserAgent, ip string) {
	label := deviceLabel(ua)
	dev := cookieValue(r, s.cookieName(r, deviceCookie))
	if len(dev) != 64 {
		dev = randomHex(32)
	}
	before, err := s.acc.deviceCount(ctx)
	if err != nil {
		s.log.Warn("device lookup failed", "err", err)
		return
	}
	known, err := s.acc.seenDevice(ctx, dev, label)
	if err != nil {
		s.log.Warn("device record failed", "err", err)
		return
	}
	s.setCookie(w, r, deviceCookie, dev, deviceCookieAge, http.SameSiteLaxMode)
	if known || before == 0 {
		return
	}
	s.audit(r, "device.new", "", label)
	n := s.d.Notifier // wired after auth: read at call time
	if n == nil {
		return
	}
	body := label
	if ip != "" {
		body += " · " + ip
	}
	if _, err := n.Notify(ctx, api.NotifyRequest{
		Kind: "security", Title: "New sign-in to Relay", Body: body,
		Link: "/settings/security", Severity: "warning",
	}); err != nil {
		s.log.Warn("new-device notification failed", "err", err)
	}
}

// --- state, setup, login, logout ------------------------------------------------------

func (s *Service) handleState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, err := s.acc.user(ctx)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	avail := s.PasskeysAvailable()
	resp := api.AuthStateResponse{PasskeysAvailable: avail}
	resp.SetupRequired = u == nil
	if u == nil {
		resp.SetupCodeRequired = !isDirectLocal(r)
	} else {
		resp.Methods.Password = true
		resp.Methods.TOTP = u.TOTPSecret != ""
		if avail {
			n, err := s.acc.passkeyCount(ctx)
			if err != nil {
				httpx.Fail(w, err)
				return
			}
			resp.Methods.Passkey = n > 0
		}
	}
	var p *server.Principal
	if s.rt != nil {
		p = s.rt.Authenticate(r)
	} else {
		p = s.Identify(r)
	}
	if p != nil {
		resp.Authenticated = true
		resp.User = p.User
		resp.SessionID = p.SessionID
		s.refreshRememberCookie(w, r)
	}
	httpx.OK(w, resp)
}

// refreshRememberCookie re-issues a remember-me cookie so its browser
// lifetime follows the sliding server-side expiry. The app calls
// /auth/state on every load, which is often enough.
func (s *Service) refreshRememberCookie(w http.ResponseWriter, r *http.Request) {
	v := cookieValue(r, s.cookieName(r, sessionCookie))
	if v == "" {
		return
	}
	if e := s.cache.get(secret.HashToken(v), s.now()); e != nil && e.remember {
		s.setSessionCookie(w, r, v, true)
	}
}

func (s *Service) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !s.publicOriginOK(w, r) {
		return
	}
	ip := server.ClientIP(r)
	if err := s.allow(s.setupLim, ip); err != nil {
		httpx.Fail(w, err)
		return
	}
	var req api.SetupRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	ctx := r.Context()
	if has, err := s.acc.HasUser(ctx); err != nil {
		httpx.Fail(w, err)
		return
	} else if has {
		httpx.Fail(w, httpx.Conflict("An account already exists. Sign in instead."))
		return
	}
	if !isDirectLocal(r) && !s.setupCodeOK(req.Code) {
		s.setupLim.Fail(ip)
		s.audit(r, "setup.fail", "", "wrong setup code")
		httpx.Fail(w, &httpx.Err{Status: http.StatusForbidden, Code: "forbidden", Field: "code",
			Message: "That setup code isn't right. It's printed by `relay serve` and saved in the data folder as setup-code."})
		return
	}
	if err := s.cookiePolicy(r); err != nil {
		httpx.Fail(w, err)
		return
	}
	name := strings.TrimSpace(req.Username)
	if err := ValidateUsername(name); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := CheckPassword(name, req.Password); err != nil {
		httpx.Fail(w, err)
		return
	}
	hash, err := s.hashPassword(ctx, req.Password)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.acc.createUser(ctx, name, hash); err != nil {
		if errors.Is(err, ErrUserExists) {
			httpx.Fail(w, httpx.Conflict("An account already exists. Sign in instead."))
			return
		}
		httpx.Fail(w, err)
		return
	}
	s.setupMu.Lock()
	s.setupCode = ""
	s.setupMu.Unlock()
	s.removeSetupCodeFile()
	s.audit(r, "account.create", name, "")
	if err := s.startSession(w, r, name, "password", req.Remember); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, api.LoginResponse{OK: true, SetupPasskey: s.PasskeysAvailable()})
}

func (s *Service) setupCodeOK(code string) bool {
	s.setupMu.Lock()
	want := s.setupCode
	s.setupMu.Unlock()
	norm := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	return want != "" && secret.Equal(norm, strings.ReplaceAll(want, "-", ""))
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.publicOriginOK(w, r) {
		return
	}
	ip := server.ClientIP(r)
	if err := s.allow(s.loginLim, ip); err != nil {
		s.audit(r, "login.limited", "", "")
		httpx.Fail(w, err)
		return
	}
	var req api.LoginRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.cookiePolicy(r); err != nil {
		httpx.Fail(w, err)
		return
	}
	ctx := r.Context()
	u, err := s.acc.user(ctx)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if u == nil {
		httpx.Fail(w, httpx.Conflict("Create your account first."))
		return
	}
	nameOK := secret.Equal(strings.TrimSpace(req.Username), u.Username)
	pwOK, rehash, err := s.verifyPassword(ctx, u.PasswordHash, req.Password)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !nameOK || !pwOK || req.Password == "" {
		s.loginLim.Fail(ip)
		s.audit(r, "login.fail", "", "password")
		httpx.Fail(w, httpx.Unauthorized(msgBadCredentials))
		return
	}
	if rehash {
		if h, err := s.hashPassword(ctx, req.Password); err == nil {
			_ = s.acc.setPasswordHash(ctx, h)
		}
	}
	if u.TOTPSecret != "" {
		if strings.TrimSpace(req.TOTP) == "" {
			httpx.OK(w, api.LoginResponse{NeedTOTP: true})
			return
		}
		if !s.checkTOTP(ctx, u.TOTPSecret, req.TOTP) {
			s.loginLim.Fail(ip)
			s.audit(r, "login.fail", "", "two-factor code")
			httpx.Fail(w, &httpx.Err{Status: http.StatusUnauthorized, Code: "unauthorized", Field: "totp", Message: msgBadCode})
			return
		}
	}
	s.loginLim.Success(ip)
	if err := s.startSession(w, r, u.Username, "password", req.Remember); err != nil {
		httpx.Fail(w, err)
		return
	}
	resp := api.LoginResponse{OK: true}
	if s.PasskeysAvailable() {
		if n, err := s.acc.passkeyCount(ctx); err == nil && n == 0 {
			resp.SetupPasskey = true
		}
	}
	httpx.OK(w, resp)
}

// checkTOTP verifies code against the active secret and claims its step.
func (s *Service) checkTOTP(ctx context.Context, secretB32, code string) bool {
	key, err := decodeTOTPSecret(secretB32)
	if err != nil {
		return false
	}
	step, ok := verifyTOTP(key, code, s.now())
	if !ok {
		return false
	}
	claimed, err := s.acc.claimTOTPStep(ctx, step)
	return err == nil && claimed
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	if p != nil && p.Method == "cookie" && p.SessionID != "" {
		if _, err := s.acc.deleteSession(r.Context(), p.SessionID); err == nil {
			s.revoked(p.SessionID)
		}
		s.audit(r, "logout", p.User, "")
	}
	s.clearSessionCookie(w, r)
	httpx.NoContent(w)
}

// --- passkey sign-in -------------------------------------------------------------

func (s *Service) handlePasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if !s.publicOriginOK(w, r) {
		return
	}
	if err := s.allow(s.passkeyLim, server.ClientIP(r)); err != nil {
		httpx.Fail(w, err)
		return
	}
	if has, err := s.acc.HasUser(r.Context()); err != nil || !has {
		if err == nil {
			err = httpx.Conflict("Create your account first.")
		}
		httpx.Fail(w, err)
		return
	}
	wa, err := s.webAuthn()
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	assertion, data, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		httpx.Fail(w, fmt.Errorf("begin passkey login: %w", err))
		return
	}
	id := s.ceremonies.put(&ceremony{kind: ceremonyLogin, data: *data})
	s.setCookie(w, r, ceremonyCookie, id, ceremonyTTL, http.SameSiteStrictMode)
	httpx.OK(w, assertion)
}

func (s *Service) handlePasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	if !s.publicOriginOK(w, r) {
		return
	}
	ip := server.ClientIP(r)
	if err := s.allow(s.passkeyLim, ip); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.cookiePolicy(r); err != nil {
		httpx.Fail(w, err)
		return
	}
	c := s.ceremonies.take(cookieValue(r, s.cookieName(r, ceremonyCookie)), ceremonyLogin)
	s.setCookie(w, r, ceremonyCookie, "", -1, http.SameSiteStrictMode)
	if c == nil {
		httpx.Fail(w, httpx.BadRequest("This sign-in request expired. Try again."))
		return
	}
	wa, err := s.webAuthn()
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, server.MaxAuthBody)
	ctx := r.Context()
	var found *passkeyRow
	usr, cred, err := wa.FinishPasskeyLogin(s.discoverableHandler(ctx, &found), c.data, r)
	if err != nil || cred == nil || found == nil {
		s.passkeyLim.Fail(ip)
		s.audit(r, "passkey.fail", "", passkeyErrDetail(err))
		httpx.Fail(w, httpx.Unauthorized("That passkey didn't work here. Try another, or sign in with your password."))
		return
	}
	if cred.Authenticator.CloneWarning {
		s.passkeyLim.Fail(ip)
		s.audit(r, "passkey.fail", "", "signature counter went backwards: possible cloned authenticator ("+found.Name+")")
		httpx.Fail(w, httpx.Unauthorized("That passkey didn't work here. Try another, or sign in with your password."))
		return
	}
	if err := s.acc.passkeyUsed(ctx, found.ID, cred, s.now()); err != nil {
		s.log.Warn("passkey update failed", "err", err)
	}
	s.passkeyLim.Success(ip)
	remember := r.URL.Query().Get("remember") != "0"
	if err := s.startSession(w, r, usr.WebAuthnName(), "passkey", remember); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, api.LoginResponse{OK: true})
}

// passkeyErrDetail keeps library error text (which never contains secrets)
// short for the audit log.
func passkeyErrDetail(err error) string {
	if err == nil {
		return "unknown credential"
	}
	var pe *protocol.Error
	if errors.As(err, &pe) {
		return clip(pe.Type+": "+pe.Details, 200)
	}
	return clip(err.Error(), 200)
}
