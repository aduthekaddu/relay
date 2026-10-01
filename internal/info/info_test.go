package info

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
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

func TestPreviewsMode(t *testing.T) {
	cases := []struct {
		mode, host, origin string
		wantMode, wantHost string
	}{
		{"off", "dev.example", "https://x", "off", ""},
		{"path", "dev.example", "https://x", "path", ""},
		{"subdomain", "dev.example", "http://x", "subdomain", "dev.example"},
		{"auto", "dev.example", "https://x", "subdomain", "dev.example"},
		{"", "dev.example", "http://x", "path", ""},
		{"auto", "", "https://x", "path", ""},
	}
	for _, tc := range cases {
		m, h := previewsMode(tc.mode, tc.host, tc.origin)
		if m != tc.wantMode || h != tc.wantHost {
			t.Errorf("previewsMode(%q,%q,%q) = %q,%q", tc.mode, tc.host, tc.origin, m, h)
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
