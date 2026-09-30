// Package terminal exposes ptyd terminal sessions over the Relay HTTP API
// (list, create, control, attach WebSocket bridge, recordings), imports
// existing tmux sessions, republishes ptyd events on the in-process bus
// and handles chunked, resumable uploads.
//
// ptyd owns the processes; this package is a thin, validating front for
// it. See docs/dev/PTYD.md and the "terminals & uploads" section of
// docs/dev/API.md.
package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/server"
)

// ClipFunc receives text an application copied with OSC 52.
type ClipFunc func(ctx context.Context, sessionID, text string)

// Service implements the terminal and upload routes.
type Service struct {
	d   *core.Deps
	pty *ptyclient.Client
	// tmux is the argv prefix used to talk to the tmux server (tests
	// point it at a private socket with -S).
	tmux []string
	up   *uploads

	// OnClip, when set, receives OSC 52 clipboard text from sessions.
	OnClip ClipFunc
}

// New constructs the service. It starts no goroutines; see Start.
func New(d *core.Deps) (*Service, error) {
	if d == nil || d.Cfg == nil {
		return nil, errors.New("terminal: missing dependencies")
	}
	if d.Pty == nil {
		d.Pty = ptyclient.New(d.Paths.PtydSocket)
	}
	up, err := newUploads(d.Paths.Uploads, d.Cfg.Files.Root, int64(d.Cfg.Terminal.UploadMaxMB)<<20)
	if err != nil {
		return nil, err
	}
	return &Service{d: d, pty: d.Pty, tmux: []string{"tmux"}, up: up}, nil
}

// Routes registers every terminal and upload endpoint.
func (s *Service) Routes(rt *server.Router) {
	rt.Handle("GET /api/v1/terminals", s.hList)
	rt.Handle("POST /api/v1/terminals", s.hCreate)
	rt.Handle("GET /api/v1/terminals/{id}", s.hGet)
	rt.Handle("PATCH /api/v1/terminals/{id}", s.hPatch)
	rt.Handle("DELETE /api/v1/terminals/{id}", s.hDelete)
	rt.Handle("POST /api/v1/terminals/{id}/input", s.hInput)
	rt.Handle("POST /api/v1/terminals/{id}/resize", s.hResize)
	rt.Handle("GET /api/v1/terminals/{id}/snapshot", s.hSnapshot)
	rt.Handle("POST /api/v1/terminals/{id}/attention/ack", s.hAck)
	rt.Handle("POST /api/v1/terminals/{id}/restore", s.hRestore)
	rt.Handle("GET /api/v1/terminals/{id}/recording", s.hRecording)
	rt.WS("GET /api/v1/terminals/{id}/attach", s.hAttach)

	rt.Handle("POST /api/v1/uploads", s.hUploadStart)
	rt.Handle("GET /api/v1/uploads/{id}", s.hUploadGet)
	rt.Handle("PUT /api/v1/uploads/{id}", s.hUploadPut)
	rt.Handle("POST /api/v1/uploads/{id}/complete", s.hUploadComplete)
	rt.Handle("DELETE /api/v1/uploads/{id}", s.hUploadAbort)
}

// Start runs the background loops (ptyd event relay, stale upload sweep)
// until ctx is done.
func (s *Service) Start(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.up.sweepLoop(ctx)
	}()
	s.relayEvents(ctx)
	<-done
	return ctx.Err()
}

// SearchProvider returns the command-center provider for scope "terminals".
func (s *Service) SearchProvider() core.SearchProvider { return &searchProvider{s: s} }

// ptyErr converts ptyclient errors into HTTP errors safe to show users.
func ptyErr(err error) error {
	var se *ptyclient.StatusError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ptyclient.ErrNotFound):
		return httpx.NotFound("terminal session not found")
	case errors.Is(err, ptyclient.ErrUnavailable):
		return httpx.Unavailable("the terminal daemon is not running")
	case errors.As(err, &se):
		code := se.Code
		if code == "" {
			code = "ptyd"
		}
		return &httpx.Err{Status: se.Status, Code: code, Message: se.Message}
	case errors.Is(err, context.DeadlineExceeded):
		return &httpx.Err{Status: http.StatusGatewayTimeout, Code: "timeout", Message: "the terminal daemon did not answer in time"}
	}
	return err
}

func fail(w http.ResponseWriter, err error) { httpx.Fail(w, ptyErr(err)) }

// validSessionID is a cheap syntactic check before an id reaches ptyd.
func validSessionID(id string) bool {
	if len(id) != 12 || !strings.HasPrefix(id, "t_") {
		return false
	}
	for _, c := range id[2:] {
		if !(c >= 'a' && c <= 'z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}

// sessionID extracts and validates the {id} path value.
func sessionID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !validSessionID(id) {
		httpx.Fail(w, httpx.NotFound("terminal session not found"))
		return "", false
	}
	return id, true
}

func (s *Service) hList(w http.ResponseWriter, r *http.Request) {
	list, err := s.pty.List(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	if list == nil {
		list = []api.TerminalSession{}
	}
	if s.d.Cfg.Terminal.ImportTmux {
		list = append(list, s.importable(r.Context(), list)...)
	}
	httpx.OK(w, list)
}

func (s *Service) hCreate(w http.ResponseWriter, r *http.Request) {
	var req api.CreateTerminalRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	var (
		sess *api.TerminalSession
		err  error
	)
	if req.Kind == api.KindTmux {
		sess, err = s.importTmux(r.Context(), req)
	} else {
		sess, err = s.create(r.Context(), req)
	}
	if err != nil {
		fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, sess)
}

// create validates a request and starts the session in ptyd.
func (s *Service) create(ctx context.Context, req api.CreateTerminalRequest) (*api.TerminalSession, error) {
	switch req.Kind {
	case "", api.KindShell, api.KindAgent, api.KindTask, api.KindToolbox:
	default:
		return nil, httpx.BadRequest("unknown terminal kind")
	}
	if len(req.Name) > 200 {
		return nil, httpx.BadRequest("name too long")
	}
	if len(req.Command) > 256 {
		return nil, httpx.BadRequest("too many command arguments")
	}
	for _, a := range req.Command {
		if strings.IndexByte(a, 0) >= 0 {
			return nil, httpx.BadRequest("command arguments must not contain NUL")
		}
	}
	if len(req.Command) > 0 && req.Command[0] == "" {
		return nil, httpx.BadRequest("empty command")
	}
	if req.Cols < 0 || req.Rows < 0 || req.Cols > 1000 || req.Rows > 500 {
		return nil, httpx.BadRequest("invalid terminal size")
	}
	spec := ptyclient.CreateSpec{CreateTerminalRequest: req}
	spec.Cwd = s.resolveCwd(req.Cwd)
	if s.d.Workspaces != nil && spec.Cwd != "" {
		spec.Workspace = s.d.Workspaces.RootOf(spec.Cwd)
	}
	return s.pty.Create(ctx, spec)
}

// resolveCwd expands "~" and makes relative paths relative to home. ptyd
// validates that the result is a directory.
func (s *Service) resolveCwd(cwd string) string {
	home := s.d.Paths.Home
	switch {
	case cwd == "":
		return ""
	case cwd == "~":
		return home
	case strings.HasPrefix(cwd, "~/"):
		return filepath.Join(home, cwd[2:])
	case !filepath.IsAbs(cwd):
		return filepath.Join(home, cwd)
	}
	return filepath.Clean(cwd)
}

func (s *Service) hGet(w http.ResponseWriter, r *http.Request) {
	id, ok := sessionID(w, r)
	if !ok {
		return
	}
	sess, err := s.pty.Get(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, sess)
}

func (s *Service) hPatch(w http.ResponseWriter, r *http.Request) {
	id, ok := sessionID(w, r)
	if !ok {
		return
	}
	var req api.UpdateTerminalRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if len(n) > 200 || strings.ContainsFunc(n, isControl) {
			httpx.Fail(w, httpx.BadRequest("invalid name"))
			return
		}
		req.Name = &n
	}
	sess, err := s.pty.Update(r.Context(), id, req)
	if err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, sess)
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

var signals = map[string]bool{"": true, "TERM": true, "KILL": true, "INT": true, "HUP": true, "QUIT": true, "USR1": true, "USR2": true}

func (s *Service) hDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := sessionID(w, r)
	if !ok {
		return
	}
	var err error
	if httpx.QueryBool(r, "forget") {
		err = s.pty.Remove(r.Context(), id)
	} else {
		sig := strings.ToUpper(strings.TrimPrefix(strings.ToUpper(r.URL.Query().Get("signal")), "SIG"))
		if !signals[sig] {
			httpx.Fail(w, httpx.BadRequest("unknown signal"))
			return
		}
		err = s.pty.Kill(r.Context(), id, sig)
	}
	if err != nil {
		fail(w, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Service) hInput(w http.ResponseWriter, r *http.Request) {
	id, ok := sessionID(w, r)
	if !ok {
		return
	}
	var in api.TerminalInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	var err error
	if in.Paste {
		err = s.pty.Paste(r.Context(), id, []byte(in.Data))
	} else {
		err = s.pty.Input(r.Context(), id, []byte(in.Data))
	}
	if err != nil {
		fail(w, err)
		return
	}
	httpx.NoContent(w)
}

type sizeRequest struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

func (s *Service) hResize(w http.ResponseWriter, r *http.Request) {
	id, ok := sessionID(w, r)
	if !ok {
		return
	}
	var req sizeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.Cols < 1 || req.Rows < 1 || req.Cols > 1000 || req.Rows > 500 {
		httpx.Fail(w, httpx.BadRequest("cols must be 1-1000 and rows 1-500"))
		return
	}
	if err := s.pty.Resize(r.Context(), id, req.Cols, req.Rows); err != nil {
		fail(w, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Service) hSnapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := sessionID(w, r)
	if !ok {
		return
	}
	snap, err := s.pty.Snapshot(r.Context(), id, httpx.QueryInt(r, "lines", 40, 1, 5000))
	if err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, snap)
}

func (s *Service) hAck(w http.ResponseWriter, r *http.Request) {
	id, ok := sessionID(w, r)
	if !ok {
		return
	}
	if err := s.pty.SetAttention(r.Context(), id, nil); err != nil {
		fail(w, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Service) hRestore(w http.ResponseWriter, r *http.Request) {
	id, ok := sessionID(w, r)
	if !ok {
		return
	}
	sess, err := s.pty.Restore(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, sess)
}

func (s *Service) hRecording(w http.ResponseWriter, r *http.Request) {
	id, ok := sessionID(w, r)
	if !ok {
		return
	}
	// Recordings can be large; allow the copy more time than an API call.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	rc, err := s.pty.Recording(ctx, id)
	if err != nil {
		fail(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/x-asciicast")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if httpx.QueryBool(r, "download") {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".cast"))
	}
	if _, err := io.Copy(w, rc); err != nil && !errors.Is(err, context.Canceled) {
		s.d.Log.Debug("recording copy interrupted", "id", id, "err", err)
	}
}

// statFunc is os.Stat, replaceable in tests.
var statFunc = os.Stat
