// Package config loads relay.toml, applies defaults and resolves the
// directories Relay uses on disk.
//
// Layout (XDG, overridable with RELAY_HOME which puts everything under one
// directory — handy for development and tests):
//
//	config   ~/.config/relay/relay.toml          (0600)
//	data     ~/.local/share/relay/               relay.db, recordings, uploads, keys
//	runtime  $XDG_RUNTIME_DIR/relay/             ptyd.sock, relay.sock, vnc.sock (0700)
//	cache    ~/.cache/relay/
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Config mirrors relay.toml. Every field has a sensible default so an empty
// file (or no file) is a valid configuration for local use.
type Config struct {
	Server    ServerConfig    `toml:"server"`
	Auth      AuthConfig      `toml:"auth"`
	Terminal  TerminalConfig  `toml:"terminal"`
	Agents    AgentsConfig    `toml:"agents"`
	Files     FilesConfig     `toml:"files"`
	Previews  PreviewsConfig  `toml:"previews"`
	Desktop   DesktopConfig   `toml:"desktop"`
	Code      CodeConfig      `toml:"code"`
	Apps      []AppConfig     `toml:"apps"`
	Notify    NotifyConfig    `toml:"notify"`
	Usage     UsageConfig     `toml:"usage"`
	Schedules SchedulesConfig `toml:"schedules"`
}

type ServerConfig struct {
	// Listen is the address for the web UI. ":443" with Domain set enables
	// automatic HTTPS. Default 127.0.0.1:7777 (use behind a tunnel/proxy).
	Listen string `toml:"listen"`
	// Domain is the public hostname, e.g. "dev.example.com". Required for
	// automatic HTTPS, passkeys and subdomain previews.
	Domain string `toml:"domain"`
	// TLS: "auto" (Let's Encrypt when Domain is set and Listen is :443),
	// "off" (plain HTTP), or "manual" (CertFile/KeyFile).
	TLS       string `toml:"tls"`
	CertFile  string `toml:"cert_file"`
	KeyFile   string `toml:"key_file"`
	ACMEEmail string `toml:"acme_email"`
	// PublicURL overrides the external origin when Relay sits behind a
	// proxy that terminates TLS, e.g. "https://relay.example.ts.net".
	PublicURL string `toml:"public_url"`
	// TrustedProxies lists CIDRs whose X-Forwarded-For is believed.
	TrustedProxies []string `toml:"trusted_proxies"`
	// RedirectHTTP listens on :80 and redirects to HTTPS (auto TLS only).
	RedirectHTTP bool `toml:"redirect_http"`
}

type AuthConfig struct {
	User         string   `toml:"user"`
	PasswordHash string   `toml:"password_hash"` // argon2id PHC string; set with `relay passwd`
	SessionTTL   Duration `toml:"session_ttl"`   // "remember me" sessions
	ShortTTL     Duration `toml:"short_ttl"`     // default sessions
	// InsecureCookies drops the Secure flag. Only for plain-HTTP LAN testing.
	InsecureCookies bool `toml:"insecure_cookies"`
}

type TerminalConfig struct {
	Shell        string `toml:"shell"`         // default $SHELL, then /bin/bash
	DefaultCwd   string `toml:"default_cwd"`   // default ~
	ScrollbackKB int    `toml:"scrollback_kb"` // replay buffer per session
	Record       string `toml:"record"`        // off | agents | all
	RecordDays   int    `toml:"record_days"`   // retention
	UploadMaxMB  int    `toml:"upload_max_mb"`
	ImportTmux   bool   `toml:"import_tmux"` // show existing tmux sessions
}

type AgentsConfig struct {
	WorkspaceRoots []string `toml:"workspace_roots"` // dirs scanned for git repos
	Hooks          bool     `toml:"hooks"`           // offer to install attention hooks
	IndexHistory   bool     `toml:"index_history"`   // full-text index transcripts
	Disabled       []string `toml:"disabled"`        // adapter ids to ignore
	IdleSeconds    int      `toml:"idle_seconds"`    // quiet time before "waiting" heuristic
}

type FilesConfig struct {
	Root       string `toml:"root"` // restrict browsing; default home
	ShowHidden bool   `toml:"show_hidden"`
	UseTrash   bool   `toml:"use_trash"`
}

type PreviewsConfig struct {
	Mode    string `toml:"mode"` // auto | subdomain | path | off
	Host    string `toml:"host"` // base host for <port>.<host>; default Server.Domain
	PortMin int    `toml:"port_min"`
	PortMax int    `toml:"port_max"`
	Ignore  []int  `toml:"ignore"`
}

type DesktopConfig struct {
	Enabled  bool               `toml:"enabled"`
	Display  string             `toml:"display"`  // ":7"
	Geometry string             `toml:"geometry"` // "1600x1000"
	IdleStop Duration           `toml:"idle_stop"`
	Apps     []DesktopAppConfig `toml:"apps"`
}

type DesktopAppConfig struct {
	ID      string   `toml:"id"`
	Name    string   `toml:"name"`
	Command []string `toml:"command"`
	Icon    string   `toml:"icon"`
}

type CodeConfig struct {
	Enabled  bool     `toml:"enabled"`
	Binary   string   `toml:"binary"` // code-server or openvscode-server; auto-detected
	IdleStop Duration `toml:"idle_stop"`
}

type AppConfig struct {
	ID          string            `toml:"id"`
	Name        string            `toml:"name"`
	Description string            `toml:"description"`
	Command     []string          `toml:"command"`
	Port        int               `toml:"port"`
	Socket      string            `toml:"socket"`
	Env         map[string]string `toml:"env"`
	Cwd         string            `toml:"cwd"`
	IdleStop    Duration          `toml:"idle_stop"`
}

type NotifyConfig struct {
	NtfyURL    string `toml:"ntfy_url"`
	WebhookURL string `toml:"webhook_url"`
	QuietStart string `toml:"quiet_start"`
	QuietEnd   string `toml:"quiet_end"`
}

type UsageConfig struct {
	ClaudeQuota bool   `toml:"claude_quota"` // read plan usage with Claude Code's OAuth token
	PricesURL   string `toml:"prices_url"`   // optional refresh source for model prices
}

type SchedulesConfig struct {
	Enabled bool `toml:"enabled"`
}

// Duration is a time.Duration that (un)marshals as "90s", "12h", "30d".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" {
		d.Duration = 0
		return nil
	}
	if strings.HasSuffix(s, "d") {
		var n float64
		if _, err := fmt.Sscanf(strings.TrimSuffix(s, "d"), "%g", &n); err != nil {
			return fmt.Errorf("invalid duration %q", s)
		}
		d.Duration = time.Duration(n * float64(24*time.Hour))
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q", s)
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) {
	if d.Duration%(24*time.Hour) == 0 && d.Duration > 0 {
		return []byte(fmt.Sprintf("%dd", d.Duration/(24*time.Hour))), nil
	}
	return []byte(d.Duration.String()), nil
}

// Defaults returns a Config with every default applied.
func Defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Listen: "127.0.0.1:7777",
			TLS:    "auto",
		},
		Auth: AuthConfig{
			User:       "admin",
			SessionTTL: Duration{30 * 24 * time.Hour},
			ShortTTL:   Duration{12 * time.Hour},
		},
		Terminal: TerminalConfig{
			DefaultCwd:   "~",
			ScrollbackKB: 2048,
			Record:       "agents",
			RecordDays:   14,
			UploadMaxMB:  512,
			ImportTmux:   true,
		},
		Agents: AgentsConfig{
			WorkspaceRoots: []string{"~/code", "~/src", "~/projects", "~/project", "~/work", "~/dev"},
			Hooks:          true,
			IndexHistory:   true,
			IdleSeconds:    8,
		},
		Files: FilesConfig{
			Root:     "~",
			UseTrash: true,
		},
		Previews: PreviewsConfig{
			Mode:    "auto",
			PortMin: 1024,
			PortMax: 65535,
		},
		Desktop: DesktopConfig{
			Enabled:  true,
			Display:  ":7",
			Geometry: "1600x1000",
			IdleStop: Duration{2 * time.Hour},
		},
		Code: CodeConfig{
			Enabled:  true,
			IdleStop: Duration{2 * time.Hour},
		},
		Schedules: SchedulesConfig{Enabled: true},
	}
}

// Paths are the on-disk locations Relay uses.
type Paths struct {
	Home       string // user's home directory
	ConfigDir  string
	ConfigFile string
	DataDir    string
	RuntimeDir string
	CacheDir   string
	DB         string // DataDir/relay.db
	PtydSocket string // RuntimeDir/ptyd.sock
	CtlSocket  string // RuntimeDir/relay.sock (local control API for the CLI)
	Uploads    string // DataDir/uploads
	Recordings string // DataDir/recordings
}

// ResolvePaths computes Paths from the environment.
func ResolvePaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	var p Paths
	p.Home = home
	if rh := os.Getenv("RELAY_HOME"); rh != "" {
		rh = expand(rh, home)
		p.ConfigDir = filepath.Join(rh, "config")
		p.DataDir = filepath.Join(rh, "data")
		p.RuntimeDir = filepath.Join(rh, "run")
		p.CacheDir = filepath.Join(rh, "cache")
	} else {
		p.ConfigDir = envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		p.ConfigDir = filepath.Join(p.ConfigDir, "relay")
		p.DataDir = filepath.Join(envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share")), "relay")
		p.CacheDir = filepath.Join(envOr("XDG_CACHE_HOME", filepath.Join(home, ".cache")), "relay")
		rt := os.Getenv("XDG_RUNTIME_DIR")
		if rt == "" {
			rt = filepath.Join(os.TempDir(), fmt.Sprintf("relay-%d", os.Getuid()))
		} else {
			rt = filepath.Join(rt, "relay")
		}
		p.RuntimeDir = rt
	}
	p.ConfigFile = filepath.Join(p.ConfigDir, "relay.toml")
	if cf := os.Getenv("RELAY_CONFIG"); cf != "" {
		p.ConfigFile = expand(cf, home)
	}
	p.DB = filepath.Join(p.DataDir, "relay.db")
	p.PtydSocket = filepath.Join(p.RuntimeDir, "ptyd.sock")
	p.CtlSocket = filepath.Join(p.RuntimeDir, "relay.sock")
	p.Uploads = filepath.Join(p.DataDir, "uploads")
	p.Recordings = filepath.Join(p.DataDir, "recordings")
	return p, nil
}

// Ensure creates the directories with private permissions.
func (p Paths) Ensure() error {
	for _, d := range []string{p.ConfigDir, p.DataDir, p.CacheDir, p.Uploads, p.Recordings} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(p.RuntimeDir, 0o700); err != nil {
		return err
	}
	return os.Chmod(p.RuntimeDir, 0o700)
}

// Load reads the config file (missing file is fine) and applies defaults.
func Load(p Paths) (*Config, error) {
	cfg := Defaults()
	b, err := os.ReadFile(p.ConfigFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := toml.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("%s: %w", p.ConfigFile, err)
		}
	}
	applyEnv(cfg)
	cfg.normalize(p.Home)
	return cfg, nil
}

// Save writes the config atomically with mode 0600.
func Save(p Paths, cfg *Config) error {
	b, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.ConfigFile), 0o700); err != nil {
		return err
	}
	tmp := p.ConfigFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.ConfigFile)
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("RELAY_LISTEN"); v != "" {
		cfg.Server.Listen = v
	}
	if v := os.Getenv("RELAY_DOMAIN"); v != "" {
		cfg.Server.Domain = v
	}
	if v := os.Getenv("RELAY_PUBLIC_URL"); v != "" {
		cfg.Server.PublicURL = v
	}
	if v := os.Getenv("RELAY_TLS"); v != "" {
		cfg.Server.TLS = v
	}
}

func (c *Config) normalize(home string) {
	c.Server.TLS = strings.ToLower(strings.TrimSpace(c.Server.TLS))
	if c.Server.TLS == "" {
		c.Server.TLS = "auto"
	}
	c.Terminal.DefaultCwd = expand(c.Terminal.DefaultCwd, home)
	c.Files.Root = expand(c.Files.Root, home)
	for i, r := range c.Agents.WorkspaceRoots {
		c.Agents.WorkspaceRoots[i] = expand(r, home)
	}
	if c.Terminal.ScrollbackKB <= 0 {
		c.Terminal.ScrollbackKB = 2048
	}
	if c.Previews.PortMin <= 0 {
		c.Previews.PortMin = 1024
	}
	if c.Previews.PortMax <= 0 || c.Previews.PortMax > 65535 {
		c.Previews.PortMax = 65535
	}
	if c.Previews.Host == "" {
		c.Previews.Host = c.Server.Domain
	}
}

// UseAutoTLS reports whether Relay should obtain certificates itself.
func (c *Config) UseAutoTLS() bool {
	if c.Server.TLS != "auto" || c.Server.Domain == "" {
		return false
	}
	return strings.HasSuffix(c.Server.Listen, ":443")
}

// Origin returns the canonical external origin (scheme://host[:port]).
func (c *Config) Origin() string {
	if c.Server.PublicURL != "" {
		return strings.TrimRight(c.Server.PublicURL, "/")
	}
	if c.Server.Domain != "" {
		if c.UseAutoTLS() || c.Server.TLS == "manual" {
			return "https://" + c.Server.Domain
		}
	}
	host := c.Server.Listen
	if strings.HasPrefix(host, ":") {
		host = "localhost" + host
	}
	host = strings.Replace(host, "0.0.0.0", "localhost", 1)
	scheme := "http"
	if c.Server.TLS == "manual" {
		scheme = "https"
	}
	return scheme + "://" + host
}

// Expand replaces a leading "~" with the home directory.
func Expand(path string) string {
	home, _ := os.UserHomeDir()
	return expand(path, home)
}

func expand(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
