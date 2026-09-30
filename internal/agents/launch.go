package agents

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/server"
)

// busAudit is the backend-only audit topic (core.BusAudit once the auth
// feature lands it). internal/auth accepts api.AuditEntry payloads.
const busAudit = "audit"

// maxTerminalName caps generated terminal names.
const maxTerminalName = 60

// audit publishes an audit entry for a security-relevant action.
func (s *Service) audit(r *http.Request, event, detail string) {
	e := api.AuditEntry{Event: event, Actor: "system", Detail: truncate(detail, 200)}
	if r != nil {
		if p := server.PrincipalFrom(r.Context()); p != nil {
			e.Actor = p.User
			if p.Method == "local" || e.Actor == "" {
				e.Actor = "local"
			}
		}
		e.IP = httpx.ClientIP(r, nil)
	}
	s.publish(busAudit, e)
}

// resolveCwd validates a client-supplied working directory: "~" expanded,
// absolute, cleaned, symlinks resolved, an existing directory.
func (s *Service) resolveCwd(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	switch {
	case cwd == "" || cwd == "~":
		cwd = s.home
	case strings.HasPrefix(cwd, "~/"):
		cwd = filepath.Join(s.home, cwd[2:])
	}
	if !filepath.IsAbs(cwd) {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "cwd must be an absolute path", Field: "cwd"}
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(cwd))
	if err != nil {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "cwd does not exist", Field: "cwd"}
	}
	st, err := os.Stat(real)
	if err != nil || !st.IsDir() {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "cwd is not a directory", Field: "cwd"}
	}
	return real, nil
}

// terminalName builds "claude · <title>" (≤ 60 chars).
func terminalName(agent, title, cwd string) string {
	title = cleanTitle(title)
	if title == "" {
		title = filepath.Base(cwd)
	}
	return truncate(agent+" · "+title, maxTerminalName)
}

// launch starts a new interactive agent session in a ptyd terminal.
func (s *Service) launch(ctx context.Context, req api.LaunchAgentRequest) (*api.TerminalSession, error) {
	a, err := s.adapter(req.Agent)
	if err != nil {
		return nil, httpx.NotFound("unknown agent")
	}
	if len(req.Prompt) > 64<<10 {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "prompt too long", Field: "prompt"}
	}
	if !validModel(req.Model) {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "invalid model name", Field: "model"}
	}
	if len(req.Name) > 200 {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "name too long", Field: "name"}
	}
	cwd, err := s.resolveCwd(req.Cwd)
	if err != nil {
		return nil, err
	}
	if s.pty == nil {
		return nil, httpx.Unavailable("terminal daemon unavailable")
	}
	bin, err := s.binary(ctx, a)
	if err != nil {
		return nil, err
	}
	if req.Worktree != nil {
		if cwd, err = s.launchWorktree(ctx, cwd, req.Worktree); err != nil {
			return nil, err
		}
	}
	prompt := req.Prompt
	if !a.Caps.Prompt {
		prompt = ""
	}
	argv := a.Interactive(bin, prompt, req.Model)
	var native string
	if a.PresetID != nil {
		var extra []string
		extra, native = a.PresetID()
		argv = append(append(append([]string{}, argv[0]), extra...), argv[1:]...)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = terminalName(a.ID, req.Prompt, cwd)
	}
	spec := ptyclient.CreateSpec{
		CreateTerminalRequest: api.CreateTerminalRequest{Name: name, Command: argv, Cwd: cwd, Kind: api.KindAgent,
			Agent: a.ID, Cols: req.Cols, Rows: req.Rows, Meta: map[string]string{"agent": a.ID}},
		AgentSessionID: native,
		Workspace:      s.workspaceOf(cwd),
	}
	t, err := s.pty.Create(ctx, spec)
	if err != nil {
		return nil, ptyErr(err)
	}
	s.live.repair()
	return t, nil
}

// launchWorktree creates the requested git worktree and returns its path.
func (s *Service) launchWorktree(ctx context.Context, cwd string, wt *api.WorktreeOption) (string, error) {
	if s.worktrees == nil || s.d.Workspaces == nil {
		return "", httpx.Unavailable("workspaces are not available")
	}
	repo := s.d.Workspaces.RootOf(cwd)
	if repo == "" {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "cwd is not inside a git repository", Field: "worktree"}
	}
	w, err := s.worktrees.CreateWorktree(ctx, repo, wt.Branch, wt.Base)
	if err != nil {
		return "", err
	}
	return w.Path, nil
}

// resume reopens (or forks) an indexed session in a new terminal. When
// the session is already running (and fork is false) its terminal is
// returned instead of starting a second process on the same transcript.
func (s *Service) resume(ctx context.Context, id string, req api.ResumeAgentRequest) (*api.TerminalSession, error) {
	_, row, err := s.getSession(ctx, id)
	if err != nil {
		return nil, err
	}
	a, err := s.adapter(row.Agent)
	if err != nil {
		return nil, httpx.NotFound("unknown agent")
	}
	build := a.Resume
	if req.Fork {
		build = a.Fork
	}
	if build == nil || (!req.Fork && !row.Resumable) {
		verb := "resume"
		if req.Fork {
			verb = "fork"
		}
		return nil, httpx.Conflict(a.Name + " cannot " + verb + " sessions")
	}
	if !validNativeID(row.NativeID) {
		return nil, httpx.Conflict("session id cannot be passed to " + a.Name)
	}
	if !req.Fork {
		if t, ok := s.live.snapshot(ctx).bySession[row.ID]; ok {
			return &t, nil
		}
	}
	if s.pty == nil {
		return nil, httpx.Unavailable("terminal daemon unavailable")
	}
	bin, err := s.binary(ctx, a)
	if err != nil {
		return nil, err
	}
	cwd := row.Cwd
	if st, err := os.Stat(cwd); cwd == "" || err != nil || !st.IsDir() {
		cwd = s.home
	}
	native := row.NativeID
	prefix := "↻ "
	if req.Fork {
		native, prefix = "", "⑂ "
	}
	title := row.Title // user title already applied by scanSession
	spec := ptyclient.CreateSpec{
		CreateTerminalRequest: api.CreateTerminalRequest{Name: truncate(a.ID+" · "+prefix+cleanTitle(title), maxTerminalName),
			Command: build(bin, row.NativeID), Cwd: cwd, Kind: api.KindAgent, Agent: a.ID, Cols: req.Cols, Rows: req.Rows,
			Meta: map[string]string{"agent": a.ID, "resumeOf": row.ID}},
		AgentSessionID: native,
		Workspace:      s.workspaceOf(cwd),
	}
	t, err := s.pty.Create(ctx, spec)
	if err != nil {
		return nil, ptyErr(err)
	}
	s.live.repair()
	return t, nil
}

func (s *Service) workspaceOf(path string) string {
	if s.d.Workspaces == nil {
		return ""
	}
	return s.d.Workspaces.RootOf(path)
}

// ptyErr maps ptyd client errors to HTTP errors.
func ptyErr(err error) error {
	var he *httpx.Err
	switch {
	case errors.As(err, &he):
		return err
	case errors.Is(err, ptyclient.ErrUnavailable):
		return httpx.Unavailable("terminal daemon unavailable")
	case errors.Is(err, ptyclient.ErrNotFound):
		return httpx.NotFound("terminal not found")
	}
	return err
}

// handleHookEvent applies one `relay hook` call: it finds the terminal
// the agent runs in, sets or clears its attention state and notifies.
func (s *Service) handleHookEvent(ctx context.Context, req api.AgentHookRequest) (*api.AgentHookResult, error) {
	a, err := s.adapter(req.Agent)
	if err != nil {
		return nil, httpx.NotFound("unknown agent")
	}
	res := &api.AgentHookResult{Action: "ignored"}
	if a.ParseHook == nil {
		return res, nil
	}
	ev := a.ParseHook(req.Event, req.Payload)
	if ev.Action == "" {
		return res, nil
	}
	ev.Message = truncate(strings.TrimSpace(ansiRe.ReplaceAllString(ev.Message, "")), 300)
	t, found := s.live.terminalFor(ctx, a.ID, req.SessionID, ev.NativeID, ev.Cwd)
	if found && ev.NativeID != "" && t.AgentSessionID == "" {
		s.live.hint(t.ID, ev.NativeID)
	}
	res.Action = ev.Action
	if found {
		res.TerminalID = t.ID
		if s.pty != nil {
			var att *api.Attention
			if ev.Action == hookAttention {
				att = &api.Attention{Reason: "hook", Message: ev.Message, At: time.Now().UTC()}
			}
			actx, cancel := context.WithTimeout(ctx, 2*time.Second)
			if err := s.pty.SetAttention(actx, t.ID, att); err != nil {
				s.log().Debug("agents: set attention", "terminal", t.ID, "err", err)
			}
			cancel()
		}
	}
	if ev.Action == hookDone {
		s.idx.trigger(false) // pick up the new turn quickly
	}
	// A turn finishing outside Relay is not worth a notification; a
	// request for input always is.
	if s.d.Notifier != nil && (found || ev.Action == hookAttention) {
		n := api.NotifyRequest{Kind: ev.Action, Agent: a.ID, Body: ev.Message, SessionID: req.SessionID}
		if ev.Action == hookAttention {
			n.Title = a.Name + " needs you"
		} else {
			n.Title = a.Name + " finished"
		}
		if found {
			n.Link = "/terminal/" + t.ID
			n.SessionID = t.ID
			if t.Name != "" {
				n.Title += " · " + truncate(t.Name, 40)
			}
		}
		nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		if _, err := s.d.Notifier.Notify(nctx, n); err != nil {
			s.log().Debug("agents: notify", "err", err)
		}
		cancel()
	}
	return res, nil
}
