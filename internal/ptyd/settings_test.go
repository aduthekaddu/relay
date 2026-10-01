package ptyd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

func reloadFixture(t *testing.T) (*testEnv, string, string, *config.Config) {
	t.Helper()
	paths := testPaths(t)
	paths.ConfigFile = filepath.Join(paths.ConfigDir, "relay.toml")
	a := filepath.Join(paths.Home, "a")
	b := filepath.Join(paths.Home, "b")
	if err := os.Mkdir(a, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(b, 0o700); err != nil {
		t.Fatal(err)
	}
	shell := filepath.Join(paths.Home, "shell-a")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nexec /bin/sh \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(paths)
	cfg.Terminal.Shell = shell
	cfg.Terminal.DefaultCwd = a
	if err := config.Save(paths, cfg); err != nil {
		t.Fatal(err)
	}
	e := startEnv(t, paths, func(c *config.Config) { c.Terminal = cfg.Terminal })
	// Set once before any settings/create request; active sessions never use it.
	e.d.loadTerminal = func(ctx context.Context) (config.TerminalConfig, error) {
		if err := ctx.Err(); err != nil {
			return config.TerminalConfig{}, err
		}
		next, err := config.Load(paths)
		if err != nil {
			return config.TerminalConfig{}, err
		}
		return next.Terminal, nil
	}
	return e, a, b, cfg
}

func TestSettingsNewSessionDefaultsPreserveExisting(t *testing.T) {
	e, a, b, cfg := reloadFixture(t)
	old := e.create(ptyclient.CreateSpec{})
	shellB := filepath.Join(e.paths.Home, "shell-b")
	if err := os.WriteFile(shellB, []byte("#!/bin/sh\nexec /bin/sh \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg.Terminal.Shell = shellB
	cfg.Terminal.DefaultCwd = b
	cfg.Terminal.Record = "agents"
	if err := config.Save(e.paths, cfg); err != nil {
		t.Fatal(err)
	}
	defaults, err := e.c.Defaults(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if defaults.DefaultShell != shellB || defaults.DefaultCwd != b || defaults.RecordingMode != "agents" || defaults.Source != "file" || defaults.ConfigID != config.SettingsSourceID(e.paths) {
		t.Fatalf("defaults %+v", defaults)
	}
	next := e.create(ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Kind: api.KindAgent, Agent: "synthetic"}})
	if next.Command[0] != shellB || next.Cwd != b || !next.Recording {
		t.Fatalf("next %+v", next)
	}
	still, err := e.c.Get(context.Background(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still.Pid != old.Pid || still.Activity == api.ActivityExited || still.Cwd != a || still.Recording {
		t.Fatal("changed existing session")
	}
	if err := e.c.Input(context.Background(), old.ID, []byte("printf 'REL135_ALIVE\\n'\n")); err != nil {
		t.Fatal(err)
	}
	e.waitSession(old.ID, 3*time.Second, "surviving output", func(s *api.TerminalSession) bool {
		snap, err := e.c.Snapshot(context.Background(), old.ID, 20)
		return err == nil && strings.Contains(snap.Text, "REL135_ALIVE")
	})
	off := false
	override := e.create(ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Kind: api.KindAgent, Cwd: a, Command: []string{"/bin/sh"}, Record: &off}})
	if override.Cwd != a || override.Recording || override.Command[0] != "/bin/sh" {
		t.Fatal("explicit session overrides lost")
	}
	if err := os.WriteFile(e.paths.ConfigFile, []byte("invalid=["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.c.Create(context.Background(), ptyclient.CreateSpec{}); err == nil {
		t.Fatal("created with unreadable defaults")
	}
	still, err = e.c.Get(context.Background(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still.Activity == api.ActivityExited {
		t.Fatal("failed reload killed existing session")
	}
}

func TestSettingsCreationUsesOneSnapshot(t *testing.T) {
	e, a, b, cfg := reloadFixture(t)
	second := config.Clone(cfg)
	second.Terminal.DefaultCwd = b
	second.Terminal.Record = "all"
	second.Terminal.Shell = filepath.Join(e.paths.Home, "shell-b")
	if err := os.WriteFile(second.Terminal.Shell, []byte("#!/bin/sh\nexec /bin/sh \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			c := cfg
			if i%2 == 0 {
				c = second
			}
			if err := config.Save(e.paths, c); err != nil {
				t.Error(err)
			}
		}
	}()
	for i := 0; i < 12; i++ {
		s := e.create(ptyclient.CreateSpec{})
		first := s.Command[0] == cfg.Terminal.Shell && s.Cwd == a && !s.Recording
		other := s.Command[0] == second.Terminal.Shell && s.Cwd == b && s.Recording
		if !first && !other {
			t.Fatalf("mixed defaults %+v", s)
		}
	}
	wg.Wait()
}

func TestSettingsUnsupportedModeRefusesOnlyNewSession(t *testing.T) {
	e, _, _, cfg := reloadFixture(t)
	old := e.create(ptyclient.CreateSpec{})
	cfg.Terminal.Record = "unsupported"
	if err := config.Save(e.paths, cfg); err != nil {
		t.Fatal(err)
	}
	_, err := e.c.Create(context.Background(), ptyclient.CreateSpec{})
	var status *ptyclient.StatusError
	if !errors.As(err, &status) || status.Status != 503 {
		t.Fatalf("unsupported mode %v", err)
	}
	still, err := e.c.Get(context.Background(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still.Activity == api.ActivityExited {
		t.Fatal("unsupported config killed session")
	}
}
