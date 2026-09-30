// Package ptyd is the terminal session daemon (`relay ptyd`). It owns
// every pty so that restarting or upgrading the web server never kills a
// shell or an agent. It speaks HTTP + WebSocket on an owner-only unix
// socket; internal/ptyclient is its only client library. See
// docs/dev/PTYD.md.
package ptyd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/version"
)

// Limits.
const (
	maxLiveSessions   = 256
	maxExitedSessions = 200
	exitedRetention   = 30 * 24 * time.Hour
	maxMetaKeys       = 32
	maxMetaKey        = 64
	maxMetaValue      = 1024
	maxArgs           = 256
	maxArgLen         = 64 << 10
)

// Options configure a Daemon.
type Options struct {
	Cfg   *config.Config
	Paths config.Paths
	// Socket overrides Paths.PtydSocket.
	Socket string
	Log    *slog.Logger
	// LoginEnv captures the login shell's environment for sessions. When
	// nil it is enabled under systemd (INVOCATION_ID set) or with
	// RELAY_LOGIN_ENV=1.
	LoginEnv *bool
}

// Daemon manages sessions. Create with New, then Run.
type Daemon struct {
	cfg       *config.Config
	paths     config.Paths
	socket    string
	log       *slog.Logger
	recordDir string
	stateFile string
	idleAfter time.Duration
	loginEnv  bool

	baseEnvOnce sync.Once
	baseEnv     map[string]string

	mu       sync.RWMutex
	sessions map[string]*Session

	subsMu sync.Mutex
	subs   map[*subscriber]struct{}

	dirty chan struct{}
	wg    sync.WaitGroup
}

// New builds a daemon; it starts nothing.
func New(opts Options) (*Daemon, error) {
	if opts.Cfg == nil {
		return nil, errors.New("ptyd: config required")
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	sock := opts.Socket
	if sock == "" {
		sock = opts.Paths.PtydSocket
	}
	if sock == "" {
		return nil, errors.New("ptyd: socket path required")
	}
	idle := time.Duration(opts.Cfg.Agents.IdleSeconds) * time.Second
	if idle <= 0 {
		idle = 8 * time.Second
	}
	login := os.Getenv("INVOCATION_ID") != "" || os.Getenv("RELAY_LOGIN_ENV") == "1"
	if opts.LoginEnv != nil {
		login = *opts.LoginEnv
	}
	d := &Daemon{
		cfg:       opts.Cfg,
		paths:     opts.Paths,
		socket:    sock,
		log:       log,
		recordDir: opts.Paths.Recordings,
		stateFile: filepath.Join(opts.Paths.DataDir, "ptyd", "sessions.json"),
		idleAfter: idle,
		loginEnv:  login,
		sessions:  map[string]*Session{},
		subs:      map[*subscriber]struct{}{},
		dirty:     make(chan struct{}, 1),
	}
	if d.recordDir == "" {
		d.recordDir = filepath.Join(opts.Paths.DataDir, "recordings")
	}
	return d, nil
}

// Socket returns the listening socket path.
func (d *Daemon) Socket() string { return d.socket }

func (d *Daemon) has(id string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.sessions[id]
	return ok
}

// get returns a session by id.
func (d *Daemon) get(id string) (*Session, error) {
	d.mu.RLock()
	s := d.sessions[id]
	d.mu.RUnlock()
	if s == nil {
		return nil, notFound("terminal session not found")
	}
	return s, nil
}

// List returns every session: live first (newest first), then exited.
func (d *Daemon) List() []api.TerminalSession {
	d.mu.RLock()
	all := make([]*Session, 0, len(d.sessions))
	for _, s := range d.sessions {
		all = append(all, s)
	}
	d.mu.RUnlock()
	out := make([]api.TerminalSession, 0, len(all))
	for _, s := range all {
		out = append(out, *s.Info())
	}
	sortSessions(out)
	return out
}

func sortSessions(out []api.TerminalSession) {
	sort.SliceStable(out, func(i, j int) bool {
		ei, ej := out[i].Activity == api.ActivityExited, out[j].Activity == api.ActivityExited
		if ei != ej {
			return !ei
		}
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
}

// Create spawns a new session.
func (d *Daemon) Create(spec ptyclient.CreateSpec) (*api.TerminalSession, error) {
	d.mu.RLock()
	live := 0
	for _, s := range d.sessions {
		select {
		case <-s.done:
		default:
			live++
		}
	}
	d.mu.RUnlock()
	if live >= maxLiveSessions {
		return nil, conflict(fmt.Sprintf("too many running sessions (%d)", maxLiveSessions))
	}
	s, err := d.startSession(spec)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.sessions[s.info.ID] = s
	d.mu.Unlock()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		s.run()
	}()
	info := s.Info()
	d.log.Info("session started", "session", info.ID, "command", info.Command[0], "kind", info.Kind)
	d.markDirty()
	d.emit(ptyclient.PtyEvent{Type: "created", ID: info.ID, Session: info})
	return info, nil
}

// prepare validates spec and computes env, display argv, resolved binary
// path and working directory. It fills defaults into spec (kept for
// restore).
func (d *Daemon) prepare(spec *ptyclient.CreateSpec) (map[string]string, []string, string, string, error) {
	if spec.Kind == "" {
		spec.Kind = api.KindShell
	}
	switch spec.Kind {
	case api.KindShell, api.KindAgent, api.KindTask, api.KindTmux, api.KindToolbox:
	default:
		return nil, nil, "", "", badRequest("unknown kind " + string(spec.Kind))
	}
	if len(spec.Command) > maxArgs {
		return nil, nil, "", "", badRequest("too many arguments")
	}
	for _, a := range spec.Command {
		if len(a) > maxArgLen || strings.IndexByte(a, 0) >= 0 {
			return nil, nil, "", "", badRequest("invalid argument")
		}
	}
	if err := validMeta(spec.Meta); err != nil {
		return nil, nil, "", "", err
	}
	spec.Name = strings.TrimSpace(cleanText([]byte(spec.Name), 128))

	env := d.sessionEnv(spec)
	shell := defaultShell(d.cfg.Terminal.Shell, env)
	if len(spec.Command) == 0 {
		spec.Command = []string{shell, "-l"}
	}
	path, err := lookPath(spec.Command[0], env["PATH"])
	if err != nil {
		return nil, nil, "", "", badRequest(err.Error())
	}
	cwd, err := d.resolveCwd(spec.Cwd)
	if err != nil {
		return nil, nil, "", "", err
	}
	spec.Cwd = cwd
	if env["SHELL"] == "" {
		env["SHELL"] = shell
	}
	return env, append([]string(nil), spec.Command...), path, cwd, nil
}

func (d *Daemon) resolveCwd(cwd string) (string, error) {
	if cwd == "" {
		cwd = d.cfg.Terminal.DefaultCwd
	}
	if cwd == "" || cwd == "~" || strings.HasPrefix(cwd, "~/") {
		home := d.paths.Home
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		switch {
		case cwd == "" || cwd == "~":
			cwd = home
		default:
			cwd = filepath.Join(home, cwd[2:])
		}
	}
	if !filepath.IsAbs(cwd) {
		return "", badRequest("cwd must be an absolute path")
	}
	cwd = filepath.Clean(cwd)
	st, err := os.Stat(cwd)
	if err != nil {
		return "", badRequest("cwd does not exist")
	}
	if !st.IsDir() {
		return "", badRequest("cwd is not a directory")
	}
	return cwd, nil
}

// sessionEnv merges the daemon env, the spec env and Relay's variables.
func (d *Daemon) sessionEnv(spec *ptyclient.CreateSpec) map[string]string {
	d.baseEnvOnce.Do(func() {
		base := envMap(os.Environ())
		if d.loginEnv {
			shell := defaultShell(d.cfg.Terminal.Shell, base)
			if m, err := loginEnv(context.Background(), shell); err != nil {
				d.log.Warn("using daemon environment", "err", err)
			} else {
				for k, v := range m {
					base[k] = v
				}
			}
		}
		for k := range envDrop {
			delete(base, k)
		}
		d.baseEnv = base
	})
	env := make(map[string]string, len(d.baseEnv)+16)
	for k, v := range d.baseEnv {
		env[k] = v
	}
	for _, m := range []map[string]string{spec.Env, spec.ExtraEnv} {
		for k, v := range m {
			if validEnvKey(k) && strings.IndexByte(v, 0) < 0 {
				env[k] = v
			}
		}
	}
	if env["LANG"] == "" && env["LC_ALL"] == "" {
		env["LANG"] = "C.UTF-8"
	}
	if env["PATH"] == "" {
		env["PATH"] = "/usr/local/bin:/usr/bin:/bin"
	}
	if env["HOME"] == "" && d.paths.Home != "" {
		env["HOME"] = d.paths.Home
	}
	if d.cfg.Desktop.Enabled {
		if sock := displaySocket(d.cfg.Desktop.Display); sock != "" {
			if _, err := os.Stat(sock); err == nil {
				env["DISPLAY"] = ":" + strings.TrimPrefix(d.cfg.Desktop.Display, ":")
			}
		}
	}
	env["TERM"] = "xterm-256color"
	env["COLORTERM"] = "truecolor"
	env["TERM_PROGRAM"] = "Relay"
	env["TERM_PROGRAM_VERSION"] = version.Version
	if d.paths.CtlSocket != "" {
		env["RELAY_SOCKET"] = d.paths.CtlSocket
	}
	return env
}

func (d *Daemon) scrollbackBytes(override int) int {
	n := override
	if n <= 0 {
		n = d.cfg.Terminal.ScrollbackKB << 10
	}
	return clamp(n, 64<<10, 64<<20)
}

func (d *Daemon) shouldRecord(spec ptyclient.CreateSpec) bool {
	if spec.Record != nil {
		return *spec.Record
	}
	switch d.cfg.Terminal.Record {
	case "all":
		return true
	case "agents":
		return spec.Kind == api.KindAgent
	}
	return false
}

func validMeta(m map[string]string) error {
	if len(m) > maxMetaKeys {
		return badRequest("too many meta keys")
	}
	for k, v := range m {
		if k == "" || len(k) > maxMetaKey || len(v) > maxMetaValue {
			return badRequest("meta key or value too long")
		}
	}
	return nil
}

// Update changes name, pinned and meta (empty meta values delete keys).
func (d *Daemon) Update(id string, req patchRequest) (*api.TerminalSession, error) {
	s, err := d.get(id)
	if err != nil {
		return nil, err
	}
	if err := validMeta(req.Meta); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if req.Name != nil {
		name := strings.TrimSpace(cleanText([]byte(*req.Name), 128))
		if name == "" {
			s.renamed = false
			if s.info.Title != "" {
				name = s.info.Title
			} else {
				name = sessionName(ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Agent: s.info.Agent, Command: s.info.Command, Meta: s.info.Meta}})
			}
		} else {
			s.renamed = true
		}
		s.info.Name = name
	}
	if req.Pinned != nil {
		s.info.Pinned = *req.Pinned
	}
	if len(req.Meta) > 0 {
		if s.info.Meta == nil {
			s.info.Meta = map[string]string{}
		}
		for k, v := range req.Meta {
			if v == "" {
				delete(s.info.Meta, k)
			} else {
				s.info.Meta[k] = v
			}
		}
		if len(s.info.Meta) > maxMetaKeys {
			s.mu.Unlock()
			return nil, badRequest("too many meta keys")
		}
	}
	ev := s.updatedLocked()
	s.mu.Unlock()
	d.markDirty()
	d.emit(ev)
	return ev.Session, nil
}

// Remove kills (if needed) and forgets a session.
func (d *Daemon) Remove(ctx context.Context, id string) error {
	s, err := d.get(id)
	if err != nil {
		return err
	}
	s.Kill(0)
	if !s.waitDone(ctx, killKillAfter+2*time.Second) {
		return unavailable("session did not exit")
	}
	d.mu.Lock()
	delete(d.sessions, id)
	d.mu.Unlock()
	removeRecording(d.recordDir, id)
	d.markDirty()
	d.emit(ptyclient.PtyEvent{Type: "removed", ID: id})
	return nil
}

// ---------------------------------------------------------------------------
// run loop

// Run acquires the daemon lock, restores metadata, serves the socket and
// runs background loops until ctx is cancelled. Sessions end with the
// daemon (their pty masters close).
func (d *Daemon) Run(ctx context.Context) error {
	unlock, err := acquireLock(lockPath(d.socket))
	if err != nil {
		return err
	}
	defer unlock()
	if err := d.load(); err != nil {
		d.log.Warn("could not restore session metadata", "err", err)
	}
	ln, err := listenSocket(d.socket)
	if err != nil {
		return err
	}
	srv := d.httpServer()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	d.log.Info("ptyd listening", "socket", d.socket, "version", version.Version)

	loopCtx, stopLoops := context.WithCancel(ctx)
	var loops sync.WaitGroup
	loops.Add(3)
	go func() { defer loops.Done(); d.tickLoop(loopCtx) }()
	go func() { defer loops.Done(); d.saveLoop(loopCtx) }()
	go func() { defer loops.Done(); d.sweepLoop(loopCtx) }()

	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	stopLoops()
	loops.Wait()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d.closeSubscribers()
	_ = srv.Shutdown(shutdownCtx)
	_ = os.Remove(d.socket)
	d.shutdownSessions()
	if serr := d.save(); serr != nil {
		d.log.Warn("save session metadata", "err", serr)
	}
	if err != nil && !isClosed(err) {
		return fmt.Errorf("ptyd serve: %w", err)
	}
	return nil
}

// shutdownSessions hangs up every live session and waits briefly, so
// exits (and recordings) are recorded before the daemon goes away.
func (d *Daemon) shutdownSessions() {
	d.mu.RLock()
	var live []*Session
	for _, s := range d.sessions {
		select {
		case <-s.done:
		default:
			live = append(live, s)
		}
	}
	d.mu.RUnlock()
	for _, s := range live {
		s.signal(1) // SIGHUP
	}
	deadline := time.Now().Add(2 * time.Second)
	for _, s := range live {
		if !s.waitDone(context.Background(), time.Until(deadline)) {
			s.signal(9)
		}
	}
	done := make(chan struct{})
	go func() { d.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

func (d *Daemon) tickLoop(ctx context.Context) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	flush := 0
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			d.mu.RLock()
			all := make([]*Session, 0, len(d.sessions))
			for _, s := range d.sessions {
				all = append(all, s)
			}
			d.mu.RUnlock()
			flush++
			for _, s := range all {
				d.emit(s.tick(now)...)
				if flush%4 == 0 {
					s.flushRecording()
				}
			}
		}
	}
}

func (s *Session) flushRecording() {
	s.mu.Lock()
	if s.rec != nil {
		s.rec.Flush()
	}
	s.mu.Unlock()
}

func (d *Daemon) sweepLoop(ctx context.Context) {
	sweep := func() {
		keep := map[string]bool{}
		d.mu.RLock()
		for id := range d.sessions {
			keep[id] = true
		}
		d.mu.RUnlock()
		days := d.cfg.Terminal.RecordDays
		if days > 0 {
			if n := sweepRecordings(d.recordDir, time.Duration(days)*24*time.Hour, keep, time.Now()); n > 0 {
				d.log.Info("removed old recordings", "files", n)
			}
		}
		d.pruneExited(time.Now())
	}
	sweep()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep()
		}
	}
}

// pruneExited forgets exited sessions beyond the count and age limits.
func (d *Daemon) pruneExited(now time.Time) {
	d.mu.Lock()
	var exited []*Session
	for _, s := range d.sessions {
		select {
		case <-s.done:
			exited = append(exited, s)
		default:
		}
	}
	sort.Slice(exited, func(i, j int) bool {
		return exited[i].exitedAt().After(exited[j].exitedAt())
	})
	removed := 0
	for i, s := range exited {
		if s.pinned() {
			continue
		}
		if i >= maxExitedSessions || now.Sub(s.exitedAt()) > exitedRetention {
			delete(d.sessions, s.id())
			removed++
		}
	}
	d.mu.Unlock()
	if removed > 0 {
		d.markDirty()
	}
}

func (s *Session) exitedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info.ExitedAt
}

func (s *Session) pinned() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info.Pinned
}

func (s *Session) id() string { return s.info.ID } // immutable after creation
