package info

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
)

type allowAll struct{}

func (allowAll) Identify(r *http.Request) *server.Principal {
	if r.Header.Get("Authorization") == "Bearer ok" {
		return &server.Principal{User: "owner", Method: "token"}
	}
	return nil
}

type fixture struct {
	svc   *Service
	d     *core.Deps
	h     http.Handler
	home  string
	paths config.Paths
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	paths := config.Paths{Home: home, ConfigDir: filepath.Join(home, "cfg"), ConfigFile: filepath.Join(home, "cfg", "relay.toml")}
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	d := &core.Deps{
		Cfg: config.Defaults(), Paths: paths, Store: st, Bus: events.New(),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "1.2.3",
	}
	svc, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	svc.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	rt := server.NewRouter(allowAll{}, func() []string { return nil })
	svc.Routes(rt)
	return &fixture{svc: svc, d: d, h: rt, home: home, paths: paths}
}

func (f *fixture) do(t *testing.T, method, path, body string, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if auth {
		r.Header.Set("Authorization", "Bearer ok")
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func TestHealthIsPublic(t *testing.T) {
	f := newFixture(t)
	w := f.do(t, "GET", "/api/v1/health", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), `"version":"1.2.3"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := f.do(t, "GET", "/api/v1/info", "", false); w.Code != 401 {
		t.Fatalf("info without auth: %d", w.Code)
	}
	if w := f.do(t, "GET", "/api/v1/settings", "", false); w.Code != 401 {
		t.Fatalf("settings without auth: %d", w.Code)
	}
}

func TestInfoFeatures(t *testing.T) {
	f := newFixture(t)
	bin := filepath.Join(f.home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// code-server only in ~/.local/bin; rg "in PATH".
	if err := os.WriteFile(filepath.Join(bin, "code-server"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.svc.lookPath = func(name string) (string, error) {
		if name == "rg" {
			return "/usr/bin/rg", nil
		}
		return "", errors.New("not found")
	}
	f.d.Apps = fixtureCapabilities{}
	f.d.Cfg.Code.Enabled = true
	f.d.Cfg.Desktop.Enabled = true
	if err := f.d.Store.SetKV(context.Background(), "notify.vapid.public", "BPk"); err != nil {
		t.Fatal(err)
	}
	w := f.do(t, "GET", "/api/v1/info", "", true)
	var info api.Info
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	ft := info.Features
	if !ft.Code || ft.Desktop || !ft.Ripgrep || !ft.Push {
		t.Fatalf("features = %+v", ft)
	}
	if info.Version != "1.2.3" || info.Home != f.home {
		t.Fatalf("info = %+v", info)
	}
}

func TestPasskeysUsable(t *testing.T) {
	cases := map[string]bool{
		"http://localhost:7777":      true,
		"http://app.localhost":       true,
		"https://relay.example.com":  true,
		"http://relay.example.com":   false,
		"https://192.168.1.4:7777":   false,
		"http://127.0.0.1:7777":      false,
		"::not a url":                false,
		"https://[2001:db8::1]:8443": false,
	}
	for origin, want := range cases {
		if got := passkeysUsable(origin); got != want {
			t.Errorf("passkeysUsable(%q) = %v, want %v", origin, got, want)
		}
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	f := newFixture(t)
	// A pre-existing field outside Settings must survive the save.
	pre := config.Defaults()
	pre.Server.Domain = "relay.example"
	pre.Terminal.Record = "all"
	if err := config.Save(f.paths, pre); err != nil {
		t.Fatal(err)
	}
	f.d.Settings = config.NewRuntime(pre, f.home)
	sub := f.d.Bus.Subscribe(4, nil)
	defer sub.Close()

	proj := filepath.Join(f.home, "code")
	if err := os.Mkdir(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.home, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"workspaceRoots":["~/code/",  "~/code", "~/other", "  "],"defaultCwd":"~/code","recordAgents":true,"idleMinutes":45,"claudeQuota":true}`
	w := f.do(t, "PATCH", "/api/v1/settings", body, true)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var got api.Settings
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.WorkspaceRoots) != 2 || got.IdleMinutes != 45 || !got.RecordAgents || !got.ClaudeQuota {
		t.Fatalf("settings = %+v", got)
	}
	// The live config is expanded, the file keeps "~/".
	if got.DefaultCwd != proj {
		t.Fatalf("live cwd = %q, want %q", got.DefaultCwd, proj)
	}
	raw, err := os.ReadFile(f.paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"relay.example", "~/code", "all"} {
		if !strings.Contains(s, want) {
			t.Fatalf("relay.toml lost %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, f.home) {
		t.Fatalf("relay.toml has an expanded home path:\n%s", s)
	}
	select {
	case ev := <-sub.C:
		if ev.Type != core.BusAudit {
			t.Fatalf("event %q", ev.Type)
		}
	default:
		t.Fatal("no audit event published")
	}

	// recordAgents=false turns recording off entirely.
	if w := f.do(t, "PATCH", "/api/v1/settings", `{"recordAgents":false}`, true); w.Code != 200 || f.d.RuntimeConfig().Terminal.Record != "off" {
		t.Fatalf("%d record=%q", w.Code, f.d.RuntimeConfig().Terminal.Record)
	}
	if w := f.do(t, "GET", "/api/v1/settings", "", true); !strings.Contains(w.Body.String(), `"recordAgents":false`) {
		t.Fatalf("GET = %s", w.Body)
	}
}

func TestSettingsValidation(t *testing.T) {
	f := newFixture(t)
	cases := map[string]string{
		"relative root":   `{"workspaceRoots":["code"]}`,
		"missing cwd":     `{"defaultCwd":"~/does-not-exist"}`,
		"relative shell":  `{"defaultShell":"bash"}`,
		"non-exec shell":  `{"defaultShell":"/etc/hostname-does-not-exist"}`,
		"negative idle":   `{"idleMinutes":-1}`,
		"huge idle":       `{"idleMinutes":99999999}`,
		"unknown field":   `{"bogus":1}`,
		"not json":        `{`,
		"newline in root": `{"workspaceRoots":["/a\nb"]}`,
		"wrong type":      `{"idleMinutes":"ten"}`,
		"too many roots":  `{"workspaceRoots":[` + strings.Repeat(`"/a",`, maxWorkspaceRoots) + `"/b"]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if w := f.do(t, "PATCH", "/api/v1/settings", body, true); w.Code != 400 {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
	if _, err := os.Stat(f.paths.ConfigFile); !os.IsNotExist(err) {
		t.Fatal("rejected patches must not write relay.toml")
	}
}

func TestCleanPath(t *testing.T) {
	cases := map[string]string{
		"~":           "~",
		"~/":          "~",
		"~/a/../b/":   "~/b",
		"~/../../etc": "~/etc",
		"/srv//x/":    "/srv/x",
	}
	for in, want := range cases {
		if got := cleanPath(in); got != want {
			t.Errorf("cleanPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func infoDaemonFixture(t *testing.T, f *fixture, handler http.Handler) {
	t.Helper()
	dir, err := os.MkdirTemp("", "rs135-info-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	socket := filepath.Join(dir, "p.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("fixture daemon: %v", err)
		}
	})
	f.d.Pty = ptyclient.New(socket)
}

func TestInfoRecordingProbeDeadline(t *testing.T) {
	f := newFixture(t)
	var requests atomic.Int64
	infoDaemonFixture(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		<-r.Context().Done()
	}))
	for _, budget := range []time.Duration{500 * time.Millisecond, 80 * time.Millisecond} {
		ctx := context.Background()
		cancel := func() {}
		if budget == 80*time.Millisecond {
			ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
		}
		start := time.Now()
		got := f.svc.Info(ctx)
		elapsed := time.Since(start)
		cancel()
		t.Logf("stalled owned unix daemon: info elapsed=%v budget=%v", elapsed, budget)
		if elapsed >= budget {
			t.Errorf("Info blocked %v on recording defaults; budget %v", elapsed, budget)
		}
		if got.Features.Recording {
			t.Error("unavailable recording defaults reported enabled")
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("fixture daemon requests=%d, want 2", requests.Load())
	}
}

func TestInfoRecordingModesRemainFresh(t *testing.T) {
	f := newFixture(t)
	var mode atomic.Value
	mode.Store("off")
	infoDaemonFixture(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(ptyclient.TerminalSettings{
			TerminalDefaults: api.TerminalDefaults{RecordingMode: mode.Load().(string), Source: "file"},
			ConfigID:         config.SettingsSourceID(f.paths),
		}); err != nil {
			t.Error(err)
		}
	}))
	for _, value := range []string{"", "off", "agents", "all", "off"} {
		mode.Store(value)
		want := value != "" && value != "off"
		if got := f.svc.Info(context.Background()).Features.Recording; got != want {
			t.Fatalf("recording mode %q: got %v, want %v", value, got, want)
		}
	}
}

type fixtureCapabilities struct{}

func (fixtureCapabilities) CodeCapability() api.AppCapability {
	return api.AppCapability{Enabled: true, Available: true, State: "stopped", Missing: []string{}}
}
func (fixtureCapabilities) DesktopCapability() api.AppCapability {
	return api.AppCapability{Enabled: true, Available: false, State: "unavailable", Missing: []string{"vnc"}}
}
