package info

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

func TestSettingsConcurrentPartialPatch(t *testing.T) {
	f := newFixture(t)
	os.Mkdir(filepath.Join(f.home, "work"), 0o700)
	bodies := []string{`{"workspaceRoots":["~/work"]}`, `{"defaultShell":"/bin/sh"}`, `{"defaultCwd":"~/work"}`, `{"recordAgents":false}`, `{"claudeQuota":true}`, `{"idleMinutes":37}`}
	var wg sync.WaitGroup
	for _, body := range bodies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				w := f.do(t, "PATCH", "/api/v1/settings", body, true)
				if w.Code != 200 {
					t.Errorf("%d %s", w.Code, w.Body)
					return
				}
			}
		}()
	}
	wg.Wait()
	var state api.SettingsState
	w := f.do(t, "GET", "/api/v1/settings", "", true)
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.DefaultShell != "/bin/sh" || state.DefaultCwd != filepath.Join(f.home, "work") || state.RecordAgents || !state.ClaudeQuota || state.IdleMinutes != 37 || len(state.WorkspaceRoots) != 1 {
		t.Fatalf("lost partial updates: %+v", state)
	}
	if state.Effective.CodeIdleStop != "37m0s" || state.Effective.DesktopIdleStop != "37m0s" || state.Effective.TerminalStatus != "unavailable" {
		t.Fatalf("effective state: %+v", state.Effective)
	}
	if f.d.Cfg.Usage.ClaudeQuota {
		t.Fatal("mutated startup config")
	}
	if w := f.do(t, "PATCH", "/api/v1/settings", `{"claudeQuota":false}`, false); w.Code != 401 {
		t.Fatalf("unauthenticated patch %d", w.Code)
	}
}

func TestSettingsFailureRollbackAndUnknownPolicy(t *testing.T) {
	for _, mode := range []string{"validation", "unknown root", "unknown nested", "unknown app", "invalid TOML", "persistence"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			raw := "# retained until accepted save\n[terminal]\nrecord = \"off\"\n"
			switch mode {
			case "unknown root":
				raw = "unknown = 1\n" + raw
			case "unknown nested":
				raw += "\n[usage]\nfuture = true\n"
			case "unknown app":
				raw += "\n[[apps]]\nid = \"fixture\"\nfuture = 1\n"
			case "invalid TOML":
				raw = "invalid=["
			}
			os.MkdirAll(f.paths.ConfigDir, 0o700)
			os.WriteFile(f.paths.ConfigFile, []byte(raw), 0o600)
			before := f.d.RuntimeConfig()
			sub := f.d.Bus.Subscribe(2, nil)
			defer sub.Close()
			body := `{"claudeQuota":true}`
			want := 500
			if mode == "validation" {
				body = `{"workspaceRoots":["/synthetic-missing-rel135"]}`
				want = 400
			}
			if strings.HasPrefix(mode, "unknown") {
				want = 409
			}
			if mode == "persistence" {
				os.Chmod(f.paths.ConfigDir, 0o500)
				defer os.Chmod(f.paths.ConfigDir, 0o700)
			}
			w := f.do(t, "PATCH", "/api/v1/settings", body, true)
			if w.Code != want {
				t.Fatalf("%s: %d %s", mode, w.Code, w.Body)
			}
			after, _ := os.ReadFile(f.paths.ConfigFile)
			if string(after) != raw || !reflect.DeepEqual(before, f.d.RuntimeConfig()) {
				t.Fatal("failed save changed saved/effective state")
			}
			select {
			case <-sub.C:
				t.Fatal("failed save emitted audit")
			default:
			}
		})
	}
}

func TestSettingsPreservesKnownFieldsEnvironmentAndCommentPolicy(t *testing.T) {
	f := newFixture(t)
	raw := config.Defaults()
	raw.Auth.PasswordHash = "synthetic-hash"
	raw.Server.PublicURL = "https://saved.example.test"
	raw.Apps = []config.AppConfig{{ID: "fixture", Command: []string{"synthetic", "--arg"}, Env: map[string]string{"UNKNOWN_ENV": "preserve"}}}
	raw.Desktop.Apps = []config.DesktopAppConfig{{ID: "fixture", Command: []string{"synthetic"}}}
	raw.Notify.WebhookURL = "https://notify.example.test"
	raw.Terminal.Record = "all"
	raw.Terminal.DefaultCwd = "~"
	config.Save(f.paths, raw)
	var readErr error
	raw, readErr = loadSaved(f.paths, true)
	if readErr != nil {
		t.Fatal(readErr)
	}
	bytes, _ := os.ReadFile(f.paths.ConfigFile)
	os.WriteFile(f.paths.ConfigFile, append([]byte("# fixture comment\n"), bytes...), 0o600)
	t.Setenv("RELAY_PUBLIC_URL", "https://effective.example.test")
	loaded, err := config.Load(f.paths)
	if err != nil {
		t.Fatal(err)
	}
	f.d.Cfg = loaded
	f.d.Settings = config.NewRuntime(loaded, f.home)
	w := f.do(t, "PATCH", "/api/v1/settings", `{"idleMinutes":42,"recordAgents":true}`, true)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	after, err := loadSaved(f.paths, true)
	if err != nil {
		t.Fatal(err)
	}
	raw.Desktop.IdleStop = after.Desktop.IdleStop
	raw.Code.IdleStop = after.Code.IdleStop
	if !reflect.DeepEqual(raw, after) {
		t.Fatal("unrelated known fields lost")
	}
	bytes, _ = os.ReadFile(f.paths.ConfigFile)
	if strings.Contains(string(bytes), "fixture comment") || strings.Contains(string(bytes), "effective.example.test") || strings.Contains(string(bytes), f.home) {
		t.Fatal("comment/env/home persistence policy violated")
	}
	if got := f.svc.Info(context.Background()).PublicURL; got != "https://effective.example.test" {
		t.Fatalf("effective origin %s", got)
	}
	if w := f.do(t, "PATCH", "/api/v1/settings", `{"effective":{}}`, true); w.Code != 400 {
		t.Fatal("accepted read-only metadata")
	}
}

func TestSettingsReportsSavedAndDifferentConsumerValues(t *testing.T) {
	f := newFixture(t)
	raw := config.Defaults()
	raw.Usage.ClaudeQuota = true
	raw.Agents.WorkspaceRoots = []string{filepath.Join(f.home, "saved-work")}
	if err := os.Mkdir(raw.Agents.WorkspaceRoots[0], 0o700); err != nil {
		t.Fatal(err)
	}
	raw.Code.IdleStop.Duration = 0
	config.Save(f.paths, raw)
	var got api.SettingsState
	w := f.do(t, "GET", "/api/v1/settings", "", true)
	json.Unmarshal(w.Body.Bytes(), &got)
	if !got.ClaudeQuota || got.Effective.ClaudeQuota || got.Effective.CodeIdleStop != "2h0m0s" || got.Apply["claudeQuota"] != "restart-required" || got.Apply["idleMinutes"] != "restart-required" || got.Apply["workspaceRoots"] != "restart-required" || len(got.Effective.WorkspaceRoots) != 0 {
		t.Fatalf("saved and runtime conflated: %+v", got)
	}
}

func TestSettingsDaemonApplicationStates(t *testing.T) {
	for _, mode := range []string{"file", "startup", "old", "down", "different"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			dir, err := os.MkdirTemp("", "rset")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			socket := filepath.Join(dir, "p.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "old" {
					http.NotFound(w, r)
					return
				}
				if mode == "down" {
					w.WriteHeader(503)
					return
				}
				id := config.SettingsSourceID(f.paths)
				source := mode
				if mode == "different" {
					id = "different"
					source = "file"
				}
				json.NewEncoder(w).Encode(ptyclient.TerminalSettings{TerminalDefaults: api.TerminalDefaults{DefaultShell: "/bin/sh", DefaultCwd: f.home, RecordingMode: "off", Source: source}, ConfigID: id})
			})}
			go server.Serve(listener)
			defer server.Close()
			f.d.Pty = ptyclient.New(socket)
			want := map[string]string{"file": "next-session", "startup": "restart-required", "old": "restart-required", "down": "unavailable", "different": "different-config"}[mode]
			defaults, status := f.svc.terminalDefaults(context.Background())
			if status != want {
				t.Fatalf("%s: %s", mode, status)
			}
			if (mode == "old" || mode == "down") && defaults != nil {
				t.Fatal("invented daemon defaults")
			}
		})
	}
}
