// Package revproxy is the hardened reverse proxy shared by previews and
// apps. It only ever dials the loopback target it is given, strips Relay
// credentials (session cookies, preview cookies, Authorization) before
// forwarding, sets X-Forwarded-* from scratch, rewrites redirects and
// cookie paths when serving under a path prefix, and drops response
// headers that would let an upstream act on the Relay origin
// (Clear-Site-Data, Service-Worker-Allowed, Strict-Transport-Security).
//
// WebSockets work through httputil.ReverseProxy's native Upgrade support.
package revproxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Target is the upstream to proxy to.
type Target struct {
	// Dial opens a connection to the upstream. It must only reach
	// loopback addresses or owner-only unix sockets.
	Dial func(ctx context.Context) (net.Conn, error)
	// Host is the Host header sent upstream, e.g. "localhost:5173".
	// Dev servers (Vite, webpack) refuse unknown hosts, so this is
	// always a loopback name; the public host goes in X-Forwarded-Host.
	Host string
}

// Options tune one proxy.
type Options struct {
	// StripPrefix is removed from the request path before forwarding
	// ("/p/5173") and prepended to redirects and cookie paths coming back.
	StripPrefix string
	// Proto is the external scheme ("https" or "http") for
	// X-Forwarded-Proto. Empty derives it from the request.
	Proto string
	// PublicOrigin is the browser-visible origin of the proxied content
	// (e.g. "https://5173.dev.example.com"). A request Origin equal to it
	// is rewritten to "http://<Target.Host>" so CSRF checks in frameworks
	// like Django and Next.js accept requests made through the proxy.
	PublicOrigin string
	// DropCookie reports additional request cookie names to strip.
	DropCookie func(name string) bool
	// ErrorHandler renders upstream failures. Default: plain 502.
	ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)
	// Activity, when set, is updated on every request and counts
	// in-flight requests (including open WebSockets).
	Activity *Activity
}

// Activity tracks in-flight requests and the time of the last one, for
// idle shutdown of on-demand apps.
type Activity struct {
	active atomic.Int64
	last   atomic.Int64 // unix nanoseconds
}

// Touch records activity now.
func (a *Activity) Touch() { a.last.Store(time.Now().UnixNano()) }

// Active returns the number of in-flight requests.
func (a *Activity) Active() int64 { return a.active.Load() }

// Last returns the time of the last request start or end (zero if none).
func (a *Activity) Last() time.Time {
	n := a.last.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// IdleFor reports how long the target has had no traffic; 0 while any
// request is in flight.
func (a *Activity) IdleFor(now time.Time) time.Duration {
	if a.Active() > 0 {
		return 0
	}
	l := a.Last()
	if l.IsZero() {
		return 0
	}
	return now.Sub(l)
}

func (a *Activity) begin() {
	a.active.Add(1)
	a.Touch()
}

func (a *Activity) end() {
	a.active.Add(-1)
	a.Touch()
}

// IsRelayCookie reports whether a cookie belongs to Relay and must never
// reach an upstream (or be set by one).
func IsRelayCookie(name string) bool {
	n := strings.ToLower(name)
	for _, p := range []string{"__host-", "__secure-"} {
		n = strings.TrimPrefix(n, p)
	}
	return n == "relay_session" || n == "relay_preview" || strings.HasPrefix(n, "relay_")
}

// Proxy is an http.Handler forwarding to one Target.
type Proxy struct {
	rp   *httputil.ReverseProxy
	opts Options
}

// New builds a proxy for t.
func New(t Target, o Options) *Proxy {
	o.StripPrefix = strings.TrimRight(o.StripPrefix, "/")
	tr := &http.Transport{
		DialContext:           func(ctx context.Context, _, _ string) (net.Conn, error) { return t.Dial(ctx) },
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true, // pass encodings through untouched
		ForceAttemptHTTP2:     false,
	}
	p := &Proxy{opts: o}
	p.rp = &httputil.ReverseProxy{
		Rewrite:        func(pr *httputil.ProxyRequest) { p.rewrite(pr, t.Host) },
		ModifyResponse: func(res *http.Response) error { p.modifyResponse(res, t.Host); return nil },
		Transport:      tr,
		FlushInterval:  -1, // stream SSE / chunked HMR immediately
		ErrorHandler:   p.errorHandler,
	}
	return p
}

// Close releases idle upstream connections.
func (p *Proxy) Close() {
	if tr, ok := p.rp.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}

// ServeHTTP forwards r upstream.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a := p.opts.Activity; a != nil {
		a.begin()
		defer a.end()
	}
	p.rp.ServeHTTP(w, r)
}

func (p *Proxy) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return // client went away
	}
	if p.opts.ErrorHandler != nil {
		p.opts.ErrorHandler(w, r, err)
		return
	}
	http.Error(w, "upstream unavailable", http.StatusBadGateway)
}

func (p *Proxy) rewrite(pr *httputil.ProxyRequest, host string) {
	in, out := pr.In, pr.Out
	out.URL.Scheme = "http"
	out.URL.Host = host
	out.Host = host
	if pre := p.opts.StripPrefix; pre != "" {
		out.URL.Path = stripPrefix(in.URL.Path, pre)
		if in.URL.RawPath != "" {
			out.URL.RawPath = stripPrefix(in.URL.RawPath, pre)
		}
		out.Header.Set("X-Forwarded-Prefix", pre)
	}
	// Rewrite already removed inbound X-Forwarded-*; set them afresh.
	pr.SetXForwarded()
	if p.opts.Proto != "" {
		out.Header.Set("X-Forwarded-Proto", p.opts.Proto)
	}
	out.Header.Del("Authorization")
	out.Header.Del("Proxy-Authorization")
	filterCookies(out.Header, p.opts.DropCookie)
	if o := out.Header.Get("Origin"); o != "" && p.opts.PublicOrigin != "" && strings.EqualFold(o, p.opts.PublicOrigin) {
		out.Header.Set("Origin", "http://"+host)
	}
}

func stripPrefix(path, prefix string) string {
	rest := strings.TrimPrefix(path, prefix)
	if rest == path && !strings.HasPrefix(path, prefix) {
		return path
	}
	if rest == "" || rest[0] != '/' {
		rest = "/" + rest
	}
	return rest
}

// filterCookies rewrites the Cookie header without Relay's cookies (and
// any the caller wants dropped), preserving the remaining pairs verbatim.
func filterCookies(h http.Header, drop func(string) bool) {
	lines := h.Values("Cookie")
	if len(lines) == 0 {
		return
	}
	var kept []string
	for _, line := range lines {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, _, _ := strings.Cut(part, "=")
			name = strings.TrimSpace(name)
			if IsRelayCookie(name) || (drop != nil && drop(name)) {
				continue
			}
			kept = append(kept, part)
		}
	}
	h.Del("Cookie")
	if len(kept) > 0 {
		h.Set("Cookie", strings.Join(kept, "; "))
	}
}

// dangerousResponseHeaders could let content act on the Relay origin.
var dangerousResponseHeaders = []string{
	"Clear-Site-Data",
	"Service-Worker-Allowed",
	"Strict-Transport-Security",
	"Alt-Svc",
}

func (p *Proxy) modifyResponse(res *http.Response, host string) {
	h := res.Header
	for _, k := range dangerousResponseHeaders {
		h.Del(k)
	}
	if loc := h.Get("Location"); loc != "" {
		h.Set("Location", RewriteLocation(loc, host, p.opts.StripPrefix))
	}
	if cl := h.Get("Content-Location"); cl != "" {
		h.Set("Content-Location", RewriteLocation(cl, host, p.opts.StripPrefix))
	}
	if sc := h.Values("Set-Cookie"); len(sc) > 0 {
		h.Del("Set-Cookie")
		for _, c := range sc {
			if nc, ok := RewriteSetCookie(c, p.opts.StripPrefix); ok {
				h.Add("Set-Cookie", nc)
			}
		}
	}
}

// RewriteLocation maps an upstream redirect target into the proxied URL
// space: absolute URLs pointing at the upstream itself become paths, and
// root-relative paths gain the prefix (unless they already carry it).
// Redirects to other sites are left untouched.
func RewriteLocation(loc, upstreamHost, prefix string) string {
	u, err := url.Parse(loc)
	if err != nil {
		return loc
	}
	if u.IsAbs() || strings.HasPrefix(loc, "//") {
		if !isUpstreamHost(u.Host, upstreamHost) {
			return loc
		}
		u.Scheme, u.Host, u.User = "", "", nil
	}
	if !strings.HasPrefix(u.Path, "/") {
		return u.String() // relative: resolves under the prefix already
	}
	if prefix != "" && u.Path != prefix && !strings.HasPrefix(u.Path, prefix+"/") {
		u.Path = prefix + u.Path
		if u.RawPath != "" {
			u.RawPath = prefix + u.RawPath
		}
	}
	return u.String()
}

func isUpstreamHost(h, upstreamHost string) bool {
	if strings.EqualFold(h, upstreamHost) {
		return true
	}
	host, port, err := net.SplitHostPort(h)
	if err != nil {
		return false
	}
	_, uport, _ := net.SplitHostPort(upstreamHost)
	if uport != "" && port != uport {
		return false
	}
	switch strings.ToLower(strings.Trim(host, "[]")) {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0", "::":
		return true
	}
	return false
}

// RewriteSetCookie drops cookies that impersonate Relay's, removes Domain
// attributes (cookies stay host-only, so a preview can never toss cookies
// onto a parent domain) and prefixes explicit Path attributes. ok is
// false when the cookie must be dropped.
func RewriteSetCookie(sc, prefix string) (string, bool) {
	parts := strings.Split(sc, ";")
	name, _, found := strings.Cut(strings.TrimSpace(parts[0]), "=")
	if !found || strings.TrimSpace(name) == "" || IsRelayCookie(strings.TrimSpace(name)) {
		return "", false
	}
	out := []string{strings.TrimSpace(parts[0])}
	for _, attr := range parts[1:] {
		a := strings.TrimSpace(attr)
		if a == "" {
			continue
		}
		k, v, _ := strings.Cut(a, "=")
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "domain":
			continue
		case "path":
			if prefix != "" {
				v = strings.TrimSpace(v)
				if !strings.HasPrefix(v, "/") {
					v = "/"
				}
				if v != prefix && !strings.HasPrefix(v, prefix+"/") {
					v = prefix + v
				}
				a = "Path=" + v
			}
		}
		out = append(out, a)
	}
	return strings.Join(out, "; "), true
}

// LoopbackTCP returns a Dial function reaching port on the loopback
// interface, trying IPv4 first and then IPv6.
func LoopbackTCP(port int, timeout time.Duration) func(ctx context.Context) (net.Conn, error) {
	addrs := []string{net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), net.JoinHostPort("::1", strconv.Itoa(port))}
	return func(ctx context.Context) (net.Conn, error) {
		d := net.Dialer{Timeout: timeout}
		var first error
		for _, a := range addrs {
			c, err := d.DialContext(ctx, "tcp", a)
			if err == nil {
				return c, nil
			}
			if first == nil {
				first = err
			}
		}
		return nil, first
	}
}

// UnixSocket returns a Dial function reaching a unix socket.
func UnixSocket(path string, timeout time.Duration) func(ctx context.Context) (net.Conn, error) {
	return func(ctx context.Context) (net.Conn, error) {
		d := net.Dialer{Timeout: timeout}
		return d.DialContext(ctx, "unix", path)
	}
}

// IsWebSocket reports whether r is a WebSocket upgrade request.
func IsWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		headerHasToken(r.Header, "Connection", "upgrade")
}

func headerHasToken(h http.Header, key, token string) bool {
	for _, v := range h.Values(key) {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// SafeNext validates a post-login redirect target: it must be a local
// absolute path (no scheme, no host, no "//" or "/\" tricks).
func SafeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	if strings.ContainsAny(next, "\r\n\x00") {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.IsAbs() || u.Host != "" {
		return "/"
	}
	return next
}
