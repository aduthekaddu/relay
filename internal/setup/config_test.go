package setup

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/config"
)

var testNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func TestRenderModes(t *testing.T) {
	tests := []struct {
		name    string
		a       Answers
		listen  string
		origin  string
		autoTLS bool
		redir   bool
		wantErr string
	}{
		{name: "local", a: Answers{Access: AccessLocal, User: "ann"}, listen: "127.0.0.1:7777", origin: "http://127.0.0.1:7777"},
		{name: "local custom", a: Answers{Access: AccessLocal, User: "ann", Listen: "127.0.0.1:47000"}, listen: "127.0.0.1:47000", origin: "http://127.0.0.1:47000"},
		{name: "tailscale", a: Answers{Access: AccessTailscale, User: "ann", PublicURL: "https://box.tail0.ts.net/"}, listen: DefaultLocalListen, origin: "https://box.tail0.ts.net"},
		{name: "proxy", a: Answers{Access: AccessProxy, User: "ann", PublicURL: "https://relay.example.com"}, listen: DefaultLocalListen, origin: "https://relay.example.com"},
		{name: "domain", a: Answers{Access: AccessDomain, User: "ann", Domain: "Relay.Example.COM.", Email: "a@example.com", RedirectHTTP: true}, listen: ":443", origin: "https://relay.example.com", autoTLS: true, redir: true},
		{name: "sslip", a: Answers{Access: AccessSslip, User: "ann", Domain: "203-0-113-7.sslip.io"}, listen: ":443", origin: "https://203-0-113-7.sslip.io", autoTLS: true},
		{name: "domain other port has no redirect", a: Answers{Access: AccessDomain, User: "ann", Domain: "relay.example.com", Listen: ":8443", RedirectHTTP: true}, listen: ":8443"},
		{name: "bad mode", a: Answers{Access: "carrier-pigeon", User: "ann"}, wantErr: "unknown access mode"},
		{name: "domain missing", a: Answers{Access: AccessDomain, User: "ann"}, wantErr: "enter a domain"},
		{name: "bad user", a: Answers{Access: AccessLocal, User: "a b"}, wantErr: "invalid username"},
		{name: "bad listen", a: Answers{Access: AccessLocal, User: "ann", Listen: "nope"}, wantErr: "listen address"},
		{name: "bad public url", a: Answers{Access: AccessProxy, User: "ann", PublicURL: "relay.example.com"}, wantErr: "must start with"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.a.PasswordHash = "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA"
			data, err := Render(tt.a, testNow)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(data), "# Relay configuration") {
				t.Errorf("missing header:\n%s", data)
			}
			p := tempPaths(t)
			cfg, err := WriteConfig(p, data)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Server.Listen != tt.listen {
				t.Errorf("listen = %q, want %q", cfg.Server.Listen, tt.listen)
			}
			if tt.origin != "" && cfg.Origin() != tt.origin {
				t.Errorf("origin = %q, want %q", cfg.Origin(), tt.origin)
			}
			if cfg.UseAutoTLS() != tt.autoTLS {
				t.Errorf("auto TLS = %v, want %v", cfg.UseAutoTLS(), tt.autoTLS)
			}
			if cfg.Server.RedirectHTTP != tt.redir {
				t.Errorf("redirect = %v, want %v", cfg.Server.RedirectHTTP, tt.redir)
			}
			if cfg.Auth.User != "ann" || cfg.Auth.PasswordHash != tt.a.PasswordHash {
				t.Errorf("auth = %+v", cfg.Auth)
			}
			if m := mode(t, p.ConfigFile); m != 0o600 {
				t.Errorf("relay.toml mode = %o", m)
			}
			if m := mode(t, p.ConfigDir); m != 0o700 {
				t.Errorf("config dir mode = %o", m)
			}
		})
	}
}

func TestWriteConfigRejectsBrokenTOML(t *testing.T) {
	p := tempPaths(t)
	if _, err := WriteConfig(p, []byte("[server\nlisten=")); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(p.ConfigFile); !os.IsNotExist(err) {
		t.Fatalf("broken config must not be written: %v", err)
	}
	entries, _ := os.ReadDir(p.ConfigDir)
	if len(entries) != 0 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestBackupConfig(t *testing.T) {
	p := tempPaths(t)
	if bak, err := BackupConfig(p.ConfigFile, testNow); err != nil || bak != "" {
		t.Fatalf("missing file: %q %v", bak, err)
	}
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ConfigFile, []byte("x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bak, err := BackupConfig(p.ConfigFile, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(bak, ".bak-20260102-030405") {
		t.Errorf("backup name %q", bak)
	}
	if b, _ := os.ReadFile(bak); string(b) != "x = 1\n" {
		t.Errorf("backup content %q", b)
	}
	if m := mode(t, bak); m != 0o600 {
		t.Errorf("backup mode %o", m)
	}
}

func TestSslipHost(t *testing.T) {
	tests := []struct {
		ip, want, err string
	}{
		{"203.0.113.7", "203-0-113-7.sslip.io", ""},
		{"2001:db8::1", "2001-db8--1.sslip.io", ""},
		{"2600::", "2600--0.sslip.io", ""},
		{"10.0.0.1", "", "not a public"},
		{"192.168.1.2", "", "not a public"},
		{"100.101.102.103", "", "not a public"},
		{"127.0.0.1", "", "not a public"},
		{"fe80::1", "", "not a public"},
	}
	for _, tt := range tests {
		got, err := SslipHost(net.ParseIP(tt.ip))
		if tt.err != "" {
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("%s: err = %v", tt.ip, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("%s: got %q, %v; want %q", tt.ip, got, err, tt.want)
		}
	}
	if _, err := SslipHost(nil); err == nil {
		t.Error("nil IP must fail")
	}
}

func TestValidDomain(t *testing.T) {
	good := []string{"relay.example.com", "a-b.example.co.uk", "RELAY.example.com.", "x1.io"}
	bad := []string{"", "localhost", "203.0.113.7", "-a.example.com", "a_b.example.com", "a..b.com", strings.Repeat("a", 64) + ".com"}
	for _, d := range good {
		if err := ValidDomain(d); err != nil {
			t.Errorf("%q: %v", d, err)
		}
	}
	for _, d := range bad {
		if err := ValidDomain(d); err == nil {
			t.Errorf("%q accepted", d)
		}
	}
}

func TestListenHelpers(t *testing.T) {
	tests := []struct {
		addr     string
		port     int
		loopback bool
		err      bool
	}{
		{"127.0.0.1:7777", 7777, true, false},
		{"localhost:80", 80, true, false},
		{"[::1]:443", 443, true, false},
		{":443", 443, false, false},
		{"0.0.0.0:8080", 8080, false, false},
		{"127.0.0.1:0", 0, false, true},
		{"127.0.0.1:99999", 0, false, true},
		{"7777", 0, false, true},
	}
	for _, tt := range tests {
		_, port, err := SplitListen(tt.addr)
		if (err != nil) != tt.err || (!tt.err && port != tt.port) {
			t.Errorf("SplitListen(%q) = %d, %v", tt.addr, port, err)
		}
		if IsLoopback(tt.addr) != tt.loopback {
			t.Errorf("IsLoopback(%q) = %v", tt.addr, !tt.loopback)
		}
	}
}

func TestIsLoopbackURL(t *testing.T) {
	for u, want := range map[string]bool{
		"http://127.0.0.1:7777":        true,
		"http://localhost:7777/x":      true,
		"http://[::1]:7777":            true,
		"https://relay.example.com":    false,
		"https://203-0-113-7.sslip.io": false,
		"http://app.localhost":         true,
	} {
		if got := IsLoopbackURL(u); got != want {
			t.Errorf("%s: %v", u, got)
		}
	}
}

func TestPublicIP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte("203.0.113.9\n"))
		case "/junk":
			w.Write([]byte("<html>"))
		default:
			http.Error(w, "no", http.StatusBadGateway)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	ip, err := PublicIP(ctx, srv.Client(), srv.URL+"/ok")
	if err != nil || ip.String() != "203.0.113.9" {
		t.Fatalf("got %v, %v", ip, err)
	}
	if _, err := PublicIP(ctx, srv.Client(), srv.URL+"/junk"); err == nil {
		t.Error("junk accepted")
	}
	if _, err := PublicIP(ctx, srv.Client(), srv.URL+"/fail"); err == nil {
		t.Error("502 accepted")
	}
}

func TestCheckDNS(t *testing.T) {
	resolver := func(addrs ...string) func(context.Context, string) ([]net.IP, error) {
		return func(context.Context, string) ([]net.IP, error) {
			if len(addrs) == 0 {
				return nil, errors.New("NXDOMAIN")
			}
			var out []net.IP
			for _, a := range addrs {
				out = append(out, net.ParseIP(a))
			}
			return out, nil
		}
	}
	ctx := context.Background()
	pub := net.ParseIP("203.0.113.9")
	if c, err := CheckDNS(ctx, resolver("203.0.113.9"), "relay.example.com", pub); err != nil || !c.Matched {
		t.Errorf("match: %+v %v", c, err)
	}
	if c, err := CheckDNS(ctx, resolver("198.51.100.1"), "relay.example.com", pub); err != nil || c.Matched {
		t.Errorf("mismatch: %+v %v", c, err)
	}
	if _, err := CheckDNS(ctx, resolver(), "relay.example.com", pub); err == nil {
		t.Error("NXDOMAIN must fail")
	}
}

func TestDetectTailscale(t *testing.T) {
	sys := newFakeSys("linux")
	if ts := DetectTailscale(context.Background(), sys); ts.Installed {
		t.Fatal("not installed")
	}
	sys.bins["tailscale"] = "/usr/bin/tailscale"
	sys.out["/usr/bin/tailscale status --json"] = `{"BackendState":"Running","Self":{"DNSName":"box.tail0.ts.net."}}`
	ts := DetectTailscale(context.Background(), sys)
	if !ts.Installed || !ts.Running || ts.DNSName != "box.tail0.ts.net" {
		t.Fatalf("%+v", ts)
	}
	if got := strings.Join(TailscaleServeArgs(7777), " "); got != "tailscale serve --bg --https=443 http://127.0.0.1:7777" {
		t.Errorf("serve args %q", got)
	}
}

func TestQR(t *testing.T) {
	q, err := QR("https://203-0-113-7.sslip.io", false)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(q, "\n"), "\n")
	if len(lines) < 12 {
		t.Fatalf("too few lines: %d", len(lines))
	}
	width := len([]rune(lines[0]))
	for i, l := range lines {
		if len([]rune(l)) != width {
			t.Fatalf("line %d width %d != %d", i, len([]rune(l)), width)
		}
	}
	if !strings.ContainsAny(q, "█▀▄") {
		t.Error("no blocks drawn")
	}
	colored, _ := QR("x", true)
	if !strings.Contains(colored, "\x1b[30;107m") {
		t.Error("color mode must set explicit colors")
	}
}

func TestPasswords(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		pw, err := GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(pw) != 29 || strings.Count(pw, "-") != 4 || strings.ContainsAny(pw, "0O1lI") {
			t.Fatalf("bad generated password %q", pw)
		}
		if err := CheckPassword(pw, "ann"); err != nil {
			t.Fatalf("generated password rejected: %v", err)
		}
		if seen[pw] {
			t.Fatal("duplicate password")
		}
		seen[pw] = true
	}
	for pw, ok := range map[string]bool{
		"short":              false,
		"aaaaaaaaaaaaaa":     false,
		" leading-space-pw":  false,
		"Annabelle1":         true,
		"correct horse batt": true,
		"annabelle1":         true,
	} {
		if err := CheckPassword(pw, "ann"); (err == nil) != ok {
			t.Errorf("%q: %v", pw, err)
		}
	}
	if err := CheckPassword("longusername", "LongUsername"); err == nil {
		t.Error("password equal to username accepted")
	}
	if ValidUser("ann") != nil || ValidUser("a:b") == nil || ValidUser("") == nil {
		t.Error("ValidUser")
	}
}

func TestParseComponents(t *testing.T) {
	tests := []struct {
		in   string
		want string
		err  bool
	}{
		{"", "", false},
		{"none", "", false},
		{"all", "desktop,code,agents,tools", false},
		{"tools, agents,tools", "tools,agents", false},
		{"DESKTOP", "desktop", false},
		{"desktop,bogus", "", true},
	}
	for _, tt := range tests {
		got, err := ParseComponents(tt.in)
		if (err != nil) != tt.err || strings.Join(got, ",") != tt.want {
			t.Errorf("%q: %v %v", tt.in, got, err)
		}
	}
}

func TestLoadedConfigKeepsDefaults(t *testing.T) {
	data, err := Render(Answers{Access: AccessLocal, User: "ann"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	p := tempPaths(t)
	cfg, err := WriteConfig(p, data)
	if err != nil {
		t.Fatal(err)
	}
	def := config.Defaults()
	if cfg.Terminal.ScrollbackKB != def.Terminal.ScrollbackKB && def.Terminal.ScrollbackKB != 0 {
		t.Errorf("setup must not override unrelated defaults")
	}
	if strings.Contains(string(data), "[terminal]") {
		t.Error("setup should only write server and auth tables")
	}
}
