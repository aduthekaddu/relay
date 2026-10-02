package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/aduthekaddu/relay/internal/httpx"
)

// Principal is the authenticated caller.
type Principal struct {
	User      string
	SessionID string // browser session id (empty for tokens / local)
	Method    string // "cookie" | "token" | "local"
	TokenID   string
}

type ctxKey int

const (
	principalKey ctxKey = iota
	localConnKey
	socketAuthKey
)

// PrincipalFrom returns the caller, or nil on public routes.
func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey).(*Principal)
	return p
}

// WithPrincipal attaches p to ctx (used by the auth middleware and tests).
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// MarkLocal marks a context as coming from the owner-only control socket.
func MarkLocal(ctx context.Context) context.Context {
	return context.WithValue(ctx, localConnKey, true)
}

// IsLocal reports whether the request arrived on the control socket
// (peer credentials already verified to be the same uid).
func IsLocal(ctx context.Context) bool {
	v, _ := ctx.Value(localConnKey).(bool)
	return v
}

// Authenticator identifies callers. Implemented by internal/auth.
type Authenticator interface {
	// Identify returns the principal for r, or nil when unauthenticated.
	Identify(r *http.Request) *Principal
}

// Router wraps http.ServeMux with authentication levels. All API routes
// should live under /api/v1/.
type Router struct {
	mux          *http.ServeMux
	auth         Authenticator
	origins      func() []string // allowed browser origins for unsafe requests
	hostDispatch []func(r *http.Request) http.Handler
}

// NewRouter returns a router using auth (may be nil until SetAuthenticator)
// and the allowed browser origins for CSRF checks.
func NewRouter(auth Authenticator, allowedOrigins func() []string) *Router {
	return &Router{mux: http.NewServeMux(), auth: auth, origins: allowedOrigins}
}

// SetAuthenticator replaces the authenticator (wiring order convenience).
func (rt *Router) SetAuthenticator(a Authenticator) { rt.auth = a }

// Public registers a handler reachable without authentication. Use for
// login, health, static assets and the manifest only.
func (rt *Router) Public(pattern string, h http.HandlerFunc) {
	rt.mux.Handle(pattern, h)
}

// Handle registers an authenticated handler. Unsafe methods authenticated
// by cookie must carry an Origin in the allowed list (CSRF defence).
func (rt *Router) Handle(pattern string, h http.HandlerFunc) {
	rt.mux.Handle(pattern, rt.protect(h, false))
}

// WS registers an authenticated WebSocket endpoint. Cookie-authenticated
// upgrades always require a matching Origin, whatever the method.
func (rt *Router) WS(pattern string, h http.HandlerFunc) {
	rt.mux.Handle(pattern, rt.protect(h, true))
}

// Raw registers a handler with no wrapping at all (the handler must do
// its own auth). Used by the preview proxy and the SPA.
func (rt *Router) Raw(pattern string, h http.Handler) {
	rt.mux.Handle(pattern, h)
}

// HostDispatch registers a function that can claim a request by Host
// before normal routing (e.g. <port>.<domain> preview subdomains).
func (rt *Router) HostDispatch(f func(r *http.Request) http.Handler) {
	rt.hostDispatch = append(rt.hostDispatch, f)
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, f := range rt.hostDispatch {
		if h := f(r); h != nil {
			h.ServeHTTP(w, r)
			return
		}
	}
	rt.mux.ServeHTTP(w, r)
}

// Authenticate returns the principal for r (nil when anonymous). Exposed
// for handlers registered with Raw.
func (rt *Router) Authenticate(r *http.Request) *Principal {
	if IsLocal(r.Context()) {
		return &Principal{User: "local", Method: "local"}
	}
	if rt.auth == nil {
		return nil
	}
	p := rt.auth.Identify(r)
	if p == nil || p.User == "" || p.Method == "" {
		return nil
	}
	return p
}

// OriginAllowed reports whether r carries an Origin header naming one of
// the allowed browser origins (and is not flagged cross-site by Fetch
// Metadata). Public handlers that accept unsafe requests use it to refuse
// cross-site form posts.
func (rt *Router) OriginAllowed(r *http.Request) bool { return rt.originAllowed(r) }

// AllowedOrigins returns the browser origins accepted for unsafe requests.
func (rt *Router) AllowedOrigins() []string {
	if rt.origins == nil {
		return nil
	}
	return rt.origins()
}

func (rt *Router) protect(h http.HandlerFunc, ws bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := rt.Authenticate(r)
		if p == nil {
			httpx.Error(w, http.StatusUnauthorized, "unauthorized", "sign in required")
			return
		}
		if p.Method == "cookie" && (ws || httpx.IsUnsafeMethod(r.Method)) {
			if !rt.originAllowed(r) {
				httpx.Error(w, http.StatusForbidden, "forbidden", "cross-origin request refused")
				return
			}
		}
		ctx := WithPrincipal(r.Context(), p)
		if ws {
			if a, ok := rt.auth.(SocketAuthenticator); ok {
				ctx = context.WithValue(ctx, socketAuthKey, a)
			}
		}
		h(w, r.WithContext(ctx))
	})
}

func (rt *Router) originAllowed(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Browsers always send Origin on unsafe fetches and WS upgrades.
		// A missing Origin with a cookie means a non-browser client
		// replaying a cookie: refuse.
		return false
	}
	o, err := url.Parse(origin)
	if err != nil {
		return false
	}
	norm := strings.ToLower(o.Scheme + "://" + o.Host)
	for _, a := range rt.AllowedOrigins() {
		if strings.EqualFold(strings.TrimRight(a, "/"), norm) {
			return true
		}
	}
	return false
}
