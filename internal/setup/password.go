package setup

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"unicode/utf8"
)

// MinPasswordLen is the shortest password setup accepts.
const MinPasswordLen = 10

// passwordAlphabet has no look-alike characters (0/O, 1/l/I), so a
// generated password can be read off a screen and typed on a phone.
const passwordAlphabet = "abcdefghijkmnpqrstuvwxyzACDEFGHJKLMNPQRSTUVWXYZ23456789"

// GeneratePassword returns a random password of five dash-separated
// groups of five characters (~143 bits of entropy).
func GeneratePassword() (string, error) {
	max := big.NewInt(int64(len(passwordAlphabet)))
	var b strings.Builder
	for g := 0; g < 5; g++ {
		if g > 0 {
			b.WriteByte('-')
		}
		for i := 0; i < 5; i++ {
			n, err := rand.Int(rand.Reader, max)
			if err != nil {
				return "", fmt.Errorf("generate password: %w", err)
			}
			b.WriteByte(passwordAlphabet[n.Int64()])
		}
	}
	return b.String(), nil
}

// CheckPassword rejects passwords that are too short or trivially weak.
func CheckPassword(pw, user string) error {
	if strings.TrimSpace(pw) != pw {
		return fmt.Errorf("the password starts or ends with whitespace")
	}
	if utf8.RuneCountInString(pw) < MinPasswordLen {
		return fmt.Errorf("use at least %d characters", MinPasswordLen)
	}
	if user != "" && strings.EqualFold(pw, user) {
		return fmt.Errorf("the password must differ from the username")
	}
	distinct := map[rune]bool{}
	for _, r := range pw {
		distinct[r] = true
	}
	if len(distinct) < 4 {
		return fmt.Errorf("the password is too repetitive")
	}
	return nil
}

// ValidUser checks a login name.
func ValidUser(s string) error {
	if s == "" || len(s) > 64 || strings.ContainsAny(s, " \t\r\n:/\\\"'") {
		return fmt.Errorf("use 1-64 characters without spaces, quotes, slashes or colons")
	}
	return nil
}
