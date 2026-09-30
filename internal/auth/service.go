// Package auth implements sign-in (password, passkeys, TOTP), browser
// sessions, API tokens, rate limiting and the audit log. It is the
// server.Authenticator for every authenticated route.
//
// Single-user: one account row. See docs/dev/AUTH.md for the flows.
package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/secret"
	"github.com/aduthekaddu/relay/internal/server"
)

// TokenPrefix starts every API token.
const TokenPrefix = "rly_"

// Tunables.
const (
	defaultShortTTL  = 12 * time.Hour
	defaultRemember  = 30 * 24 * time.Hour
	touchEvery       = time.Minute     // session last_seen / expiry write throttle
	tokenTouchEvery  = time.Minute     // token last_used write throttle
	cacheTTL         = 5 * time.Second // principal cache (bounded staleness for CLI revocations)
	cacheMax         = 1024
	cookieRefreshAge = 24 * time.Hour // re-issue remember-me cookies at most daily
	maxHashers       = 2              // concurrent argon2 verifications (32 MiB each)
)

// Service is the auth feature.
type Service struct {
	d   *core.Deps
	acc *Accounts
	log *slog.Logger
	now func() time.Time

	rt      *server.Router
	origins func() []string

	loginLim   *limiter
	passkeyLim *limiter
	setupLim   *limiter
	hashSem    chan struct{}
	dummyHash  string

	cache      *principalCache
	ceremonies *ceremonyStore

	setupMu   sync.Mutex
	setupCode string
}

// New migrates the auth tables, imports a password hash written by
// `relay setup` into an empty database, and prepares the service. It starts
// no goroutines.
func New(d *core.Deps) (*Service, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	acc, err := OpenAccounts(ctx, d.Store)
	if err != nil {
		return nil, err
	}
	log := d.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	now := func() time.Time { return time.Now().UTC() }
	dummy, err := secret.HashPassword("relay-timing-equaliser")
	if err != nil {
		return nil, err
	}
	s := &Service{
		d: d, acc: acc, log: log.With("feature", "auth"), now: now,
		loginLim:   newLimiter(defaultLimits, now),
		passkeyLim: newLimiter(defaultLimits, now),
		setupLim:   newLimiter(defaultLimits, now),
		hashSem:    make(chan struct{}, maxHashers),
		dummyHash:  dummy,
		cache:      newPrincipalCache(),
		ceremonies: newCeremonyStore(now),
	}
	s.origins = func() []string { return []string{d.Cfg.Origin()} }
	if err := s.bootstrap(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// bootstrap imports cfg.Auth.PasswordHash (written by `relay setup`) when
// the database has no account, or prepares a one-time setup code.
func (s *Service) bootstrap(ctx context.Context) error {
	has, err := s.acc.HasUser(ctx)
	if err != nil {
		return err
	}
	if has {
		s.removeSetupCodeFile()
		return nil
	}
	if h := strings.TrimSpace(s.d.Cfg.Auth.PasswordHash); h != "" {
		if !strings.HasPrefix(h, "$argon2id$") {
			return errors.New("auth.password_hash in relay.toml is not an argon2id hash; set it with `relay passwd`")
		}
		name := strings.TrimSpace(s.d.Cfg.Auth.User)
		if name == "" {
			name = "admin"
		}
		if err := s.acc.createUser(ctx, name, h); err != nil && !errors.Is(err, ErrUserExists) {
			return err
		}
		_ = s.acc.Record(ctx, api.AuditEntry{Event: "account.import", Actor: name, Detail: "password imported from relay.toml"})
		return nil
	}
	return s.newSetupCode()
}

// newSetupCode creates the one-time code that remote first-run setup must
// present, logs it, and writes it to <data>/setup-code (0600).
func (s *Service) newSetupCode() error {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O/1/I
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	code := string(b[:4]) + "-" + string(b[4:])
	s.setupMu.Lock()
	s.setupCode = code
	s.setupMu.Unlock()
	if dir := s.d.Paths.DataDir; dir != "" {
		if err := os.WriteFile(filepath.Join(dir, "setup-code"), []byte(code+"\n"), 0o600); err != nil {
			s.log.Warn("could not write setup code file", "err", err)
		}
	}
	s.log.Info("no account yet: open Relay in a browser to create it", "url", s.d.Cfg.Origin()+"/login", "setup_code", code)
	return nil
}

func (s *Service) removeSetupCodeFile() {
	if dir := s.d.Paths.DataDir; dir != "" {
		_ = os.Remove(filepath.Join(dir, "setup-code"))
	}
}

// SetOrigins sets the allowed browser origins (App.Origins) used for
// passkey origin checks. Defaults to the canonical origin.
func (s *Service) SetOrigins(f func() []string) {
	if f != nil {
		s.origins = f
	}
}

// Accounts exposes the persistence layer (tests, wiring).
func (s *Service) Accounts() *Accounts { return s.acc }

// Start runs housekeeping (expired sessions, audit pruning, limiter GC) and
// records core.BusAudit events until ctx is done.
func (s *Service) Start(ctx context.Context) error {
	sub := s.d.Bus.Subscribe(256, func(ev api.Event) bool { return ev.Type == core.BusAudit })
	defer sub.Close()
	tick := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	s.housekeep(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-sub.C:
			if !ok {
				return nil
			}
			s.recordBusAudit(ctx, ev)
		case <-tick.C:
			s.housekeep(ctx)
		}
	}
}

func (s *Service) housekeep(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.acc.deleteExpiredSessions(ctx); err != nil && ctx.Err() == nil {
		s.log.Warn("expire sessions", "err", err)
	}
	if err := s.acc.pruneAudit(ctx); err != nil && ctx.Err() == nil {
		s.log.Warn("prune audit log", "err", err)
	}
	s.loginLim.GC()
	s.passkeyLim.GC()
	s.setupLim.GC()
	s.ceremonies.gc()
}

// recordBusAudit stores an audit event published by another feature. It
// accepts core.AuditEvent, api.AuditEntry (values or pointers).
func (s *Service) recordBusAudit(ctx context.Context, ev api.Event) {
	var e api.AuditEntry
	switch v := ev.Data.(type) {
	case core.AuditEvent:
		e = api.AuditEntry{Event: v.Event, Actor: v.Actor, IP: v.IP, Detail: v.Detail}
	case *core.AuditEvent:
		if v == nil {
			return
		}
		e = api.AuditEntry{Event: v.Event, Actor: v.Actor, IP: v.IP, Detail: v.Detail}
	case api.AuditEntry:
		e = v
	case *api.AuditEntry:
		if v == nil {
			return
		}
		e = *v
	default:
		s.log.Debug("ignoring audit event with unknown payload", "type", fmt.Sprintf("%T", ev.Data))
		return
	}
	if strings.TrimSpace(e.Event) == "" {
		return
	}
	e.ID = 0
	e.At = ev.At
	if e.Actor == "" {
		e.Actor = "system"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.acc.Record(ctx, e); err != nil && ctx.Err() == nil {
		s.log.Warn("record audit event", "event", e.Event, "err", err)
	}
}

// --- Authenticator -----------------------------------------------------------

// Identify implements server.Authenticator: Bearer API tokens first, then
// the session cookie. It returns nil for anonymous requests.
func (s *Service) Identify(r *http.Request) *server.Principal {
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, tok, ok := strings.Cut(h, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			return nil
		}
		return s.identifyToken(r.Context(), strings.TrimSpace(tok))
	}
	c, err := r.Cookie(s.cookieName(r, sessionCookie))
	if err != nil || c.Value == "" || len(c.Value) > 128 {
		return nil
	}
	return s.identifySession(r.Context(), c.Value, server.ClientIP(r))
}

func (s *Service) identifyToken(ctx context.Context, tok string) *server.Principal {
	if !strings.HasPrefix(tok, TokenPrefix) || len(tok) > 128 {
		return nil
	}
	hash := secret.HashToken(tok)
	now := s.now()
	if e := s.cache.get(hash, now); e != nil {
		return s.maybeTouchToken(ctx, hash, e, now)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	t, err := s.acc.lookupToken(ctx, hash)
	if err != nil {
		s.log.Warn("token lookup failed", "err", err)
		return nil
	}
	if t == nil {
		return nil
	}
	user, err := s.acc.Username(ctx)
	if err != nil || user == "" {
		return nil
	}
	e := &cacheEntry{p: server.Principal{User: user, Method: "token", TokenID: t.ID}, touched: t.LastUsed}
	s.cache.put(hash, e, now)
	return s.maybeTouchToken(ctx, hash, e, now)
}

func (s *Service) maybeTouchToken(ctx context.Context, hash string, e *cacheEntry, now time.Time) *server.Principal {
	if now.Sub(e.lastTouched()) >= tokenTouchEvery {
		e.setTouched(now)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := s.acc.touchToken(ctx, e.p.TokenID, now); err != nil {
			s.log.Debug("token touch failed", "err", err)
		}
	}
	p := e.p
	return &p
}

func (s *Service) identifySession(ctx context.Context, value, ip string) *server.Principal {
	hash := secret.HashToken(value)
	now := s.now()
	e := s.cache.get(hash, now)
	if e == nil {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		row, err := s.acc.sessionByHash(ctx, hash)
		if err != nil {
			s.log.Warn("session lookup failed", "err", err)
			return nil
		}
		if row == nil {
			return nil
		}
		e = &cacheEntry{
			p:        server.Principal{User: row.Username, SessionID: row.ID, Method: "cookie"},
			touched:  row.LastSeenAt,
			expires:  row.ExpiresAt,
			remember: row.Remember,
		}
		s.cache.put(hash, e, now)
	}
	if !now.Before(e.expiresAt()) {
		s.cache.drop(hash)
		return nil
	}
	if now.Sub(e.lastTouched()) >= touchEvery {
		exp := now.Add(s.ttl(e.remember))
		e.setTouched(now)
		e.setExpires(exp)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := s.acc.touchSession(ctx, e.p.SessionID, now, exp, cleanIP(ip)); err != nil {
			s.log.Debug("session touch failed", "err", err)
		}
	}
	p := e.p
	return &p
}

func (s *Service) ttl(remember bool) time.Duration {
	if remember {
		if d := s.d.Cfg.Auth.SessionTTL.Duration; d > 0 {
			return d
		}
		return defaultRemember
	}
	if d := s.d.Cfg.Auth.ShortTTL.Duration; d > 0 {
		return d
	}
	return defaultShortTTL
}

// revoked ends sessions everywhere: cache, and long-lived connections via
// core.BusSessionRevoked.
func (s *Service) revoked(ids ...string) {
	if len(ids) == 0 {
		return
	}
	s.cache.dropSessions(ids)
	if s.d.Bus != nil {
		s.d.Bus.Publish(core.BusSessionRevoked, core.SessionRevoked{SessionIDs: ids})
	}
}

// --- principal cache -----------------------------------------------------------

type cacheEntry struct {
	p        server.Principal
	remember bool
	mu       sync.Mutex
	touched  time.Time
	expires  time.Time // zero for tokens
	cachedAt time.Time
}

func (e *cacheEntry) lastTouched() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.touched }
func (e *cacheEntry) setTouched(t time.Time) { e.mu.Lock(); e.touched = t; e.mu.Unlock() }
func (e *cacheEntry) setExpires(t time.Time) { e.mu.Lock(); e.expires = t; e.mu.Unlock() }
func (e *cacheEntry) expiresAt() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.expires.IsZero() {
		return time.Unix(1<<40, 0)
	}
	return e.expires
}

type principalCache struct {
	mu sync.Mutex
	m  map[string]*cacheEntry
}

func newPrincipalCache() *principalCache { return &principalCache{m: map[string]*cacheEntry{}} }

func (c *principalCache) get(hash string, now time.Time) *cacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.m[hash]
	if e == nil {
		return nil
	}
	if now.Sub(e.cachedAt) > cacheTTL {
		delete(c.m, hash)
		return nil
	}
	return e
}

func (c *principalCache) put(hash string, e *cacheEntry, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= cacheMax {
		clear(c.m)
	}
	e.cachedAt = now
	c.m[hash] = e
}

func (c *principalCache) drop(hash string) {
	c.mu.Lock()
	delete(c.m, hash)
	c.mu.Unlock()
}

func (c *principalCache) dropSessions(ids []string) {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for h, e := range c.m {
		if e.p.SessionID != "" && set[e.p.SessionID] {
			delete(c.m, h)
		}
	}
}

func (c *principalCache) dropToken(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for h, e := range c.m {
		if e.p.TokenID == id {
			delete(c.m, h)
		}
	}
}

// --- request helpers -------------------------------------------------------------

// isLoopbackHost reports whether host (from r.Host, maybe with port) names
// this machine.
func isLoopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isDirectLocal reports whether r comes straight from this machine: the
// control socket, or a loopback peer addressing a loopback host with no
// proxy headers (tunnels such as cloudflared connect from loopback too, but
// they carry the public Host and forwarding headers).
func isDirectLocal(r *http.Request) bool {
	if server.IsLocal(r.Context()) {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || !isLoopbackHost(r.Host) {
		return false
	}
	for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded", "X-Real-Ip", "Cf-Connecting-Ip", "Tailscale-User-Login"} {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	return true
}
