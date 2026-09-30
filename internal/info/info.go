// Package info serves /api/v1/health, /api/v1/info and the server-side
// settings (/api/v1/settings) that map onto relay.toml.
package info

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
)

// probeTTL bounds how often binary presence is re-checked.
const probeTTL = time.Minute

// Service is the info feature.
type Service struct {
	d   *core.Deps
	log *slog.Logger

	// lookPath finds executables (replaceable in tests).
	lookPath func(name string) (string, error)

	mu      sync.Mutex
	probed  time.Time
	bins    map[string]bool
	osUser  string
	host    string
	cfgLock sync.Mutex // serialises settings writes
}

// New constructs the service. It starts no goroutines.
func New(d *core.Deps) (*Service, error) {
	log := d.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	s := &Service{d: d, log: log.With("feature", "info"), lookPath: exec.LookPath}
	if u, err := user.Current(); err == nil {
		s.osUser = u.Username
	}
	s.host, _ = os.Hostname()
	return s, nil
}

// Routes registers the info endpoints.
func (s *Service) Routes(rt *server.Router) {
	rt.Public("GET /api/v1/health", s.handleHealth)
	rt.Handle("GET /api/v1/info", s.handleInfo)
	rt.Handle("GET /api/v1/settings", s.handleGetSettings)
	rt.Handle("PATCH /api/v1/settings", s.handlePatchSettings)
}

type health struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
}

func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, health{OK: true, Version: s.d.Version})
}

func (s *Service) handleInfo(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, s.Info(r.Context()))
}

// Info describes this installation (also the payload of the live "hello").
func (s *Service) Info(ctx context.Context) api.Info {
	cfg := s.d.Cfg
	origin := cfg.Origin()
	info := api.Info{
		Version:   s.d.Version,
		Commit:    s.d.Commit,
		Hostname:  s.host,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		User:      s.osUser,
		Home:      s.d.Paths.Home,
		StartedAt: s.d.StartedAt,
		PublicURL: origin,
	}
	bins := s.binaries()
	f := &info.Features
	f.Passkeys = passkeysUsable(origin)
	f.Push = s.pushConfigured(ctx)
	f.Desktop = cfg.Desktop.Enabled && bins["Xvnc"]
	f.Code = cfg.Code.Enabled && (bins["code-server"] || bins["openvscode-server"] || bins["custom-code"])
	f.PreviewsMode, f.PreviewsHost = previewsMode(cfg.Previews.Mode, cfg.Previews.Host, origin)
	f.Recording = cfg.Terminal.Record != "" && cfg.Terminal.Record != "off"
	f.Ripgrep = bins["rg"]
	return info
}

// passkeysUsable: WebAuthn needs a secure context with a host name —
// https://<name> or http(s)://localhost. IP addresses can't be RP ids.
func passkeysUsable(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "" || net.ParseIP(h) != nil {
		return false
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	return u.Scheme == "https"
}

// previewsMode resolves "auto": subdomain previews need a base host and an
// HTTPS origin (each <port>.<host> gets its own certificate); otherwise
// path mode (/p/<port>/).
func previewsMode(mode, host, origin string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "off":
		return "off", ""
	case "path":
		return "path", ""
	case "subdomain":
		return "subdomain", host
	}
	if host != "" && strings.HasPrefix(origin, "https://") {
		return "subdomain", host
	}
	return "path", ""
}

// pushConfigured reports whether the notify feature generated VAPID keys
// (read-only check of the store KV).
func (s *Service) pushConfigured(ctx context.Context) bool {
	if s.d.Store == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	v, ok, err := s.d.Store.GetKV(ctx, "notify.vapid.public")
	return err == nil && ok && v != ""
}

// binaries reports which optional tools are installed, cached for probeTTL.
func (s *Service) binaries() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bins != nil && time.Since(s.probed) < probeTTL {
		return s.bins
	}
	out := map[string]bool{}
	for _, name := range []string{"Xvnc", "code-server", "openvscode-server", "rg"} {
		out[name] = s.findBinary(name)
	}
	if b := strings.TrimSpace(s.d.Cfg.Code.Binary); b != "" {
		out["custom-code"] = s.findBinary(b)
	}
	s.bins, s.probed = out, time.Now()
	return out
}

// findBinary looks in PATH, then ~/.local/bin (where the installer puts
// optional components, often not on a service's PATH).
func (s *Service) findBinary(name string) bool {
	if strings.ContainsRune(name, filepath.Separator) {
		return isExecutable(expandHome(name, s.d.Paths.Home))
	}
	if _, err := s.lookPath(name); err == nil {
		return true
	}
	if home := s.d.Paths.Home; home != "" {
		return isExecutable(filepath.Join(home, ".local", "bin", name))
	}
	return false
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
