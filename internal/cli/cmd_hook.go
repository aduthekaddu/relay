package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// `relay hook <agent> <event> [payload]` is what agent hooks installed by
// Relay run (Claude Code Notification/Stop, Codex notify, Gemini, Cursor,
// OpenCode). It forwards the agent's hook JSON (stdin, or the last
// argument for Codex) to POST /api/v1/agents/hook with RELAY_SESSION, so
// the terminal shows "needs you" and a notification goes out.
//
// It must never slow down or break the agent: input is capped at 1 MiB,
// stdin is read for at most 1 s, the request has a 2 s budget, nothing is
// printed on stdout, and the exit status is always 0.
const (
	hookMaxInput    = 1 << 20
	hookStdinWait   = time.Second
	hookCallTimeout = 2 * time.Second
)

var hookNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,31}$`)

func init() {
	Register(&Command{
		Name:    "hook",
		Group:   "Terminal",
		Summary: "Forward a coding-agent hook event to Relay (used by installed hooks)",
		Usage:   "relay hook <agent> <event> [payload-json]",
		Run: func(ctx context.Context, fs *flag.FlagSet, args []string) error {
			runHook(ctx, args, os.Stdin, os.Stderr, os.Getenv)
			return nil
		},
	})
}

// runHook does the work of `relay hook`; errors only go to stderr when
// RELAY_HOOK_DEBUG is set.
func runHook(ctx context.Context, args []string, stdin *os.File, stderr io.Writer, getenv func(string) string) {
	debug := func(format string, a ...any) {
		if getenv("RELAY_HOOK_DEBUG") != "" {
			fmt.Fprintf(stderr, "relay hook: "+format+"\n", a...)
		}
	}
	var in []byte
	if len(args) < 3 && stdin != nil && !isTTY(stdin) {
		in = readHookInput(stdin, hookStdinWait)
	}
	req, err := buildHookRequest(args, in, getenv("RELAY_SESSION"))
	if err != nil {
		debug("%v", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, hookCallTimeout)
	defer cancel()
	var res api.AgentHookResult
	if err := CallLocal(ctx, "POST", "/api/v1/agents/hook", req, &res); err != nil {
		debug("%v", err)
		return
	}
	debug("%s (terminal %s)", res.Action, res.TerminalID)
}

// buildHookRequest validates argv and assembles the request. The payload
// is the last argument when given (Codex notify), else stdin; anything
// that is not a JSON value is wrapped as a JSON string.
func buildHookRequest(args []string, stdin []byte, session string) (*api.AgentHookRequest, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("usage: relay hook <agent> <event> [payload]")
	}
	if !hookNameRe.MatchString(args[0]) || !hookNameRe.MatchString(args[1]) {
		return nil, fmt.Errorf("invalid agent or event name")
	}
	payload := stdin
	if len(args) >= 3 {
		payload = []byte(args[len(args)-1])
	}
	if len(payload) > hookMaxInput {
		payload = payload[:hookMaxInput]
	}
	req := &api.AgentHookRequest{Agent: args[0], Event: args[1], SessionID: session}
	if t := bytes.TrimSpace(payload); len(t) > 0 {
		if json.Valid(t) {
			req.Payload = json.RawMessage(t)
		} else {
			b, _ := json.Marshal(string(bytes.ToValidUTF8(t, []byte("\uFFFD"))))
			req.Payload = b
		}
	}
	return req, nil
}

// readHookInput reads up to 1 MiB from r, giving up after wait (an agent
// that keeps stdin open must not block us). The reader goroutine exits
// when the process does.
func readHookInput(r io.Reader, wait time.Duration) []byte {
	done := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(r, hookMaxInput))
		done <- b
	}()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case b := <-done:
		return b
	case <-t.C:
		return nil
	}
}
