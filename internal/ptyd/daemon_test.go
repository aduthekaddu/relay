package ptyd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

func TestEchoRoundTripAndExitCode(t *testing.T) {
	e := startEnv(t, testPaths(t), nil)
	ev := e.events()
	s := e.create(shSpec(`read x; echo "got:$x"; exit 3`))
	if !validID(s.ID) || s.Kind != api.KindShell || s.Name != "sh" || s.Pid == 0 {
		t.Fatalf("session = %+v", s)
	}
	ev.wait(t, "created", s.ID)
	a := e.attach(s.ID, ptyclient.AttachOptions{Cols: 80, Rows: 24, Replay: true})
	hello := a.waitMsg("hello")
	if hello.Session == nil || hello.Session.ID != s.ID || hello.Cols != 80 {
		t.Fatalf("hello = %+v", hello)
	}
	a.send("ping\r")
	a.waitOutput("got:ping")
	exit := a.waitMsg("exit")
	if exit.Code == nil || *exit.Code != 3 {
		t.Fatalf("exit = %+v", exit)
	}
	done := e.waitExit(s.ID, 5*time.Second)
	if done.ExitCode == nil || *done.ExitCode != 3 || done.ExitedAt.IsZero() {
		t.Fatalf("exited session = %+v", done)
	}
	ev.wait(t, "exited", s.ID)
	// The finished session can still be snapshotted.
	snap, err := e.c.Snapshot(context.Background(), s.ID, 10)
	if err != nil || !strings.Contains(snap.Text, "got:ping") {
		t.Fatalf("snapshot = %+v, %v", snap, err)
	}
}

func TestEnvironment(t *testing.T) {
	p := testPaths(t)
	e := startEnv(t, p, nil)
	spec := shSpec(`echo "T=$TERM C=$COLORTERM P=$TERM_PROGRAM S=$RELAY_SESSION K=$RELAY_SOCKET X=$EXTRA TM=${TMUX:-none}"; pwd`)
	spec.Env = map[string]string{"EXTRA": "yes", "bad key": "no"}
	spec.Cols = 400
	sub := filepath.Join(p.Home, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	spec.Cwd = sub
	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	e.d.baseEnvOnce = sync.Once{} // pick up TMUX set above
	s := e.create(spec)
	e.waitExit(s.ID, 5*time.Second)
	snap, _ := e.c.Snapshot(context.Background(), s.ID, 5)
	want := fmt.Sprintf("T=xterm-256color C=truecolor P=Relay S=%s K=%s X=yes TM=none", s.ID, p.CtlSocket)
	if !strings.Contains(snap.Text, want) || !strings.Contains(snap.Text, sub) {
		t.Fatalf("snapshot = %q, want %q and %q", snap.Text, want, sub)
	}
}

func TestCreateValidation(t *testing.T) {
	e := startEnv(t, testPaths(t), nil)
	tests := []struct {
		name string
		spec ptyclient.CreateSpec
	}{
		{"missing command", ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Command: []string{"definitely-not-a-command-xyz"}}}},
		{"relative cwd", ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Cwd: "relative/dir"}}},
		{"missing cwd", ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Cwd: "/nonexistent/dir/xyz"}}},
		{"bad kind", ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Kind: "evil"}}},
		{"nul in arg", ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Command: []string{"/bin/sh", "-c", "a\x00b"}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.c.Create(context.Background(), tc.spec)
			var se *ptyclient.StatusError
			if !errors.As(err, &se) || se.Status != 400 {
				t.Fatalf("err = %v, want 400", err)
			}
		})
	}
	if _, err := e.c.Get(context.Background(), "t_aaaaaaaaaa"); !errors.Is(err, ptyclient.ErrNotFound) {
		t.Fatalf("get unknown = %v", err)
	}
}

func TestResizeReachesProcess(t *testing.T) {
	e := startEnv(t, testPaths(t), nil)
	s := e.create(shSpec(`read x; stty size`))
	if err := e.c.Resize(context.Background(), s.ID, 100, 30); err != nil {
		t.Fatal(err)
	}
	a := e.attach(s.ID, ptyclient.AttachOptions{})
	a.waitMsg("hello")
	a.send("\r")
	a.waitOutput("30 100")
	got, _ := e.c.Get(context.Background(), s.ID)
	if got.Cols != 100 || got.Rows != 30 {
		t.Fatalf("size = %dx%d", got.Cols, got.Rows)
	}
}

func TestKillEscalation(t *testing.T) {
	oldTerm, oldKill := killTermAfter, killKillAfter
	killTermAfter, killKillAfter = 200*time.Millisecond, 600*time.Millisecond
	t.Cleanup(func() { killTermAfter, killKillAfter = oldTerm, oldKill })

	e := startEnv(t, testPaths(t), nil)
	tests := []struct {
		name   string
		script string
		signal string
		code   int
	}{
		{"hup closes a shell", `while :; do sleep 0.05; done`, "", 128 + 1},
		{"term when hup ignored", `trap "" HUP; while :; do sleep 0.05; done`, "", 128 + 15},
		{"kill when everything ignored", `trap "" HUP TERM; while :; do sleep 0.05; done`, "", 128 + 9},
		{"explicit TERM escalates to KILL", `trap "" TERM; while :; do sleep 0.05; done`, "TERM", 128 + 9},
		{"explicit INT", `while :; do sleep 0.05; done`, "INT", 128 + 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := e.create(shSpec(tc.script))
			time.Sleep(150 * time.Millisecond) // let the traps install
			start := time.Now()
			if err := e.c.Kill(context.Background(), s.ID, tc.signal); err != nil {
				t.Fatal(err)
			}
			done := e.waitExit(s.ID, 5*time.Second)
			if done.ExitCode == nil || *done.ExitCode != tc.code {
				t.Fatalf("exit code = %v, want %d (after %s)", done.ExitCode, tc.code, time.Since(start))
			}
		})
	}
	if err := e.c.Kill(context.Background(), "t_aaaaaaaaaa", ""); !errors.Is(err, ptyclient.ErrNotFound) {
		t.Fatalf("kill unknown: %v", err)
	}
}

func TestReplayAfterTruncation(t *testing.T) {
	e := startEnv(t, testPaths(t), nil)
	spec := shSpec(`i=0; while [ $i -lt 6000 ]; do printf '\033[32mline %05d\033[0m\n' $i; i=$((i+1)); done; echo END; read x`)
	spec.Scrollback = 64 << 10
	s := e.create(spec)
	e.waitSession(s.ID, 20*time.Second, "END in snapshot", func(*api.TerminalSession) bool {
		snap, _ := e.c.Snapshot(context.Background(), s.ID, 3)
		return snap != nil && strings.Contains(snap.Text, "END")
	})
	a := e.attach(s.ID, ptyclient.AttachOptions{Replay: true})
	a.waitMsg("replay-end")
	a.mu.Lock()
	rep := string(a.replay)
	a.mu.Unlock()
	if !strings.HasPrefix(rep, "\x1bc\x1b[32mline ") {
		t.Fatalf("replay must start with a reset then a whole line: %q", rep[:min(len(rep), 40)])
	}
	if len(rep) > 64<<10+16 {
		t.Fatalf("replay is %d bytes, larger than the buffer", len(rep))
	}
	if !strings.Contains(rep, "line 05999\x1b[0m\r\nEND") {
		t.Fatalf("replay misses the tail: %q", rep[len(rep)-60:])
	}
	if strings.Contains(rep, "line 00000") {
		t.Fatal("replay contains dropped output")
	}
}

func TestAltScreenReassertAndNudge(t *testing.T) {
	e := startEnv(t, testPaths(t), nil)
	// 28 = SIGWINCH; the trap proves the redraw nudge reached the app.
	s := e.create(shSpec(`trap 'echo winch' 28; printf 'before\033[?1049h\033[?2004h\033[?1000hTUI'; while :; do sleep 0.05; done`))
	e.waitSession(s.ID, 5*time.Second, "TUI on screen", func(*api.TerminalSession) bool {
		snap, _ := e.c.Snapshot(context.Background(), s.ID, 5)
		return snap != nil && strings.Contains(snap.Text, "TUI")
	})
	// Same size as the session (80x24): no resize, so a nudge is needed.
	a := e.attach(s.ID, ptyclient.AttachOptions{Cols: 80, Rows: 24, Replay: true})
	a.waitMsg("replay-end")
	a.mu.Lock()
	rep := string(a.replay)
	a.mu.Unlock()
	want := "TUI\x1b[?1049h\x1b[?1000h\x1b[?2004h"
	if !strings.HasPrefix(rep, "\x1bc") || !strings.HasSuffix(rep, want) {
		t.Fatalf("replay = %q, want reset … %q", rep, want)
	}
	a.waitOutput("winch")
	snap, _ := e.c.Snapshot(context.Background(), s.ID, 5)
	if strings.Contains(snap.Text, "before") {
		t.Fatalf("alt-screen snapshot shows main screen: %q", snap.Text)
	}
	_ = e.c.Kill(context.Background(), s.ID, "KILL")
}

func TestOSCTitleCwdAndClipEvents(t *testing.T) {
	e := startEnv(t, testPaths(t), nil)
	ev := e.events()
	s := e.create(shSpec(`read go; printf '\033]0;my title\007\033]7;file://h/tmp\007\033]52;c;Y29waWVk\007'; read x`))
	a := e.attach(s.ID, ptyclient.AttachOptions{})
	a.waitMsg("hello")
	a.send("\r")
	if m := a.waitMsg("title"); m.Title != "my title" {
		t.Fatalf("title msg = %+v", m)
	}
	if m := a.waitMsg("cwd"); m.Cwd != "/tmp" {
		t.Fatalf("cwd msg = %+v", m)
	}
	clip := ev.wait(t, "clip", s.ID)
	if clip.Text != "copied" {
		t.Fatalf("clip = %+v", clip)
	}
	got, _ := e.c.Get(context.Background(), s.ID)
	if got.Name != "my title" || got.Title != "my title" || got.CurrentCwd != "/tmp" {
		t.Fatalf("session = %+v", got)
	}
	// A user rename sticks even when the title changes again.
	name := "renamed"
	if _, err := e.c.Update(context.Background(), s.ID, api.UpdateTerminalRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if err := e.c.Input(context.Background(), s.ID, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	e.waitExit(s.ID, 5*time.Second)
	got, _ = e.c.Get(context.Background(), s.ID)
	if got.Name != "renamed" {
		t.Fatalf("name = %q", got.Name)
	}
}

func TestAttentionTransitions(t *testing.T) {
	e := startEnv(t, testPaths(t), nil)
	ev := e.events()
	spec := shSpec(`while read cmd; do case "$cmd" in bell) printf '\007';; osc) printf '\033]9;Need input\007';; ask) printf 'Continue? (y/n) ';; esac; done`)
	spec.Kind = api.KindAgent
	spec.Agent = "codex"
	s := e.create(spec)
	if s.Name != "codex" {
		t.Fatalf("agent session name = %q", s.Name)
	}
	isWaiting := func(reason string) func(*api.TerminalSession) bool {
		return func(s *api.TerminalSession) bool {
			return s.Activity == api.ActivityWaiting && s.Attention != nil && s.Attention.Reason == reason
		}
	}
	notWaiting := func(s *api.TerminalSession) bool { return s.Attention == nil && s.Activity != api.ActivityWaiting }

	// BEL from an agent session → attention "bell"; input clears it.
	_ = e.c.Input(context.Background(), s.ID, []byte("bell\n"))
	e.waitSession(s.ID, 5*time.Second, "bell attention", isWaiting("bell"))
	ev.wait(t, "bell", s.ID)
	_ = e.c.Input(context.Background(), s.ID, []byte("x\n"))
	e.waitSession(s.ID, 5*time.Second, "cleared by input", notWaiting)

	// OSC 9 → attention "osc" with the message and a notify event.
	_ = e.c.Input(context.Background(), s.ID, []byte("osc\n"))
	got := e.waitSession(s.ID, 5*time.Second, "osc attention", isWaiting("osc"))
	if got.Attention.Message != "Need input" {
		t.Fatalf("attention = %+v", got.Attention)
	}
	if n := ev.wait(t, "notify", s.ID); n.Body != "Need input" {
		t.Fatalf("notify event = %+v", n)
	}
	// Explicit ack clears.
	if err := e.c.SetAttention(context.Background(), s.ID, nil); err != nil {
		t.Fatal(err)
	}
	e.waitSession(s.ID, 5*time.Second, "cleared by ack", notWaiting)

	// A hook sets attention explicitly.
	if err := e.c.SetAttention(context.Background(), s.ID, &api.Attention{Reason: "hook", Message: "Permission needed"}); err != nil {
		t.Fatal(err)
	}
	e.waitSession(s.ID, 5*time.Second, "hook attention", isWaiting("hook"))
	_ = e.c.Input(context.Background(), s.ID, []byte("x\n"))
	e.waitSession(s.ID, 5*time.Second, "cleared", notWaiting)

	// Prompt heuristic: output stops on a confirmation prompt.
	_ = e.c.Input(context.Background(), s.ID, []byte("ask\n"))
	got = e.waitSession(s.ID, 6*time.Second, "prompt attention", isWaiting("prompt"))
	if !strings.Contains(got.Attention.Message, "(y/n)") {
		t.Fatalf("prompt attention = %+v", got.Attention)
	}
	_ = e.c.Input(context.Background(), s.ID, []byte("y\n"))
	// Quiet for idle_seconds (1 s in tests) → idle.
	e.waitSession(s.ID, 6*time.Second, "idle", func(s *api.TerminalSession) bool { return s.Activity == api.ActivityIdle })
	_ = e.c.Kill(context.Background(), s.ID, "KILL")
}

func TestShellBellIsNotAttention(t *testing.T) {
	e := startEnv(t, testPaths(t), nil)
	ev := e.events()
	s := e.create(shSpec(`printf '\007'; read x`))
	ev.wait(t, "bell", s.ID)
	time.Sleep(100 * time.Millisecond)
	got, _ := e.c.Get(context.Background(), s.ID)
	if got.Attention != nil {
		t.Fatalf("shell bell set attention: %+v", got.Attention)
	}
	_ = e.c.Kill(context.Background(), s.ID, "KILL")
}

func TestTwoClientsAndReadOnly(t *testing.T) {
	e := startEnv(t, testPaths(t), nil)
	s := e.create(shSpec(`while read l; do echo "echo:$l"; done`))
	rw := e.attach(s.ID, ptyclient.AttachOptions{Cols: 90, Rows: 20})
	rw.waitMsg("hello")
	ro := e.attach(s.ID, ptyclient.AttachOptions{Cols: 40, Rows: 10, ReadOnly: true})
	if h := ro.waitMsg("hello"); !h.ReadOnly || h.Cols != 90 {
		t.Fatalf("read-only hello = %+v", h)
	}
	e.waitSession(s.ID, 5*time.Second, "2 clients", func(s *api.TerminalSession) bool { return s.Clients == 2 })

	ro.send("from-ro\r")
	ro.control(api.TermClientMsg{T: "resize", Cols: 30, Rows: 5})
	rw.send("from-rw\r")
	rw.waitOutput("echo:from-rw")
	ro.waitOutput("echo:from-rw")
	if strings.Contains(rw.output(), "from-ro") {
		t.Fatal("read-only input reached the pty")
	}
	got, _ := e.c.Get(context.Background(), s.ID)
	if got.Cols != 90 || got.Rows != 20 {
		t.Fatalf("read-only client resized the pty: %dx%d", got.Cols, got.Rows)
	}

	// Latest active writer wins the size.
	rw2 := e.attach(s.ID, ptyclient.AttachOptions{Cols: 120, Rows: 40})
	rw2.waitMsg("hello")
	e.waitSession(s.ID, 5*time.Second, "resize to 120x40", func(s *api.TerminalSession) bool { return s.Cols == 120 && s.Rows == 40 })
	rw.send("x\r") // typing makes rw the size owner again
	e.waitSession(s.ID, 5*time.Second, "resize back to 90x20", func(s *api.TerminalSession) bool { return s.Cols == 90 && s.Rows == 20 })
	rw.waitMsg("resize")

	rw.control(api.TermClientMsg{T: "ping"})
	rw.waitMsg("pong")
	rw2.conn.CloseNow()
	e.waitSession(s.ID, 5*time.Second, "2 clients after close", func(s *api.TerminalSession) bool { return s.Clients == 2 })
	_ = e.c.Kill(context.Background(), s.ID, "KILL")
	rw.waitMsg("exit")
}

func TestLaggingClientIsDropped(t *testing.T) {
	e := startEnv(t, testPaths(t), nil)
	s := e.create(shSpec(`read x; yes 0123456789012345678901234567890123456789 | head -c 8000000; read y`))
	a := e.attach(s.ID, ptyclient.AttachOptions{})
	a.waitMsg("hello")
	// The client acknowledges nothing beyond 1 byte: flow control stops
	// writes after 1 MiB and the queue overflows another 1 MiB later.
	a.control(api.TermClientMsg{T: "ack", Bytes: 1})
	time.Sleep(50 * time.Millisecond)
	a.send("\r")
	a.waitMsg("error")
	a.waitFor("close", 10*time.Second, func() bool { return a.closed })
	a.mu.Lock()
	n := a.out.Len()
	a.mu.Unlock()
	if n > 2*flowWindow+clientQueueMax {
		t.Fatalf("lagging client received %d bytes", n)
	}
	_ = e.c.Kill(context.Background(), s.ID, "KILL")
}

func TestRecordingEndpoint(t *testing.T) {
	e := startEnv(t, testPaths(t), func(c *config.Config) { c.Terminal.Record = "all" })
	s := e.create(shSpec(`printf 'recorded output\r\n'`))
	if !s.Recording {
		t.Fatal("session not recording")
	}
	e.waitExit(s.ID, 5*time.Second)
	rc, err := e.c.Recording(context.Background(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	sc := bufio.NewScanner(rc)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	var h castHeader
	if len(lines) < 2 || json.Unmarshal([]byte(lines[0]), &h) != nil || h.Version != 2 || h.Width != 80 {
		t.Fatalf("recording = %q", lines)
	}
	if !strings.Contains(strings.Join(lines[1:], ""), "recorded output") {
		t.Fatalf("recording misses output: %q", lines)
	}
	// Sessions without recording answer 404.
	e2 := e.create(ptyclient.CreateSpec{CreateTerminalRequest: api.CreateTerminalRequest{Command: []string{"/bin/sh", "-c", "true"}, Record: new(bool)}})
	e.waitExit(e2.ID, 5*time.Second)
	if _, err := e.c.Recording(context.Background(), e2.ID); !errors.Is(err, ptyclient.ErrNotFound) {
		t.Fatalf("recording of unrecorded session: %v", err)
	}
}

func TestRestartKeepsMetadataAndRestore(t *testing.T) {
	p := testPaths(t)
	e := startEnv(t, p, nil)
	live := e.create(shSpec(`while :; do sleep 1; done`))
	done := e.create(shSpec(`exit 7`))
	e.waitExit(done.ID, 5*time.Second)
	pinned := true
	name := "keep me"
	if _, err := e.c.Update(context.Background(), live.ID, api.UpdateTerminalRequest{Name: &name, Pinned: &pinned}); err != nil {
		t.Fatal(err)
	}
	if err := e.c.SetMeta(context.Background(), live.ID, map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	e.stop()
	st, err := os.Stat(filepath.Join(p.DataDir, "ptyd", "sessions.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("sessions.json: %v %v", err, st)
	}

	e2 := startEnv(t, p, nil)
	list, err := e2.c.List(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("list after restart = %+v, %v", list, err)
	}
	byID := map[string]api.TerminalSession{}
	for _, s := range list {
		byID[s.ID] = s
	}
	l := byID[live.ID]
	if l.Activity != api.ActivityExited || l.Meta["lost"] != "1" && l.ExitCode == nil || l.Name != "keep me" || !l.Pinned || l.Meta["k"] != "v" {
		t.Fatalf("restored live session = %+v", l)
	}
	if d := byID[done.ID]; d.ExitCode == nil || *d.ExitCode != 7 || d.Meta["lost"] == "1" {
		t.Fatalf("restored exited session = %+v", d)
	}
	// Attaching to a restored session reports its exit.
	a := e2.attach(done.ID, ptyclient.AttachOptions{Replay: true})
	a.waitMsg("exit")

	restored, err := e2.c.Restore(context.Background(), live.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID == live.ID || restored.Meta["restoredFrom"] != live.ID || restored.Name != "keep me" || restored.Activity == api.ActivityExited {
		t.Fatalf("restored = %+v", restored)
	}
	if err := e2.c.Remove(context.Background(), restored.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.c.Get(context.Background(), restored.ID); !errors.Is(err, ptyclient.ErrNotFound) {
		t.Fatalf("removed session still there: %v", err)
	}
}

func TestSecondDaemonRefused(t *testing.T) {
	p := testPaths(t)
	startEnv(t, p, nil)
	d, err := New(Options{Cfg: testConfig(p), Paths: p})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Run(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second daemon: %v", err)
	}
}

func TestSocketPermissions(t *testing.T) {
	p := testPaths(t)
	startEnv(t, p, nil)
	st, err := os.Stat(p.PtydSocket)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode: %v %v", st.Mode(), err)
	}
	dir, _ := os.Stat(p.RuntimeDir)
	if dir.Mode().Perm() != 0o700 {
		t.Fatalf("runtime dir mode %v", dir.Mode())
	}
}

func TestPasteBracketed(t *testing.T) {
	e := startEnv(t, testPaths(t), nil)
	s := e.create(shSpec(`printf '\033[?2004h'; stty -echo raw; head -c 30 | od -c | head -3`))
	e.waitSession(s.ID, 5*time.Second, "bracketed paste on", func(*api.TerminalSession) bool {
		ses, _ := e.d.get(s.ID)
		ses.mu.Lock()
		defer ses.mu.Unlock()
		return ses.term.Mode(2004)
	})
	time.Sleep(100 * time.Millisecond)
	// An embedded end marker must be stripped so the paste cannot end early.
	if err := e.c.Paste(context.Background(), s.ID, []byte("ab\x1b[201~cd")); err != nil {
		t.Fatal(err)
	}
	_ = e.c.Input(context.Background(), s.ID, []byte("01234567890123"))
	e.waitExit(s.ID, 5*time.Second)
	snap, _ := e.c.Snapshot(context.Background(), s.ID, 10)
	flat := strings.Join(strings.Fields(snap.Text), " ")
	if !strings.Contains(flat, "033 [ 2 0 0 ~ a b c d 033 [ 2 0 1 ~") {
		t.Fatalf("paste bytes = %q", flat)
	}
}

func TestEmptyListAndUnknownInput(t *testing.T) {
	e := startEnv(t, testPaths(t), nil)
	res, err := e.c.List(context.Background())
	if err != nil || len(res) != 0 {
		t.Fatalf("empty list = %v %v", res, err)
	}
	// Input to an unknown session is a 404.
	if err := e.c.Input(context.Background(), "t_aaaaaaaaaa", []byte("x")); !errors.Is(err, ptyclient.ErrNotFound) {
		t.Fatalf("input unknown: %v", err)
	}
}
