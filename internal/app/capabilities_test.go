package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
)

type capabilityAuth struct{}

func (capabilityAuth) Identify(r *http.Request) *server.Principal {
	if r.Header.Get("Authorization") == "Bearer synthetic" {
		return &server.Principal{User: "fixture", Method: "token"}
	}
	return nil
}
func capabilityGraph(t *testing.T, cfg *config.Config, home string) *App {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	run, err := os.MkdirTemp("", "rel134-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(run); err != nil {
			t.Error(err)
		}
	})
	a := &App{D: &core.Deps{Cfg: cfg, Paths: config.Paths{Home: home, RuntimeDir: run, DataDir: run}, Store: st, Bus: events.New(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Search: core.NewSearchRegistry()}}
	a.Router = server.NewRouter(capabilityAuth{}, func() []string { return []string{cfg.Origin()} })
	for _, wire := range []func(context.Context, *App) error{wirePreviews, wireApps, wireInfo} {
		if err := wire(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(a.Close)
	return a
}
func capabilityGET(t *testing.T, a *App, path string, authenticated bool, out any) int {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	if authenticated {
		r.Header.Set("Authorization", "Bearer synthetic")
	}
	w := httptest.NewRecorder()
	a.Router.ServeHTTP(w, r)
	if out != nil && w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code
}
func fixtureExecutable(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
}
func TestCapabilityPreviewV01(t *testing.T) {
	for _, tc := range []struct{ name, mode, host, origin, want string }{
		{"localhost", "auto", "localhost", "http://localhost:47790", "subdomain"},
		{"empty-host", "subdomain", "", "https://relay.example.test", "path"},
		{"https-pending", "auto", "relay.example.test", "https://relay.example.test", "path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			cfg := config.Defaults()
			cfg.Previews.Mode = tc.mode
			cfg.Previews.Host = tc.host
			cfg.Server.PublicURL = tc.origin
			cfg.Code.Enabled = false
			cfg.Desktop.Enabled = false
			a := capabilityGraph(t, cfg, t.TempDir())
			var got api.Info
			if code := capabilityGET(t, a, "/api/v1/info", true, &got); code != 200 {
				t.Fatal(code)
			}
			if got.Features.PreviewsMode != tc.want {
				t.Errorf("reported mode=%q; feature effective mode=%q", got.Features.PreviewsMode, tc.want)
			}
		})
	}
}
func TestCapabilityCodeStandaloneV01(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	home := t.TempDir()
	fixtureExecutable(t, filepath.Join(home, ".local/lib/code-server-4.100.0/bin/code-server"))
	cfg := config.Defaults()
	cfg.Previews.Mode = "off"
	cfg.Desktop.Enabled = false
	a := capabilityGraph(t, cfg, home)
	var got api.Info
	var list []api.App
	capabilityGET(t, a, "/api/v1/info", true, &got)
	capabilityGET(t, a, "/api/v1/apps", true, &list)
	if len(list) == 0 || list[0].ID != "code" || !list[0].Installed {
		t.Fatalf("feature did not discover synthetic standalone install")
	}
	if !got.Features.Code {
		t.Error("Info hid standalone Code accepted by apps")
	}
}
func TestCapabilityDesktopV01(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux desktop fixture")
	}
	for _, tc := range []struct {
		name    string
		bins    []string
		display string
		want    bool
	}{
		{"Xtigervnc", []string{"Xtigervnc", "openbox"}, ":7", true},
		{"missing-openbox", []string{"Xvnc"}, ":7", false},
		{"invalid-display", []string{"Xvnc", "openbox"}, "invalid", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			for _, name := range tc.bins {
				fixtureExecutable(t, filepath.Join(bin, name))
			}
			t.Setenv("PATH", bin)
			cfg := config.Defaults()
			cfg.Previews.Mode = "off"
			cfg.Code.Enabled = false
			cfg.Desktop.Display = tc.display
			a := capabilityGraph(t, cfg, t.TempDir())
			var got api.Info
			var st api.DesktopState
			capabilityGET(t, a, "/api/v1/info", true, &got)
			capabilityGET(t, a, "/api/v1/desktop", true, &st)
			if (st.State != "unavailable") != tc.want {
				t.Fatalf("feature state=%q", st.State)
			}
			if got.Features.Desktop != tc.want {
				t.Errorf("Info desktop=%v, feature available=%v", got.Features.Desktop, tc.want)
			}
		})
	}
}

func TestCapabilityPublicAPIsStayAuthenticatedAndAgree(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux desktop fixture")
	}
	home, bin := t.TempDir(), t.TempDir()
	for _, name := range []string{"Xvnc", "openbox", "code-server"} {
		fixtureExecutable(t, filepath.Join(bin, name))
	}
	t.Setenv("PATH", bin)
	cfg := config.Defaults()
	cfg.Previews.Mode, cfg.Previews.Host = "auto", "localhost"
	cfg.Server.PublicURL = "http://localhost:47790"
	cfg.Desktop.Display = ":7"
	a := capabilityGraph(t, cfg, home)
	for _, path := range []string{"/api/v1/info", "/api/v1/previews", "/api/v1/apps", "/api/v1/desktop"} {
		if code := capabilityGET(t, a, path, false, nil); code != 401 {
			t.Fatalf("unauthenticated %s: %d", path, code)
		}
	}
	var info api.Info
	var apps []api.App
	var desktop api.DesktopState
	var previews []api.Preview
	for _, tc := range []struct {
		path string
		out  any
	}{
		{"/api/v1/info", &info}, {"/api/v1/previews", &previews}, {"/api/v1/apps", &apps}, {"/api/v1/desktop", &desktop},
	} {
		if code := capabilityGET(t, a, tc.path, true, tc.out); code != 200 {
			t.Fatalf("authenticated %s: %d", tc.path, code)
		}
	}
	if !reflect.DeepEqual(info.Capabilities.Previews, a.D.Previews.PreviewCapability()) ||
		!reflect.DeepEqual(info.Capabilities.Desktop, desktop.Capability) {
		t.Fatal("Info disagrees with feature owner")
	}
	for _, app := range apps {
		if app.ID == "code" && (app.Capability == nil || !reflect.DeepEqual(*app.Capability, info.Capabilities.Code)) {
			t.Fatal("Code API disagrees with Info")
		}
		if app.ID == "desktop" && (app.Capability == nil || !reflect.DeepEqual(*app.Capability, desktop.Capability)) {
			t.Fatal("Desktop app disagrees with owner")
		}
	}
	if info.Capabilities.Code.State != "stopped" || desktop.Capability.State != "stopped" {
		t.Fatal("Installation was reported as running")
	}
	if err := os.Remove(filepath.Join(bin, "code-server")); err != nil {
		t.Fatal(err)
	}
	var next api.Info
	capabilityGET(t, a, "/api/v1/info", true, &next)
	if next.Capabilities.Code.Available || next.Capabilities.Code.State != "unavailable" || next.Features.Code {
		t.Fatal("Info cached removed Code")
	}
}
