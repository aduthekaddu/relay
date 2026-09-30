package search

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

// Quick AI limits.
const (
	askPromptMax = 32 << 10
	askChunkMax  = 4 << 10
	askStderrMax = 4 << 10
)

// Ask chunk types.
const (
	ChunkText  = "text"
	ChunkDone  = "done"
	ChunkError = "error"
)

func (s *Service) handleAsk(w http.ResponseWriter, r *http.Request) {
	var req api.AskRequest
	if err := httpx.DecodeLimit(r, &req, maxBody); err != nil {
		httpx.Fail(w, httpx.BadRequest("invalid body"))
		return
	}
	argv, cwd, err := s.prepareAsk(r.Context(), req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	release, refused := s.ask.acquire()
	if refused != nil {
		msg := "Quick AI allows " + strconv.Itoa(AskPerHour) + " questions per hour"
		if refused.busy {
			msg = "Quick AI is already answering a question"
		}
		httpx.Fail(w, tooMany(msg, refused.wait))
		return
	}
	defer release()

	h := w.Header()
	h.Set("Content-Type", "application/x-ndjson; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s.streamAsk(r.Context(), newChunkWriter(w), argv, cwd)
}

// prepareAsk validates the request, picks the agent and resolves argv.
func (s *Service) prepareAsk(ctx context.Context, req api.AskRequest) ([]string, string, error) {
	prompt := strings.TrimSpace(req.Prompt)
	switch {
	case prompt == "":
		return nil, "", httpx.BadRequest("prompt is required")
	case len(prompt) > askPromptMax:
		return nil, "", httpx.BadRequest("prompt is too long")
	case strings.ContainsRune(prompt, 0):
		return nil, "", httpx.BadRequest("prompt contains a NUL byte")
	}
	if s.d.Agents == nil {
		return nil, "", httpx.Unavailable("no agent service")
	}
	agent, err := s.pickAgent(ctx, req.Agent)
	if err != nil {
		return nil, "", err
	}
	cwd, err := s.askCwd(req.Cwd)
	if err != nil {
		return nil, "", err
	}
	argv, err := s.d.Agents.HeadlessCommand(ctx, agent, prompt, "")
	if err != nil || len(argv) == 0 {
		return nil, "", httpx.BadRequest("agent " + agent + " cannot answer headlessly")
	}
	return argv, cwd, nil
}

// pickAgent returns want when it is installed and headless-capable, or the
// first such agent when want is empty.
func (s *Service) pickAgent(ctx context.Context, want string) (string, error) {
	for _, a := range s.d.Agents.List(ctx) {
		ok := a.Installed && a.Capabilities.Headless
		if want == "" && ok {
			return a.ID, nil
		}
		if a.ID == want {
			if !ok {
				return "", httpx.BadRequest("agent " + want + " is not installed or has no headless mode")
			}
			return a.ID, nil
		}
	}
	if want != "" {
		return "", httpx.BadRequest("unknown agent " + want)
	}
	return "", httpx.Unavailable("no installed agent supports headless mode")
}

// askCwd defaults to the home directory and requires an existing
// directory otherwise.
func (s *Service) askCwd(cwd string) (string, error) {
	if cwd == "" {
		return s.homeDir(), nil
	}
	cwd = filepath.Clean(expandHome(cwd, s.homeDir()))
	if !filepath.IsAbs(cwd) {
		return "", httpx.BadRequest("cwd must be absolute")
	}
	fi, err := os.Stat(cwd)
	if err != nil || !fi.IsDir() {
		return "", httpx.BadRequest("cwd is not a directory")
	}
	return cwd, nil
}

// streamAsk runs argv and forwards stdout as text chunks, ending with a
// done or error chunk. Cancelling ctx (client gone) kills the agent.
func (s *Service) streamAsk(ctx context.Context, cw *chunkWriter, argv []string, cwd string) {
	ctx, cancel := context.WithTimeout(ctx, s.askTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Dir = cwd
	c.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "CI=1")
	stderr := &tailBuffer{max: askStderrMax}
	c.Stderr = stderr
	setProcessGroup(c)
	c.WaitDelay = 2 * time.Second
	stdout, err := c.StdoutPipe()
	if err != nil {
		cw.write(api.AskChunk{T: ChunkError, Text: "cannot start agent"})
		return
	}
	if err := c.Start(); err != nil {
		cw.write(api.AskChunk{T: ChunkError, Text: "cannot start agent: " + err.Error()})
		return
	}
	pumpText(stdout, cw)
	err = c.Wait()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		cw.write(api.AskChunk{T: ChunkError, Text: "the agent did not finish within " + s.askTimeout.String()})
	case ctx.Err() != nil:
		// client went away; nothing to write to
	case err != nil:
		msg := "the agent failed"
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg += " (exit " + strconv.Itoa(ee.ExitCode()) + ")"
		}
		if t := strings.TrimSpace(stderr.String()); t != "" {
			msg += ": " + t
		}
		cw.write(api.AskChunk{T: ChunkError, Text: msg})
	default:
		cw.write(api.AskChunk{T: ChunkDone})
	}
}

// pumpText copies r to text chunks, never splitting a UTF-8 sequence.
func pumpText(r io.Reader, cw *chunkWriter) {
	buf := make([]byte, askChunkMax)
	pending := 0
	for {
		n, err := r.Read(buf[pending:])
		n += pending
		cut := validPrefix(buf[:n])
		if cut > 0 {
			cw.write(api.AskChunk{T: ChunkText, Text: string(buf[:cut])})
		}
		pending = copy(buf, buf[cut:n])
		if err != nil {
			if pending > 0 {
				cw.write(api.AskChunk{T: ChunkText, Text: strings.ToValidUTF8(string(buf[:pending]), "\uFFFD")})
			}
			return
		}
	}
}

// validPrefix returns the length of b without a trailing incomplete rune.
// Invalid bytes elsewhere are passed through (json.Marshal replaces them).
// At most utf8.UTFMax-1 bytes are held back, so the pump always has room
// for the next read.
func validPrefix(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return i
			}
			break
		}
	}
	return len(b)
}

// chunkWriter encodes AskChunks as NDJSON and flushes after each one.
type chunkWriter struct {
	rc  *http.ResponseController
	enc *json.Encoder
	err error
}

func newChunkWriter(w http.ResponseWriter) *chunkWriter {
	return &chunkWriter{rc: http.NewResponseController(w), enc: json.NewEncoder(w)}
}

func (c *chunkWriter) write(ch api.AskChunk) {
	if c.err != nil {
		return
	}
	if c.err = c.enc.Encode(ch); c.err == nil {
		if err := c.rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			c.err = err
		}
	}
}

// tailBuffer keeps the last max bytes written.
type tailBuffer struct {
	b   []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if over := len(t.b) - t.max; over > 0 {
		t.b = append(t.b[:0], t.b[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return strings.ToValidUTF8(string(t.b), "") }
