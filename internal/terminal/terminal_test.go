package terminal

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

func TestSessionLifecycleOverAPI(t *testing.T) {
	e := newEnv(t, true, nil)
	s := e.create(api.CreateTerminalRequest{Command: []string{"/bin/sh"}, Cwd: "~", Name: "work", Cols: 100, Rows: 30})
	if s.Name != "work" || s.Cwd != e.paths.Home || s.Cols != 100 {
		t.Fatalf("created %+v", s)
	}

	var list []api.TerminalSession
	if code := e.call("GET", "/api/v1/terminals", nil, &list); code != 200 || len(list) != 1 || list[0].ID != s.ID {
		t.Fatalf("list: %d %+v", code, list)
	}

	if code := e.call("POST", "/api/v1/terminals/"+s.ID+"/input", api.TerminalInput{Data: "echo hel''lo-api\r"}, nil); code != 204 {
		t.Fatalf("input: %d", code)
	}
	e.waitSnapshot(s.ID, "hello-api")

	if code := e.call("POST", "/api/v1/terminals/"+s.ID+"/resize", map[string]int{"cols": 91, "rows": 27}, nil); code != 204 {
		t.Fatalf("resize: %d", code)
	}
	e.call("POST", "/api/v1/terminals/"+s.ID+"/input", api.TerminalInput{Data: "stty size\r"}, nil)
	e.waitSnapshot(s.ID, "27 91")

	name := "renamed"
	var got api.TerminalSession
	if code := e.call("PATCH", "/api/v1/terminals/"+s.ID, api.UpdateTerminalRequest{Name: &name}, &got); code != 200 || got.Name != "renamed" {
		t.Fatalf("patch: %d %+v", code, got)
	}
	if code := e.call("POST", "/api/v1/terminals/"+s.ID+"/attention/ack", nil, nil); code != 204 {
		t.Fatalf("ack: %d", code)
	}
	if code := e.call("DELETE", "/api/v1/terminals/"+s.ID+"?signal=KILL", nil, nil); code != 204 {
		t.Fatalf("kill: %d", code)
	}
	exited := e.waitExit(s.ID)
	if exited.ExitCode == nil {
		t.Fatal("no exit code")
	}

	var restored api.TerminalSession
	if code := e.call("POST", "/api/v1/terminals/"+s.ID+"/restore", nil, &restored); code != 201 || restored.ID == s.ID || restored.Meta["restoredFrom"] != s.ID {
		t.Fatalf("restore: %d %+v", code, restored)
	}
	if code := e.call("DELETE", "/api/v1/terminals/"+s.ID+"?forget=1", nil, nil); code != 204 {
		t.Fatalf("forget: %d", code)
	}
	if code := e.call("GET", "/api/v1/terminals/"+s.ID, nil, nil); code != 404 {
		t.Fatalf("get forgotten: %d", code)
	}
}

func TestValidationErrors(t *testing.T) {
	e := newEnv(t, true, nil)
	s := e.create(sh("sleep 30"))
	cases := []struct {
		name   string
		method string
		path   string
		body   any
		want   int
	}{
		{"bad id", "GET", "/api/v1/terminals/../../etc", nil, 404},
		{"unknown id", "GET", "/api/v1/terminals/t_aaaaaaaaaa", nil, 404},
		{"bad kind", "POST", "/api/v1/terminals", api.CreateTerminalRequest{Kind: "evil"}, 400},
		{"nul arg", "POST", "/api/v1/terminals", api.CreateTerminalRequest{Command: []string{"/bin/sh", "a\x00b"}}, 400},
		{"missing cwd", "POST", "/api/v1/terminals", api.CreateTerminalRequest{Cwd: "/does/not/exist"}, 400},
		{"unknown field", "POST", "/api/v1/terminals", map[string]any{"bogus": 1}, 400},
		{"zero resize", "POST", "/api/v1/terminals/" + s.ID + "/resize", map[string]int{"cols": 0, "rows": 5}, 400},
		{"huge resize", "POST", "/api/v1/terminals/" + s.ID + "/resize", map[string]int{"cols": 5000, "rows": 5}, 400},
		{"bad signal", "DELETE", "/api/v1/terminals/" + s.ID + "?signal=STOP", nil, 400},
		{"control name", "PATCH", "/api/v1/terminals/" + s.ID, map[string]string{"name": "a\x1b[31m"}, 400},
		{"no recording", "GET", "/api/v1/terminals/" + s.ID + "/recording", nil, 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := e.call(tc.method, tc.path, tc.body, nil); got != tc.want {
				t.Fatalf("status %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDaemonDownIs503(t *testing.T) {
	e := newEnv(t, false, nil)
	var eb api.ErrorBody
	if code := e.call("GET", "/api/v1/terminals", nil, &eb); code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", code)
	}
	if eb.Error.Code != "unavailable" {
		t.Fatalf("error %+v", eb)
	}
}

func TestRecordingDownload(t *testing.T) {
	e := newEnv(t, true, func(c *config.Config) { c.Terminal.Record = "all" })
	s := e.create(sh("printf 'rec-me\\n'; sleep 0.2"))
	e.waitExit(s.ID)
	res, err := http.Get(e.srv.URL + "/api/v1/terminals/" + s.ID + "/recording?download=1")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/x-asciicast" || !strings.Contains(res.Header.Get("Content-Disposition"), s.ID+".cast") {
		t.Fatalf("status %d headers %v", res.StatusCode, res.Header)
	}
	var head map[string]any
	if err := json.NewDecoder(res.Body).Decode(&head); err != nil || head["version"] != float64(2) {
		t.Fatalf("header %v %v", head, err)
	}
}

// dialAttach opens the browser-side WebSocket.
func (e *env) dialAttach(t *testing.T, id, query string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/api/v1/terminals/" + id + "/attach?" + query
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.SetReadLimit(4 << 20)
	t.Cleanup(func() { c.CloseNow() })
	return c
}

// readUntil collects binary output until it contains want; text frames
// are returned in order as control messages.
func readUntil(t *testing.T, c *websocket.Conn, want string) (string, []api.TermServerMsg) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out strings.Builder
	var msgs []api.TermServerMsg
	for want == "" || !strings.Contains(out.String(), want) {
		typ, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read (have %q): %v", out.String(), err)
		}
		if typ == websocket.MessageBinary {
			out.Write(data)
			continue
		}
		var m api.TermServerMsg
		_ = json.Unmarshal(data, &m)
		msgs = append(msgs, m)
		if want == "" {
			break
		}
	}
	return out.String(), msgs
}

func writeText(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := c.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func TestAttachBridgeRoundTripAndReadOnly(t *testing.T) {
	e := newEnv(t, true, nil)
	s := e.create(api.CreateTerminalRequest{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	e.call("POST", "/api/v1/terminals/"+s.ID+"/input", api.TerminalInput{Data: "echo before-att''ach\r"}, nil)
	e.waitSnapshot(s.ID, "before-attach")

	rw := e.dialAttach(t, s.ID, "cols=90&rows=20&replay=1")
	out, msgs := readUntil(t, rw, "before-attach")
	if len(msgs) == 0 || msgs[0].T != "hello" {
		t.Fatalf("first control message %+v", msgs)
	}
	if !strings.Contains(out, "before-attach") {
		t.Fatalf("replay missing: %q", out)
	}

	ro := e.dialAttach(t, s.ID, "readonly=1&replay=0")
	_, roMsgs := readUntil(t, ro, "")
	if roMsgs[0].T != "hello" || !roMsgs[0].ReadOnly {
		t.Fatalf("read-only hello %+v", roMsgs[0])
	}
	// Input and resize from the read-only client are dropped by the bridge.
	if err := ro.Write(context.Background(), websocket.MessageBinary, []byte("echo SHOULD''NOTRUN\r")); err != nil {
		t.Fatal(err)
	}
	writeText(t, ro, api.TermClientMsg{T: "resize", Cols: 33, Rows: 11})
	writeText(t, ro, map[string]any{"t": "bogus", "x": 1})

	// The writer's input reaches the shell and both clients see it.
	if err := rw.Write(context.Background(), websocket.MessageBinary, []byte("echo via''-ws\r")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, rw, "via-ws")
	readUntil(t, ro, "via-ws")
	writeText(t, rw, api.TermClientMsg{T: "resize", Cols: 70, Rows: 21})
	writeText(t, rw, api.TermClientMsg{T: "ack", Bytes: 10})
	rw.Write(context.Background(), websocket.MessageBinary, []byte("stty size\r"))
	readUntil(t, rw, "21 70")

	snap := e.waitSnapshot(s.ID, "21 70")
	if strings.Contains(snap, "SHOULDNOTRUN") {
		t.Fatalf("read-only input reached the pty: %q", snap)
	}

	// Ping is answered through the bridge.
	writeText(t, rw, api.TermClientMsg{T: "ping"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		typ, data, err := rw.Read(ctx)
		if err != nil {
			t.Fatalf("no pong: %v", err)
		}
		if typ == websocket.MessageText && strings.Contains(string(data), `"pong"`) {
			break
		}
	}
}

func TestAttachExitForwardsCloseAndUnknown404(t *testing.T) {
	e := newEnv(t, true, nil)
	// Unknown session: plain HTTP error before the upgrade.
	res, err := http.Get(e.srv.URL + "/api/v1/terminals/t_aaaaaaaaaa/attach")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("status %d", res.StatusCode)
	}

	s := e.create(sh("read x; exit 3"))
	c := e.dialAttach(t, s.ID, "")
	readUntil(t, c, "")
	c.Write(context.Background(), websocket.MessageBinary, []byte("go\r"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var code *int
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				t.Fatalf("close: %v", err)
			}
			break
		}
		if typ == websocket.MessageText {
			var m api.TermServerMsg
			_ = json.Unmarshal(data, &m)
			if m.T == "exit" {
				code = m.Code
			}
		}
	}
	if code == nil || *code != 3 {
		t.Fatalf("exit code %v", code)
	}
}

func TestFilterClientMsg(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		readOnly bool
		want     string // "" = dropped
	}{
		{"resize", `{"t":"resize","cols":80,"rows":24,"bytes":5}`, false, `{"t":"resize","cols":80,"rows":24}`},
		{"resize ro", `{"t":"resize","cols":80,"rows":24}`, true, ""},
		{"resize zero", `{"t":"resize","cols":0,"rows":24}`, false, ""},
		{"resize huge", `{"t":"resize","cols":80,"rows":9999}`, false, ""},
		{"focus", `{"t":"focus","visible":true}`, true, `{"t":"focus","visible":true}`},
		{"focus missing", `{"t":"focus"}`, false, ""},
		{"ack", `{"t":"ack","bytes":42}`, true, `{"t":"ack","bytes":42}`},
		{"ack negative", `{"t":"ack","bytes":-1}`, false, ""},
		{"ping", `{"t":"ping","cols":3}`, true, `{"t":"ping"}`},
		{"unknown", `{"t":"exec","cmd":"rm"}`, false, ""},
		{"garbage", `not json`, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := filterClientMsg([]byte(tc.in), tc.readOnly)
			if tc.want == "" {
				if ok {
					t.Fatalf("kept %s", got)
				}
				return
			}
			if !ok || string(got) != tc.want {
				t.Fatalf("got %s %v, want %s", got, ok, tc.want)
			}
		})
	}
}

func TestEventRelayNotifyAndClip(t *testing.T) {
	e := newEnv(t, true, nil)
	n := &fakeNotifier{}
	e.d.Notifier = n
	clips := make(chan [2]string, 4)
	e.svc.OnClip = func(_ context.Context, id, text string) { clips <- [2]string{id, text} }
	sub := e.d.Bus.Subscribe(256, nil)
	defer sub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.relayEvents(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	// Wait for the relay to subscribe (resync lists sessions first).
	time.Sleep(300 * time.Millisecond)

	agent := e.create(api.CreateTerminalRequest{
		Command: []string{"/bin/sh", "-c", `read x; printf '\033]9;Build finished\007'; printf '\033]52;c;aGVsbG8gY2xpcA==\007'; sleep 5`},
		Kind:    api.KindAgent, Agent: "claude", Name: "agent-1",
	})
	shell := e.create(sh(`printf '\007'; sleep 5`))
	e.call("POST", "/api/v1/terminals/"+agent.ID+"/input", api.TerminalInput{Data: "go\r"}, nil)

	seen := map[string]bool{}
	deadline := time.After(5 * time.Second)
	for !(seen[api.EvTerminalCreated] && seen[api.EvTerminalUpdated] && len(n.all()) > 0) {
		select {
		case ev := <-sub.C:
			seen[ev.Type] = true
		case <-deadline:
			t.Fatalf("events seen %v, notifications %d", seen, len(n.all()))
		}
	}
	got := n.all()
	if len(got) != 1 {
		t.Fatalf("notifications %+v (the shell bell must not notify)", got)
	}
	if got[0].Kind != "attention" || got[0].SessionID != agent.ID || got[0].Link != "/terminal/"+agent.ID || got[0].Agent != "claude" || !strings.Contains(got[0].Title+got[0].Body, "Build finished") {
		t.Fatalf("notification %+v", got[0])
	}
	select {
	case c := <-clips:
		if c[0] != agent.ID || c[1] != "hello clip" {
			t.Fatalf("clip %v", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no clip")
	}

	e.call("DELETE", "/api/v1/terminals/"+shell.ID+"?forget=1", nil, nil)
	deadline = time.After(8 * time.Second)
	for {
		select {
		case ev := <-sub.C:
			if ev.Type == api.EvTerminalRemoved {
				if m, ok := ev.Data.(map[string]string); !ok || m["id"] != shell.ID {
					t.Fatalf("removed payload %#v", ev.Data)
				}
				return
			}
		case <-deadline:
			t.Fatal("no terminal.removed")
		}
	}
}

func TestEventRelayReconnects(t *testing.T) {
	e := newEnv(t, false, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.relayEvents(ctx); close(done) }()
	time.Sleep(200 * time.Millisecond) // a failed attempt or two
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not stop with its context")
	}
}

func TestSearchProvider(t *testing.T) {
	e := newEnv(t, true, nil)
	proj := filepath.Join(e.paths.Home, "webshop")
	os.MkdirAll(proj, 0o700)
	a := e.create(api.CreateTerminalRequest{Command: []string{"/bin/sh", "-c", "sleep 30"}, Name: "api-server", Cwd: proj})
	b := e.create(api.CreateTerminalRequest{Command: []string{"/bin/sh", "-c", "sleep 30"}, Name: "notes", Kind: api.KindAgent, Agent: "codex"})
	p := e.svc.SearchProvider()
	if p.Scope() != "terminals" {
		t.Fatal(p.Scope())
	}
	cases := []struct {
		q    string
		want []string
	}{
		{"api", []string{a.ID}},
		{"API-SERVER", []string{a.ID}},
		{"webshop", []string{a.ID}},
		{"codex", []string{b.ID}},
		{"", []string{a.ID, b.ID}},
		{"nothing-matches", nil},
	}
	for _, tc := range cases {
		res := p.Search(context.Background(), tc.q, 10)
		var ids []string
		for _, r := range res {
			ids = append(ids, r.ID)
			if r.Link != "/terminal/"+r.ID || r.Scope != "terminals" {
				t.Fatalf("result %+v", r)
			}
		}
		if len(ids) != len(tc.want) {
			t.Fatalf("q=%q got %v want %v", tc.q, ids, tc.want)
		}
		for _, w := range tc.want {
			if !strings.Contains(strings.Join(ids, ","), w) {
				t.Fatalf("q=%q got %v want %v", tc.q, ids, tc.want)
			}
		}
	}
}

func TestParseTmux(t *testing.T) {
	out := "main\t3\t1\t1700000000\t/home/u/code\nbad line\n\x1bevil\t1\t0\t1\t/\nsp ace\t1\t0\t0\t/tmp\twith tab\n"
	got := parseTmux([]byte(out))
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if got[0].Name != "main" || got[0].Windows != 3 || got[0].Attached != 1 || got[0].Created.Unix() != 1700000000 || got[0].Path != "/home/u/code" {
		t.Fatalf("%+v", got[0])
	}
	if got[1].Name != "sp ace" || got[1].Path != "/tmp\twith tab" || !got[1].Created.IsZero() {
		t.Fatalf("%+v", got[1])
	}
}

func TestTmuxImport(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	e := newEnv(t, true, func(c *config.Config) { c.Terminal.ImportTmux = true })
	// A private tmux server: never touch the user's default one.
	sock := filepath.Join(e.paths.RuntimeDir, "tmux.sock")
	e.svc.tmux = []string{"tmux", "-S", sock}
	tm := func(args ...string) {
		cmd := exec.Command("tmux", append([]string{"-S", sock, "-f", "/dev/null"}, args...)...)
		cmd.Env = tmuxEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v %s", args, err, out)
		}
	}
	tm("new-session", "-d", "-s", "legacy", "-c", e.paths.Home, "sh")
	t.Cleanup(func() {
		cmd := exec.Command("tmux", "-S", sock, "kill-server")
		cmd.Env = tmuxEnv()
		_ = cmd.Run()
	})

	var list []api.TerminalSession
	e.call("GET", "/api/v1/terminals", nil, &list)
	var imp *api.TerminalSession
	for i := range list {
		if list[i].Kind == api.KindTmux && list[i].Meta["importable"] == "1" {
			imp = &list[i]
		}
	}
	if imp == nil || imp.Name != "legacy" || imp.Meta["tmux"] != "legacy" || imp.Cwd != e.paths.Home {
		t.Fatalf("importable: %+v", list)
	}

	if code := e.call("POST", "/api/v1/terminals", api.CreateTerminalRequest{Kind: api.KindTmux, Meta: map[string]string{"tmux": "nope"}}, nil); code != 404 {
		t.Fatalf("unknown tmux session: %d", code)
	}
	s := e.create(api.CreateTerminalRequest{Kind: api.KindTmux, Meta: map[string]string{"tmux": "legacy"}, Cols: 80, Rows: 24})
	if s.Kind != api.KindTmux || s.Meta["tmux"] != "legacy" || s.Command[len(s.Command)-1] != "=legacy" {
		t.Fatalf("imported %+v", s)
	}
	// Importing again returns the same live session.
	again := e.create(api.CreateTerminalRequest{Kind: api.KindTmux, Name: "legacy"})
	if again.ID != s.ID {
		t.Fatalf("second import created %s", again.ID)
	}
	// The imported session drives the tmux pane.
	e.call("POST", "/api/v1/terminals/"+s.ID+"/input", api.TerminalInput{Data: "echo in''side-tmux\r"}, nil)
	e.waitSnapshot(s.ID, "inside-tmux")

	list = nil
	e.call("GET", "/api/v1/terminals", nil, &list)
	for _, l := range list {
		if l.Meta["importable"] == "1" {
			t.Fatalf("still importable while attached: %+v", l)
		}
	}
	e.call("DELETE", "/api/v1/terminals/"+s.ID, nil, nil)
	e.waitExit(s.ID)
}

func TestPtyErrMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{ptyclient.ErrNotFound, 404},
		{ptyclient.ErrUnavailable, 503},
		{&ptyclient.StatusError{Status: 400, Code: "bad_request", Message: "cwd"}, 400},
		{context.DeadlineExceeded, 504},
	}
	for _, tc := range cases {
		rec := &statusRecorder{}
		fail(rec, tc.err)
		if rec.code != tc.want {
			t.Fatalf("%v -> %d want %d", tc.err, rec.code, tc.want)
		}
	}
}

type statusRecorder struct {
	code int
	h    http.Header
}

func (r *statusRecorder) Header() http.Header {
	if r.h == nil {
		r.h = http.Header{}
	}
	return r.h
}
func (r *statusRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (r *statusRecorder) WriteHeader(c int)           { r.code = c }
