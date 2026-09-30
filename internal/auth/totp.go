package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // HMAC-SHA1 is what RFC 6238 authenticator apps implement.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP parameters (RFC 6238 defaults understood by every authenticator app).
const (
	totpPeriod = 30
	totpDigits = 6
	totpSkew   = 1 // accept one step either side of now
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret returns a random 160-bit secret, base32 encoded.
func newTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

func decodeTOTPSecret(s string) ([]byte, error) {
	s = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	s = strings.TrimRight(s, "=")
	k, err := b32.DecodeString(s)
	if err != nil || len(k) == 0 {
		return nil, errors.New("invalid TOTP secret")
	}
	return k, nil
}

// hotp implements RFC 4226 with HMAC-SHA1 and dynamic truncation.
func hotp(key []byte, counter uint64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, code%mod)
}

// totpStep returns the RFC 6238 time step for t.
func totpStep(t time.Time) int64 { return t.Unix() / totpPeriod }

// verifyTOTP checks code against key at time now (±totpSkew steps) in
// constant time per candidate. It returns the matched step, which the caller
// must claim (replay protection) before accepting.
func verifyTOTP(key []byte, code string, now time.Time) (int64, bool) {
	code = normalizeCode(code)
	if len(code) != totpDigits {
		return 0, false
	}
	cur := totpStep(now)
	var matched int64
	ok := false
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		step := cur + d
		if step < 0 {
			continue
		}
		want := hotp(key, uint64(step), totpDigits)
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 && !ok {
			matched, ok = step, true
		}
	}
	return matched, ok
}

// normalizeCode strips the spaces and dashes people type ("123 456").
func normalizeCode(c string) string {
	var b strings.Builder
	for _, r := range c {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-':
		default:
			return "" // anything else is not a code
		}
	}
	return b.String()
}

// otpauthURL builds the Key URI Format understood by authenticator apps
// (rendered as a QR code by the web app).
func otpauthURL(issuer, account, secretB32 string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", secretB32)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}
