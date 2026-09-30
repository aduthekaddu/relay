package previews

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/previews/revproxy"
	"github.com/aduthekaddu/relay/internal/server"
)

const (
	cookieName   = "relay_preview"
	authPath     = "/_relay/preview-auth"
	callbackPath = "/_relay/preview-callback"

	// sandboxCSP makes path-mode previews run in an opaque origin, so
	// they can never read Relay cookies/storage or call the API as the user.
	sandboxCSP = "sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads"
)

// Routes registers the API, the path-mode proxy, the handshake endpoint
// and the subdomain host dispatcher.
func (s *Service) Routes(rt *server.Router) {
	s.rt = rt
	rt.Handle("GET /api/v1/previews", s.handleList)
	rt.Handle("GET /api/v1/previews/{port}/link", s.handleLink)
	rt.Handle("PATCH /api/v1/previews/{port}", s.handlePatch)
	rt.Raw("/p/{port}", http.HandlerFunc(s.handlePathRedirect))
	rt.Raw("/p/{port}/", http.HandlerFunc(s.handlePath))
	rt.Raw("GET "+authPath, http.HandlerFunc(s.handleAuth))
	rt.HostDispatch(s.dispatchHost)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, s.List())
}

func parsePort(r *http.Request) (int, error) {
	p, err := strconv.Atoi(r.PathValue("port"))
	if err != nil || p < 1 || p > 65535 {
		return 0, httpx.BadRequest("invalid port")
	}
	return p, nil
}

func (s *Service) handleLink(w http.ResponseWriter, r *http.Request) {
	port, err := parsePort(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	mode := s.Mode()
	if mode == modeOff {
		httpx.Fail(w, httpx.Unavailable("previews are turned off (previews.mode = \"off\")"))
		return
	}
	if s.excluded(port) {
		httpx.Fail(w, httpx.BadRequest(fmt.Sprintf("port %d is outside the preview range or ignored", port)))
		return
	}
	on := s.listening(r.Context(), port)
	p, _ := s.get(port)
	link := api.PreviewLink{Port: port, URL: p.URL, Mode: mode, Listening: on}
	if on {
		link.Preview = &p
	}
	httpx.OK(w, link)
}

func (s *Service) handlePatch(w http.ResponseWriter, r *http.Request) {
	port, err := parsePort(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req api.UpdatePreviewRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	p, err := s.update(r.Context(), port, req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, p)
}

// ---------------------------------------------------------------------------
// Path mode: /p/{port}/...

func (s *Service) handlePathRedirect(w http.ResponseWriter, r *http.Request) {
	u := *r.URL
	u.Path += "/"
	u.RawPath = ""
	http.Redirect(w, r, u.RequestURI(), http.StatusPermanentRedirect)
}

func (s *Service) handlePath(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", sandboxCSP)
	h.Del("Cross-Origin-Opener-Policy")
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil || port < 1 || port > 65535 || s.Mode() == modeOff {
		http.NotFound(w, r)
		return
	}
	if s.rt.Authenticate(r) == nil {
		s.unauthenticated(w, r, s.origin.String()+"/login?next="+url.QueryEscape(r.URL.RequestURI()))
		return
	}
	if !s.listening(r.Context(), port) {
		s.errorPage(w, http.StatusNotFound, port, "Nothing is listening on this port.")
		return
	}
	s.proxyFor(modePath, port).ServeHTTP(w, r)
}

// unauthenticated redirects navigations to sign-in and refuses the rest.
func (s *Service) unauthenticated(w http.ResponseWriter, r *http.Request, loginURL string) {
	w.Header().Set("Cache-Control", "no-store")
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && !revproxy.IsWebSocket(r) {
		http.Redirect(w, r, loginURL, http.StatusFound)
		return
	}
	http.Error(w, "sign in required", http.StatusUnauthorized)
}

// proxyFor returns the cached proxy for port in mode.
func (s *Service) proxyFor(mode string, port int) *revproxy.Proxy {
	key := mode[:1] + ":" + strconv.Itoa(port)
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.proxies[key]; p != nil {
		return p
	}
	opts := revproxy.Options{
		Proto:        s.origin.Scheme,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) { s.upstreamError(w, port, err) },
	}
	if mode == modePath {
		opts.StripPrefix = "/p/" + strconv.Itoa(port)
	} else {
		opts.PublicOrigin = subdomainOrigin(port, s.baseHost(), s.origin)
	}
	p := revproxy.New(revproxy.Target{
		Dial: revproxy.LoopbackTCP(port, 2*time.Second),
		Host: "localhost:" + strconv.Itoa(port),
	}, opts)
	s.proxies[key] = p
	return p
}

func (s *Service) upstreamError(w http.ResponseWriter, port int, err error) {
	s.log.Debug("preview upstream error", "port", port, "err", err)
	msg := "The dev server did not answer."
	if errors.Is(err, context.DeadlineExceeded) {
		msg = "The dev server took too long to answer."
	}
	s.errorPage(w, http.StatusBadGateway, port, msg)
}

// errorPage renders a small self-contained page that retries itself.
func (s *Service) errorPage(w http.ResponseWriter, status, port int, msg string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Refresh", "3")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="dark light"><title>:%d — Relay preview</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;font:15px/1.5 system-ui,sans-serif;background:#0b0b0c;color:#ede9e0}
@media (prefers-color-scheme:light){body{background:#f3f0e8;color:#16150f}}
main{max-width:28rem;padding:2rem;text-align:center}h1{font-size:1.25rem;margin:0 0 .5rem}p{margin:.25rem 0;opacity:.75}
.dot{display:inline-block;width:.6rem;height:.6rem;border-radius:50%%;background:#ff5b1f;margin-right:.5rem}</style></head>
<body><main role="status"><h1><span class="dot" aria-hidden="true"></span>Port %d</h1><p>%s</p><p>Retrying every few seconds…</p></main></body></html>`,
		port, port, html.EscapeString(msg))
}

// ---------------------------------------------------------------------------
// Subdomain mode: https://<port>.<host>/...

func (s *Service) dispatchHost(r *http.Request) http.Handler {
	if s.Mode() != modeSubdomain {
		return nil
	}
	port, ok := parsePreviewHost(r.Host, s.baseHost())
	if !ok {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serveSubdomain(w, r, port) })
}

func (s *Service) serveSubdomain(w http.ResponseWriter, r *http.Request, port int) {
	h := w.Header()
	// The preview is its own origin: replace the app-shell policy with one
	// that only controls who may frame it (the Relay UI).
	h.Set("Content-Security-Policy", "frame-ancestors 'self' "+s.origin.String())
	h.Del("X-Frame-Options")
	h.Del("Cross-Origin-Opener-Policy")
	if s.excluded(port) {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == callbackPath {
		s.handleCallback(w, r, port)
		return
	}
	if !s.subdomainAuthorized(r, port) {
		next := revproxy.SafeNext(r.URL.RequestURI())
		s.unauthenticated(w, r, s.origin.String()+authPath+"?port="+strconv.Itoa(port)+"&next="+url.QueryEscape(next))
		return
	}
	if !s.listening(r.Context(), port) {
		s.errorPage(w, http.StatusNotFound, port, "Nothing is listening on this port.")
		return
	}
	s.proxyFor(modeSubdomain, port).ServeHTTP(w, r)
}

// subdomainAuthorized accepts a valid relay_preview cookie for this port
// or an API token (Authorization: Bearer) for scripted access.
func (s *Service) subdomainAuthorized(r *http.Request, port int) bool {
	for _, c := range r.Cookies() {
		if c.Name != cookieName {
			continue
		}
		if _, err := s.tok.verify(c.Value, kindCookie, port); err == nil {
			return true
		}
	}
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && s.rt != nil {
		if p := s.rt.Authenticate(r); p != nil && p.Method == "token" {
			return true
		}
	}
	return false
}

// handleAuth runs on the Relay origin: the signed-in user is sent back to
// the preview origin with a 60-second, single-use handshake token.
func (s *Service) handleAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	p := s.rt.Authenticate(r)
	if p == nil {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	if s.Mode() != modeSubdomain {
		httpx.Fail(w, httpx.NotFound("subdomain previews are not enabled"))
		return
	}
	port, err := strconv.Atoi(r.URL.Query().Get("port"))
	if err != nil || port < 1 || port > 65535 || s.excluded(port) {
		httpx.Fail(w, httpx.BadRequest("invalid port"))
		return
	}
	next := revproxy.SafeNext(r.URL.Query().Get("next"))
	tok, err := s.tok.issue(kindHandshake, port, sessionRef(p), handshakeTTL)
	if err != nil {
		httpx.Fail(w, fmt.Errorf("issue preview token: %w", err))
		return
	}
	dest := subdomainOrigin(port, s.baseHost(), s.origin) + callbackPath + "?token=" + url.QueryEscape(tok) + "&next=" + url.QueryEscape(next)
	http.Redirect(w, r, dest, http.StatusFound)
}

// handleCallback runs on the preview origin: it exchanges the handshake
// token for the host-only preview cookie.
func (s *Service) handleCallback(w http.ResponseWriter, r *http.Request, port int) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c, err := s.tok.verify(r.URL.Query().Get("token"), kindHandshake, port)
	if err != nil {
		s.log.Info("preview handshake refused", "port", port, "reason", err.Error())
		http.Error(w, "preview sign-in link is invalid or expired; open the preview again from Relay", http.StatusForbidden)
		return
	}
	val, err := s.tok.issue(kindCookie, port, c.Session, cookieTTL)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    val,
		Path:     "/",
		MaxAge:   int(cookieTTL / time.Second),
		HttpOnly: true,
		Secure:   s.origin.Secure(),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, revproxy.SafeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
}
