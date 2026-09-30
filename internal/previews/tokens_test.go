package previews

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
)

func TestTokens(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tk := newTokens([]byte(strings.Repeat("k", 32)))
	tk.now = func() time.Time { return now }
	sess := sessionRef(&server.Principal{User: "u", SessionID: "s1", Method: "cookie"})

	good, err := tk.issue(kindHandshake, 5173, sess, handshakeTTL)
	if err != nil {
		t.Fatal(err)
	}
	cookie, _ := tk.issue(kindCookie, 5173, sess, cookieTTL)
	expired, _ := tk.issue(kindHandshake, 5173, sess, -time.Second)
	other := newTokens([]byte(strings.Repeat("x", 32)))
	other.now = tk.now
	foreign, _ := other.issue(kindHandshake, 5173, sess, handshakeTTL)

	tamper := func(tok string) string {
		p, sig, _ := strings.Cut(tok, ".")
		b := []byte(p)
		if b[5] == 'A' {
			b[5] = 'B'
		} else {
			b[5] = 'A'
		}
		return string(b) + "." + sig
	}

	tests := []struct {
		name string
		tok  string
		kind string
		port int
		want error
	}{
		{"valid cookie", cookie, kindCookie, 5173, nil},
		{"cookie reused ok", cookie, kindCookie, 5173, nil},
		{"wrong port", cookie, kindCookie, 3000, errTokenPort},
		{"wrong kind", cookie, kindHandshake, 5173, errTokenKind},
		{"expired", expired, kindHandshake, 5173, errTokenExpired},
		{"tampered payload", tamper(good), kindHandshake, 5173, errTokenSignature},
		{"other key", foreign, kindHandshake, 5173, errTokenSignature},
		{"garbage", "not-a-token", kindHandshake, 5173, errTokenMalformed},
		{"bad base64", "!!!.???", kindHandshake, 5173, errTokenMalformed},
		{"empty", "", kindHandshake, 5173, errTokenMalformed},
		{"valid handshake", good, kindHandshake, 5173, nil},
		{"handshake replay", good, kindHandshake, 5173, errTokenReplayed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := tk.verify(tt.tok, tt.kind, tt.port)
			if !errors.Is(err, tt.want) {
				t.Fatalf("verify err = %v, want %v", err, tt.want)
			}
			if err == nil && (c.Session != sess || c.Port != tt.port) {
				t.Errorf("claims = %+v", c)
			}
		})
	}

	// Cookie tokens expire after 12 h.
	now = now.Add(cookieTTL + time.Second)
	if _, err := tk.verify(cookie, kindCookie, 5173); !errors.Is(err, errTokenExpired) {
		t.Errorf("cookie after 12h: %v", err)
	}
}

func TestSessionRefDistinct(t *testing.T) {
	a := sessionRef(&server.Principal{Method: "cookie", SessionID: "a"})
	b := sessionRef(&server.Principal{Method: "cookie", SessionID: "b"})
	c := sessionRef(&server.Principal{Method: "token", TokenID: "a"})
	if a == b || a == c || len(a) != 32 {
		t.Errorf("refs not distinct/opaque: %s %s %s", a, b, c)
	}
}

func TestLoadTokensPersistsKey(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	a, err := loadTokens(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadTokens(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := a.issue(kindCookie, 80, "s", time.Hour)
	if _, err := b.verify(tok, kindCookie, 80); err != nil {
		t.Errorf("key not persisted: %v", err)
	}
}

func TestParsePreviewHost(t *testing.T) {
	tests := []struct {
		host, base string
		port       int
		ok         bool
	}{
		{"5173.dev.example.com", "dev.example.com", 5173, true},
		{"5173.DEV.example.com:443", "dev.example.com", 5173, true},
		{"5173.localhost:47705", "localhost", 5173, true},
		{"5173.dev.example.com.", "dev.example.com", 5173, true},
		{"dev.example.com", "dev.example.com", 0, false},
		{"a.5173.dev.example.com", "dev.example.com", 0, false},
		{"05173.dev.example.com", "dev.example.com", 0, false},
		{"99999.dev.example.com", "dev.example.com", 0, false},
		{"0.dev.example.com", "dev.example.com", 0, false},
		{"abc.dev.example.com", "dev.example.com", 0, false},
		{"5173.evil-dev.example.com", "dev.example.com", 0, false},
		{"5173.dev.example.com.evil.org", "dev.example.com", 0, false},
		{"5173.dev.example.com", "", 0, false},
	}
	for _, tt := range tests {
		port, ok := parsePreviewHost(tt.host, tt.base)
		if port != tt.port || ok != tt.ok {
			t.Errorf("parsePreviewHost(%q, %q) = %d, %v", tt.host, tt.base, port, ok)
		}
	}
}

type fakeResolver map[string][]string

func (f fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if v, ok := f[host]; ok {
		return v, nil
	}
	if strings.HasPrefix(host, "relay-") {
		if v, ok := f["*"]; ok {
			return v, nil
		}
	}
	return nil, errors.New("no such host")
}

func TestDetectMode(t *testing.T) {
	wild := fakeResolver{"dev.example.com": {"192.0.2.1"}, "*": {"192.0.2.1"}}
	otherWild := fakeResolver{"dev.example.com": {"192.0.2.1"}, "*": {"198.51.100.7"}}
	noWild := fakeResolver{"dev.example.com": {"192.0.2.1"}}
	tests := []struct {
		name, mode, base string
		r                resolver
		want             string
	}{
		{"auto wildcard", "auto", "dev.example.com", wild, modeSubdomain},
		{"auto wildcard elsewhere", "auto", "dev.example.com", otherWild, modePath},
		{"auto no wildcard", "auto", "dev.example.com", noWild, modePath},
		{"auto unresolvable", "", "nope.example.com", noWild, modePath},
		{"auto localhost", "auto", "localhost", noWild, modeSubdomain},
		{"auto no host", "auto", "", wild, modePath},
		{"auto ip literal", "auto", "192.0.2.1", wild, modePath},
		{"explicit path", "path", "dev.example.com", wild, modePath},
		{"explicit subdomain", "subdomain", "dev.example.com", noWild, modeSubdomain},
		{"subdomain without host", "subdomain", "", noWild, modePath},
		{"off", "off", "dev.example.com", wild, modeOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectMode(context.Background(), tt.mode, tt.base, tt.r); got != tt.want {
				t.Errorf("detectMode = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPreviewURL(t *testing.T) {
	o := parseOrigin("https://dev.example.com")
	if got := previewURL(modeSubdomain, 5173, "dev.example.com", o); got != "https://5173.dev.example.com/" {
		t.Errorf("subdomain url = %s", got)
	}
	if got := previewURL(modePath, 5173, "dev.example.com", o); got != "https://dev.example.com/p/5173/" {
		t.Errorf("path url = %s", got)
	}
	lo := parseOrigin("http://localhost:47705")
	if got := previewURL(modeSubdomain, 3000, "localhost", lo); got != "http://3000.localhost:47705/" {
		t.Errorf("dev url = %s", got)
	}
}
