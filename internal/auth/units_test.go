package auth

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 Appendix B (SHA-1, 8 digits, key "12345678901234567890").
func TestTOTPRFC6238Vectors(t *testing.T) {
	key := []byte("12345678901234567890")
	tests := []struct {
		unix int64
		want string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, tt := range tests {
		got := hotp(key, uint64(totpStep(time.Unix(tt.unix, 0))), 8)
		if got != tt.want {
			t.Errorf("T=%d: got %s, want %s", tt.unix, got, tt.want)
		}
	}
}

// RFC 4226 Appendix D (6 digits).
func TestHOTPRFC4226Vectors(t *testing.T) {
	key := []byte("12345678901234567890")
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for i, w := range want {
		if got := hotp(key, uint64(i), 6); got != w {
			t.Errorf("counter %d: got %s, want %s", i, got, w)
		}
	}
}

func TestVerifyTOTPSkew(t *testing.T) {
	sec, err := newTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	key, err := decodeTOTPSecret(sec)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_010, 0)
	cur := totpStep(now)
	tests := []struct {
		name   string
		code   string
		ok     bool
		wantSt int64
	}{
		{"current", hotp(key, uint64(cur), 6), true, cur},
		{"previous step", hotp(key, uint64(cur-1), 6), true, cur - 1},
		{"next step", hotp(key, uint64(cur+1), 6), true, cur + 1},
		{"two steps old", hotp(key, uint64(cur-2), 6), false, 0},
		{"with space", hotp(key, uint64(cur), 6)[:3] + " " + hotp(key, uint64(cur), 6)[3:], true, cur},
		{"letters", "12a456", false, 0},
		{"short", "12345", false, 0},
		{"empty", "", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, ok := verifyTOTP(key, tt.code, now)
			if ok != tt.ok || (ok && st != tt.wantSt) {
				t.Fatalf("verify(%q) = %d,%v; want %d,%v", tt.code, st, ok, tt.wantSt, tt.ok)
			}
		})
	}
}

func TestOtpauthURL(t *testing.T) {
	u := otpauthURL("Relay", "owner@dev.example.com", "JBSWY3DPEHPK3PXP")
	for _, want := range []string{"otpauth://totp/Relay:owner@dev.example.com?", "secret=JBSWY3DPEHPK3PXP", "issuer=Relay", "digits=6", "period=30"} {
		if !strings.Contains(u, want) {
			t.Errorf("%s missing %q", u, want)
		}
	}
}

func TestParseUserAgent(t *testing.T) {
	tests := []struct {
		ua   string
		want UserAgent
	}{
		{uaMacChrome, UserAgent{"Mac", "Chrome", "macOS"}},
		{uaIPhone, UserAgent{"iPhone", "Safari", "iOS"}},
		{uaAndroidFox, UserAgent{"Android phone", "Firefox", "Android"}},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0", UserAgent{"Windows PC", "Edge", "Windows"}},
		{"Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/140.0 Mobile/15E148 Safari/604.1", UserAgent{"iPad", "Chrome", "iPadOS"}},
		{"Mozilla/5.0 (Linux; Android 14; SM-X710) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/25.0 Chrome/121.0 Safari/537.36", UserAgent{"Android tablet", "Samsung Internet", "Android"}},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0", UserAgent{"Linux PC", "Firefox", "Linux"}},
		{"Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36", UserAgent{"Chromebook", "Chrome", "ChromeOS"}},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", UserAgent{"Mac", "Safari", "macOS"}},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 OPR/110.0", UserAgent{"Windows PC", "Opera", "Windows"}},
		{"curl/8.5.0", UserAgent{"Command line", "curl", ""}},
		{"", UserAgent{}},
	}
	for _, tt := range tests {
		if got := ParseUserAgent(tt.ua); got != tt.want {
			t.Errorf("ParseUserAgent(%.40q) = %+v, want %+v", tt.ua, got, tt.want)
		}
	}
	if got := deviceLabel(ParseUserAgent(uaIPhone)); got != "Safari on iPhone" {
		t.Errorf("label = %q", got)
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestLimiterBurstLockoutAndBackoff(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	l := newLimiter(limiterConfig{Burst: 5, Window: 5 * time.Minute, BaseLock: time.Minute, MaxLock: time.Hour, GlobalBurst: 1000, GlobalWindow: time.Minute}, clk.now)
	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("attempt %d refused", i+1)
		}
		l.Fail("a")
	}
	ok, wait := l.Allow("a")
	if ok || wait <= 0 || wait > time.Minute {
		t.Fatalf("after 5 failures: ok=%v wait=%v, want locked ≤1m", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("other keys must not be affected")
	}
	clk.advance(time.Minute + time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("lock should have expired")
	}
	l.Fail("a") // second strike: 2 minutes
	if _, wait := l.Allow("a"); wait < 90*time.Second {
		t.Fatalf("second lockout %v, want ~2m", wait)
	}
	clk.advance(2*time.Minute + time.Second) // refills ~2 tokens
	l.Fail("a")
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("one refilled token should remain")
	}
	l.Fail("a") // bucket empty again: third strike, 4 minutes
	if _, wait := l.Allow("a"); wait < 3*time.Minute {
		t.Fatalf("third lockout %v, want ~4m", wait)
	}
	l.Success("a")
	clk.advance(10 * time.Minute)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("success + refill should allow again")
	}
	l.GC()
	if len(l.keys) != 0 {
		t.Fatalf("GC kept %d idle keys", len(l.keys))
	}
}

func TestLimiterGlobalCap(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	l := newLimiter(limiterConfig{Burst: 5, Window: time.Minute, BaseLock: time.Minute, MaxLock: time.Hour, GlobalBurst: 3, GlobalWindow: time.Minute}, clk.now)
	for _, k := range []string{"a", "b", "c"} {
		l.Fail(k)
	}
	if ok, wait := l.Allow("fresh"); ok || wait <= 0 {
		t.Fatalf("global cap not enforced: ok=%v wait=%v", ok, wait)
	}
	clk.advance(30 * time.Second)
	if ok, _ := l.Allow("fresh"); !ok {
		t.Fatal("global bucket should refill")
	}
}

func TestPasswordPolicy(t *testing.T) {
	tests := []struct {
		pw string
		ok bool
	}{
		{"short", false},
		{"1234567890", false},
		{"aaaaaaaaaaaa", false},
		{"owner-owner", true},
		{"owner", false},
		{testPassword, true},
		{strings.Repeat("xy1!", 300), false}, // > 1024 bytes
	}
	for _, tt := range tests {
		err := CheckPassword("owner", tt.pw)
		if (err == nil) != tt.ok {
			t.Errorf("CheckPassword(%.20q) err=%v, want ok=%v", tt.pw, err, tt.ok)
		}
	}
	if CheckPassword("longusername", "longusername") == nil {
		t.Error("password equal to username must be refused")
	}
}
