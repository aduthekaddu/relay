package cli

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

func TestBuildHookRequest(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		stdin   string
		payload string
		err     bool
	}{
		{"stdin json", []string{"claude", "stop"}, `{"session_id":"s"}` + "\n", `{"session_id":"s"}`, false},
		{"codex arg wins", []string{"codex", "notify", `{"type":"agent-turn-complete"}`}, `ignored`, `{"type":"agent-turn-complete"}`, false},
		{"non-json wrapped", []string{"x", "y"}, "hello\xff", `"hello` + "\ufffd" + `"`, false},
		{"empty", []string{"claude", "notification"}, "", "", false},
		{"missing event", []string{"claude"}, "", "", true},
		{"bad names", []string{"../x", "stop"}, "", "", true},
	}
	for _, c := range cases {
		req, err := buildHookRequest(c.args, []byte(c.stdin), "T1")
		if (err != nil) != c.err {
			t.Errorf("%s: err = %v", c.name, err)
			continue
		}
		if err != nil {
			continue
		}
		if string(req.Payload) != c.payload || req.SessionID != "T1" || req.Agent != c.args[0] {
			t.Errorf("%s: req = %+v payload %s", c.name, req, req.Payload)
		}
	}
	big := make([]byte, hookMaxInput+10)
	for i := range big {
		big[i] = 'a'
	}
	if req, _ := buildHookRequest([]string{"a", "b"}, big, ""); len(req.Payload) > hookMaxInput+2 {
		t.Errorf("payload not capped: %d", len(req.Payload))
	}
}

func TestReadHookInputTimeout(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	start := time.Now()
	if b := readHookInput(r, 50*time.Millisecond); b != nil {
		t.Errorf("got %q from a stdin that never closes", b)
	}
	if time.Since(start) > time.Second {
		t.Error("readHookInput blocked")
	}
	if b := readHookInput(strings.NewReader("abc"), time.Second); string(b) != "abc" {
		t.Errorf("got %q", b)
	}
}

// TestRunHookPostsToSocket runs the command end to end against a fake
// control socket, and checks it stays silent when the server is down.
func TestRunHookPostsToSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "rh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("unix sockets unavailable:", err)
	}
	got := make(chan api.AgentHookRequest, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req api.AgentHookRequest
		json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Path == "/api/v1/agents/hook" && r.Method == "POST" {
			got <- req
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"action":"attention","terminalId":"T9"}`))
	})}
	go srv.Serve(ln)
	defer srv.Close()
	t.Setenv("RELAY_SOCKET", sock)

	f, _ := os.CreateTemp(dir, "stdin")
	f.WriteString(`{"hook_event_name":"Notification","message":"hi"}`)
	f.Seek(0, 0)
	defer f.Close()
	env := map[string]string{"RELAY_SESSION": "T9"}
	var stderr strings.Builder
	runHook(context.Background(), []string{"claude", "notification"}, f, &stderr, func(k string) string { return env[k] })
	select {
	case req := <-got:
		if req.Agent != "claude" || req.Event != "notification" || req.SessionID != "T9" || !strings.Contains(string(req.Payload), "Notification") {
			t.Errorf("req = %+v", req)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no request")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}

	// Server gone: returns quickly, prints nothing unless debugging.
	t.Setenv("RELAY_SOCKET", filepath.Join(dir, "missing.sock"))
	start := time.Now()
	runHook(context.Background(), []string{"codex", "notify", `{}`}, nil, &stderr, func(k string) string { return env[k] })
	if time.Since(start) > hookCallTimeout+time.Second || stderr.Len() != 0 {
		t.Errorf("took %v, stderr %q", time.Since(start), stderr.String())
	}
	env["RELAY_HOOK_DEBUG"] = "1"
	runHook(context.Background(), []string{"codex", "notify", `{}`}, nil, &stderr, func(k string) string { return env[k] })
	if !strings.Contains(stderr.String(), "relay hook:") {
		t.Errorf("debug output missing: %q", stderr.String())
	}
}
