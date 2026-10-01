package apps

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

func (k *desktop) changed() {
	if k.onChange != nil {
		k.onChange()
	}
}

// sessionChanged ends the whole desktop when the session (openbox) exits
// on its own, e.g. after "Exit" from the menu.
func (k *desktop) sessionChanged() {
	st, _, _ := k.session.Status()
	xs, _, _ := k.xvnc.Status()
	k.mu.Lock()
	stopping := k.stopping
	k.mu.Unlock()
	if !stopping && (st == stateStopped || st == stateError) && (xs == stateRunning || xs == stateStarting) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := k.Stop(ctx); err != nil {
				k.log.Warn("desktop stop after session exit", "err", err)
			}
		}()
	}
	k.changed()
}

// status combines both processes into one desktop state.
func (k *desktop) status() (state, errMsg string) {
	if k.unavailable != "" {
		return "unavailable", k.unavailable
	}
	xs, _, xerr := k.xvnc.Status()
	ss, _, serr := k.session.Status()
	switch {
	case xs == stateRunning && ss == stateRunning:
		return stateRunning, ""
	case xs == stateStarting, xs == stateRunning:
		return stateStarting, ""
	case xs == stateError:
		return stateStopped, xerr
	case ss == stateError:
		return stateStopped, serr
	}
	return stateStopped, ""
}

// State is the API view of the desktop.
func (k *desktop) State() api.DesktopState {
	state, msg := k.status()
	k.mu.Lock()
	defer k.mu.Unlock()
	st := api.DesktopState{
		State: state, Display: k.display, Width: k.width, Height: k.height,
		Viewers: k.viewers, Error: msg, Apps: []api.DesktopApp{},
	}
	if state == "unavailable" {
		st.Error, st.InstallHint = k.unavailable, k.hint
		if k.hint != "" {
			st.Error = ""
		}
	}
	var running map[string]bool
	if state == stateRunning {
		running = k.scanner.Running(time.Now())
	}
	for _, a := range k.catalog {
		st.Apps = append(st.Apps, api.DesktopApp{ID: a.ID, Name: a.Name, Icon: a.ID, Running: running[a.ID]})
	}
	return st
}

// Start brings the desktop up (Xvnc, then the session) and waits until
// both run. Starting a running desktop is a no-op.
func (k *desktop) Start(ctx context.Context) error {
	if k.unavailable != "" {
		msg := k.unavailable
		if k.hint != "" {
			msg += " Install with: " + k.hint
		}
		return httpx.Unavailable(msg)
	}
	k.opMu.Lock()
	defer k.opMu.Unlock()
	k.touch()
	if err := k.xvnc.Start(ctx); err != nil {
		return startErr(err)
	}
	if err := k.session.Start(ctx); err != nil {
		_ = k.stopLocked(context.Background())
		return startErr(err)
	}
	k.log.Info("desktop started", "display", k.display)
	return nil
}

func startErr(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return httpx.Unavailable("The desktop could not start: " + err.Error())
}

// Stop ends the session and the X server. Viewers are disconnected.
func (k *desktop) Stop(ctx context.Context) error {
	if k.unavailable != "" {
		return nil
	}
	k.opMu.Lock()
	defer k.opMu.Unlock()
	return k.stopLocked(ctx)
}

func (k *desktop) stopLocked(ctx context.Context) error {
	k.mu.Lock()
	k.stopping = true
	k.mu.Unlock()
	defer func() {
		k.mu.Lock()
		k.stopping = false
		k.scanner.Reset()
		k.mu.Unlock()
		k.changed()
	}()
	serr := k.session.Stop(ctx)
	xerr := k.xvnc.Stop(ctx)
	k.closeViewers()
	k.killLeftovers(ctx)
	_ = os.Remove(k.sock)
	_ = os.Remove(filepath.Join(k.dir, "dbus-address"))
	if xerr != nil {
		return xerr
	}
	return serr
}

// checkIdle stops a running desktop nobody has watched for idle_stop.
func (k *desktop) checkIdle(ctx context.Context, now time.Time) {
	if k.unavailable != "" {
		return
	}
	limit := k.d.RuntimeConfig().Desktop.IdleStop.Duration
	if limit <= 0 {
		return
	}
	if st, _ := k.status(); st != stateRunning {
		return
	}
	k.mu.Lock()
	idle := k.viewers == 0 && now.Sub(k.lastUse) >= limit
	k.mu.Unlock()
	if idle {
		k.log.Info("stopping idle desktop", "display", k.display)
		if err := k.Stop(ctx); err != nil {
			k.log.Warn("idle desktop stop", "err", err)
		}
	}
}

func (k *desktop) touch() {
	k.mu.Lock()
	k.lastUse = time.Now()
	k.mu.Unlock()
}

// addViewer registers a bridged RFB connection; false when full.
func (k *desktop) addViewer(c net.Conn) bool {
	k.mu.Lock()
	if k.viewers >= maxViewers {
		k.mu.Unlock()
		return false
	}
	k.viewers++
	k.conns[c] = struct{}{}
	k.lastUse = time.Now()
	k.mu.Unlock()
	k.changed()
	return true
}

func (k *desktop) removeViewer(c net.Conn) {
	k.mu.Lock()
	if _, ok := k.conns[c]; ok {
		delete(k.conns, c)
		k.viewers--
	}
	k.lastUse = time.Now()
	k.mu.Unlock()
	k.changed()
}

// closeViewers drops every bridged connection (their bridges then end).
func (k *desktop) closeViewers() {
	k.mu.Lock()
	conns := make([]net.Conn, 0, len(k.conns))
	for c := range k.conns {
		conns = append(conns, c)
	}
	k.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// killLeftovers ends launcher-started apps that outlived the X server
// (e.g. waiting on D-Bus): SIGTERM, then SIGKILL after two seconds. Only
// the user's own processes carrying this display's marker are touched.
func (k *desktop) killLeftovers(ctx context.Context) {
	procs := k.scanner.Procs()
	if len(procs) == 0 {
		return
	}
	for _, p := range procs {
		_ = syscall.Kill(p.PID, syscall.SIGTERM)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(100 * time.Millisecond)
		if len(k.scanner.Procs()) == 0 {
			return
		}
	}
	for _, p := range k.scanner.Procs() { // re-scanned: no stale pids
		_ = syscall.Kill(p.PID, syscall.SIGKILL)
	}
}
