package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/term"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

// detachPrefix (Ctrl+\) followed by 'd' detaches `relay attach`.
const detachPrefix = 0x1c

// ackEvery is how many output bytes the CLI processes between flow
// control acknowledgements.
const ackEvery = 64 << 10

func init() {
	Register(&Command{
		Name:    "attach",
		Group:   "Terminal",
		Summary: "Attach this terminal to a Relay session (detach: Ctrl+\\ then d)",
		Usage:   "relay attach [--readonly] [--no-replay] <id|name>",
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("readonly", false, "watch without sending input or resizing")
			fs.Bool("no-replay", false, "do not replay the scrollback buffer")
		},
		Run: runAttach,
	})
}

// ptydClient returns a client for this user's ptyd. With ensure, the
// daemon is started when it is not running.
func ptydClient(ctx context.Context, ensure bool) (*ptyclient.Client, error) {
	paths, err := config.ResolvePaths()
	if err != nil {
		return nil, err
	}
	if ensure {
		if err := paths.Ensure(); err != nil {
			return nil, fmt.Errorf("create directories: %w", err)
		}
		if err := ptyclient.EnsureDaemon(ctx, paths, ""); err != nil {
			return nil, err
		}
	}
	return ptyclient.New(paths.PtydSocket), nil
}

// friendly rewrites daemon connection errors for people.
func friendly(err error) error {
	if errors.Is(err, ptyclient.ErrUnavailable) {
		return &ExitError{Code: 1, Msg: "the terminal daemon is not running (start it with `relay ptyd` or `relay serve`)"}
	}
	return err
}

// resolveSession finds a session by exact id, exact name (live sessions
// first) or unique id prefix.
func resolveSession(ctx context.Context, c *ptyclient.Client, ref string) (*api.TerminalSession, error) {
	list, err := c.List(ctx)
	if err != nil {
		return nil, friendly(err)
	}
	return pickSession(list, ref)
}

func pickSession(list []api.TerminalSession, ref string) (*api.TerminalSession, error) {
	var byName, byPrefix []*api.TerminalSession
	for i := range list {
		t := &list[i]
		if t.ID == ref {
			return t, nil
		}
		if t.Name == ref {
			byName = append(byName, t)
		}
		if strings.HasPrefix(t.ID, ref) && len(ref) >= 4 {
			byPrefix = append(byPrefix, t)
		}
	}
	for _, cands := range [][]*api.TerminalSession{byName, byPrefix} {
		var live []*api.TerminalSession
		for _, t := range cands {
			if t.Activity != api.ActivityExited {
				live = append(live, t)
			}
		}
		if len(live) == 1 {
			return live[0], nil
		}
		if len(live) == 0 && len(cands) == 1 {
			return cands[0], nil
		}
		if len(live) > 1 || len(cands) > 1 {
			ids := make([]string, 0, len(cands))
			for _, t := range cands {
				ids = append(ids, t.ID)
			}
			return nil, &ExitError{Code: 1, Msg: fmt.Sprintf("%q matches several sessions: %s", ref, strings.Join(ids, ", "))}
		}
	}
	return nil, &ExitError{Code: 1, Msg: fmt.Sprintf("no session %q (see `relay ls`)", ref)}
}

func runAttach(ctx context.Context, fs *flag.FlagSet, args []string) error {
	if len(args) != 1 {
		fs.Usage()
		return &ExitError{Code: 2}
	}
	c, err := ptydClient(ctx, false)
	if err != nil {
		return err
	}
	sess, err := resolveSession(ctx, c, args[0])
	if err != nil {
		return err
	}
	return attachSession(ctx, c, sess, fs.Lookup("readonly").Value.String() == "true", fs.Lookup("no-replay").Value.String() != "true")
}

// attachSession runs an interactive attach on the controlling terminal.
// It returns an *ExitError carrying the session's exit code when the
// session ends, or nil on detach.
func attachSession(ctx context.Context, c *ptyclient.Client, sess *api.TerminalSession, readOnly, replay bool) error {
	if sess.Activity == api.ActivityExited {
		return &ExitError{Code: 1, Msg: fmt.Sprintf("session %s has exited", sess.ID)}
	}
	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if !term.IsTerminal(in) || !term.IsTerminal(out) {
		return &ExitError{Code: 1, Msg: "relay attach needs an interactive terminal"}
	}
	cols, rows, err := term.GetSize(out)
	if err != nil {
		cols, rows = 80, 24
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn, err := c.Attach(ctx, sess.ID, ptyclient.AttachOptions{Cols: cols, Rows: rows, Replay: replay, ReadOnly: readOnly})
	if err != nil {
		return friendly(err)
	}
	defer conn.CloseNow()

	state, err := term.MakeRaw(in)
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	restore := sync.OnceFunc(func() { _ = term.Restore(in, state) })
	defer restore()

	a := &attacher{conn: conn, out: os.Stdout, readOnly: readOnly}
	result := make(chan attachResult, 3)
	go func() { result <- a.readLoop(ctx) }()
	if !readOnly {
		go func() { result <- a.inputLoop(ctx, os.Stdin) }()
		go a.resizeLoop(ctx, out)
	} else {
		go func() { result <- a.watchDetach(ctx, os.Stdin) }()
	}

	var res attachResult
	select {
	case res = <-result:
	case <-ctx.Done():
		res = attachResult{err: ctx.Err()}
	}
	cancel()
	_ = conn.Close(websocket.StatusNormalClosure, "")
	restore()
	// Leave the local terminal usable whatever the app left behind:
	// main screen, visible cursor, no mouse or bracketed paste.
	fmt.Fprint(os.Stdout, "\x1b[?1049l\x1b[?25h\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?2004l\x1b[?1l\x1b[0m")
	switch {
	case res.detached:
		fmt.Fprintf(os.Stderr, "\r\n[detached from %s]\n", displayName(sess))
		return nil
	case res.exitCode != nil:
		fmt.Fprintf(os.Stderr, "\r\n[%s exited with code %d]\n", displayName(sess), *res.exitCode)
		if *res.exitCode != 0 {
			return &ExitError{Code: min(max(*res.exitCode, 1), 255)}
		}
		return nil
	case res.message != "":
		return &ExitError{Code: 1, Msg: res.message}
	case res.err != nil && !errors.Is(res.err, context.Canceled):
		return &ExitError{Code: 1, Msg: "connection to the terminal daemon lost"}
	}
	return nil
}

func displayName(s *api.TerminalSession) string {
	if s.Name != "" {
		return s.Name + " (" + s.ID + ")"
	}
	return s.ID
}

type attachResult struct {
	detached bool
	exitCode *int
	message  string
	err      error
}

// attacher runs one attach; writes to conn are serialised by coder/websocket.
type attacher struct {
	conn     *websocket.Conn
	out      io.Writer
	readOnly bool
}

// readLoop copies output to the terminal and acknowledges it.
func (a *attacher) readLoop(ctx context.Context) attachResult {
	var total, acked int64
	for {
		typ, data, err := a.conn.Read(ctx)
		if err != nil {
			var ce websocket.CloseError
			if errors.As(err, &ce) && ce.Code == websocket.StatusTryAgainLater {
				return attachResult{message: "disconnected: " + ce.Reason}
			}
			return attachResult{err: err}
		}
		if typ == websocket.MessageBinary {
			if _, err := a.out.Write(data); err != nil {
				return attachResult{err: err}
			}
			total += int64(len(data))
			if total-acked >= ackEvery {
				acked = total
				a.send(ctx, api.TermClientMsg{T: "ack", Bytes: total})
			}
			continue
		}
		var m api.TermServerMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.T {
		case "exit":
			code := 0
			if m.Code != nil {
				code = *m.Code
			}
			return attachResult{exitCode: &code}
		case "error":
			return attachResult{message: m.Message}
		}
	}
}

func (a *attacher) send(ctx context.Context, m api.TermClientMsg) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = a.conn.Write(wctx, websocket.MessageText, b)
}

// detacher recognises Ctrl+\ d in the input stream. A doubled Ctrl+\
// sends one literal Ctrl+\.
type detacher struct{ pending bool }

// feed returns the bytes to forward and whether the user detached.
func (d *detacher) feed(p []byte) ([]byte, bool) {
	out := make([]byte, 0, len(p)+1)
	for _, b := range p {
		if d.pending {
			d.pending = false
			switch b {
			case 'd', 'D', 0x04:
				return out, true
			case detachPrefix:
				out = append(out, detachPrefix)
			default:
				out = append(out, detachPrefix, b)
			}
			continue
		}
		if b == detachPrefix {
			d.pending = true
			continue
		}
		out = append(out, b)
	}
	return out, false
}

// inputLoop forwards keystrokes until detach or error. The blocking read
// on stdin ends with the process; it holds no other resources.
func (a *attacher) inputLoop(ctx context.Context, r io.Reader) attachResult {
	var d detacher
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			data, detach := d.feed(buf[:n])
			if len(data) > 0 {
				wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				werr := a.conn.Write(wctx, websocket.MessageBinary, data)
				cancel()
				if werr != nil {
					return attachResult{err: werr}
				}
			}
			if detach {
				return attachResult{detached: true}
			}
		}
		if err != nil {
			return attachResult{err: err}
		}
	}
}

// watchDetach only looks for the detach keys (read-only attach).
func (a *attacher) watchDetach(_ context.Context, r io.Reader) attachResult {
	var d detacher
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, detach := d.feed(buf[:n]); detach {
				return attachResult{detached: true}
			}
		}
		if err != nil {
			return attachResult{err: err}
		}
	}
}

// resizeLoop sends the terminal size on SIGWINCH.
func (a *attacher) resizeLoop(ctx context.Context, fd int) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	defer signal.Stop(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			cols, rows, err := term.GetSize(fd)
			if err == nil && cols > 0 && rows > 0 {
				a.send(ctx, api.TermClientMsg{T: "resize", Cols: cols, Rows: rows})
			}
		}
	}
}
