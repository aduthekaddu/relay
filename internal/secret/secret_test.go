package secret

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPasswordWith("correct horse battery staple", Params{Memory: 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=1024,t=1,p=1$") {
		t.Fatalf("unexpected format: %s", h)
	}
	rehash, err := VerifyPassword("correct horse battery staple", h)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !rehash {
		t.Fatal("weak params should request a rehash")
	}
	if _, err := VerifyPassword("wrong", h); err != ErrMismatch {
		t.Fatalf("want mismatch, got %v", err)
	}
	if _, err := VerifyPassword("x", "$bcrypt$foo"); err == nil {
		t.Fatal("want error for unsupported format")
	}
}

func TestToken(t *testing.T) {
	a, _ := Token("rly_", 32)
	b, _ := Token("rly_", 32)
	if a == b || !strings.HasPrefix(a, "rly_") || len(a) != 4+52 {
		t.Fatalf("bad tokens %q %q", a, b)
	}
	if HashToken(a) == HashToken(b) || len(HashToken(a)) != 64 {
		t.Fatal("bad token hash")
	}
	if !Equal("abc", "abc") || Equal("abc", "abd") {
		t.Fatal("Equal broken")
	}
}
