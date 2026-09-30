package terminal

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

// tmuxTimeout bounds every tmux invocation.
const tmuxTimeout = 2 * time.Second

// tmuxFormat asks list-sessions for tab-separated fields we parse.
const tmuxFormat = "#{session_name}\t#{session_windows}\t#{session_attached}\t#{session_created}\t#{session_path}"

// metaTmux names the tmux session a Relay session is attached to.
const metaTmux = "tmux"

// tmuxSession is one line of `tmux list-sessions`.
type tmuxSession struct {
	Name     string
	Windows  int
	Attached int
	Created  time.Time
	Path     string
}

// listTmux returns the sessions of the tmux server named by s.tmux. A
// missing tmux binary or a server that is not running yields no sessions.
func (s *Service) listTmux(ctx context.Context) []tmuxSession {
	if len(s.tmux) == 0 {
		return nil
	}
	bin, err := exec.LookPath(s.tmux[0])
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, tmuxTimeout)
	defer cancel()
	args := append(append([]string{}, s.tmux[1:]...), "list-sessions", "-F", tmuxFormat)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = tmuxEnv()
	out, err := cmd.Output()
	if err != nil {
		return nil // "no server running" exits 1
	}
	return parseTmux(out)
}

// tmuxEnv is the environment for tmux queries: the server's own, minus
// TMUX so that listing works even when relay itself runs inside tmux.
func tmuxEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// parseTmux parses list-sessions output in tmuxFormat. Malformed lines
// and names with control characters are skipped.
func parseTmux(out []byte) []tmuxSession {
	var list []tmuxSession
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		f := strings.Split(string(line), "\t")
		if len(f) < 5 || f[0] == "" || len(f[0]) > 200 || strings.ContainsFunc(f[0], isControl) {
			continue
		}
		t := tmuxSession{Name: f[0], Path: strings.Join(f[4:], "\t")}
		t.Windows, _ = strconv.Atoi(f[1])
		t.Attached, _ = strconv.Atoi(f[2])
		if sec, err := strconv.ParseInt(f[3], 10, 64); err == nil && sec > 0 {
			t.Created = time.Unix(sec, 0).UTC()
		}
		list = append(list, t)
	}
	return list
}

// tmuxAttachArgv is the command a Relay session runs to import name. The
// "=" prefix makes tmux match the name exactly rather than as a prefix.
func (s *Service) tmuxAttachArgv(name string) []string {
	return append(append([]string{}, s.tmux...), "attach-session", "-t", "="+name)
}

// importable lists tmux sessions not already open in a live Relay
// session, shaped as TerminalSession entries with meta.importable=1.
func (s *Service) importable(ctx context.Context, live []api.TerminalSession) []api.TerminalSession {
	tm := s.listTmux(ctx)
	if len(tm) == 0 {
		return nil
	}
	open := map[string]bool{}
	for _, t := range live {
		if t.Activity != api.ActivityExited && t.Kind == api.KindTmux && t.Meta[metaTmux] != "" {
			open[t.Meta[metaTmux]] = true
		}
	}
	out := make([]api.TerminalSession, 0, len(tm))
	for _, t := range tm {
		if open[t.Name] {
			continue
		}
		out = append(out, api.TerminalSession{
			ID:        "tmux:" + t.Name,
			Name:      t.Name,
			Kind:      api.KindTmux,
			Command:   s.tmuxAttachArgv(t.Name),
			Cwd:       t.Path,
			Activity:  api.ActivityIdle,
			Clients:   t.Attached,
			CreatedAt: t.Created,
			Meta: map[string]string{
				"importable": "1",
				metaTmux:     t.Name,
				"windows":    strconv.Itoa(t.Windows),
			},
		})
	}
	return out
}

// importTmux creates (or returns the existing live) Relay session attached
// to a tmux session. The tmux name comes from meta.tmux, else name.
func (s *Service) importTmux(ctx context.Context, req api.CreateTerminalRequest) (*api.TerminalSession, error) {
	name := req.Meta[metaTmux]
	if name == "" {
		name = req.Name
	}
	if name == "" {
		return nil, httpx.BadRequest("tmux session name required (meta.tmux)")
	}
	var found *tmuxSession
	for _, t := range s.listTmux(ctx) {
		if t.Name == name {
			found = &t
			break
		}
	}
	if found == nil {
		return nil, httpx.NotFound("no such tmux session")
	}
	live, err := s.pty.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range live {
		t := &live[i]
		if t.Kind == api.KindTmux && t.Meta[metaTmux] == name && t.Activity != api.ActivityExited {
			return t, nil
		}
	}
	meta := map[string]string{metaTmux: name}
	for k, v := range req.Meta {
		if k != "importable" && k != metaTmux {
			meta[k] = v
		}
	}
	cwd := s.resolveCwd(req.Cwd)
	if cwd == "" {
		cwd = found.Path
	}
	if cwd != "" {
		if st, err := statFunc(cwd); err != nil || !st.IsDir() {
			cwd = "" // ptyd falls back to the default directory
		}
	}
	spec := ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{
		Name:    name,
		Command: s.tmuxAttachArgv(name),
		Cwd:     cwd,
		Env:     req.Env,
		Kind:    api.KindTmux,
		Cols:    req.Cols,
		Rows:    req.Rows,
		Record:  req.Record,
		Meta:    meta,
	}}
	if s.d.Workspaces != nil && cwd != "" {
		spec.Workspace = s.d.Workspaces.RootOf(cwd)
	}
	return s.pty.Create(ctx, spec)
}
