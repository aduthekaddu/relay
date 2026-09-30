package setup

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/secret"
	"github.com/aduthekaddu/relay/internal/toolbox"
)

func ipify(t *testing.T, ip string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(ip)) }))
	t.Cleanup(srv.Close)
	return srv
}

func newWizard(t *testing.T, opt Options, ui *UI, sys *fakeSys) (*Wizard, config.Paths) {
	t.Helper()
	p := tempPaths(t)
	if opt.Binary == "" {
		opt.Binary = "/opt/relay/relay"
	}
	w := &Wizard{
		Opt:     opt,
		UI:      ui,
		Sys:     sys,
		Paths:   p,
		Getenv:  func(k string) string { return map[string]string{"PATH": "/usr/bin:/bin"}[k] },
		Now:     func() time.Time { return testNow },
		Finder:  toolbox.Finder{Dirs: []string{}},
		Resolve: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.9")}, nil },
	}
	return w, p
}

func loadCfg(t *testing.T, p config.Paths) *config.Config {
	t.Helper()
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestWizardNonInteractiveLocal(t *testing.T) {
	var out bytes.Buffer
	ui, _ := NewUI(false, &out)
	units := t.TempDir()
	sys := newFakeSys("linux")
	w, p := newWizard(t, Options{
		Access: AccessLocal, User: "ann", PasswordStdin: true, Stdin: strings.NewReader("correct-horse-battery\n"),
		Yes: true, NoSystemd: true, UnitsDir: units, ComponentsSet: true,
	}, ui, sys)
	res, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if res.URL != "http://127.0.0.1:7777" || res.Started {
		t.Errorf("result %+v", res)
	}
	cfg := loadCfg(t, p)
	if cfg.Auth.User != "ann" {
		t.Errorf("user %q", cfg.Auth.User)
	}
	if _, err := secret.VerifyPassword("correct-horse-battery", cfg.Auth.PasswordHash); err != nil {
		t.Errorf("hash does not verify: %v", err)
	}
	if mode(t, p.ConfigFile) != 0o600 {
		t.Error("config mode")
	}
	b, err := os.ReadFile(filepath.Join(units, "relay.service"))
	if err != nil || !strings.Contains(string(b), "ExecStart=/opt/relay/relay serve") {
		t.Errorf("unit: %v\n%s", err, b)
	}
	if len(sys.calls) != 0 || len(sys.interact) != 0 {
		t.Errorf("--no-systemd must not run commands: %v %v", sys.calls, sys.interact)
	}
	if strings.Contains(out.String(), "█") {
		t.Error("no QR for a loopback URL")
	}
	if strings.Contains(out.String(), "correct-horse-battery") {
		t.Error("typed password echoed")
	}
}

func TestWizardSslipGeneratesPasswordAndQR(t *testing.T) {
	var out bytes.Buffer
	ui, _ := NewUI(false, &out)
	w, p := newWizard(t, Options{Access: AccessSslip, User: "ann", Yes: true, NoSystemd: true, ComponentsSet: true}, ui, newFakeSys("linux"))
	w.IPURL = ipify(t, "203.0.113.9").URL
	res, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if res.URL != "https://203-0-113-9.sslip.io" {
		t.Errorf("url %q", res.URL)
	}
	cfg := loadCfg(t, p)
	if cfg.Server.Domain != "203-0-113-9.sslip.io" || !cfg.UseAutoTLS() {
		t.Errorf("server %+v", cfg.Server)
	}
	pw := regexp.MustCompile(`password\s+(\S{29})`).FindStringSubmatch(out.String())
	if pw == nil {
		t.Fatalf("generated password not shown:\n%s", out.String())
	}
	if _, err := secret.VerifyPassword(pw[1], cfg.Auth.PasswordHash); err != nil {
		t.Errorf("shown password does not match the hash: %v", err)
	}
	if strings.Count(out.String(), pw[1]) != 1 {
		t.Error("generated password must be shown exactly once")
	}
	if !strings.Contains(out.String(), "█") {
		t.Error("QR code missing")
	}
}

func TestWizardKeepsExistingConfig(t *testing.T) {
	var out bytes.Buffer
	ui, _ := NewUI(false, &out)
	w, p := newWizard(t, Options{Access: AccessSslip, Yes: true, NoSystemd: true, ComponentsSet: true}, ui, newFakeSys("linux"))
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	orig := "[server]\nlisten = \"127.0.0.1:47001\"\n[auth]\nuser = \"keep\"\n"
	if err := os.WriteFile(p.ConfigFile, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if b, _ := os.ReadFile(p.ConfigFile); string(b) != orig {
		t.Errorf("config changed:\n%s", b)
	}
	if res.URL != "http://127.0.0.1:47001" {
		t.Errorf("url %q", res.URL)
	}
}

func TestWizardForceBacksUp(t *testing.T) {
	var out bytes.Buffer
	ui, _ := NewUI(false, &out)
	w, p := newWizard(t, Options{Access: AccessLocal, User: "ann", Yes: true, Force: true, NoSystemd: true, ComponentsSet: true}, ui, newFakeSys("linux"))
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ConfigFile, []byte("[auth]\nuser = \"old\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loadCfg(t, p).Auth.User != "ann" {
		t.Error("not replaced")
	}
	baks, _ := filepath.Glob(p.ConfigFile + ".bak-*")
	if len(baks) != 1 {
		t.Errorf("backups %v", baks)
	}
}

func TestWizardInteractiveLineMode(t *testing.T) {
	// access 5 = local, username ann, password 2 = type own (twice, first
	// attempt mismatched), components 0 = none.
	in := strings.NewReader(strings.Join([]string{"5", "ann", "2", "sup3r-secret-pw", "different-pw-1", "sup3r-secret-pw", "sup3r-secret-pw", "0", ""}, "\n"))
	var out bytes.Buffer
	ui := NewLineUI(in, &out)
	w, p := newWizard(t, Options{NoSystemd: true}, ui, newFakeSys("linux"))
	if _, err := w.Run(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	cfg := loadCfg(t, p)
	if cfg.Server.Listen != DefaultLocalListen || cfg.Auth.User != "ann" {
		t.Errorf("cfg %+v %+v", cfg.Server, cfg.Auth)
	}
	if _, err := secret.VerifyPassword("sup3r-secret-pw", cfg.Auth.PasswordHash); err != nil {
		t.Error("password")
	}
	if !strings.Contains(out.String(), "do not match") {
		t.Error("mismatch not reported")
	}
}

func TestWizardInteractiveAbort(t *testing.T) {
	var out bytes.Buffer
	ui := NewLineUI(strings.NewReader(""), &out) // EOF at the first prompt
	w, p := newWizard(t, Options{NoSystemd: true}, ui, newFakeSys("linux"))
	if _, err := w.Run(context.Background()); err == nil {
		t.Fatal("want abort")
	}
	if _, err := os.Stat(p.ConfigFile); !os.IsNotExist(err) {
		t.Error("aborted setup wrote a config")
	}
}

// serveHealth answers /api/v1/health on a unix socket.
func serveHealth(t *testing.T, socket string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("unix socket: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"version":"v9.9.9","sessions":2}`))
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
}

func TestWizardDomainWithSystemd(t *testing.T) {
	var out bytes.Buffer
	ui, _ := NewUI(false, &out)
	sys := newFakeSys("linux")
	sys.bins["systemctl"] = "/usr/bin/systemctl"
	sys.out["loginctl show-user tester --property=Linger"] = "Linger=no"
	sys.fail["loginctl enable-linger tester"] = true
	units := t.TempDir()
	w, p := newWizard(t, Options{Access: AccessDomain, Domain: "relay.example.com", User: "ann", Yes: true, UnitsDir: units, ComponentsSet: true}, ui, sys)
	w.IPURL = ipify(t, "203.0.113.9").URL
	serveHealth(t, p.CtlSocket)
	res, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !res.Started || res.URL != "https://relay.example.com" {
		t.Errorf("result %+v\n%s", res, out.String())
	}
	for _, c := range []string{
		"systemctl --user enable --now relay-ptyd.service relay.service",
		"sudo -n loginctl enable-linger tester",
		"sudo setcap cap_net_bind_service=+ep /opt/relay/relay",
	} {
		if !sys.called(c) {
			t.Errorf("missing %q\ncalls %v\ninteractive %v", c, sys.calls, sys.interact)
		}
	}
	if !strings.Contains(out.String(), "points at this machine") {
		t.Errorf("DNS result missing:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "v9.9.9 is running") {
		t.Error("health not reported")
	}
}

func TestWizardDomainDNSMismatchWarns(t *testing.T) {
	var out bytes.Buffer
	ui, _ := NewUI(false, &out)
	w, _ := newWizard(t, Options{Access: AccessDomain, Domain: "relay.example.com", User: "ann", Yes: true, NoSystemd: true, ComponentsSet: true}, ui, newFakeSys("linux"))
	w.IPURL = ipify(t, "198.51.100.4").URL
	if _, err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "but this machine's public IP is 198.51.100.4") {
		t.Errorf("no mismatch warning:\n%s", out.String())
	}
}

// scriptSys checks recipe scripts at the moment they run.
type scriptSys struct {
	*fakeSys
	t    *testing.T
	seen []string
}

func (s *scriptSys) Interactive(ctx context.Context, env []string, name string, args ...string) error {
	if name == "bash" && len(args) == 1 {
		st, err := os.Stat(args[0])
		if err != nil {
			s.t.Errorf("script missing: %v", err)
		} else if st.Mode().Perm() != 0o700 {
			s.t.Errorf("script mode %o", st.Mode().Perm())
		}
		b, _ := os.ReadFile(args[0])
		if !strings.Contains(string(b), "# ---- recipe ----") {
			s.t.Error("script lacks the helper library")
		}
		s.seen = append(s.seen, strings.TrimSuffix(filepath.Base(args[0]), ".sh"))
	}
	return s.fakeSys.Interactive(ctx, env, name, args...)
}

func TestWizardInstallsComponents(t *testing.T) {
	var out bytes.Buffer
	ui, _ := NewUI(false, &out)
	sys := &scriptSys{fakeSys: newFakeSys("linux"), t: t}
	p := tempPaths(t)
	w := &Wizard{
		Opt:    Options{Access: AccessLocal, User: "ann", Yes: true, NoSystemd: true, Components: "tools,code", ComponentsSet: true, Binary: "/opt/relay/relay"},
		UI:     ui,
		Sys:    sys,
		Paths:  p,
		Getenv: func(string) string { return "" },
		Finder: toolbox.Finder{Dirs: []string{}},
	}
	if _, err := w.Run(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	joined := strings.Join(sys.seen, ",")
	for _, id := range []string{"code-server", "ripgrep", "tmux"} {
		if !strings.Contains(joined, id) {
			t.Errorf("%s not installed; ran %v", id, sys.seen)
		}
	}
	left, _ := filepath.Glob(filepath.Join(p.CacheDir, "setup", "relay-setup-*"))
	if len(left) != 0 {
		t.Errorf("temp scripts left: %v", left)
	}
}
