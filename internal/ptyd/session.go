package ptyd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

// Activity timing.
const (
	workingWindow    = 1500 * time.Millisecond // output this recent = working
	echoWindow       = 200 * time.Millisecond  // small output this soon after input = echo
	echoMaxBytes     = 256
	bellInterval     = 2 * time.Second // rate limit for bell events
	notifyInterval   = time.Second     // rate limit for OSC notifications
	exitedReplayMax  = 256 << 10       // replay bytes kept after exit
	screenScrollback = 1000            // lines kept by the snapshot model
	replayFrame      = 64 << 10        // max bytes per replay frame
)

// Kill escalation timing (from the first signal).
var (
	killTermAfter = 2 * time.Second
	killKillAfter = 5 * time.Second
)

// Session is one terminal: a process on a pty plus everything derived
// from its output. All mutable state is guarded by mu.
type Session struct {
	d *Daemon

	mu      sync.Mutex
	info    api.TerminalSession
	spec    ptyclient.CreateSpec
	renamed bool // user-chosen name: do not follow OSC titles
	lost    bool // restored from disk, the process is gone

	ptmx    *os.File
	cmd     *exec.Cmd
	writeMu sync.Mutex // serialises pty writes (pastes stay contiguous)

	term *term
	ring *ring
	rec  *recorder

	clients   map[*client]struct{}
	sizeOwner *client

	activityOutput time.Time // last output that counts as activity
	promptChecked  bool
	lastBell       time.Time
	lastNotify     time.Time
	previewSent    string
	echoPending    []byte // recent input the pty may echo back
	dirtyPreview   bool

	done     chan struct{}           // closed when the process exited
	killMu   sync.Mutex              // serialises signal delivery and close retries
	closing  bool                    // guarded by killMu; one escalation per session
	killSent map[syscall.Signal]bool // guarded by killMu; terminating-signal retries
}

// startSession spawns spec. The caller registers the session.
func (d *Daemon) startSession(spec ptyclient.CreateSpec) (*Session, error) {
	terminal, err := d.terminalConfig(context.Background())
	if err != nil {
		return nil, err
	}
	if spec.Record == nil {
		record := d.shouldRecord(spec, terminal.Record)
		// Kind defaults to shell; only explicit agent sessions use agents mode.
		spec.Record = &record
	}
	env, argv, path, cwd, err := d.prepare(&spec, terminal)
	if err != nil {
		return nil, err
	}
	cols, rows := clampSize(spec.Cols, spec.Rows)
	id := newID()
	for d.has(id) {
		id = newID()
	}
	env["RELAY_SESSION"] = id

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Path = path // resolved against the session PATH
	cmd.Env = envList(env)
	cmd.Dir = cwd
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, badRequest(fmt.Sprintf("start %s: %v", argv[0], err))
	}
	now := time.Now().UTC()
	s := &Session{
		d:       d,
		spec:    spec,
		renamed: spec.Name != "",
		ptmx:    ptmx,
		cmd:     cmd,
		term:    newTerm(cols, rows, screenScrollback),
		ring:    newRing(d.scrollbackBytes(spec.Scrollback)),
		clients: map[*client]struct{}{},
		done:    make(chan struct{}),
	}
	s.info = api.TerminalSession{
		ID:             id,
		Name:           sessionName(spec),
		Kind:           spec.Kind,
		Agent:          spec.Agent,
		AgentSessionID: spec.AgentSessionID,
		Command:        argv,
		Cwd:            cwd,
		Workspace:      spec.Workspace,
		Pid:            cmd.Process.Pid,
		Cols:           cols,
		Rows:           rows,
		Activity:       api.ActivityIdle,
		Meta:           copyMeta(spec.Meta),
		CreatedAt:      now,
	}
	if d.shouldRecord(spec, terminal.Record) {
		rec, err := newRecorder(d.recordDir, id, cols, rows, env["SHELL"], now)
		if err != nil {
			d.log.Warn("recording disabled", "session", id, "err", err)
		} else {
			s.rec = rec
			s.info.Recording = true
		}
	}
	return s, nil
}

func sessionName(spec ptyclient.CreateSpec) string {
	if spec.Name != "" {
		return truncate(spec.Name, 128)
	}
	if spec.Agent != "" {
		return spec.Agent
	}
	if len(spec.Command) > 0 {
		base := filepath.Base(spec.Command[0])
		if base == "tmux" && spec.Meta["tmux"] != "" {
			return spec.Meta["tmux"]
		}
		return base
	}
	return "shell"
}

// run reads output until the pty closes, then records the exit. It is the
// session's only long-lived goroutine besides the waiter.
func (s *Session) run() {
	waited := make(chan struct{})
	var state *os.ProcessState
	go func() {
		_ = s.cmd.Wait()
		state = s.cmd.ProcessState
		close(waited)
	}()
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 32<<10)
		for {
			n, err := s.ptmx.Read(buf)
			if n > 0 {
				s.output(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-readDone:
		<-waited
	case <-waited:
		// Background jobs may keep the pty open; give the reader a moment
		// to drain the last output, then close the master (which hangs
		// them up, like closing a terminal window).
		select {
		case <-readDone:
		case <-time.After(500 * time.Millisecond):
		}
	}
	_ = s.ptmx.Close()
	<-readDone
	s.exited(exitCode(state))
}

func exitCode(st *os.ProcessState) int {
	if st == nil {
		return -1
	}
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return st.ExitCode()
}

// output handles one chunk read from the pty.
func (s *Session) output(b []byte) {
	now := time.Now()
	data := bytes.Clone(b) // shared, read-only, by every client queue
	var out []ptyclient.PtyEvent

	s.mu.Lock()
	base := s.ring.total
	s.ring.Write(data)
	evs := s.feed(data, base)
	if s.rec != nil {
		s.rec.Output(now, data)
	}
	s.info.LastOutputAt = now.UTC()
	echo := s.isEchoLocked(now, data)
	if !echo {
		s.activityOutput = now
		s.promptChecked = false
		if s.info.Activity != api.ActivityWorking && s.info.Attention == nil {
			s.info.Activity = api.ActivityWorking
			out = append(out, s.updatedLocked())
		}
	}
	s.dirtyPreview = true
	for c := range s.clients {
		if !c.push(frame{data: data, counted: true}) {
			s.dropLocked(c)
		}
	}
	for _, ev := range evs {
		out = append(out, s.handleLocked(ev, now)...)
	}
	s.mu.Unlock()
	s.d.emit(out...)
}

// expectEchoLocked remembers input so that its echo is not mistaken for
// the application working.
func (s *Session) expectEchoLocked(data []byte) {
	if len(data) > echoMaxBytes {
		s.echoPending = nil
		return
	}
	if len(s.echoPending)+len(data) > echoMaxBytes {
		s.echoPending = s.echoPending[:0]
	}
	for _, c := range data {
		if c != '\r' {
			s.echoPending = append(s.echoPending, c)
		}
	}
}

// isEchoLocked reports whether output is the terminal echoing recent input
// (CRs ignored: the tty turns LF into CRLF).
func (s *Session) isEchoLocked(now time.Time, data []byte) bool {
	if len(s.echoPending) == 0 || now.Sub(s.info.LastInputAt) > echoWindow {
		s.echoPending = s.echoPending[:0]
		return false
	}
	i := 0
	for _, c := range data {
		if c == '\r' {
			continue
		}
		if i >= len(s.echoPending) || s.echoPending[i] != c {
			s.echoPending = s.echoPending[:0]
			return false
		}
		i++
	}
	s.echoPending = append(s.echoPending[:0], s.echoPending[i:]...)
	return true
}

// feed parses data (which starts at absolute offset base), recording safe
// replay marks after newlines seen in the ground state.
func (s *Session) feed(data []byte, base int64) []termEvent {
	var evs []termEvent
	pos := 0
	for pos < len(data) {
		i := bytes.IndexByte(data[pos:], '\n')
		end := len(data)
		if i >= 0 {
			end = pos + i + 1
		}
		evs = append(evs, s.term.Feed(data[pos:end])...)
		if i >= 0 && s.term.parser.Ground() {
			s.ring.Mark(base + int64(end))
		}
		pos = end
	}
	return evs
}

// handleLocked applies one parsed output event.
func (s *Session) handleLocked(ev termEvent, now time.Time) []ptyclient.PtyEvent {
	var out []ptyclient.PtyEvent
	switch ev.kind {
	case "title":
		if ev.title == s.info.Title {
			return nil
		}
		s.info.Title = ev.title
		if !s.renamed && ev.title != "" {
			s.info.Name = ev.title
		}
		s.broadcastLocked(api.TermServerMsg{T: "title", Title: ev.title})
		out = append(out, s.updatedLocked())
	case "cwd":
		if ev.body == s.info.CurrentCwd {
			return nil
		}
		s.info.CurrentCwd = ev.body
		s.broadcastLocked(api.TermServerMsg{T: "cwd", Cwd: ev.body})
		out = append(out, s.updatedLocked())
	case "bell":
		if now.Sub(s.lastBell) < bellInterval {
			return nil
		}
		s.lastBell = now
		s.broadcastLocked(api.TermServerMsg{T: "bell"})
		out = append(out, ptyclient.PtyEvent{Type: "bell", ID: s.info.ID, Session: s.infoLocked()})
		if s.info.Kind == api.KindAgent {
			out = append(out, s.setAttentionLocked(&api.Attention{Reason: "bell", At: now.UTC()})...)
		}
	case "notify":
		if now.Sub(s.lastNotify) < notifyInterval {
			return nil
		}
		s.lastNotify = now
		s.broadcastLocked(api.TermServerMsg{T: "notify", Title: ev.title, Message: ev.body})
		msg := ev.body
		if msg == "" {
			msg = ev.title
		}
		out = append(out, ptyclient.PtyEvent{Type: "notify", ID: s.info.ID, Title: ev.title, Body: ev.body, Session: s.infoLocked()})
		out = append(out, s.setAttentionLocked(&api.Attention{Reason: "osc", Message: msg, At: now.UTC()})...)
	case "clip":
		out = append(out, ptyclient.PtyEvent{Type: "clip", ID: s.info.ID, Text: ev.body})
	case "prompt":
		// OSC 133 A (prompt start) / D (command finished): the shell is
		// back at its prompt, so it is idle whatever printed last.
		if (ev.mark == 'A' || ev.mark == 'D') && s.info.Attention == nil && s.info.Activity == api.ActivityWorking {
			s.info.Activity = api.ActivityIdle
			s.activityOutput = time.Time{}
			out = append(out, s.updatedLocked())
		}
	}
	return out
}

// setAttentionLocked marks or clears "needs you".
func (s *Session) setAttentionLocked(a *api.Attention) []ptyclient.PtyEvent {
	if s.info.Activity == api.ActivityExited {
		return nil
	}
	if a == nil {
		if s.info.Attention == nil {
			return nil
		}
		s.info.Attention = nil
		s.info.Activity = s.quietActivity(time.Now())
	} else {
		if s.info.Attention != nil && s.info.Attention.Reason == a.Reason && s.info.Attention.Message == a.Message {
			return nil
		}
		cp := *a
		cp.Reason = truncate(cleanText([]byte(cp.Reason), 32), 32)
		cp.Message = cleanText([]byte(cp.Message), maxNotify)
		if cp.At.IsZero() {
			cp.At = time.Now().UTC()
		}
		s.info.Attention = &cp
		s.info.Activity = api.ActivityWaiting
	}
	info := s.infoLocked()
	s.broadcastLocked(api.TermServerMsg{T: "attention", Session: info})
	return []ptyclient.PtyEvent{{Type: "updated", ID: info.ID, Session: info}}
}

// quietActivity is the activity when nothing asks for the user.
func (s *Session) quietActivity(now time.Time) api.Activity {
	if !s.activityOutput.IsZero() && now.Sub(s.activityOutput) < s.d.idleAfter {
		return api.ActivityWorking
	}
	return api.ActivityIdle
}

// tick advances the activity state machine; called periodically.
func (s *Session) tick(now time.Time) []ptyclient.PtyEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.Activity == api.ActivityExited {
		return nil
	}
	var out []ptyclient.PtyEvent
	quiet := now.Sub(s.activityOutput)
	if s.info.Kind == api.KindAgent && s.info.Attention == nil && !s.promptChecked &&
		!s.activityOutput.IsZero() && quiet >= workingWindow {
		s.promptChecked = true
		if line := matchPrompt(s.info.Agent, s.term.scr.Preview(4)); line != "" {
			out = append(out, s.setAttentionLocked(&api.Attention{Reason: "prompt", Message: line, At: now.UTC()})...)
		}
	}
	if s.info.Attention == nil {
		if act := s.quietActivity(now); act != s.info.Activity {
			s.info.Activity = act
			out = append(out, s.updatedLocked())
		}
	}
	if s.dirtyPreview && len(out) == 0 {
		s.dirtyPreview = false
		if p := s.term.scr.Preview(3); p != s.previewSent {
			out = append(out, s.updatedLocked())
		}
	}
	return out
}

// promptPatterns recognise agents waiting for a decision. Keys are agent
// adapter ids; "*" applies to every agent.
var promptPatterns = map[string][]*regexp.Regexp{
	"*": {
		regexp.MustCompile(`(?i)\((y/n|yes/no)\)\s*[:?]?\s*$`),
		regexp.MustCompile(`(?i)\[(y/n|y/N|Y/n)\]\s*[:?]?\s*$`),
		regexp.MustCompile(`(?i)\bdo you want to (proceed|continue|allow|make this edit|run|create)\b`),
		regexp.MustCompile(`(?i)\b(allow|approve) (this|command|execution|once|always)\b`),
		regexp.MustCompile(`(?i)\bpress enter to (continue|confirm)\b`),
		regexp.MustCompile(`(?i)\bwaiting for (your )?(user )?(confirmation|approval|input)\b`),
	},
	"claude": {regexp.MustCompile(`^\s*❯\s*1\.\s*Yes\b`)},
	"codex":  {regexp.MustCompile(`(?i)\b(approve|run) this command\?`)},
	"gemini": {regexp.MustCompile(`(?i)\ballow execution\b`)},
}

// matchPrompt returns the matching screen line when the tail of the screen
// looks like an agent waiting for a decision.
func matchPrompt(agent, tail string) string {
	lines := strings.Split(tail, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		for _, key := range []string{agent, "*"} {
			for _, re := range promptPatterns[key] {
				if re.MatchString(line) {
					return truncate(line, 200)
				}
			}
		}
	}
	return ""
}

// infoLocked returns a copy of the session metadata with a fresh preview.
func (s *Session) infoLocked() *api.TerminalSession {
	cp := s.info
	cp.Command = append([]string(nil), s.info.Command...)
	cp.Meta = copyMeta(s.info.Meta)
	if s.info.Attention != nil {
		a := *s.info.Attention
		cp.Attention = &a
	}
	if s.info.ExitCode != nil {
		c := *s.info.ExitCode
		cp.ExitCode = &c
	}
	cp.Clients = len(s.clients)
	if s.term != nil {
		cp.Preview = s.term.scr.Preview(3)
	}
	return &cp
}

func (s *Session) updatedLocked() ptyclient.PtyEvent {
	info := s.infoLocked()
	s.previewSent = info.Preview
	return ptyclient.PtyEvent{Type: "updated", ID: info.ID, Session: info}
}

// Info returns a snapshot of the session metadata.
func (s *Session) Info() *api.TerminalSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.infoLocked()
}

func (s *Session) broadcastLocked(m api.TermServerMsg) {
	for c := range s.clients {
		c.pushMsg(m)
	}
}

// ---------------------------------------------------------------------------
// input, size, attach

var errReadOnly = errors.New("read-only client")

// Input writes data to the pty on behalf of c (nil = HTTP API).
func (s *Session) Input(c *client, data []byte) error {
	if c != nil && c.readOnly {
		return errReadOnly
	}
	if len(data) == 0 {
		return nil
	}
	now := time.Now()
	s.mu.Lock()
	if s.info.Activity == api.ActivityExited {
		s.mu.Unlock()
		return conflict("session has exited")
	}
	s.info.LastInputAt = now.UTC()
	s.expectEchoLocked(data)
	var out []ptyclient.PtyEvent
	out = append(out, s.setAttentionLocked(nil)...)
	if c != nil {
		c.lastActive = now
		if s.sizeOwner != c {
			s.sizeOwner = c
			out = append(out, s.applySizeLocked(c.cols, c.rows)...)
		}
	}
	ptmx := s.ptmx
	s.mu.Unlock()
	s.d.emit(out...)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = ptmx.SetWriteDeadline(time.Now().Add(10 * time.Second))
	for len(data) > 0 {
		n, err := ptmx.Write(data)
		if err != nil {
			return fmt.Errorf("write to pty: %w", err)
		}
		data = data[n:]
	}
	return nil
}

// Paste writes text wrapped in bracketed-paste markers when the app
// enabled them. Embedded end markers are removed so pasted text cannot
// break out of the paste.
func (s *Session) Paste(c *client, data []byte) error {
	s.mu.Lock()
	bracketed := s.term.Mode(2004)
	s.mu.Unlock()
	data = bytes.ReplaceAll(data, []byte("\x1b[201~"), nil)
	if bracketed {
		data = append(append([]byte("\x1b[200~"), data...), "\x1b[201~"...)
	}
	return s.Input(c, data)
}

// Resize sets the pty size on behalf of c (nil = HTTP API).
func (s *Session) Resize(c *client, cols, rows int) error {
	if c != nil && c.readOnly {
		return errReadOnly
	}
	s.mu.Lock()
	if c != nil {
		c.cols, c.rows = cols, rows
		c.lastActive = time.Now()
	}
	s.sizeOwner = c
	out := s.applySizeLocked(cols, rows)
	s.mu.Unlock()
	s.d.emit(out...)
	return nil
}

// applySizeLocked resizes the pty and every model if the size changed.
func (s *Session) applySizeLocked(cols, rows int) []ptyclient.PtyEvent {
	if cols <= 0 || rows <= 0 || s.info.Activity == api.ActivityExited {
		return nil
	}
	cols, rows = clampSize(cols, rows)
	if cols == s.info.Cols && rows == s.info.Rows {
		return nil
	}
	if err := pty.Setsize(s.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		return nil
	}
	s.info.Cols, s.info.Rows = cols, rows
	s.term.scr.Resize(cols, rows)
	if s.rec != nil {
		s.rec.Resize(time.Now(), cols, rows)
	}
	s.broadcastLocked(api.TermServerMsg{T: "resize", Cols: cols, Rows: rows})
	return []ptyclient.PtyEvent{s.updatedLocked()}
}

// nudge makes a full-screen application redraw by shrinking the pty one
// row and restoring it, which delivers two SIGWINCHes.
func (s *Session) nudge() {
	s.mu.Lock()
	if s.info.Activity == api.ActivityExited || s.ptmx == nil {
		s.mu.Unlock()
		return
	}
	cols, rows := s.info.Cols, s.info.Rows
	ptmx := s.ptmx
	s.mu.Unlock()
	if rows < 2 {
		return
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows - 1)})
	time.Sleep(30 * time.Millisecond)
	s.mu.Lock()
	// Restore whatever the size is now (a client may have resized).
	_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(s.info.Cols), Rows: uint16(s.info.Rows)})
	s.mu.Unlock()
}

// attach registers c and queues hello (+ replay). It returns whether the
// session needs a redraw nudge and whether it has already exited.
func (s *Session) attach(c *client, replay bool) (nudge, exited bool) {
	now := time.Now()
	s.mu.Lock()
	exited = s.info.Activity == api.ActivityExited
	var out []ptyclient.PtyEvent
	if !exited && !c.readOnly && c.cols > 0 && c.rows > 0 {
		c.lastActive = now
		s.sizeOwner = c
		out = append(out, s.applySizeLocked(c.cols, c.rows)...)
		nudge = len(out) == 0 && s.term.AltScreen()
	}
	if !exited {
		s.clients[c] = struct{}{}
	}
	info := s.infoLocked()
	c.pushMsg(api.TermServerMsg{T: "hello", Session: info, Cols: info.Cols, Rows: info.Rows, ReadOnly: c.readOnly})
	if replay && s.ring != nil {
		c.pushMsg(api.TermServerMsg{T: "replay-begin"})
		var b bytes.Buffer
		b.WriteString("\x1bc")
		b.Write(s.ring.Replay())
		if s.term != nil {
			b.Write(s.term.Reassert())
		}
		// Frames of at most replayFrame bytes: progressive on slow links
		// and within every reader's frame limit.
		for data := b.Bytes(); len(data) > 0; {
			n := min(len(data), replayFrame)
			c.push(frame{data: data[:n]})
			data = data[n:]
		}
		c.pushMsg(api.TermServerMsg{T: "replay-end"})
	} else if s.term == nil {
		// restored from disk: nothing to replay
	} else if m := s.term.Reassert(); len(m) > 0 {
		c.push(frame{data: m})
	}
	if exited {
		c.pushMsg(api.TermServerMsg{T: "exit", Code: s.info.ExitCode})
		c.finish()
	} else {
		s.broadcastLocked(api.TermServerMsg{T: "clients", Clients: len(s.clients)})
		out = append(out, s.updatedLocked())
	}
	s.mu.Unlock()
	s.d.emit(out...)
	return nudge, exited
}

// detach unregisters c. If c owned the size, the most recently active
// remaining writer takes over.
func (s *Session) detach(c *client) {
	s.mu.Lock()
	if _, ok := s.clients[c]; !ok {
		s.mu.Unlock()
		return
	}
	out := s.removeClientLocked(c)
	s.mu.Unlock()
	s.d.emit(out...)
}

// dropLocked removes a lagging client (its writer reports and closes).
func (s *Session) dropLocked(c *client) {
	delete(s.clients, c)
	if s.sizeOwner == c {
		s.sizeOwner = nil
	}
}

func (s *Session) removeClientLocked(c *client) []ptyclient.PtyEvent {
	delete(s.clients, c)
	var out []ptyclient.PtyEvent
	if s.sizeOwner == c {
		s.sizeOwner = nil
		var next *client
		for o := range s.clients {
			if !o.readOnly && o.cols > 0 && (next == nil || o.lastActive.After(next.lastActive)) {
				next = o
			}
		}
		if next != nil {
			s.sizeOwner = next
			out = append(out, s.applySizeLocked(next.cols, next.rows)...)
		}
	}
	if s.info.Activity != api.ActivityExited {
		s.broadcastLocked(api.TermServerMsg{T: "clients", Clients: len(s.clients)})
		out = append(out, s.updatedLocked())
	}
	return out
}

// Snapshot returns the last lines of the screen model as plain text.
func (s *Session) Snapshot(lines int) *api.TerminalSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := &api.TerminalSnapshot{Cols: s.info.Cols, Rows: s.info.Rows}
	if s.term != nil {
		snap.Text = s.term.scr.Text(lines)
	} else {
		snap.Text = s.info.Preview
	}
	return snap
}

// SetAttention marks or clears attention from outside (hooks, ack).
func (s *Session) SetAttention(a *api.Attention) {
	s.mu.Lock()
	out := s.setAttentionLocked(a)
	s.mu.Unlock()
	s.d.emit(out...)
}

// ---------------------------------------------------------------------------
// exit and kill

// exited records the exit and closes every client.
func (s *Session) exited(code int) {
	s.mu.Lock()
	now := time.Now().UTC()
	s.info.ExitCode = &code
	s.info.ExitedAt = now
	s.info.Activity = api.ActivityExited
	s.info.Attention = nil
	s.info.Preview = s.term.scr.Preview(3)
	if s.rec != nil {
		if err := s.rec.Close(); err != nil {
			s.d.log.Warn("close recording", "session", s.info.ID, "err", err)
		}
		s.rec = nil
	}
	// Keep a bounded tail for replay/snapshot of the finished session.
	s.ring = s.ring.Tail(exitedReplayMax)
	for c := range s.clients {
		c.pushMsg(api.TermServerMsg{T: "exit", Code: &code})
		c.finish()
	}
	clear(s.clients)
	s.sizeOwner = nil
	info := s.infoLocked()
	s.mu.Unlock()
	close(s.done)
	s.d.log.Info("session exited", "session", info.ID, "code", code)
	s.d.markDirty()
	s.d.emit(ptyclient.PtyEvent{Type: "exited", ID: info.ID, Session: info})
}

// Done is closed when the process has exited.
func (s *Session) Done() <-chan struct{} { return s.done }

var signals = map[string]syscall.Signal{
	"HUP": syscall.SIGHUP, "TERM": syscall.SIGTERM, "KILL": syscall.SIGKILL,
	"INT": syscall.SIGINT, "QUIT": syscall.SIGQUIT, "USR1": syscall.SIGUSR1, "USR2": syscall.SIGUSR2,
}

// parseSignal accepts "TERM", "SIGTERM", "term" or "" (escalating close).
func parseSignal(name string) (syscall.Signal, bool) {
	name = strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(name)), "SIG")
	if name == "" {
		return 0, true
	}
	sig, ok := signals[name]
	return sig, ok
}

// Kill signals the process group. sig 0 means "close the terminal":
// SIGHUP, then SIGTERM after 2 s, then SIGKILL after 5 s. SIGHUP and
// SIGTERM also escalate to SIGKILL at 5 s; other signals are sent once.
func (s *Session) Kill(sig syscall.Signal) (bool, error) {
	s.killMu.Lock()
	defer s.killMu.Unlock()
	select {
	case <-s.done:
		return false, nil
	default:
	}
	first := sig
	if first == 0 {
		first = syscall.SIGHUP
	}
	terminating := first == syscall.SIGHUP || first == syscall.SIGTERM || first == syscall.SIGKILL || first == syscall.SIGQUIT
	if terminating && s.killSent[first] {
		return false, nil
	}
	changed, err := s.signal(first)
	if err != nil || !changed {
		return changed, err
	}
	if terminating {
		if s.killSent == nil {
			s.killSent = make(map[syscall.Signal]bool)
		}
		s.killSent[first] = true
	}
	if (first == syscall.SIGHUP || first == syscall.SIGTERM) && !s.closing {
		s.closing = true
		go s.escalate(sig == 0)
	}
	return true, nil
}

func (s *Session) escalate(withTerm bool) {
	start := time.Now()
	if withTerm {
		select {
		case <-s.done:
			return
		case <-time.After(killTermAfter):
			s.signal(syscall.SIGTERM)
		}
	}
	select {
	case <-s.done:
	case <-time.After(killKillAfter - time.Since(start)):
		s.signal(syscall.SIGKILL)
	}
}

// signal delivers sig to the session's process group and to the pty's
// foreground process group (a job the shell started).
func (s *Session) signal(sig syscall.Signal) (bool, error) {
	s.mu.Lock()
	pid := s.info.Pid
	ptmx := s.ptmx
	exited := s.info.Activity == api.ActivityExited
	s.mu.Unlock()
	if exited || pid <= 0 {
		return false, nil
	}
	err := syscall.Kill(-pid, sig)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return false, fmt.Errorf("signal process group: %w", err)
	}
	changed := err == nil
	if fg := foregroundPgrp(ptmx); fg > 0 && fg != pid {
		// The main process group accepted the signal even if its foreground
		// job has already exited. Do not misreport a committed delivery.
		if err := syscall.Kill(-fg, sig); err == nil {
			changed = true
		}
	}
	return changed, nil
}

func foregroundPgrp(f *os.File) int {
	if f == nil {
		return 0
	}
	rc, err := f.SyscallConn()
	if err != nil {
		return 0
	}
	pgrp := 0
	_ = rc.Control(func(fd uintptr) {
		if v, err := unix.IoctlGetInt(int(fd), unix.TIOCGPGRP); err == nil {
			pgrp = v
		}
	})
	return pgrp
}

// waitDone waits for exit up to d.
func (s *Session) waitDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-s.done:
		return true
	case <-t.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func copyMeta(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
