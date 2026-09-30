package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
)

// Cookie base names. Over HTTPS they get the __Host- prefix (Secure, no
// Domain, Path=/), which also stops sibling subdomains such as preview
// origins from planting them.
const (
	sessionCookie   = "relay_session"
	deviceCookie    = "relay_device"   // long-lived, identifies a browser for new-device notices
	ceremonyCookie  = "relay_webauthn" // WebAuthn ceremony id, 5 minutes
	hostPrefix      = "__Host-"
	deviceCookieAge = 400 * 24 * time.Hour // browsers cap cookie lifetime at 400 days
)

// secureContext reports whether cookies for r must be Secure: the request
// arrived over HTTPS (directly or via a trusted proxy) or the canonical
// origin is HTTPS.
func (s *Service) secureContext(r *http.Request) bool {
	return server.IsSecureRequest(r) || strings.HasPrefix(s.d.Cfg.Origin(), "https://")
}

func (s *Service) cookieName(r *http.Request, base string) string {
	if s.secureContext(r) {
		return hostPrefix + base
	}
	return base
}

// cookiePolicy decides whether a sign-in cookie can be issued for r. Plain
// HTTP is accepted on loopback hosts (browsers treat them as secure
// contexts) or when auth.insecure_cookies is set; anywhere else a cookie
// would travel in clear text, so sign-in is refused with an explanation.
func (s *Service) cookiePolicy(r *http.Request) error {
	if s.secureContext(r) || isLoopbackHost(r.Host) || s.d.Cfg.Auth.InsecureCookies || server.IsLocal(r.Context()) {
		return nil
	}
	return &httpx.Err{Status: http.StatusForbidden, Code: "insecure_origin",
		Message: "Sign-in needs HTTPS. Open Relay through its HTTPS address, or set auth.insecure_cookies for a trusted LAN."}
}

func (s *Service) setCookie(w http.ResponseWriter, r *http.Request, base, value string, maxAge time.Duration, sameSite http.SameSite) {
	c := &http.Cookie{
		Name:     s.cookieName(r, base),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureContext(r),
		SameSite: sameSite,
	}
	switch {
	case maxAge < 0:
		c.MaxAge = -1
		c.Expires = time.Unix(1, 0)
	case maxAge > 0:
		c.MaxAge = int(maxAge / time.Second)
	}
	http.SetCookie(w, c)
}

// setSessionCookie issues the session cookie. Remember-me sessions persist
// for the session TTL; others are browser-session cookies.
func (s *Service) setSessionCookie(w http.ResponseWriter, r *http.Request, value string, remember bool) {
	var age time.Duration
	if remember {
		age = s.ttl(true)
	}
	s.setCookie(w, r, sessionCookie, value, age, http.SameSiteLaxMode)
}

func (s *Service) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	s.setCookie(w, r, sessionCookie, "", -1, http.SameSiteLaxMode)
}

func cookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil || len(c.Value) > 128 {
		return ""
	}
	return c.Value
}
