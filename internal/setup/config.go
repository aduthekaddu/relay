package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/aduthekaddu/relay/internal/config"
)

// Access modes.
const (
	AccessTailscale = "tailscale"
	AccessDomain    = "domain"
	AccessSslip     = "sslip"
	AccessProxy     = "proxy"
	AccessLocal     = "local"
)

// AccessModes lists the modes in wizard order.
var AccessModes = []string{AccessTailscale, AccessDomain, AccessSslip, AccessProxy, AccessLocal}

// DefaultLocalListen is the loopback address used behind Tailscale, a
// proxy, or for local-only use.
const DefaultLocalListen = "127.0.0.1:7777"

// Answers are the decisions that shape relay.toml.
type Answers struct {
	Access       string
	Domain       string // domain / sslip
	Email        string // ACME contact (optional)
	Listen       string // override; empty = mode default
	PublicURL    string // tailscale / proxy
	RedirectHTTP bool   // domain / sslip: also serve :80 → https redirects
	User         string
	PasswordHash string
}

// fileConfig is the subset of relay.toml that setup writes. Everything
// else keeps its default and can be added by hand later.
type fileConfig struct {
	Server fileServer `toml:"server"`
	Auth   fileAuth   `toml:"auth"`
}

type fileServer struct {
	Listen         string   `toml:"listen"`
	Domain         string   `toml:"domain,omitempty"`
	TLS            string   `toml:"tls"`
	ACMEEmail      string   `toml:"acme_email,omitempty"`
	PublicURL      string   `toml:"public_url,omitempty"`
	TrustedProxies []string `toml:"trusted_proxies,omitempty"`
	RedirectHTTP   bool     `toml:"redirect_http,omitempty"`
}

type fileAuth struct {
	User         string `toml:"user"`
	PasswordHash string `toml:"password_hash,omitempty"`
}

// Validate checks answers for consistency.
func (a Answers) Validate() error {
	switch a.Access {
	case AccessDomain, AccessSslip:
		if err := ValidDomain(a.Domain); err != nil {
			return err
		}
	case AccessTailscale, AccessProxy, AccessLocal:
	default:
		return fmt.Errorf("unknown access mode %q (use %s)", a.Access, strings.Join(AccessModes, ", "))
	}
	if a.Listen != "" {
		if _, _, err := SplitListen(a.Listen); err != nil {
			return err
		}
	}
	if a.PublicURL != "" && !strings.HasPrefix(a.PublicURL, "https://") && !strings.HasPrefix(a.PublicURL, "http://") {
		return fmt.Errorf("public URL %q must start with https://", a.PublicURL)
	}
	if a.User == "" || strings.ContainsAny(a.User, " \t\n:") || len(a.User) > 64 {
		return fmt.Errorf("invalid username %q", a.User)
	}
	return nil
}

// Render returns the relay.toml contents for a.
func Render(a Answers, now time.Time) ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	fc := fileConfig{Auth: fileAuth{User: a.User, PasswordHash: a.PasswordHash}}
	s := &fc.Server
	switch a.Access {
	case AccessDomain, AccessSslip:
		s.Listen = firstNonEmpty(a.Listen, ":443")
		s.Domain = strings.TrimSuffix(strings.ToLower(a.Domain), ".")
		s.TLS = "auto"
		s.ACMEEmail = a.Email
		// :80 only redirects to HTTPS; certificates use TLS-ALPN on :443,
		// so it is opt-in (a busy :80 would stop the server).
		s.RedirectHTTP = a.RedirectHTTP && strings.HasSuffix(s.Listen, ":443")
	case AccessTailscale, AccessProxy:
		s.Listen = firstNonEmpty(a.Listen, DefaultLocalListen)
		s.TLS = "off"
		s.PublicURL = strings.TrimRight(a.PublicURL, "/")
		s.TrustedProxies = []string{"127.0.0.1/32", "::1/128"}
	case AccessLocal:
		s.Listen = firstNonEmpty(a.Listen, DefaultLocalListen)
		s.TLS = "off"
	}
	body, err := toml.Marshal(fc)
	if err != nil {
		return nil, fmt.Errorf("render relay.toml: %w", err)
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Relay configuration, written by `relay setup` on %s.\n", now.UTC().Format("2006-01-02"))
	fmt.Fprintf(&b, "# Access mode: %s. Every other setting keeps its default; see\n", a.Access)
	b.WriteString("# https://github.com/aduthekaddu/relay for the full reference.\n")
	b.WriteString("# This file holds your password hash: keep it private (mode 0600).\n\n")
	b.Write(body)
	return b.Bytes(), nil
}

// WriteConfig validates data by loading it with the real config loader,
// then writes it atomically with mode 0600 (directory 0700).
func WriteConfig(paths config.Paths, data []byte) (*config.Config, error) {
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(paths.ConfigFile), ".relay.toml-*")
	if err != nil {
		return nil, fmt.Errorf("write relay.toml: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	_, werr := tmp.Write(data)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr, os.Chmod(name, 0o600)); err != nil {
		return nil, fmt.Errorf("write relay.toml: %w", err)
	}
	check := paths
	check.ConfigFile = name
	cfg, err := config.Load(check)
	if err != nil {
		return nil, fmt.Errorf("generated relay.toml does not load: %w", err)
	}
	if err := os.Rename(name, paths.ConfigFile); err != nil {
		return nil, fmt.Errorf("write relay.toml: %w", err)
	}
	return cfg, nil
}

// BackupConfig copies an existing relay.toml to relay.toml.bak-<time>
// (0600) and returns the backup path ("" when there was nothing).
func BackupConfig(path string, now time.Time) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	dst := path + ".bak-" + now.UTC().Format("20060102-150405")
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return "", fmt.Errorf("back up relay.toml: %w", err)
	}
	return dst, nil
}

// SplitListen parses "host:port" / ":port" and returns host and port.
func SplitListen(addr string) (string, int, error) {
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("listen address %q: %w", addr, err)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("listen address %q: invalid port", addr)
	}
	return host, port, nil
}

// IsLoopback reports whether a listen address only accepts local clients.
func IsLoopback(addr string) bool {
	host, _, err := SplitListen(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
