package setup

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/aduthekaddu/relay/internal/toolbox"
)

type doctorFixture struct {
	d      *Doctor
	sys    *fakeSys
	health map[string]*Health // socket → answer (nil = down)
	bin    string
}

func newDoctor(t *testing.T, cfg string) *doctorFixture {
	t.Helper()
	p := tempPaths(t)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	if cfg != "" {
		if err := os.WriteFile(p.ConfigFile, []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	binDir := filepath.Join(p.Home, "bin")
	f := &doctorFixture{sys: newFakeSys("linux"), health: map[string]*Health{}, bin: filepath.Join(binDir, "relay")}
	f.d = &Doctor{
		Sys:      f.sys,
		Paths:    p,
		Binary:   f.bin,
		Getenv:   func(k string) string { return map[string]string{"PATH": binDir + ":/usr/bin"}[k] },
		UnitsDir: filepath.Join(p.Home, "units"),
		Resolve:  func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.9")}, nil },
		Health: func(_ context.Context, socket, _ string) (*Health, error) {
			if h := f.health[socket]; h != nil {
				return h, nil
			}
			return nil, errors.New("dial unix " + socket + ": connect: no such file or directory")
		},
		DiskFree: func(string) (uint64, error) { return 50 << 30, nil },
		Listen: func(string, string) (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		},
		Finder: toolbox.Finder{Dirs: []string{}},
	}
	return f
}

func byID(r Report) map[string]Check {
	m := map[string]Check{}
	for _, c := range r.Checks {
		m[c.ID] = c
	}
	return m
}

func TestDoctorFreshInstall(t *testing.T) {
	f := newDoctor(t, "")
	r := f.d.Run(context.Background())
	c := byID(r)
	if c["config"].Status != StatusWarn || !strings.Contains(c["config"].Fix, "relay setup") {
		t.Errorf("config: %+v", c["config"])
	}
	if c["server"].Status != StatusFail || c["ptyd"].Status != StatusFail {
		t.Errorf("sockets: %+v %+v", c["server"], c["ptyd"])
	}
	if strings.Contains(c["server"].Detail, "no such file") {
		t.Errorf("noise in detail: %q", c["server"].Detail)
	}
	if c["services"].Status != StatusWarn {
		t.Errorf("services: %+v", c["services"])
	}
	if c["tools-core"].Status != StatusWarn || c["agents"].Status != StatusInfo {
		t.Errorf("tools: %+v %+v", c["tools-core"], c["agents"])
	}
	if r.OK {
		t.Error("report OK with the server down")
	}
}

func TestDoctorHealthyLocal(t *testing.T) {
	f := newDoctor(t, "[server]\nlisten = \"127.0.0.1:47123\"\ntls = \"off\"\n[auth]\nuser = \"ann\"\npassword_hash = \"$argon2id$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA\"\n")
	f.health[f.d.Paths.CtlSocket] = &Health{OK: true, Version: "dev"}
	f.health[f.d.Paths.PtydSocket] = &Health{OK: true, Version: "dev", Sessions: 3}
	if _, err := WriteUnits("linux", f.d.UnitsDir, UnitData{Binary: f.bin}); err != nil {
		t.Fatal(err)
	}
	f.sys.bins["systemctl"] = "/usr/bin/systemctl"
	for _, u := range []string{"relay.service", "relay-ptyd.service"} {
		f.sys.out["systemctl --user is-active "+u] = "active"
		f.sys.out["systemctl --user is-enabled "+u] = "enabled"
	}
	f.sys.out["loginctl show-user tester --property=Linger"] = "Linger=yes"
	r := f.d.Run(context.Background())
	for _, ch := range r.Checks {
		if ch.Status == StatusFail {
			t.Errorf("unexpected failure: %+v", ch)
		}
	}
	c := byID(r)
	if !strings.Contains(c["ptyd"].Detail, "3 session(s)") {
		t.Errorf("ptyd: %+v", c["ptyd"])
	}
	for _, id := range []string{"service-serve", "service-ptyd", "linger", "account", "binary", "dirs", "disk"} {
		if c[id].Status != StatusOK {
			t.Errorf("%s: %+v", id, c[id])
		}
	}
	if _, ok := c["dns"]; ok {
		t.Error("no DNS check without a domain")
	}
	if !r.OK {
		t.Error("report not OK")
	}
	var buf bytes.Buffer
	PrintReport(&UI{out: &buf}, r)
	if !strings.Contains(buf.String(), "✓") || !strings.Contains(buf.String(), "0 problem(s)") {
		t.Errorf("printed:\n%s", buf.String())
	}
}

func TestDoctorProblems(t *testing.T) {
	f := newDoctor(t, "[server]\nlisten = \":443\"\ndomain = \"relay.example.com\"\n[auth]\nuser = \"ann\"\n")
	if err := os.Chmod(f.d.Paths.ConfigFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.d.Paths.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.d.IPURL = ipify(t, "198.51.100.4").URL
	f.d.DiskFree = func(string) (uint64, error) { return 100 << 20, nil }
	f.d.Listen = func(string, string) (net.Listener, error) { return nil, syscall.EADDRINUSE }
	f.d.Getenv = func(string) string { return "/usr/bin" } // binary dir not on PATH
	r := f.d.Run(context.Background())
	c := byID(r)
	want := map[string]string{
		"config-perms": StatusFail,
		"dirs":         StatusFail,
		"account":      StatusWarn,
		"dns":          StatusFail,
		"disk":         StatusFail,
		"listen":       StatusFail,
		"binary":       StatusWarn,
	}
	for id, st := range want {
		if c[id].Status != st {
			t.Errorf("%s: status %q, want %q (%+v)", id, c[id].Status, st, c[id])
		}
		if c[id].Fix == "" && st != StatusOK {
			t.Errorf("%s: no fix hint", id)
		}
	}
	if !strings.Contains(c["dns"].Fix, "198.51.100.4") {
		t.Errorf("dns fix %q", c["dns"].Fix)
	}
	if !strings.Contains(c["config-perms"].Fix, "chmod 600") {
		t.Errorf("perm fix %q", c["config-perms"].Fix)
	}
	if !strings.Contains(c["listen"].Detail, "cap_net_bind_service") {
		t.Errorf("listen %+v", c["listen"])
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, r); err != nil || !strings.Contains(buf.String(), `"ok": false`) {
		t.Errorf("json: %v %s", err, buf.String())
	}
}

func TestDoctorBadConfig(t *testing.T) {
	f := newDoctor(t, "[server\n")
	c := byID(f.d.Run(context.Background()))
	if c["config"].Status != StatusFail {
		t.Errorf("%+v", c["config"])
	}
}

func TestDoctorVersionSkew(t *testing.T) {
	f := newDoctor(t, "")
	f.health[f.d.Paths.CtlSocket] = &Health{OK: true, Version: "v0.0.1"}
	c := f.d.checkSocket(context.Background(), "server", "Web server", f.d.Paths.CtlSocket, "/api/v1/health")
	// This test binary is "dev", which never reports skew.
	if c.Status != StatusOK {
		t.Errorf("%+v", c)
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[uint64]string{512: "512 B", 2048: "2.0 KB", 5 << 30: "5.0 GB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("%d: %q", n, got)
		}
	}
	if free, err := DiskFree(t.TempDir()); err != nil || free == 0 {
		t.Errorf("DiskFree: %d %v", free, err)
	}
}
