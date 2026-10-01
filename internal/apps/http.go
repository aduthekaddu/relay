package apps

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/previews/revproxy"
	"github.com/aduthekaddu/relay/internal/server"
)

// Routes registers the apps API, the app proxies and the desktop API.
func (s *Service) Routes(rt *server.Router) {
	s.rt = rt
	rt.Handle("GET /api/v1/apps", func(w http.ResponseWriter, r *http.Request) { httpx.OK(w, s.List()) })
	rt.Handle("POST /api/v1/apps/{id}/start", s.handleAppStart)
	rt.Handle("POST /api/v1/apps/{id}/stop", s.handleAppStop)
	rt.Raw("/apps/{id}", http.HandlerFunc(s.handleAppRedirect))
	rt.Raw("/apps/{id}/", http.HandlerFunc(s.serveApp))

	rt.Handle("GET /api/v1/desktop", func(w http.ResponseWriter, r *http.Request) { httpx.OK(w, s.desk.State()) })
	rt.Handle("POST /api/v1/desktop/start", s.handleDesktopStart)
	rt.Handle("POST /api/v1/desktop/stop", s.handleDesktopStop)
	rt.Handle("POST /api/v1/desktop/launch", s.handleDesktopLaunch)
	rt.Handle("GET /api/v1/desktop/clipboard", s.handleClipboardGet)
	rt.Handle("POST /api/v1/desktop/clipboard", s.handleClipboardSet)
	rt.Handle("POST /api/v1/desktop/resize", s.handleDesktopResize)
	rt.WS("GET /api/v1/desktop/ws", s.desk.ServeWS)
}

// ---------------------------------------------------------------------------
// Apps API

func (s *Service) handleAppStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "desktop" {
		s.handleDesktopStartApp(w, r)
		return
	}
	a := s.apps[id]
	if a == nil {
		httpx.Fail(w, httpx.NotFound("no such app"))
		return
	}
	if !s.appUsable(a) {
		httpx.Fail(w, httpx.Unavailable(a.name+" is not installed: "+a.installHint))
		return
	}
	if a.proc == nil {
		httpx.Fail(w, httpx.BadRequest(a.name+" is not managed by Relay (no command configured)"))
		return
	}
	a.proc.StartAsync() // long start: 200 with state "starting", then app.state events
	httpx.OK(w, s.appState(a))
}

func (s *Service) handleAppStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "desktop" {
		if err := s.desk.Stop(r.Context()); err != nil {
			httpx.Fail(w, err)
			return
		}
		httpx.OK(w, desktopApp(s.desk.State()))
		return
	}
	a := s.apps[id]
	if a == nil {
		httpx.Fail(w, httpx.NotFound("no such app"))
		return
	}
	if a.proc == nil {
		httpx.Fail(w, httpx.BadRequest(a.name+" is not managed by Relay (no command configured)"))
		return
	}
	if err := a.proc.Stop(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, s.appState(a))
}

// ---------------------------------------------------------------------------
// App proxy: /apps/{id}/...

func (s *Service) handleAppRedirect(w http.ResponseWriter, r *http.Request) {
	u := *r.URL
	u.Path += "/"
	u.RawPath = ""
	http.Redirect(w, r, u.RequestURI(), http.StatusPermanentRedirect)
}

func (s *Service) serveApp(w http.ResponseWriter, r *http.Request) {
	a := s.apps[r.PathValue("id")]
	if a == nil {
		http.NotFound(w, r)
		return
	}
	p := s.rt.Authenticate(r)
	if p == nil {
		w.Header().Set("Cache-Control", "no-store")
		if isNavigation(r) {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		http.Error(w, "sign in required", http.StatusUnauthorized)
		return
	}
	if p.Method == "cookie" && (httpx.IsUnsafeMethod(r.Method) || revproxy.IsWebSocket(r)) && !s.sameOrigin(r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	if !s.appUsable(a) {
		writePage(w, http.StatusServiceUnavailable, pageData{Title: a.name, Heading: a.name + " is not installed", Body: a.installHint})
		return
	}
	if a.proc != nil && !s.ensureRunning(w, r, a) {
		return
	}
	// The app ships its own security headers; the shell's CSP would break
	// it (code-server needs workers, wasm and inline styles). It is still
	// framed only by Relay itself (X-Frame-Options stays SAMEORIGIN).
	w.Header().Del("Content-Security-Policy")
	a.proxy.ServeHTTP(w, r)
}

// ensureRunning starts a stopped app. Navigations get a "starting…" page
// that reloads itself; other requests get 503 + Retry-After. It returns
// true when the request can be proxied now.
func (s *Service) ensureRunning(w http.ResponseWriter, r *http.Request, a *webApp) bool {
	st, _, msg := a.proc.Status()
	if a.id == "code" && msg != "" {
		msg = "Code could not start or exited unexpectedly."
	}
	if st == stateRunning {
		return true
	}
	if st == stateError && !isNavigation(r) {
		http.Error(w, a.name+" failed to start: "+msg, http.StatusServiceUnavailable)
		return false
	}
	a.act.Touch()
	ready := a.proc.StartAsync()
	// Fast starts skip the interstitial page entirely.
	select {
	case <-ready:
	case <-time.After(1500 * time.Millisecond):
	case <-r.Context().Done():
		return false
	}
	st, _, msg = a.proc.Status()
	if a.id == "code" && msg != "" {
		msg = "Code could not start or exited unexpectedly."
	}
	switch {
	case st == stateRunning:
		return true
	case st == stateError:
		writePage(w, http.StatusServiceUnavailable, pageData{Title: a.name, Heading: a.name + " could not start", Body: "Relay tried to start it and it failed.", Detail: msg})
	case isNavigation(r):
		writePage(w, http.StatusServiceUnavailable, pageData{Title: a.name, Heading: "Starting " + a.name + "…", Body: "This page reloads by itself when it is ready.", Retry: 1, Spinner: true})
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, a.name+" is starting", http.StatusServiceUnavailable)
	}
	return false
}

func isNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if revproxy.IsWebSocket(r) {
		return false
	}
	if m := r.Header.Get("Sec-Fetch-Mode"); m != "" {
		return m == "navigate"
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// sameOrigin is the CSRF check for cookie-authenticated unsafe requests
// and WebSocket upgrades to apps: the Origin must be this host or the
// canonical Relay origin, and never a cross-site fetch.
func (s *Service) sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	return strings.EqualFold(strings.TrimRight(o, "/"), strings.TrimRight(s.d.Cfg.Origin(), "/"))
}

// ---------------------------------------------------------------------------
// Desktop API

func (s *Service) handleDesktopStart(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.desk.Start(ctx); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, s.desk.State())
}

func (s *Service) handleDesktopStartApp(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.desk.Start(ctx); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, desktopApp(s.desk.State()))
}

func (s *Service) handleDesktopStop(w http.ResponseWriter, r *http.Request) {
	if err := s.desk.Stop(r.Context()); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, s.desk.State())
}

func (s *Service) handleDesktopLaunch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		App string `json:"app"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.desk.Launch(ctx, req.App); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, s.desk.State())
}

func (s *Service) handleClipboardGet(w http.ResponseWriter, r *http.Request) {
	text, err := s.desk.Clipboard(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]string{"text": text})
}

func (s *Service) handleClipboardSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := httpx.DecodeLimit(r, &req, maxClipboard+4096); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.desk.SetClipboard(r.Context(), req.Text); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Service) handleDesktopResize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.desk.Resize(r.Context(), req.Width, req.Height); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, s.desk.State())
}

var _ = api.EvAppState
