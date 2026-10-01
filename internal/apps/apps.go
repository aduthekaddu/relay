// Package apps runs on-demand apps behind Relay's login: the browser IDE
// (code-server / openvscode-server on a private unix socket), user-defined
// web apps from [[apps]] in relay.toml, and the remote desktop (TigerVNC
// Xvnc + openbox + tint2, RFB bridged over a WebSocket).
//
// Every app is started on first use as a supervised child in its own
// process group and stopped again after its idle timeout.
package apps

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/previews/revproxy"
	"github.com/aduthekaddu/relay/internal/server"
)

const (
	idleCheckEvery = 30 * time.Second
	codeInstallURL = "https://coder.com/docs/code-server/install"
)

var appIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// webApp is one proxied app (the IDE or a user-defined service).
type webApp struct {
	id, name, desc, kind, icon string
	base                       string // "/apps/<id>"
	installed                  bool
	capMu                      sync.Mutex
	implementation, source     string
	installHint                string
	idle                       func() time.Duration // 0 = never stop
	dial                       func(ctx context.Context) (net.Conn, error)
	proc                       *proc // nil for external services Relay does not run
	act                        *revproxy.Activity
	proxy                      *revproxy.Proxy
}

// Service is the apps feature.
type Service struct {
	d        *core.Deps
	log      *slog.Logger
	rt       *server.Router
	lookPath func(string) (string, error)

	apps  map[string]*webApp
	order []string
	desk  *desktop

	closeOnce          sync.Once
	capMu              sync.Mutex
	lastCode, lastDesk api.AppCapability
}

// New detects the IDE, reads user apps from config and prepares the
// desktop manager. It starts nothing.
func New(d *core.Deps) (*Service, error) {
	s := &Service{d: d, log: d.Log, lookPath: exec.LookPath, apps: map[string]*webApp{}}
	if s.log == nil {
		s.log = slog.Default()
	}
	if d.Cfg.Code.Enabled {
		s.add(s.codeApp())
	}
	for _, ac := range d.Cfg.Apps {
		a, err := s.userApp(ac)
		if err != nil {
			s.log.Warn("ignoring app from config", "id", ac.ID, "err", err)
			continue
		}
		s.add(a)
	}
	desk, err := newDesktop(d, s.log, s.lookPath)
	if err != nil {
		return nil, err
	}
	s.desk = desk
	s.lastCode, s.lastDesk = s.CodeCapability(), s.DesktopCapability()
	desk.onChange = func() {
		st := desk.State()
		s.refreshCapabilities()
		d.Bus.Publish(api.EvDesktopState, st)
		d.Bus.Publish(api.EvAppState, desktopApp(st))
	}
	return s, nil
}

func (s *Service) add(a *webApp) {
	s.apps[a.id] = a
	s.order = append(s.order, a.id)
}

// codeApp builds the IDE entry.
func (s *Service) codeApp() *webApp {
	p := s.d.Paths
	bin, flavor, ok := codeBinary(s.d.Cfg.Code.Binary, p.Home, s.lookPath)
	sock := filepath.Join(p.RuntimeDir, "code.sock")
	a := &webApp{
		id: "code", name: "Code", kind: "code", icon: "code", base: "/apps/code",
		desc:        "VS Code in the browser, with your files and terminals",
		installed:   ok,
		installHint: "Install code-server (" + codeInstallURL + ") or set code.binary in relay.toml",
		idle:        func() time.Duration { return s.d.RuntimeConfig().Code.IdleStop.Duration },
		dial:        revproxy.UnixSocket(sock, 2*time.Second),
		act:         &revproxy.Activity{},
	}
	a.proc = newProc(procSpec{
		Name:           "code-server",
		ResolveCommand: func() ([]string, []string, error) { return s.resolveCode(a, sock) },
		Argv:           codeArgs(bin, flavor, sock, p.DataDir, a.base),
		Env:            childEnv(os.Environ(), nil),
		Dir:            p.Home,
		Ready:          socketReady(sock),
		ReadyTimeout:   90 * time.Second,
		BeforeStart: func() error {
			for _, dir := range []string{"user", "extensions"} {
				if err := os.MkdirAll(filepath.Join(p.DataDir, "code", dir), 0o700); err != nil {
					return err
				}
			}
			if err := os.WriteFile(filepath.Join(p.DataDir, "code", "config.yaml"), []byte(codeConfigYAML), 0o600); err != nil {
				return err
			}
			return removeStaleSocket(sock)
		},
	}, func() { s.publishApp(a) })
	a.proxy = s.newProxy(a)
	return a
}

// userApp validates one [[apps]] entry.
func (s *Service) userApp(ac config.AppConfig) (*webApp, error) {
	id := strings.ToLower(strings.TrimSpace(ac.ID))
	if !appIDRe.MatchString(id) || id == "code" || id == "desktop" {
		return nil, fmt.Errorf("invalid app id %q (lowercase letters, digits, dashes; not code/desktop)", ac.ID)
	}
	if _, dup := s.apps[id]; dup {
		return nil, fmt.Errorf("duplicate app id %q", id)
	}
	name := ac.Name
	if name == "" {
		name = id
	}
	a := &webApp{
		id: id, name: name, desc: ac.Description, kind: "web", icon: "app", base: "/apps/" + id,
		installed: true, act: &revproxy.Activity{},
		idle: func() time.Duration { return ac.IdleStop.Duration },
	}
	env := map[string]string{"RELAY_BASE_PATH": a.base}
	for k, v := range ac.Env {
		env[k] = v
	}
	var ready func(ctx context.Context) error
	var before func() error
	switch {
	case ac.Socket != "" && ac.Port != 0:
		return nil, fmt.Errorf("app %q: set either port or socket, not both", id)
	case ac.Socket != "":
		sock := expandHome(ac.Socket, s.d.Paths.Home)
		if !filepath.IsAbs(sock) {
			sock = filepath.Join(s.d.Paths.RuntimeDir, sock)
		}
		a.dial = revproxy.UnixSocket(sock, 2*time.Second)
		ready = socketReady(sock)
		before = func() error { return removeStaleSocket(sock) }
		env["RELAY_APP_SOCKET"] = sock
	case ac.Port > 0 && ac.Port <= 65535:
		a.dial = revproxy.LoopbackTCP(ac.Port, 2*time.Second)
		ready = func(ctx context.Context) error {
			c, err := a.dial(ctx)
			if err == nil {
				c.Close()
			}
			return err
		}
		env["PORT"] = strconv.Itoa(ac.Port)
	default:
		return nil, fmt.Errorf("app %q: needs a loopback port or a unix socket", id)
	}
	if len(ac.Command) > 0 {
		argv := append([]string(nil), ac.Command...)
		argv[0] = expandHome(argv[0], s.d.Paths.Home)
		if lp, err := s.lookPath(argv[0]); err == nil {
			argv[0] = lp
		} else {
			a.installed = false
			a.installHint = "command not found: " + ac.Command[0]
		}
		dir := expandHome(ac.Cwd, s.d.Paths.Home)
		if dir == "" {
			dir = s.d.Paths.Home
		}
		a.proc = newProc(procSpec{
			Name: id, Argv: argv, Env: childEnv(os.Environ(), env), Dir: dir, Ready: ready,
			BeforeStart: before,
		}, func() { s.publishApp(a) })
	}
	a.proxy = s.newProxy(a)
	return a, nil
}

func (s *Service) newProxy(a *webApp) *revproxy.Proxy {
	return revproxy.New(revproxy.Target{Dial: a.dial, Host: "localhost"}, revproxy.Options{
		StripPrefix: a.base,
		Proto:       originScheme(s.d.Cfg.Origin()),
		Activity:    a.act,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.log.Debug("app upstream error", "app", a.id, "err", err)
			writePage(w, http.StatusBadGateway, pageData{Title: a.name, Heading: a.name + " is not answering", Body: "The app stopped responding. Reload to try again.", Retry: 3})
		},
	})
}

func originScheme(o string) string {
	if strings.HasPrefix(o, "https://") {
		return "https"
	}
	return "http"
}

// socketReady succeeds once the unix socket accepts a connection.
func socketReady(sock string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		d := net.Dialer{Timeout: 500 * time.Millisecond}
		c, err := d.DialContext(ctx, "unix", sock)
		if err != nil {
			return err
		}
		return c.Close()
	}
}

// removeStaleSocket deletes a leftover socket file (never anything else).
func removeStaleSocket(path string) error {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return os.MkdirAll(filepath.Dir(path), 0o700)
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket", path)
	}
	return os.Remove(path)
}

// ---------------------------------------------------------------------------
// State

func (s *Service) appState(a *webApp) api.App {
	if a.id == "code" {
		return s.codeState(a)
	}
	out := api.App{
		ID: a.id, Name: a.name, Description: a.desc, Kind: a.kind, Icon: a.icon,
		Installed: a.installed, URL: a.base + "/",
	}
	if !a.installed {
		out.State = "unavailable"
		out.InstallHint = a.installHint
		return out
	}
	if a.proc == nil {
		// External service: running when it accepts connections.
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if c, err := a.dial(ctx); err == nil {
			c.Close()
			out.State = stateRunning
		} else {
			out.State = stateStopped
		}
		return out
	}
	st, since, msg := a.proc.Status()
	out.State, out.Error = st, msg
	if st != stateStopped {
		out.Since = since
	}
	return out
}

func (s *Service) publishApp(a *webApp) {
	s.refreshCapabilities()
	s.d.Bus.Publish(api.EvAppState, s.appState(a))
}

func desktopApp(st api.DesktopState) api.App {
	a := api.App{
		ID: "desktop", Name: "Desktop", Kind: "desktop", Icon: "desktop", URL: "/desktop",
		Description: "A full Linux desktop in the browser",
		State:       st.State, Installed: st.Capability.Enabled && st.Capability.Available, Error: st.Error, InstallHint: st.InstallHint,
		Capability: &st.Capability,
	}
	if st.Error != "" && st.State == stateStopped {
		a.State = stateError
	}
	return a
}

// List returns every app: code, desktop, then user apps by id.
func (s *Service) List() []api.App {
	out := make([]api.App, 0, len(s.order)+1)
	ids := append([]string(nil), s.order...)
	sort.SliceStable(ids, func(i, j int) bool { return ids[i] == "code" && ids[j] != "code" })
	for _, id := range ids {
		if id == "code" {
			out = append(out, s.appState(s.apps[id]))
		}
	}
	out = append(out, desktopApp(s.desk.State()))
	for _, id := range ids {
		if id != "code" {
			out = append(out, s.appState(s.apps[id]))
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Lifecycle

// Start runs the idle-stop loop until ctx is done, then stops every child.
func (s *Service) Start(ctx context.Context) error {
	t := time.NewTicker(idleCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.stopAll()
			return ctx.Err()
		case now := <-t.C:
			s.checkIdle(ctx, now)
			s.refreshCapabilities()
		}
	}
}

func (s *Service) checkIdle(ctx context.Context, now time.Time) {
	for _, id := range s.order {
		a := s.apps[id]
		if a.proc == nil {
			continue
		}
		limit := a.idle()
		if limit <= 0 {
			continue
		}
		st, since, _ := a.proc.Status()
		if st != stateRunning {
			continue
		}
		idle := a.act.IdleFor(now)
		if a.act.Last().IsZero() && a.act.Active() == 0 {
			idle = now.Sub(since) // started but never used
		}
		if idle >= limit {
			s.log.Info("stopping idle app", "app", id, "idle", idle.Round(time.Second))
			_ = a.proc.Stop(ctx)
		}
	}
	s.desk.checkIdle(ctx, now)
}

func (s *Service) stopAll() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, a := range s.apps {
		if a.proc != nil {
			wg.Add(1)
			go func(p *proc) { defer wg.Done(); _ = p.Stop(ctx) }(a.proc)
		}
	}
	wg.Add(1)
	go func() { defer wg.Done(); _ = s.desk.Stop(ctx) }()
	wg.Wait()
}

// Close stops every child process and releases proxy connections.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.stopAll()
		for _, a := range s.apps {
			a.proxy.Close()
		}
	})
	return nil
}
