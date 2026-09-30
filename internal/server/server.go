package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/aduthekaddu/relay/internal/config"
)

// Server runs the public listener (HTTP or HTTPS) and the owner-only
// control socket used by the `relay` CLI from inside terminals.
type Server struct {
	cfg     *config.Config
	paths   config.Paths
	log     *slog.Logger
	handler http.Handler
	// HostPolicy extends the autocert host whitelist (preview subdomains).
	HostPolicy func(host string) bool
}

func New(cfg *config.Config, paths config.Paths, log *slog.Logger, h http.Handler) *Server {
	return &Server{cfg: cfg, paths: paths, log: log, handler: h}
}

// Run blocks until ctx is cancelled or a listener fails.
func (s *Server) Run(ctx context.Context) error {
	h := SecurityHeaders(s.cfg, Recover(s.log, s.handler))
	h = RequestLog(s.log, WithRequestInfo(TrustedProxies(s.cfg), LimitBodies(h)))
	errc := make(chan error, 3)
	var servers []*http.Server

	// Control socket (always on): same routes, principal = local.
	ctl, err := s.listenControl()
	if err != nil {
		s.log.Warn("control socket unavailable", "err", err)
	} else {
		cs := &http.Server{
			Handler:           h,
			ReadHeaderTimeout: 10 * time.Second,
			ConnContext: func(c context.Context, conn net.Conn) context.Context {
				if peerIsSelf(conn) {
					return MarkLocal(c)
				}
				return c
			},
		}
		servers = append(servers, cs)
		go func() { errc <- ignoreClosed(cs.Serve(ctl)) }()
	}

	main := &http.Server{
		Addr:              s.cfg.Server.Listen,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelDebug),
	}
	servers = append(servers, main)

	switch {
	case s.cfg.UseAutoTLS():
		m := &autocert.Manager{
			Prompt: autocert.AcceptTOS,
			Cache:  autocert.DirCache(s.paths.DataDir + "/acme"),
			Email:  s.cfg.Server.ACMEEmail,
			HostPolicy: func(_ context.Context, host string) error {
				if strings.EqualFold(host, s.cfg.Server.Domain) {
					return nil
				}
				if s.HostPolicy != nil && s.HostPolicy(host) {
					return nil
				}
				return fmt.Errorf("host %q not allowed", host)
			},
		}
		main.TLSConfig = m.TLSConfig()
		main.TLSConfig.MinVersion = tls.VersionTLS12
		if s.cfg.Server.RedirectHTTP {
			rs := &http.Server{Addr: ":80", Handler: m.HTTPHandler(nil), ReadHeaderTimeout: 10 * time.Second}
			servers = append(servers, rs)
			go func() { errc <- ignoreClosed(rs.ListenAndServe()) }()
		}
		s.log.Info("relay listening (automatic HTTPS)", "addr", main.Addr, "domain", s.cfg.Server.Domain)
		go func() { errc <- ignoreClosed(main.ListenAndServeTLS("", "")) }()
	case s.cfg.Server.TLS == "manual":
		main.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		s.log.Info("relay listening (HTTPS)", "addr", main.Addr)
		go func() {
			errc <- ignoreClosed(main.ListenAndServeTLS(config.Expand(s.cfg.Server.CertFile), config.Expand(s.cfg.Server.KeyFile)))
		}()
	default:
		s.log.Info("relay listening (HTTP)", "addr", main.Addr, "origin", s.cfg.Origin())
		go func() { errc <- ignoreClosed(main.ListenAndServe()) }()
	}

	select {
	case <-ctx.Done():
	case err := <-errc:
		if err != nil {
			shutdown(servers)
			return err
		}
	}
	shutdown(servers)
	return nil
}

func shutdown(servers []*http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(ctx)
	}
}

func ignoreClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (s *Server) listenControl() (net.Listener, error) {
	path := s.paths.CtlSocket
	if err := os.MkdirAll(s.paths.RuntimeDir, 0o700); err != nil {
		return nil, err
	}
	_ = os.Remove(path)
	old := syscall.Umask(0o177)
	l, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return l, nil
}

// SecurityHeaders applies the default hardening headers. Handlers that
// serve untrusted content (file previews, preview proxy) override CSP.
func SecurityHeaders(cfg *config.Config, next http.Handler) http.Handler {
	secure := cfg.UseAutoTLS() || cfg.Server.TLS == "manual" || strings.HasPrefix(cfg.Server.PublicURL, "https://")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), geolocation=(), payment=(), usb=(), microphone=(self), clipboard-read=(self), clipboard-write=(self)")
		if secure || IsSecureRequest(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		if h.Get("Content-Security-Policy") == "" {
			h.Set("Content-Security-Policy", DefaultCSP)
		}
		next.ServeHTTP(w, r)
	})
}

// DefaultCSP for the app shell. xterm.js and CodeMirror inject style
// elements, so style-src needs 'unsafe-inline'; scripts never do.
const DefaultCSP = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:; connect-src 'self' ws: wss:; worker-src 'self' blob:; frame-src 'self'; frame-ancestors 'self'; base-uri 'none'; form-action 'self'; object-src 'none'"

// Recover turns handler panics into 500s instead of killing the process.
func Recover(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Error("panic in handler", "path", r.URL.Path, "panic", v)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
