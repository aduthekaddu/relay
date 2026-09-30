package ptyclient_test

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/ptyd"
)

// TestMain doubles as the `relay ptyd` binary: EnsureDaemon is pointed at
// the test executable, which then runs a daemon when invoked as
// "<test-binary> ptyd --socket <path>".
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "ptyd" {
		os.Exit(runTestDaemon(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func runTestDaemon(args []string) int {
	fs := flag.NewFlagSet("ptyd", flag.ContinueOnError)
	socket := fs.String("socket", "", "")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return 1
	}
	cfg := config.Defaults()
	cfg.Terminal.Shell = "/bin/sh"
	cfg.Terminal.Record = "off"
	cfg.Desktop.Enabled = false
	off := false
	d, err := ptyd.New(ptyd.Options{Cfg: cfg, Paths: paths, Socket: *socket, Log: slog.New(slog.NewTextHandler(os.Stderr, nil)), LoginEnv: &off})
	if err != nil {
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := d.Run(ctx); err != nil {
		if errors.Is(err, ptyd.ErrAlreadyRunning) {
			os.Stderr.WriteString("already running\n")
		}
		return 1
	}
	return 0
}

func testHome(t *testing.T) config.Paths {
	t.Helper()
	root, err := os.MkdirTemp("", "pc")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RELAY_HOME", root)
	paths, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopDaemon(t, paths)
		os.RemoveAll(root)
	})
	return paths
}

// stopDaemon terminates the daemon started by the test (PID from its
// lock file) and waits for it to release the socket.
func stopDaemon(t *testing.T, paths config.Paths) {
	b, err := os.ReadFile(filepath.Join(paths.RuntimeDir, "ptyd.lock"))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 || pid == os.Getpid() {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Logf("daemon %d needed SIGKILL", pid)
}

func countListening(t *testing.T, paths config.Paths) int {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(paths.DataDir, "ptyd", "ptyd.log"))
	return strings.Count(string(b), "ptyd listening")
}

func TestEnsureDaemonStartsOnceAndIsIdempotent(t *testing.T) {
	paths := testHome(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := ptyclient.New(paths.PtydSocket)
	if err := c.Health(context.Background()); !errors.Is(err, ptyclient.ErrUnavailable) {
		t.Fatalf("health before start: %v", err)
	}

	// Several callers race to ensure the daemon: exactly one runs.
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			errs <- ptyclient.EnsureDaemon(ctx, paths, exe)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("EnsureDaemon: %v", err)
		}
	}
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
	time.Sleep(300 * time.Millisecond) // let any loser log and exit
	if n := countListening(t, paths); n != 1 {
		t.Fatalf("%d daemons listened", n)
	}

	// A session survives further EnsureDaemon calls (no restart).
	s, err := c.Create(context.Background(), ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Command: []string{"/bin/sh", "-c", "sleep 30"}}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := ptyclient.EnsureDaemon(context.Background(), paths, exe); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := c.Get(context.Background(), s.ID); err != nil || got.Activity == api.ActivityExited {
		t.Fatalf("session after re-ensure: %+v %v", got, err)
	}
	if n := countListening(t, paths); n != 1 {
		t.Fatalf("%d daemons listened after re-ensure", n)
	}
	if st, err := os.Stat(paths.PtydSocket); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v %v", st, err)
	}
}

func TestEnsureDaemonReportsFailure(t *testing.T) {
	paths := testHome(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := ptyclient.EnsureDaemon(ctx, paths, "/bin/false")
	if !errors.Is(err, ptyclient.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestClientErrors(t *testing.T) {
	paths := testHome(t)
	exe, _ := os.Executable()
	if err := ptyclient.EnsureDaemon(context.Background(), paths, exe); err != nil {
		t.Fatal(err)
	}
	c := ptyclient.New(paths.PtydSocket)
	ctx := context.Background()
	if _, err := c.Get(ctx, "t_aaaaaaaaaa"); !errors.Is(err, ptyclient.ErrNotFound) {
		t.Fatalf("get unknown: %v", err)
	}
	_, err := c.Create(ctx, ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Cwd: "/definitely/missing"}})
	var se *ptyclient.StatusError
	if !errors.As(err, &se) || se.Status != 400 || se.Message == "" {
		t.Fatalf("bad cwd: %v", err)
	}
	if _, err := c.Attach(ctx, "t_aaaaaaaaaa", ptyclient.AttachOptions{}); !errors.Is(err, ptyclient.ErrNotFound) {
		t.Fatalf("attach unknown: %v", err)
	}
	evs, err := c.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Create(ctx, ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Command: []string{"/bin/sh", "-c", "exit 0"}}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-evs:
			if !ok {
				t.Fatal("events closed")
			}
			if ev.Type == "exited" && ev.ID == s.ID {
				return
			}
		case <-deadline:
			t.Fatal("no exited event")
		}
	}
}
