package server

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/httpx"
)

// requestInfo is computed once per request by WithRequestInfo.
type requestInfo struct {
	secure   bool
	clientIP string
	origin   string
	originOK bool
}

const requestInfoKey ctxKey = 100

// TrustedProxies returns a predicate for peers whose X-Forwarded-* headers
// are believed: the CIDRs (or bare IPs) in server.trusted_proxies. When the
// list is empty but server.public_url is set, Relay is assumed to sit behind
// a local proxy or tunnel (Tailscale serve, cloudflared, Caddy) and loopback
// peers are trusted. Invalid entries are ignored.
func TrustedProxies(cfg *config.Config) func(net.IP) bool {
	var nets []*net.IPNet
	for _, s := range cfg.Server.TrustedProxies {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			if ip := net.ParseIP(s); ip != nil {
				bits := 128
				if ip.To4() != nil {
					bits = 32
					ip = ip.To4()
				}
				nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			}
			continue
		}
		if _, n, err := net.ParseCIDR(s); err == nil {
			nets = append(nets, n)
		}
	}
	loopback := len(nets) == 0 && cfg.Server.PublicURL != ""
	return func(ip net.IP) bool {
		if ip == nil {
			return false
		}
		if loopback && ip.IsLoopback() {
			return true
		}
		for _, n := range nets {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
}

// WithRequestInfo annotates each request with its client IP and whether it
// reached Relay over HTTPS (directly or via a trusted proxy), so handlers
// can use ClientIP and IsSecureRequest without re-deriving them.
func WithRequestInfo(trusted func(net.IP) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := &requestInfo{secure: secureFrom(r, trusted)}
		host := r.Host
		if trustedPeer(r, trusted) {
			if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
				host = strings.TrimSpace(strings.SplitN(forwarded, ",", 2)[0])
			}
		}
		info.origin, info.originOK = requestOrigin(host, info.secure)
		if IsLocal(r.Context()) {
			info.clientIP = "local"
		} else {
			info.clientIP = httpx.ClientIP(r, trusted)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestInfoKey, info)))
	})
}

func secureFrom(r *http.Request, trusted func(net.IP) bool) bool {
	if r.TLS != nil {
		return true
	}
	if !trustedPeer(r, trusted) {
		return false
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i] // first hop is the client-facing one
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

func trustedPeer(r *http.Request, trusted func(net.IP) bool) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return trusted != nil && trusted(net.ParseIP(host))
}

func requestOrigin(host string, secure bool) (string, bool) {
	scheme := "http"
	if secure {
		scheme = "https"
	}
	return NormalizeOrigin(scheme + "://" + host)
}

// ExternalOrigin returns the accessed origin, using forwarded host/protocol
// only within WithRequestInfo's trusted-proxy boundary. Host is still client
// input: callers must also compare the result to configured allowed origins.
// Without the middleware, only the request Host and direct TLS are used.
func ExternalOrigin(r *http.Request) (string, bool) {
	if info, ok := r.Context().Value(requestInfoKey).(*requestInfo); ok {
		return info.origin, info.originOK
	}
	return requestOrigin(r.Host, r.TLS != nil)
}

// IsSecureRequest reports whether r arrived over HTTPS, either directly or
// through a trusted proxy that set X-Forwarded-Proto: https.
func IsSecureRequest(r *http.Request) bool {
	if info, ok := r.Context().Value(requestInfoKey).(*requestInfo); ok {
		return info.secure
	}
	return r.TLS != nil
}

// ClientIP returns the caller's IP address, honouring X-Forwarded-For only
// from trusted proxies. Requests on the control socket report "local".
func ClientIP(r *http.Request) string {
	if info, ok := r.Context().Value(requestInfoKey).(*requestInfo); ok {
		return info.clientIP
	}
	return httpx.ClientIP(r, nil)
}

// Body caps applied before any handler runs. Feature handlers still apply
// their own, tighter limits (httpx.Decode caps JSON at 1 MiB).
const (
	// MaxAuthBody caps every request under /api/v1/auth/.
	MaxAuthBody = 64 << 10
	// MaxJSONRequest is the safety net for any JSON request body (large
	// enough for a 5 MiB text file saved as JSON).
	MaxJSONRequest = 16 << 20
)

// LimitBodies caps request bodies: auth routes at MaxAuthBody, JSON bodies
// at MaxJSONRequest. Raw streams (uploads, proxies) are left to their
// handlers.
func LimitBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			switch {
			case strings.HasPrefix(r.URL.Path, "/api/v1/auth/"):
				r.Body = http.MaxBytesReader(w, r.Body, MaxAuthBody)
			case strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json"):
				r.Body = http.MaxBytesReader(w, r.Body, MaxJSONRequest)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequestLog logs method, path, status and duration at debug level. Query
// strings are dropped for auth routes and bodies are never logged. It is a
// no-op wrapper when debug logging is disabled.
func RequestLog(log *slog.Logger, next http.Handler) http.Handler {
	if log == nil || !log.Enabled(context.Background(), slog.LevelDebug) {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		path := r.URL.Path
		if q := r.URL.RawQuery; q != "" && !strings.HasPrefix(path, "/api/v1/auth/") && !hasSecretQuery(r) {
			path += "?" + q
		}
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		if sw.hijacked {
			status = http.StatusSwitchingProtocols
		}
		log.Debug("http", "method", r.Method, "path", path, "status", status,
			"dur", time.Since(start).Round(time.Microsecond).String(), "bytes", sw.bytes)
	})
}

// hasSecretQuery reports query parameters that may carry credentials.
func hasSecretQuery(r *http.Request) bool {
	q := r.URL.Query()
	for _, k := range []string{"token", "access_token", "code", "secret", "password", "key"} {
		if q.Has(k) {
			return true
		}
	}
	return false
}

// statusWriter records the status code and body size while keeping
// Flusher/Hijacker (SSE, WebSockets) reachable via Unwrap and direct
// implementations.
type statusWriter struct {
	http.ResponseWriter
	status   int
	bytes    int64
	hijacked bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush implements http.Flusher when the underlying writer does.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker when the underlying writer does.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack not supported")
	}
	c, rw, err := h.Hijack()
	if err == nil {
		w.hijacked = true
	}
	return c, rw, err
}
