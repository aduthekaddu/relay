package search

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

// maxBody bounds JSON request bodies (SECURITY.md rule 4).
const maxBody = 1 << 20

func (s *Service) handleScripts(w http.ResponseWriter, _ *http.Request) {
	httpx.OK(w, s.Scripts())
}

func (s *Service) handleRunScript(w http.ResponseWriter, r *http.Request) {
	var req api.RunScriptRequest
	if r.ContentLength != 0 {
		if err := httpx.DecodeLimit(r, &req, maxBody); err != nil {
			httpx.Fail(w, httpx.BadRequest("invalid body"))
			return
		}
	}
	res, err := s.RunScript(r.Context(), r.PathValue("id"), req.Args)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, res)
}

// RunScript runs the script command id with args in its mode. Inline and
// silent runs are bounded by the script timeout; terminal runs start a
// task terminal and return its id immediately.
func (s *Service) RunScript(ctx context.Context, id string, args []string) (*api.RunScriptResult, error) {
	cmd, ok := s.script(id)
	if !ok {
		return nil, httpx.NotFound("no such script command")
	}
	argv, msg := validateArgs(cmd, args)
	if msg != "" {
		return nil, httpx.BadRequest(msg)
	}
	if s.d.Log != nil {
		s.d.Log.Info("script command run", "id", cmd.ID, "mode", cmd.Mode)
	}
	if cmd.Mode == ModeTerminal {
		return s.runScriptTerminal(ctx, cmd, argv)
	}
	select {
	case s.scriptSlots <- struct{}{}:
		defer func() { <-s.scriptSlots }()
	default:
		return nil, tooMany("too many script commands running", time.Second)
	}
	out, code, err := runCaptured(ctx, s.scriptTimeout, cmd.Cwd, cmd.Path, argv, ScriptOutputMax)
	if err != nil {
		return nil, err
	}
	if cmd.Mode == ModeSilent {
		out = lastLine(out)
	}
	return &api.RunScriptResult{Output: out, ExitCode: code}, nil
}

func (s *Service) runScriptTerminal(ctx context.Context, cmd api.ScriptCommand, argv []string) (*api.RunScriptResult, error) {
	if s.pty == nil {
		return nil, httpx.Unavailable("terminal daemon unavailable")
	}
	term, err := s.pty.Create(ctx, ptyclient.CreateSpec{
		CreateTerminalRequest: api.CreateTerminalRequest{
			Name:    "▶ " + cmd.Title,
			Command: append([]string{cmd.Path}, argv...),
			Cwd:     cmd.Cwd,
			Kind:    "task",
			Meta:    map[string]string{"script": cmd.ID},
		},
	})
	if err != nil {
		if errors.Is(err, ptyclient.ErrUnavailable) {
			return nil, httpx.Unavailable("terminal daemon unavailable")
		}
		return nil, err
	}
	return &api.RunScriptResult{TerminalID: term.ID}, nil
}

// runCaptured executes path with argv in dir under timeout and returns up
// to max bytes of stdout and the exit code. When stdout is empty and the
// command failed, the stderr tail is returned instead so the user sees why.
// The whole process group is killed on timeout or cancellation.
func runCaptured(ctx context.Context, timeout time.Duration, dir, path string, argv []string, max int) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.CommandContext(ctx, path, argv...)
	c.Dir = dir
	c.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	stdout := &capBuffer{max: max}
	stderr := &capBuffer{max: 8 << 10}
	c.Stdout, c.Stderr = stdout, stderr
	setProcessGroup(c)
	c.WaitDelay = 2 * time.Second
	err := c.Run()
	out := stdout.String()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return out, 0, nil
	case ctx.Err() != nil:
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return out, -1, &httpx.Err{Status: http.StatusGatewayTimeout, Code: "timeout", Message: "script timed out after " + timeout.String()}
		}
		return out, -1, ctx.Err()
	case errors.As(err, &exitErr):
		if strings.TrimSpace(out) == "" {
			out = stderr.String()
		}
		return out, exitErr.ExitCode(), nil
	default:
		return "", -1, httpx.BadRequest("cannot start script: " + err.Error())
	}
}

// lastLine returns the last non-empty line of s.
func lastLine(s string) string {
	s = strings.TrimRight(s, "\r\n\t ")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

// capBuffer keeps the first max bytes written and discards the rest while
// still reporting full writes (so the child never sees EPIPE).
type capBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
			b.truncated = true
		} else {
			b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

// String returns the captured bytes as valid UTF-8 (a rune cut at the cap
// is dropped).
func (b *capBuffer) String() string {
	return strings.ToValidUTF8(b.buf.String(), "")
}
