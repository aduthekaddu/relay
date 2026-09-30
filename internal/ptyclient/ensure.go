package ptyclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/aduthekaddu/relay/internal/config"
)

// ensureWait is how long EnsureDaemon waits for a started daemon.
const ensureWait = 5 * time.Second

// maxDaemonLog is the size at which ptyd.log is rotated to ptyd.log.1.
const maxDaemonLog = 10 << 20

// EnsureDaemon makes sure a ptyd answers on paths.PtydSocket. When none
// does, it starts `binary ptyd` detached (new session, stdio to
// $DATA/ptyd/ptyd.log) and waits up to 5 s for it to become healthy. It
// never starts a second daemon: the daemon's lock file is probed first,
// and a daemon that loses the race exits on its own.
func EnsureDaemon(ctx context.Context, paths config.Paths, binary string) error {
	c := New(paths.PtydSocket)
	if healthy(ctx, c) {
		return nil
	}
	if !lockHeld(filepath.Join(filepath.Dir(paths.PtydSocket), "ptyd.lock")) {
		if err := spawnDaemon(paths, binary); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(ensureWait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if healthy(ctx, c) {
			return nil
		}
	}
	return fmt.Errorf("%w: ptyd did not become healthy within %s (see %s)", ErrUnavailable, ensureWait, daemonLog(paths))
}

func healthy(ctx context.Context, c *Client) bool {
	hctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return c.Health(hctx) == nil
}

// lockHeld reports whether a live daemon (or one starting up) holds the
// lock file.
func lockHeld(path string) bool {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false // no lock file yet: nobody ever ran here
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.Is(err, unix.EWOULDBLOCK)
	}
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return false
}

func daemonLog(paths config.Paths) string {
	return filepath.Join(paths.DataDir, "ptyd", "ptyd.log")
}

// spawnDaemon starts the daemon in its own session so it outlives the
// caller's terminal and process group.
func spawnDaemon(paths config.Paths, binary string) error {
	if binary == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate relay binary: %w", err)
		}
		binary = exe
	}
	logPath := daemonLog(paths)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return fmt.Errorf("ptyd log dir: %w", err)
	}
	if st, err := os.Stat(logPath); err == nil && st.Size() > maxDaemonLog {
		_ = os.Rename(logPath, logPath+".1")
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open ptyd log: %w", err)
	}
	defer logf.Close()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer devnull.Close()

	cmd := exec.Command(binary, "ptyd", "--socket", paths.PtydSocket)
	cmd.Stdin = devnull
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Dir = paths.Home
	if cmd.Dir == "" {
		cmd.Dir = "/"
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ptyd: %w", err)
	}
	// Reap the daemon if it exits while we run (otherwise it would stay a
	// zombie until we exit). This goroutine lives as long as the daemon.
	go func() { _ = cmd.Wait() }()
	return nil
}
