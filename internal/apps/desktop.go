package apps

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aduthekaddu/relay/internal/core"
)

const (
	maxClipboard    = 1 << 20 // bytes of text through the clipboard API
	maxViewers      = 8
	deskInstallHint = "sudo apt-get install --no-install-recommends tigervnc-standalone-server openbox tint2 xclip x11-xserver-utils"
	defaultWidth    = 1600
	defaultHeight   = 1000
	minW, minH      = 320, 240
	maxW, maxH      = 8192, 8192
)

var (
	displayRe  = regexp.MustCompile(`^:([0-9]{1,3})$`)
	geometryRe = regexp.MustCompile(`^([0-9]{3,4})x([0-9]{3,4})$`)
)

// desktop manages the remote desktop: an Xvnc server whose RFB endpoint is
// an owner-only unix socket, plus the openbox/tint2 session on top of it.
type desktop struct {
	d        *core.Deps
	log      *slog.Logger
	onChange func()

	display string // ":7"
	num     int
	dir     string // rendered session files (0700)
	sock    string // RFB unix socket (0600)
	xauth   string // Xauthority for the display
	xsock   string // X11 unix socket of the display
	catalog []deskApp
	// unavailable is non-empty when the desktop cannot run here.
	unavailable, hint string

	xvnc, session *proc
	// ensure makes the desktop run before a viewer is bridged (Start;
	// replaced in tests).
	ensure func(ctx context.Context) error

	opMu sync.Mutex // serializes Start and Stop

	mu       sync.Mutex
	width    int
	height   int
	viewers  int
	lastUse  time.Time
	stopping bool
	scanner  *runningScanner
	conns    map[net.Conn]struct{}
}

// newDesktop reads desktop config and detects the X tools. It never
// fails: problems make the desktop "unavailable" with a reason.
func newDesktop(d *core.Deps, log *slog.Logger, lookPath func(string) (string, error)) (*desktop, error) {
	cfg := d.Cfg.Desktop
	k := &desktop{
		d: d, log: log, display: cfg.Display, width: defaultWidth, height: defaultHeight,
		dir:   filepath.Join(d.Paths.RuntimeDir, "desktop"),
		sock:  filepath.Join(d.Paths.RuntimeDir, "vnc.sock"),
		conns: map[net.Conn]struct{}{},
	}
	k.xauth = filepath.Join(k.dir, "Xauthority")
	k.ensure = k.Start
	if m := geometryRe.FindStringSubmatch(cfg.Geometry); m != nil {
		w, _ := strconv.Atoi(m[1])
		h, _ := strconv.Atoi(m[2])
		if w >= minW && h >= minH && w <= maxW && h <= maxH {
			k.width, k.height = w, h
		}
	} else if cfg.Geometry != "" {
		log.Warn("invalid desktop.geometry, using default", "geometry", cfg.Geometry)
	}
	m := displayRe.FindStringSubmatch(cfg.Display)
	switch {
	case runtime.GOOS != "linux":
		k.unavailable = "The remote desktop needs Linux (TigerVNC Xvnc)."
	case !cfg.Enabled:
		k.unavailable = "The desktop is disabled (desktop.enabled = false in relay.toml)."
	case m == nil:
		k.unavailable = fmt.Sprintf("desktop.display %q is not a local display like \":7\".", cfg.Display)
	case len(k.sock) > 100:
		k.unavailable = "The runtime directory path is too long for a unix socket."
	}
	if m != nil {
		k.num, _ = strconv.Atoi(m[1])
		k.xsock = fmt.Sprintf("/tmp/.X11-unix/X%d", k.num)
	}
	k.scanner = &runningScanner{procDir: "/proc", display: k.display, ttl: 2 * time.Second}
	xvncBin, openbox := findFirst(lookPath, "Xvnc", "Xtigervnc"), findFirst(lookPath, "openbox")
	if k.unavailable == "" && (xvncBin == "" || openbox == "") {
		k.unavailable = "TigerVNC (Xvnc) and openbox are not installed."
		k.hint = deskInstallHint
	}
	var errs []error
	k.catalog, errs = deskCatalog(cfg.Apps, d.Paths.DataDir, d.Paths.Home, lookPath)
	for _, err := range errs {
		log.Warn("ignoring desktop app", "err", err)
	}
	if k.unavailable != "" {
		k.xvnc = newProc(procSpec{Name: "Xvnc"}, nil)
		k.session = newProc(procSpec{Name: "desktop session"}, nil)
		return k, nil
	}
	k.xvnc = newProc(procSpec{
		Name:         "Xvnc",
		Argv:         k.xvncArgs(xvncBin),
		Env:          k.sessionEnv(),
		Dir:          d.Paths.Home,
		Ready:        k.xReady,
		ReadyTimeout: 20 * time.Second,
		StopGrace:    3 * time.Second,
		BeforeStart:  k.prepare,
	}, k.changed)
	argv := []string{"/bin/sh", filepath.Join(k.dir, "xstartup")}
	if drs := findFirst(lookPath, "dbus-run-session"); drs != "" {
		argv = append([]string{drs, "--"}, argv...)
	}
	k.session = newProc(procSpec{
		Name: "desktop session", Argv: argv, Env: k.sessionEnv(), Dir: d.Paths.Home,
		StopGrace: 3 * time.Second,
	}, k.sessionChanged)
	return k, nil
}

func findFirst(lookPath func(string) (string, error), names ...string) string {
	for _, n := range names {
		if p, err := lookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// xvncArgs builds the Xvnc command line: RFB only on the unix socket, no
// TCP listener for RFB or X11, no VNC password (the socket is 0600 and
// Relay's login guards the bridge).
func (k *desktop) xvncArgs(bin string) []string {
	return []string{
		bin, k.display,
		"-geometry", fmt.Sprintf("%dx%d", k.width, k.height),
		"-depth", "24",
		"-rfbport", "-1",
		"-rfbunixpath", k.sock,
		"-rfbunixmode", "0600",
		"-SecurityTypes", "None",
		"-localhost",
		"-nolisten", "tcp",
		"-auth", k.xauth,
		"-desktop", "Relay",
		"-AlwaysShared",
		"-AcceptSetDesktopSize",
		"-dpi", "96",
	}
}

// sessionEnv is the environment of Xvnc, the session and launched apps.
func (k *desktop) sessionEnv() []string {
	base := make([]string, 0, 64)
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "WAYLAND_DISPLAY", "DBUS_SESSION_BUS_ADDRESS", "XDG_SESSION_ID", "DESKTOP_STARTUP_ID":
			continue
		}
		base = append(base, kv)
	}
	return childEnv(base, map[string]string{
		"DISPLAY":             k.display,
		"XAUTHORITY":          k.xauth,
		"RELAY_DESKTOP_DIR":   k.dir,
		"XDG_SESSION_TYPE":    "x11",
		"XDG_CURRENT_DESKTOP": "Relay:Openbox",
		"DESKTOP_SESSION":     "relay",
	})
}

// prepare runs before Xvnc starts: refuse a display owned by another X
// server, clean our own leftovers, render the session and a new cookie.
func (k *desktop) prepare() error {
	if _, err := os.Lstat(k.xsock); err == nil {
		c, derr := net.DialTimeout("unix", k.xsock, 500*time.Millisecond)
		if derr == nil {
			c.Close()
			return fmt.Errorf("display %s is already used by another X server (set desktop.display in relay.toml)", k.display)
		}
		if !ownedBy(k.xsock, os.Getuid()) {
			return fmt.Errorf("display %s has a stale socket owned by another user", k.display)
		}
		_ = os.Remove(k.xsock)
		removeStaleXLock(fmt.Sprintf("/tmp/.X%d-lock", k.num))
	}
	if err := renderSession(k.dir, k.catalog); err != nil {
		return err
	}
	if err := writeXauthority(k.xauth, k.num); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(k.d.Paths.DataDir, "desktop"), 0o700); err != nil {
		return err
	}
	return removeStaleSocket(k.sock)
}

// removeStaleXLock deletes an X lock file of ours whose server is gone.
func removeStaleXLock(path string) {
	b, err := os.ReadFile(path)
	if err != nil || !ownedBy(path, os.Getuid()) {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err == nil && pid > 0 && syscall.Kill(pid, 0) == nil {
		return // still alive
	}
	_ = os.Remove(path)
}

// xReady succeeds once both the X11 socket and the RFB socket accept.
func (k *desktop) xReady(ctx context.Context) error {
	for _, s := range []string{k.xsock, k.sock} {
		if err := socketReady(s)(ctx); err != nil {
			return err
		}
	}
	return nil
}
