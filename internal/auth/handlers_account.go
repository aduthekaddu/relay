package auth

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
)

// decodeOptional decodes a JSON body when one was sent.
func decodeOptional(r *http.Request, v any) error {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return nil
	}
	return httpx.Decode(r, v)
}

// --- password ------------------------------------------------------------------

func (s *Service) handlePassword(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	if err := requireInteractive(p); err != nil {
		httpx.Fail(w, err)
		return
	}
	ip := server.ClientIP(r)
	if err := s.allow(s.loginLim, ip); err != nil {
		httpx.Fail(w, err)
		return
	}
	var req api.ChangePasswordRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	ctx := r.Context()
	u, err := s.acc.user(ctx)
	if err != nil || u == nil {
		if err == nil {
			err = httpx.Conflict("Create your account first.")
		}
		httpx.Fail(w, err)
		return
	}
	ok, _, err := s.verifyPassword(ctx, u.PasswordHash, req.Current)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !ok {
		s.loginLim.Fail(ip)
		s.audit(r, "password.fail", actorOf(p), "wrong current password")
		// 400, not 401: the session is fine, only the field is wrong.
		httpx.Fail(w, &httpx.Err{Status: http.StatusBadRequest, Code: "bad_request", Field: "current", Message: "Your current password isn't right."})
		return
	}
	if err := CheckPassword(u.Username, req.Next); err != nil {
		if e, ok := err.(*httpx.Err); ok {
			e.Field = "next"
		}
		httpx.Fail(w, err)
		return
	}
	hash, err := s.hashPassword(ctx, req.Next)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.acc.setPasswordHash(ctx, hash); err != nil {
		httpx.Fail(w, err)
		return
	}
	s.loginLim.Success(ip)
	ids, err := s.acc.RevokeAllSessions(ctx, p.SessionID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.revoked(ids...)
	s.audit(r, "password.change", actorOf(p), fmt.Sprintf("signed out %d other sessions", len(ids)))
	httpx.NoContent(w)
}

// --- TOTP ------------------------------------------------------------------------

func (s *Service) handleTOTPStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.acc.user(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, api.TOTPStatus{Enabled: u != nil && u.TOTPSecret != ""})
}

func (s *Service) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	if err := requireInteractive(p); err != nil {
		httpx.Fail(w, err)
		return
	}
	ctx := r.Context()
	u, err := s.acc.user(ctx)
	if err != nil || u == nil {
		if err == nil {
			err = httpx.Conflict("Create your account first.")
		}
		httpx.Fail(w, err)
		return
	}
	if u.TOTPSecret != "" {
		httpx.Fail(w, httpx.Conflict("Two-factor is already on. Turn it off first to set up a new app."))
		return
	}
	sec, err := newTOTPSecret()
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.acc.setTOTPPending(ctx, sec); err != nil {
		httpx.Fail(w, err)
		return
	}
	account := u.Username
	if rp, ok := s.rpID(); ok {
		account += "@" + rp
	} else if h := s.hostLabel(); h != "" {
		account += "@" + h
	}
	httpx.OK(w, api.TOTPSetup{Secret: sec, OtpauthURL: otpauthURL("Relay", account, sec)})
}

// hostLabel names this machine in authenticator apps when there is no
// domain (host part of the canonical origin).
func (s *Service) hostLabel() string {
	o := s.d.Cfg.Origin()
	o = strings.TrimPrefix(strings.TrimPrefix(o, "https://"), "http://")
	if i := strings.IndexByte(o, ':'); i >= 0 {
		o = o[:i]
	}
	return o
}

func (s *Service) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	if err := requireInteractive(p); err != nil {
		httpx.Fail(w, err)
		return
	}
	ip := server.ClientIP(r)
	if err := s.allow(s.loginLim, ip); err != nil {
		httpx.Fail(w, err)
		return
	}
	var req api.CodeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	ctx := r.Context()
	u, err := s.acc.user(ctx)
	if err != nil || u == nil {
		if err == nil {
			err = httpx.Conflict("Create your account first.")
		}
		httpx.Fail(w, err)
		return
	}
	if u.TOTPSecret != "" {
		httpx.Fail(w, httpx.Conflict("Two-factor is already on."))
		return
	}
	if u.TOTPPending == "" {
		httpx.Fail(w, httpx.Conflict("Start two-factor setup first."))
		return
	}
	key, err := decodeTOTPSecret(u.TOTPPending)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	step, ok := verifyTOTP(key, req.Code, s.now())
	if !ok {
		s.loginLim.Fail(ip)
		s.audit(r, "totp.fail", actorOf(p), "enable")
		httpx.Fail(w, &httpx.Err{Status: http.StatusBadRequest, Code: "bad_request", Field: "code", Message: msgBadCode})
		return
	}
	if err := s.acc.enableTOTP(ctx, u.TOTPPending, step); err != nil {
		httpx.Fail(w, err)
		return
	}
	s.loginLim.Success(ip)
	s.audit(r, "totp.enable", actorOf(p), "")
	httpx.NoContent(w)
}

func (s *Service) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	if err := requireInteractive(p); err != nil {
		httpx.Fail(w, err)
		return
	}
	ip := server.ClientIP(r)
	if err := s.allow(s.loginLim, ip); err != nil {
		httpx.Fail(w, err)
		return
	}
	var req api.CodeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	ctx := r.Context()
	u, err := s.acc.user(ctx)
	if err != nil || u == nil || u.TOTPSecret == "" {
		if err == nil {
			err = httpx.Conflict("Two-factor is already off.")
		}
		httpx.Fail(w, err)
		return
	}
	if !s.checkTOTP(ctx, u.TOTPSecret, req.Code) {
		s.loginLim.Fail(ip)
		s.audit(r, "totp.fail", actorOf(p), "disable")
		httpx.Fail(w, &httpx.Err{Status: http.StatusBadRequest, Code: "bad_request", Field: "code", Message: msgBadCode})
		return
	}
	if err := s.acc.disableTOTP(ctx); err != nil {
		httpx.Fail(w, err)
		return
	}
	s.loginLim.Success(ip)
	s.audit(r, "totp.disable", actorOf(p), "")
	httpx.NoContent(w)
}

// --- device sessions -------------------------------------------------------------------

func (s *Service) handleSessions(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	rows, err := s.acc.listSessions(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	out := make([]api.DeviceSession, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toAPI(p.SessionID))
	}
	httpx.OK(w, out)
}

func (s *Service) handleSessionRevoke(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	id := r.PathValue("id")
	row, err := s.acc.deleteSession(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.revoked(row.ID)
	if row.ID == p.SessionID {
		s.clearSessionCookie(w, r)
	}
	s.audit(r, "session.revoke", actorOf(p), deviceLabel(UserAgent{Device: row.Device, Browser: row.Browser, OS: row.OS}))
	httpx.NoContent(w)
}

func (s *Service) handleSessionRevokeOthers(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	ids, err := s.acc.RevokeAllSessions(r.Context(), p.SessionID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.revoked(ids...)
	s.audit(r, "session.revoke_others", actorOf(p), fmt.Sprintf("%d sessions", len(ids)))
	httpx.OK(w, api.RevokedCount{Revoked: len(ids)})
}

// --- API tokens --------------------------------------------------------------------------

func (s *Service) handleTokens(w http.ResponseWriter, r *http.Request) {
	list, err := s.acc.ListTokens(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, list)
}

func (s *Service) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	if err := requireInteractive(p); err != nil {
		httpx.Fail(w, err)
		return
	}
	var req api.NameRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	t, err := s.acc.CreateToken(r.Context(), req.Name)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "token.create", actorOf(p), t.Name+" ("+t.Prefix+"…)")
	httpx.JSON(w, http.StatusCreated, t)
}

func (s *Service) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	t, err := s.acc.RevokeToken(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if s.d.Bus != nil {
		s.d.Bus.Publish(core.BusSessionRevoked, core.SessionRevoked{TokenIDs: []string{t.ID}})
	}
	s.audit(r, "token.revoke", actorOf(p), t.Name+" ("+t.Prefix+"…)")
	httpx.NoContent(w)
}

// --- activity ----------------------------------------------------------------------------

func (s *Service) handleActivity(w http.ResponseWriter, r *http.Request) {
	list, err := s.acc.Activity(r.Context(), httpx.QueryInt(r, "limit", 100, 1, 500))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, list)
}

// --- passkey management ------------------------------------------------------------------

func (s *Service) handlePasskeyList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.acc.listPasskeys(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	out := make([]api.PasskeyDetail, 0, len(rows))
	for _, p := range rows {
		out = append(out, p.toAPI())
	}
	httpx.OK(w, out)
}

func (s *Service) handlePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	if err := requireInteractive(p); err != nil {
		httpx.Fail(w, err)
		return
	}
	var req api.NameRequest
	if err := decodeOptional(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = deviceLabel(ParseUserAgent(r.UserAgent()))
	}
	name, err := validPasskeyName(name)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	wa, err := s.webAuthn()
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	ctx := r.Context()
	u, err := s.acc.user(ctx)
	if err != nil || u == nil {
		if err == nil {
			err = httpx.Conflict("Create your account first.")
		}
		httpx.Fail(w, err)
		return
	}
	existing, err := s.acc.listPasskeys(ctx)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	creds := make([]webauthn.Credential, 0, len(existing))
	exclude := make([]protocol.CredentialDescriptor, 0, len(existing))
	for _, e := range existing {
		creds = append(creds, e.Cred)
		exclude = append(exclude, e.Cred.Descriptor())
	}
	creation, data, err := wa.BeginRegistration(&waUser{u: u, creds: creds},
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationPreferred,
		}),
		webauthn.WithExclusions(exclude),
	)
	if err != nil {
		httpx.Fail(w, fmt.Errorf("begin passkey registration: %w", err))
		return
	}
	id := s.ceremonies.put(&ceremony{kind: ceremonyRegister, data: *data, name: name, session: p.SessionID})
	s.setCookie(w, r, ceremonyCookie, id, ceremonyTTL, http.SameSiteStrictMode)
	httpx.OK(w, creation)
}

func (s *Service) handlePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	if err := requireInteractive(p); err != nil {
		httpx.Fail(w, err)
		return
	}
	c := s.ceremonies.take(cookieValue(r, s.cookieName(r, ceremonyCookie)), ceremonyRegister)
	s.setCookie(w, r, ceremonyCookie, "", -1, http.SameSiteStrictMode)
	if c == nil || c.session != p.SessionID {
		httpx.Fail(w, httpx.BadRequest("This passkey request expired. Try again."))
		return
	}
	wa, err := s.webAuthn()
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	ctx := r.Context()
	u, err := s.acc.user(ctx)
	if err != nil || u == nil {
		if err == nil {
			err = httpx.Conflict("Create your account first.")
		}
		httpx.Fail(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, server.MaxAuthBody)
	cred, err := wa.FinishRegistration(&waUser{u: u}, c.data, r)
	if err != nil {
		s.audit(r, "passkey.fail", actorOf(p), "register: "+passkeyErrDetail(err))
		httpx.Fail(w, httpx.BadRequest("Couldn't save that passkey. Try again."))
		return
	}
	row := &passkeyRow{ID: "pk_" + randomHex(8), Cred: *cred, Name: c.name, CreatedAt: s.now()}
	if err := s.acc.insertPasskey(ctx, row); err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "passkey.add", actorOf(p), row.Name)
	httpx.JSON(w, http.StatusCreated, row.toAPI())
}

func (s *Service) handlePasskeyRename(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	if err := requireInteractive(p); err != nil {
		httpx.Fail(w, err)
		return
	}
	var req api.NameRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	name, err := validPasskeyName(req.Name)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	if err := s.acc.renamePasskey(ctx, id, name); err != nil {
		httpx.Fail(w, err)
		return
	}
	row, err := s.acc.passkeyByID(ctx, id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, row.toAPI())
}

func (s *Service) handlePasskeyDelete(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	if err := requireInteractive(p); err != nil {
		httpx.Fail(w, err)
		return
	}
	row, err := s.acc.deletePasskey(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, "passkey.remove", actorOf(p), row.Name)
	httpx.NoContent(w)
}
