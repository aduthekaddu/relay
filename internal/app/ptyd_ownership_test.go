package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/ptyd"
	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
)

// Start managed serve wiring before the owner exists, then connect a late
// owner. Closing and reconstructing serve must preserve that owner's PTY.
func TestManagedServeLeavesDaemonOwnership(t *testing.T) {
	root, err := os.MkdirTemp("", "r26a-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	t.Setenv("RELAY_HOME", root)
	t.Setenv("RELAY_CONFIG", "")
	t.Setenv("RELAY_NO_PTYD", "1")
	paths, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	paths.Home = root
	if err = paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Terminal.Shell = "/bin/sh"
	cfg.Terminal.DefaultCwd = root
	cfg.Terminal.Record = "off"
	cfg.Terminal.ImportTmux = false
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	d := &core.Deps{Cfg: cfg, Paths: paths, Store: st, Bus: events.New(), Log: log, Pty: ptyclient.New(paths.PtydSocket)}
	wire := func() *App {
		a := &App{D: d}
		a.Router = server.NewRouter(nil, a.Origins)
		if err := wireTerminal(context.Background(), a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	first := wire()
	if err := d.Pty.Health(context.Background()); !errors.Is(err, ptyclient.ErrUnavailable) {
		t.Fatalf("missing owner: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.RuntimeDir, "ptyd.lock")); !os.IsNotExist(err) {
		t.Fatalf("managed serve created daemon lock: %v", err)
	}
	dm, err := ptyd.New(ptyd.Options{Cfg: cfg, Paths: paths, Log: log, LoginEnv: new(bool)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dm.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(8 * time.Second):
			t.Error("owned daemon did not stop")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for d.Pty.Health(ctx) != nil {
		if time.Now().After(deadline) {
			t.Fatal("late daemon unavailable")
		}
		time.Sleep(20 * time.Millisecond)
	}
	session, err := d.Pty.Create(ctx, ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Command: []string{"/bin/sh", "-c", "printf rel026-ready; read answer; printf '%s' \"$answer\"; sleep 30"}}})
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	second := wire()
	second.Close()
	got, err := d.Pty.Get(ctx, session.ID)
	if err != nil || got.Pid != session.Pid || got.Activity == api.ActivityExited {
		t.Fatalf("PTY after serve reconstruction: %+v %v", got, err)
	}
	if err := d.Pty.Input(ctx, session.ID, []byte("still-owned\n")); err != nil {
		t.Fatal(err)
	}
}
