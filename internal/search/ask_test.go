package search

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
)

// fakeAgents answers headless runs with a shell script chosen per prompt.
type fakeAgents struct {
	agents []api.AgentInfo
	script func(prompt string) string
	gotArg chan string
}

func (f fakeAgents) List(context.Context) []api.AgentInfo { return f.agents }

func (f fakeAgents) Command(context.Context, string, string, string) ([]string, map[string]string, error) {
	return nil, nil, nil
}

func (f fakeAgents) HeadlessCommand(_ context.Context, agent, prompt, _ string) ([]string, error) {
	if f.gotArg != nil {
		f.gotArg <- agent
	}
	return []string{"/bin/sh", "-c", f.script(prompt), "agent", prompt}, nil
}

func agentInfo(id string, installed, headless bool) api.AgentInfo {
	return api.AgentInfo{ID: id, Installed: installed, Capabilities: api.AgentCapabilities{Headless: headless}}
}

func askService(t *testing.T, fa fakeAgents, opts ...Option) *Service {
	t.Helper()
	d := &core.Deps{Search: core.NewSearchRegistry(), Agents: fa, Paths: config.Paths{Home: t.TempDir()}}
	s, err := New(d, append([]Option{WithScriptDirs()}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func doAsk(t *testing.T, s *Service, body string) (*httptest.ResponseRecorder, []api.AskChunk) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ask", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	var chunks []api.AskChunk
	if w.Code == http.StatusOK {
		sc := bufio.NewScanner(bytes.NewReader(w.Body.Bytes()))
		for sc.Scan() {
			var c api.AskChunk
			if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
				t.Fatalf("bad NDJSON line %q: %v", sc.Text(), err)
			}
			chunks = append(chunks, c)
		}
	}
	return w, chunks
}

func joinText(chunks []api.AskChunk) string {
	var b strings.Builder
	for _, c := range chunks {
		if c.T == ChunkText {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

func TestAskStreams(t *testing.T) {
	picked := make(chan string, 1)
	fa := fakeAgents{
		agents: []api.AgentInfo{agentInfo("nohead", true, false), agentInfo("missing", false, true), agentInfo("good", true, true)},
		// The prompt arrives as a single argv element ($1), never via a shell string.
		script: func(string) string { return `printf 'hel'; sleep 0.05; printf 'lo %s' "$1"` },
		gotArg: picked,
	}
	s := askService(t, fa)
	w, chunks := doAsk(t, s, `{"prompt":"what is $(id)?"}`)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/x-ndjson; charset=utf-8" {
		t.Fatalf("code %d, ct %q", w.Code, w.Header().Get("Content-Type"))
	}
	if a := <-picked; a != "good" {
		t.Fatalf("picked %q", a)
	}
	if got := joinText(chunks); got != "hello what is $(id)?" {
		t.Fatalf("text = %q", got)
	}
	if last := chunks[len(chunks)-1]; last.T != ChunkDone {
		t.Fatalf("last = %+v", last)
	}
}

func TestAskFailureAndTimeout(t *testing.T) {
	fa := fakeAgents{
		agents: []api.AgentInfo{agentInfo("a", true, true)},
		script: func(p string) string {
			if p == "slow" {
				return "printf partial; sleep 30"
			}
			return "echo 'not logged in' >&2; exit 4"
		},
	}
	s := askService(t, fa, WithAskTimeout(300*time.Millisecond))
	_, chunks := doAsk(t, s, `{"prompt":"fail"}`)
	last := chunks[len(chunks)-1]
	if last.T != ChunkError || !strings.Contains(last.Text, "exit 4") || !strings.Contains(last.Text, "not logged in") {
		t.Fatalf("failure chunk = %+v", last)
	}
	start := time.Now()
	_, chunks = doAsk(t, s, `{"prompt":"slow"}`)
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout not enforced")
	}
	if joinText(chunks) != "partial" || chunks[len(chunks)-1].T != ChunkError {
		t.Fatalf("timeout chunks = %+v", chunks)
	}
}

func TestAskValidation(t *testing.T) {
	fa := fakeAgents{
		agents: []api.AgentInfo{agentInfo("a", true, true), agentInfo("b", false, true)},
		script: func(string) string { return "true" },
	}
	s := askService(t, fa)
	tests := []struct {
		name, body string
		code       int
	}{
		{"bad json", `{`, 400},
		{"empty prompt", `{"prompt":"  "}`, 400},
		{"huge prompt", `{"prompt":"` + strings.Repeat("x", askPromptMax+1) + `"}`, 400},
		{"unknown agent", `{"prompt":"hi","agent":"zzz"}`, 400},
		{"not installed", `{"prompt":"hi","agent":"b"}`, 400},
		{"relative cwd", `{"prompt":"hi","cwd":"rel/dir"}`, 400},
		{"missing cwd", `{"prompt":"hi","cwd":"/definitely/not/here"}`, 400},
		{"ok", `{"prompt":"hi","agent":"a","cwd":"/"}`, 200},
	}
	for _, tt := range tests {
		if w, _ := doAsk(t, s, tt.body); w.Code != tt.code {
			t.Errorf("%s: code %d, want %d (%s)", tt.name, w.Code, tt.code, w.Body.String())
		}
	}

	none := askService(t, fakeAgents{agents: []api.AgentInfo{agentInfo("b", false, true)}})
	if w, _ := doAsk(t, none, `{"prompt":"hi"}`); w.Code != http.StatusServiceUnavailable {
		t.Errorf("no headless agent: %d", w.Code)
	}
}

func TestAskRateLimit(t *testing.T) {
	fa := fakeAgents{agents: []api.AgentInfo{agentInfo("a", true, true)}, script: func(string) string { return "echo ok" }}
	s := askService(t, fa)
	for i := 0; i < AskPerHour; i++ {
		if w, _ := doAsk(t, s, `{"prompt":"hi"}`); w.Code != 200 {
			t.Fatalf("ask %d: %d", i, w.Code)
		}
	}
	w, _ := doAsk(t, s, `{"prompt":"hi"}`)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("over quota: %d", w.Code)
	}
}

func TestPumpTextKeepsRunesWhole(t *testing.T) {
	const text = "héllo ✓ wörld 🚀"
	var buf bytes.Buffer
	cw := &chunkWriter{rc: http.NewResponseController(httptest.NewRecorder()), enc: json.NewEncoder(&buf)}
	pumpText(iotest.OneByteReader(strings.NewReader(text)), cw)
	var got strings.Builder
	dec := json.NewDecoder(&buf)
	for {
		var c api.AskChunk
		if err := dec.Decode(&c); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsRune(c.Text, '\uFFFD') {
			t.Fatalf("chunk split a rune: %q", c.Text)
		}
		got.WriteString(c.Text)
	}
	if got.String() != text {
		t.Fatalf("got %q", got.String())
	}
}

func TestTailBuffer(t *testing.T) {
	b := &tailBuffer{max: 4}
	b.Write([]byte("abc"))
	b.Write([]byte("defg"))
	if b.String() != "defg" {
		t.Fatalf("got %q", b.String())
	}
}
