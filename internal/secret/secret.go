// Package secret holds small cryptographic helpers shared by the server
// and the CLI: argon2id password hashing and random tokens.
package secret

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params are argon2id cost parameters. Defaults follow OWASP guidance
// while staying gentle on small servers (a login costs ~40 ms, 32 MiB).
type Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

var DefaultParams = Params{Memory: 32 * 1024, Time: 2, Threads: 1, KeyLen: 32, SaltLen: 16}

// ErrMismatch is returned when a password does not match its hash.
var ErrMismatch = errors.New("password mismatch")

// HashPassword returns a PHC-formatted argon2id hash:
// $argon2id$v=19$m=32768,t=2,p=1$<salt b64>$<key b64>
func HashPassword(password string) (string, error) {
	return HashPasswordWith(password, DefaultParams)
}

func HashPasswordWith(password string, p Params) (string, error) {
	if password == "" {
		return "", errors.New("empty password")
	}
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	b := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads, b.EncodeToString(salt), b.EncodeToString(key)), nil
}

// VerifyPassword checks password against a PHC argon2id hash in constant
// time. needsRehash reports whether the hash uses weaker-than-default params.
func VerifyPassword(password, phc string) (needsRehash bool, err error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("unsupported argon2 version")
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return false, errors.New("invalid argon2 parameters")
	}
	b := base64.RawStdEncoding
	salt, err := b.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("invalid salt")
	}
	want, err := b.DecodeString(parts[5])
	if err != nil {
		return false, errors.New("invalid key")
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, ErrMismatch
	}
	d := DefaultParams
	return p.Memory < d.Memory || p.Time < d.Time || uint32(len(want)) < d.KeyLen, nil
}

// Token returns a random token with the given prefix, e.g. Token("rly_", 32)
// → "rly_" + 52 base32 characters (lowercase, no padding).
func Token(prefix string, nbytes int) (string, error) {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

// HashToken returns the hex SHA-256 of a token for storage. Tokens are
// high-entropy, so a fast hash is appropriate (unlike passwords).
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Equal compares two strings in constant time.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
