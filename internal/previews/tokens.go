package previews

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
)

// Token kinds. A handshake token rides in a URL from the Relay origin to
// the preview origin (short-lived, single use); a cookie token is the
// value of the host-only relay_preview cookie on <port>.<host>.
const (
	kindHandshake = "h"
	kindCookie    = "c"

	handshakeTTL = 60 * time.Second
	cookieTTL    = 12 * time.Hour

	hmacKeyKV = "previews.hmac_key"
)

// Token validation errors.
var (
	errTokenMalformed = errors.New("malformed token")
	errTokenSignature = errors.New("bad token signature")
	errTokenExpired   = errors.New("token expired")
	errTokenPort      = errors.New("token is for another port")
	errTokenKind      = errors.New("wrong token kind")
	errTokenReplayed  = errors.New("token already used")
)

// claims are the signed contents of a token.
type claims struct {
	Kind    string
	Port    int
	Session string // hashed reference to the Relay session
	Expires time.Time
	Nonce   string
}

// tokens issues and verifies HMAC-SHA256 preview tokens.
type tokens struct {
	key []byte
	now func() time.Time

	mu   sync.Mutex
	used map[string]time.Time // handshake nonce → expiry (replay guard)
}

// loadTokens reads (or creates) the HMAC key from the store's KV table.
func loadTokens(ctx context.Context, st *store.Store) (*tokens, error) {
	v, ok, err := st.GetKV(ctx, hmacKeyKV)
	if err != nil {
		return nil, fmt.Errorf("read preview key: %w", err)
	}
	var key []byte
	if ok {
		key, err = base64.RawStdEncoding.DecodeString(v)
		if err != nil || len(key) < 32 {
			ok = false
		}
	}
	if !ok {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate preview key: %w", err)
		}
		if err := st.SetKV(ctx, hmacKeyKV, base64.RawStdEncoding.EncodeToString(key)); err != nil {
			return nil, fmt.Errorf("store preview key: %w", err)
		}
	}
	return newTokens(key), nil
}

func newTokens(key []byte) *tokens {
	return &tokens{key: key, now: time.Now, used: map[string]time.Time{}}
}

// sessionRef derives an opaque, non-reversible reference to the caller's
// Relay session so a preview cookie is bound to it without revealing it.
func sessionRef(p *server.Principal) string {
	id := p.SessionID
	if id == "" {
		id = p.TokenID
	}
	if id == "" {
		id = p.User
	}
	sum := sha256.Sum256([]byte("relay-preview-session|" + p.Method + "|" + id))
	return hex.EncodeToString(sum[:16])
}

// issue signs a token of kind for port and session, valid for ttl.
func (t *tokens) issue(kind string, port int, session string, ttl time.Duration) (string, error) {
	nb := make([]byte, 12)
	if _, err := rand.Read(nb); err != nil {
		return "", err
	}
	exp := t.now().Add(ttl).Unix()
	payload := strings.Join([]string{"v1", kind, strconv.Itoa(port), session, strconv.FormatInt(exp, 10), hex.EncodeToString(nb)}, "|")
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(t.mac(payload)), nil
}

func (t *tokens) mac(payload string) []byte {
	m := hmac.New(sha256.New, t.key)
	m.Write([]byte("relay-preview|"))
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// verify checks signature, kind, port and expiry. Handshake tokens are
// single use: a second verify of the same token fails.
func (t *tokens) verify(tok, kind string, port int) (claims, error) {
	p64, s64, ok := strings.Cut(tok, ".")
	if !ok || len(tok) > 512 {
		return claims{}, errTokenMalformed
	}
	payload, err := base64.RawURLEncoding.DecodeString(p64)
	if err != nil {
		return claims{}, errTokenMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(s64)
	if err != nil {
		return claims{}, errTokenMalformed
	}
	if !hmac.Equal(sig, t.mac(string(payload))) {
		return claims{}, errTokenSignature
	}
	f := strings.Split(string(payload), "|")
	if len(f) != 6 || f[0] != "v1" {
		return claims{}, errTokenMalformed
	}
	c := claims{Kind: f[1], Session: f[3], Nonce: f[5]}
	if c.Port, err = strconv.Atoi(f[2]); err != nil {
		return claims{}, errTokenMalformed
	}
	exp, err := strconv.ParseInt(f[4], 10, 64)
	if err != nil {
		return claims{}, errTokenMalformed
	}
	c.Expires = time.Unix(exp, 0)
	if c.Kind != kind {
		return claims{}, errTokenKind
	}
	if c.Port != port {
		return claims{}, errTokenPort
	}
	now := t.now()
	if !now.Before(c.Expires) {
		return claims{}, errTokenExpired
	}
	if kind == kindHandshake {
		t.mu.Lock()
		defer t.mu.Unlock()
		for n, e := range t.used {
			if !now.Before(e) {
				delete(t.used, n)
			}
		}
		if _, seen := t.used[c.Nonce]; seen {
			return claims{}, errTokenReplayed
		}
		t.used[c.Nonce] = c.Expires
	}
	return c, nil
}
