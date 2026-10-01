package apps

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
)

func capExecutable(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
}
func capService(t *testing.T, configured string, enabled bool) (*Service, string, string) {
	t.Helper()
	d := testDeps(t, nil)
	if strings.HasPrefix(configured, "abs:") {
		configured = filepath.Join(d.Paths.Home, strings.TrimPrefix(configured, "abs:"))
	}
	d.Cfg.Code.Binary = configured
	d.Cfg.Code.Enabled = enabled
	d.Cfg.Desktop.Enabled = false
	bin := filepath.Join(d.Paths.Home, "path")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	s, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, d.Paths.Home, bin
}

func TestCodeCapabilityDiscoveryAndRemoval(t *testing.T) {
	for _, tc := range []struct {
		name, layout, configured, source, implementation string
		executable, available                            bool
	}{
		{"configured-absolute", ".local/custom", "abs:.local/custom", "configured", "code-server", true, true},
		{"configured-PATH-name", "path/custom", "custom", "configured", "code-server", true, true},
		{"configured-home", ".local/custom", "~/.local/custom", "configured", "code-server", true, true},
		{"configured-local-name", ".local/bin/custom", "custom", "configured", "code-server", true, true},
		{"PATH", "path/code-server", "", "path", "code-server", true, true},
		{"PATH-openvscode", "path/openvscode-server", "", "path", "openvscode-server", true, true},
		{"local-bin", ".local/bin/code-server", "", "local-bin", "code-server", true, true},
		{"standalone", ".local/lib/code-server-4.100.0/bin/code-server", "", "standalone", "code-server", true, true},
		{"missing", "", "", "", "", false, false},
		{"non-executable", ".local/bin/code-server", "", "", "", false, false},
		{"configured-missing-no-fallback", ".local/bin/code-server", "~/missing", "configured", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, home, _ := capService(t, tc.configured, true)
			if tc.layout != "" {
				capExecutable(t, filepath.Join(home, tc.layout))
				if !tc.executable {
					if err := os.Chmod(filepath.Join(home, tc.layout), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			cap := s.CodeCapability()
			if cap.Available != tc.available || cap.Source != tc.source || cap.Implementation != tc.implementation {
				t.Fatalf("capability=%+v", cap)
			}
			app := s.appState(s.apps["code"])
			if app.Capability == nil || !reflect.DeepEqual(*app.Capability, cap) || app.Installed != cap.Available {
				t.Fatal("Apps disagrees with owner")
			}
			if cap.Available && cap.State != "stopped" {
				t.Fatal("installation implied running")
			}
			if s.apps["code"].proc.PID() != 0 {
				t.Fatal("query launched Code")
			}
			if tc.available {
				if err := os.Remove(filepath.Join(home, tc.layout)); err != nil {
					t.Fatal(err)
				}
				if got := s.CodeCapability(); got.Available || got.State != "unavailable" {
					t.Fatal("removed binary remained cached")
				}
			}
		})
	}
}

func TestCodeCapabilityRefreshAndNextStartSelection(t *testing.T) {
	s, home, bin := capService(t, "", true)
	if s.CodeCapability().Available {
		t.Fatal("missing tool available")
	}
	standalone := filepath.Join(home, ".local/lib/code-server-4.10.0/bin/code-server")
	capExecutable(t, standalone)
	if got := s.CodeCapability(); !got.Available || got.Source != "standalone" {
		t.Fatal("negative result cached")
	}
	newer := filepath.Join(home, ".local/lib/code-server-4.100.0/bin/code-server")
	capExecutable(t, newer)
	code := s.apps["code"]
	argv, _, err := code.proc.spec.ResolveCommand()
	if err != nil || argv[0] != newer {
		t.Fatal("newest standalone not selected")
	}
	pathBin := filepath.Join(bin, "openvscode-server")
	capExecutable(t, pathBin)
	code.proc.mu.Lock()
	code.proc.setState(stateRunning, "")
	code.proc.mu.Unlock()
	t.Cleanup(func() { code.proc.mu.Lock(); code.proc.setState(stateStopped, ""); code.proc.mu.Unlock() })
	if got := s.CodeCapability(); got.State != "running" || got.Implementation != "code-server" || got.Source != "standalone" {
		t.Fatal("changed installation replaced active selection")
	}
	if err := os.Remove(newer); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(standalone); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(pathBin); err != nil {
		t.Fatal(err)
	}
	if got := s.CodeCapability(); got.Available || got.State != "running" {
		t.Fatal("removal changed actual lifecycle")
	}
	code.proc.mu.Lock()
	code.proc.setState(stateStopped, "")
	code.proc.mu.Unlock()
	if _, _, err := code.proc.spec.ResolveCommand(); err == nil {
		t.Fatal("start resolver accepted removed tools")
	}
	capExecutable(t, pathBin)
	argv, _, err = code.proc.spec.ResolveCommand()
	if err != nil || argv[0] != pathBin {
		t.Fatal("next start did not refresh selection")
	}
	if err := os.Chmod(pathBin, 0600); err != nil {
		t.Fatal(err)
	}
	if s.CodeCapability().Available {
		t.Fatal("permission removal cached")
	}
	other := t.TempDir()
	capExecutable(t, filepath.Join(other, "code-server"))
	t.Setenv("PATH", other)
	if got := s.CodeCapability(); !got.Available || got.Source != "path" {
		t.Fatal("PATH change not refreshed")
	}
}

func TestCodeCapabilityLifecycleAndDisabled(t *testing.T) {
	for _, state := range []string{stateStopped, stateStarting, stateRunning, stateError} {
		t.Run(state, func(t *testing.T) {
			s, home, _ := capService(t, "", true)
			capExecutable(t, filepath.Join(home, ".local/bin/code-server"))
			p := s.apps["code"].proc
			p.mu.Lock()
			p.setState(state, "synthetic-private-output")
			p.mu.Unlock()
			cap := s.CodeCapability()
			want := state
			if state == stateError {
				want = "failed"
			}
			if cap.State != want {
				t.Fatalf("state=%s want %s", cap.State, want)
			}
			payload, err := json.Marshal(s.appState(s.apps["code"]))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(payload), "synthetic-private-output") {
				t.Fatal("raw child output leaked")
			}
			p.mu.Lock()
			p.setState(stateStopped, "")
			p.mu.Unlock()
		})
	}
	s, home, _ := capService(t, "", false)
	capExecutable(t, filepath.Join(home, ".local/bin/code-server"))
	if got := s.CodeCapability(); !got.Available || got.Enabled || got.State != "disabled" {
		t.Fatal("disabled collapsed with unavailable")
	}
	if s.apps["code"] != nil {
		t.Fatal("disabled Code added to legacy Apps")
	}
}

func TestDesktopCapabilityPrerequisitesAndRefresh(t *testing.T) {
	for _, tc := range []struct {
		name, platform, display, runtime string
		tools                            []string
		missing                          string
	}{
		{"Xvnc", "linux", ":7", "", []string{"Xvnc", "openbox"}, ""},
		{"Xtigervnc", "linux", ":7", "", []string{"Xtigervnc", "openbox"}, ""},
		{"openbox-missing", "linux", ":7", "", []string{"Xvnc"}, "openbox"},
		{"vnc-missing", "linux", ":7", "", []string{"openbox"}, "vnc"},
		{"non-Linux", "darwin", ":7", "", []string{"Xvnc", "openbox"}, "linux"},
		{"display-invalid", "linux", "host:0", "", []string{"Xvnc", "openbox"}, "display"},
		{"path-long", "linux", ":7", "/tmp/" + strings.Repeat("a", 101), []string{"Xvnc", "openbox"}, "runtime-path-length"},
		{"path-characters", "linux", ":7", "/tmp/unsupported space", []string{"Xvnc", "openbox"}, "runtime-path-characters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDeps(t, nil)
			d.Cfg.Desktop.Display = tc.display
			if tc.runtime != "" {
				d.Paths.RuntimeDir = tc.runtime
			}
			bin := t.TempDir()
			for _, name := range tc.tools {
				capExecutable(t, filepath.Join(bin, name))
			}
			t.Setenv("PATH", bin)
			k, err := newDesktop(d, d.Log, func(name string) (string, error) { return lookExecutable(bin, name) })
			if err != nil {
				t.Fatal(err)
			}
			k.platform = tc.platform
			cap := k.Capability()
			if cap.Available != (tc.missing == "") {
				t.Fatalf("%+v", cap)
			}
			if tc.missing != "" && !containsMissing(cap.Missing, tc.missing) {
				t.Fatalf("missing identifier not reported: %+v", cap)
			}
			if cap.Available && cap.State != "stopped" {
				t.Fatal("tool detection implied running")
			}
			if !reflect.DeepEqual(k.State().Capability, cap) || !reflect.DeepEqual(*desktopApp(k.State()).Capability, cap) {
				t.Fatal("desktop consumers disagree")
			}
			if k.xvnc.PID() != 0 || k.session.PID() != 0 {
				t.Fatal("query launched desktop")
			}
			if tc.missing == "" {
				if err := os.Remove(filepath.Join(bin, tc.tools[0])); err != nil {
					t.Fatal(err)
				}
				if k.Capability().Available {
					t.Fatal("removed X tool cached")
				}
				capExecutable(t, filepath.Join(bin, tc.tools[0]))
				if !k.Capability().Available {
					t.Fatal("installed X tool hidden")
				}
			}
			// A manager that never started owns no files or display cleanup.
			marker := filepath.Join(d.Paths.DataDir, "marker")
			capExecutable(t, marker)
			if err := k.Stop(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatal("query cleanup touched unrelated fixture")
			}
		})
	}
}
func lookExecutable(bin, name string) (string, error) {
	p := filepath.Join(bin, name)
	if isExecutable(p) {
		return p, nil
	}
	return "", os.ErrNotExist
}
func containsMissing(missing []string, want string) bool {
	for _, x := range missing {
		if x == want {
			return true
		}
	}
	return false
}

func TestDesktopCapabilityLifecycleAndFailedCleanup(t *testing.T) {
	for _, tc := range []struct {
		x, session, want string
		failed           bool
	}{
		{stateStopped, stateStopped, "stopped", false},
		{stateStarting, stateStopped, "starting", false},
		{stateRunning, stateStarting, "starting", false},
		{stateRunning, stateRunning, "running", false},
		{stateError, stateStopped, "failed", false},
		{stateRunning, stateError, "failed", false},
		{stateStopped, stateStopped, "failed", true},
	} {
		d := testDeps(t, nil)
		k, err := newDesktop(d, d.Log, fakeLookPath("Xvnc", "openbox"))
		if err != nil {
			t.Fatal(err)
		}
		k.platform = "linux"
		k.failed = tc.failed
		k.xvnc.mu.Lock()
		k.xvnc.setState(tc.x, "synthetic-private-output")
		k.xvnc.mu.Unlock()
		k.session.mu.Lock()
		k.session.setState(tc.session, "synthetic-private-output")
		k.session.mu.Unlock()
		if got := k.Capability(); got.State != tc.want {
			t.Fatalf("%s/%s: %+v", tc.x, tc.session, got)
		}
		payload, err := json.Marshal(k.State())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), "synthetic-private-output") {
			t.Fatal("desktop output leaked")
		}
	}
	d := testDeps(t, func(c *config.Config) { c.Desktop.Enabled = false })
	k, err := newDesktop(d, d.Log, fakeLookPath("Xvnc", "openbox"))
	if err != nil {
		t.Fatal(err)
	}
	k.platform = "linux"
	if got := k.Capability(); got.State != "disabled" || !got.Available {
		t.Fatal("disabled lost independent availability")
	}
}

func TestDesktopFailedSyntheticSessionRemainsFailed(t *testing.T) {
	d := testDeps(t, nil)
	k, err := newDesktop(d, d.Log, fakeLookPath("Xvnc", "openbox"))
	if err != nil {
		t.Fatal(err)
	}
	k.platform = "linux"
	k.scanner = &runningScanner{procDir: t.TempDir()}
	k.xvnc = newProc(procSpec{Name: "synthetic-X-stand-in", Argv: helperArgv("serve"), Env: helperEnv("RELAY_APP_SOCKET=" + k.sock), Ready: socketReady(k.sock), StopGrace: time.Second}, k.changed)
	k.session = newProc(procSpec{Name: "synthetic-session", Argv: helperArgv("exit"), Env: helperEnv(), StopGrace: time.Second}, k.sessionChanged)
	t.Cleanup(func() {
		if err := k.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := k.Start(ctx); err == nil {
		t.Fatal("failed synthetic session accepted")
	}
	waitFor(t, func() bool { return k.xvnc.PID() == 0 && k.session.PID() == 0 })
	if got := k.State(); got.Capability.State != "failed" || got.Error == "" || got.State != "stopped" {
		t.Fatalf("cleanup erased failure: %+v", got)
	}
	if err := k.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if got := k.Capability(); got.State != "stopped" {
		t.Fatal("explicit stop did not clear failure")
	}
}

func TestCapabilityRefreshEventsAndConcurrentQueries(t *testing.T) {
	s, home, _ := capService(t, "", true)
	sub := s.d.Bus.Subscribe(32, nil)
	defer sub.Close()
	s.refreshCapabilities()
	select {
	case <-sub.C:
		t.Fatal("unchanged capability published")
	default:
	}
	capExecutable(t, filepath.Join(home, ".local/bin/code-server"))
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 40 {
				s.CodeCapability()
				s.DesktopCapability()
				s.List()
			}
		}()
	}
	s.refreshCapabilities()
	wg.Wait()
	select {
	case ev := <-sub.C:
		if ev.Type != api.EvCapabilitiesChanged {
			t.Fatal("wrong change event")
		}
	default:
		t.Fatal("install did not invalidate clients")
	}
	s.refreshCapabilities()
	select {
	case <-sub.C:
		t.Fatal("same capability re-emitted")
	default:
	}
}

func TestCodeCapabilitySyntheticLifecycle(t *testing.T) {
	s, home, _ := capService(t, "", true)
	executable := filepath.Join(home, ".local/bin/code-server")
	capExecutable(t, executable)
	a := s.apps["code"]
	sock := filepath.Join(s.d.Paths.RuntimeDir, "synthetic-code.sock")
	gate := make(chan struct{})
	var fail atomic.Bool
	if err := os.MkdirAll(s.d.Paths.RuntimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	ready := socketReady(sock)
	a.proc = newProc(procSpec{
		Name: "synthetic-Code-stand-in", Dir: home, StopGrace: time.Second,
		ResolveCommand: func() ([]string, []string, error) {
			if _, _, err := s.resolveCode(a, sock); err != nil {
				return nil, nil, err
			}
			mode := "serve"
			if fail.Load() {
				mode = "exit"
			}
			return helperArgv(mode), helperEnv("RELAY_APP_SOCKET=" + sock), nil
		},
		Ready: func(ctx context.Context) error {
			select {
			case <-gate:
				return ready(ctx)
			default:
				return os.ErrNotExist
			}
		},
	}, func() { s.publishApp(a) })
	a.proc.StartAsync()
	waitFor(t, func() bool { return a.proc.PID() != 0 })
	if got := s.CodeCapability(); got.State != "starting" || !got.Available {
		t.Fatal("Starting owner was hidden")
	}
	close(gate)
	waitFor(t, func() bool { return s.CodeCapability().State == "running" })
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if got := s.CodeCapability(); got.Available || got.State != "running" {
		t.Fatal("Removal overrode ready owner")
	}
	if err := a.proc.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := s.CodeCapability(); got.State != "unavailable" {
		t.Fatal("Stop retained active state")
	}
	capExecutable(t, executable)
	fail.Store(true)
	if err := a.proc.Start(t.Context()); err == nil {
		t.Fatal("Failed synthetic child accepted")
	}
	waitFor(t, func() bool { return s.CodeCapability().State == "failed" })
	if got := s.appState(a); got.State != "error" || got.Error != "Code could not start or exited unexpectedly." {
		t.Fatal("Failure contract changed")
	}
	if err := a.proc.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := s.CodeCapability(); got.State != "stopped" {
		t.Fatal("Explicit stop did not clear failure")
	}
}

func TestDesktopQueryClosePreservesUnownedRuntimeFiles(t *testing.T) {
	d := testDeps(t, nil)
	k, err := newDesktop(d, d.Log, fakeLookPath("Xvnc", "openbox"))
	if err != nil {
		t.Fatal(err)
	}
	files := []string{k.sock, filepath.Join(k.dir, "dbus-address")}
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("synthetic-existing-owner"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	k.Capability()
	k.State()
	if err := k.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil || string(b) != "synthetic-existing-owner" {
			t.Fatal("Query-only manager removed another owner's fixture file")
		}
	}
}

func TestCodeConfiguredRelativePathResolvesBeforeChildCwd(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	s, _, _ := capService(t, "bin/custom-code", true)
	bin := filepath.Join(cwd, "bin/custom-code")
	capExecutable(t, bin)
	if !s.CodeCapability().Available {
		t.Fatal("Configured relative binary hidden")
	}
	argv, _, err := s.apps["code"].proc.spec.ResolveCommand()
	if err != nil || len(argv) == 0 || argv[0] != bin {
		t.Fatal("Detected binary differs from the path executed in child cwd")
	}
}

func TestDesktopFailedPreparationDoesNotClaimRuntimeOwnership(t *testing.T) {
	d := testDeps(t, nil)
	k, err := newDesktop(d, d.Log, fakeLookPath("Xvnc", "openbox"))
	if err != nil {
		t.Fatal(err)
	}
	k.platform = "linux"
	files := []string{k.sock, filepath.Join(k.dir, "dbus-address")}
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("synthetic-other-owner"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	k.xvnc = newProc(procSpec{Name: "synthetic-prepare-failure", BeforeStart: func() error { return errors.New("synthetic display already owned") }}, k.changed)
	if err := k.Start(t.Context()); err == nil {
		t.Fatal("Preparation failure accepted")
	}
	if got := k.Capability(); got.State != "failed" {
		t.Fatal("Failed preparation was hidden")
	}
	if err := k.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := k.Capability(); got.State != "stopped" {
		t.Fatal("Explicit stop retained failed attempt")
	}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil || string(b) != "synthetic-other-owner" {
			t.Fatal("Failed attempt cleaned another owner's fixture")
		}
	}
}
