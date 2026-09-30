package agents

import (
	"context"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aduthekaddu/relay/internal/api"
)

// Every fixture below is synthetic: it mimics the on-disk layout and the
// JSON/SQLite shapes of each agent, never real content.

const (
	t0 = "2026-01-02T03:04:05Z"
	t1 = "2026-01-02T03:04:06Z"
	t2 = "2026-01-02T03:04:07Z"
	// unix seconds / ms for the same instants
	t0s  = 1767323045
	t0ms = 1767323045000
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func jl(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func newTestEnv(t *testing.T) *env {
	t.Helper()
	e := &env{home: t.TempDir(), tmp: t.TempDir(), dbs: newAgentDBs()}
	t.Cleanup(e.dbs.closeAll)
	return e
}

func sqliteFile(t *testing.T, path string, stmts ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

type parsedSrc struct {
	src source
	p   *parsed
}

// readAll lists and parses every source of a (whole files, no chunking).
func readAll(t *testing.T, a *Adapter, e *env) []parsedSrc {
	t.Helper()
	ctx := context.Background()
	srcs, err := a.History.sources(ctx, e)
	if err != nil {
		t.Fatalf("%s sources: %v", a.ID, err)
	}
	var out []parsedSrc
	for _, s := range srcs {
		p, err := a.History.parse(ctx, e, s, 0)
		if err != nil {
			t.Fatalf("%s parse %s: %v", a.ID, filepath.Base(s.Path), err)
		}
		out = append(out, parsedSrc{s, p})
	}
	return out
}

// session returns the parsed session (not usage-only) with native id.
func session(t *testing.T, ps []parsedSrc, native string) *parsed {
	t.Helper()
	for _, x := range ps {
		if x.p.NativeID == native && !x.p.UsageOnly {
			return x.p
		}
	}
	var ids []string
	for _, x := range ps {
		ids = append(ids, x.p.NativeID)
	}
	t.Fatalf("session %q not found among %v", native, ids)
	return nil
}

func roles(ms []api.AgentMessage) string {
	var r []string
	for _, m := range ms {
		r = append(r, m.Role)
	}
	return strings.Join(r, ",")
}

func partTypes(m api.AgentMessage) string {
	var r []string
	for _, p := range m.Parts {
		r = append(r, p.Type)
	}
	return strings.Join(r, ",")
}

func sumUsage(us []usageRec) usageRec {
	var s usageRec
	for _, u := range us {
		s.Input += u.Input
		s.Output += u.Output
		s.CacheRead += u.CacheRead
		s.CacheWrite += u.CacheWrite
		s.Reasoning += u.Reasoning
		s.NativeCost += u.NativeCost
		s.Credits += u.Credits
	}
	return s
}

type want struct {
	title, first, cwd, model, roles string
	usage                           usageRec
	hidden                          bool
}

func checkParsed(t *testing.T, p *parsed, w want) {
	t.Helper()
	if w.title != "" && p.Title != w.title {
		t.Errorf("title = %q, want %q", p.Title, w.title)
	}
	if w.first != "" && p.FirstUser != w.first {
		t.Errorf("first user = %q, want %q", p.FirstUser, w.first)
	}
	if w.cwd != "" && p.Cwd != w.cwd {
		t.Errorf("cwd = %q, want %q", p.Cwd, w.cwd)
	}
	if w.model != "" && p.Model != w.model {
		t.Errorf("model = %q, want %q", p.Model, w.model)
	}
	if got := roles(p.Messages); got != w.roles {
		t.Errorf("roles = %q, want %q", got, w.roles)
	}
	got := sumUsage(p.Usage)
	got.Key, got.Model, got.At = "", "", w.usage.At
	if got != w.usage {
		t.Errorf("usage = %+v, want %+v", got, w.usage)
	}
	if p.Hidden != w.hidden {
		t.Errorf("hidden = %v, want %v", p.Hidden, w.hidden)
	}
	if p.StartedAt.IsZero() || p.UpdatedAt.Before(p.StartedAt) {
		t.Errorf("bad time range %v .. %v", p.StartedAt, p.UpdatedAt)
	}
}

func TestClaudeReader(t *testing.T) {
	e := newTestEnv(t)
	id := "11111111-1111-4111-8111-111111111111"
	dir := e.path(".claude", "projects", "-tmp-proj")
	writeFile(t, filepath.Join(dir, id+".jsonl"), jl(
		`{"type":"user","uuid":"u1","sessionId":"`+id+`","timestamp":"`+t0+`","cwd":"/tmp/proj","gitBranch":"main","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>ctx</system-reminder>"},{"type":"text","text":"Fix the   login bug\nplease"}]}}`,
		`{"type":"assistant","uuid":"a1","requestId":"req1","timestamp":"`+t1+`","message":{"id":"msg1","role":"assistant","model":"claude-sonnet-4-5-20250929","content":[{"type":"thinking","thinking":"hmm"}],"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}`,
		`{"type":"assistant","uuid":"a2","requestId":"req1","timestamp":"`+t1+`","message":{"id":"msg1","role":"assistant","model":"claude-sonnet-4-5-20250929","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"/tmp/proj/a.go","old_string":"a","new_string":"b"}}],"usage":{"input_tokens":10,"output_tokens":50,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}`,
		`{"type":"user","uuid":"u2","timestamp":"`+t1+`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
		`{"type":"user","uuid":"u3","isMeta":true,"timestamp":"`+t1+`","message":{"role":"user","content":"meta"}}`,
		`{"type":"assistant","uuid":"a3","requestId":"req2","timestamp":"`+t2+`","message":{"id":"msg2","role":"assistant","model":"claude-sonnet-4-5-20250929","content":[{"type":"text","text":"Done."}],"usage":{"input_tokens":1,"output_tokens":2}}}`,
		`{"type":"custom-title","customTitle":"Login fix"}`,
	)+`{"type":"user","uuid":"partial`) // line still being written
	writeFile(t, filepath.Join(dir, id, "subagents", "agent-x.jsonl"), jl(
		`{"type":"assistant","uuid":"s1","requestId":"r9","timestamp":"`+t1+`","message":{"id":"m9","role":"assistant","model":"claude-haiku-4-5","content":[{"type":"text","text":"sub"}],"usage":{"input_tokens":7,"output_tokens":3}}}`,
	))
	ps := readAll(t, newClaude(), e)
	if len(ps) != 2 {
		t.Fatalf("sources = %d, want 2", len(ps))
	}
	p := session(t, ps, id)
	checkParsed(t, p, want{title: "Login fix", first: "Fix the login bug please", cwd: "/tmp/proj",
		model: "claude-sonnet-4-5-20250929", roles: "user,assistant,tool,assistant",
		usage: usageRec{Input: 11, Output: 52, CacheRead: 100, CacheWrite: 20}})
	if got := partTypes(p.Messages[1]); got != "thinking,tool_call,diff" {
		t.Errorf("streamed assistant parts = %q", got)
	}
	if p.GitBranch != "main" {
		t.Errorf("branch = %q", p.GitBranch)
	}
	st, _ := os.Stat(filepath.Join(dir, id+".jsonl"))
	if p.Consumed >= st.Size() || p.Consumed == 0 {
		t.Errorf("consumed %d of %d: partial line must be left for later", p.Consumed, st.Size())
	}
	for _, x := range ps {
		if x.p.UsageOnly {
			if x.p.NativeID != id || len(x.p.Messages) != 0 || sumUsage(x.p.Usage).Input != 7 {
				t.Errorf("subagent source: %+v", x.p)
			}
		}
	}
}

func TestClaudeIncremental(t *testing.T) {
	e := newTestEnv(t)
	id := "22222222-1111-4111-8111-111111111111"
	path := e.path(".claude", "projects", "-p", id+".jsonl")
	first := jl(`{"type":"user","uuid":"u1","timestamp":"` + t0 + `","message":{"role":"user","content":"one"}}`)
	writeFile(t, path, first)
	a := newClaude()
	src, _ := fileSource(path, true)
	src.NativeID = id
	p, err := a.History.parse(context.Background(), e, src, 0)
	if err != nil || len(p.Messages) != 1 || p.Consumed != int64(len(first)) {
		t.Fatalf("first parse: %v %+v", err, p)
	}
	writeFile(t, path, first+jl(`{"type":"user","uuid":"u2","timestamp":"`+t1+`","message":{"role":"user","content":"two"}}`))
	p2, err := a.History.parse(context.Background(), e, src, p.Consumed)
	if err != nil || len(p2.Messages) != 1 || p2.Messages[0].ID != "u2" {
		t.Fatalf("incremental parse: %v %+v", err, p2.Messages)
	}
}

func TestCodexReader(t *testing.T) {
	e := newTestEnv(t)
	id := "22222222-2222-4222-8222-222222222222"
	tc := `{"timestamp":"` + t1 + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":150},"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":50,"reasoning_output_tokens":10,"total_tokens":150}},"rate_limits":{"primary":{"used_percent":9,"window_minutes":300,"resets_at":1767340000},"secondary":{"used_percent":75,"window_minutes":10080,"resets_at":1767900000},"plan_type":"pro"}}}`
	writeFile(t, e.path(".codex", "sessions", "2026", "01", "02", "rollout-2026-01-02T03-04-05-"+id+".jsonl"), jl(
		`{"timestamp":"`+t0+`","type":"session_meta","payload":{"id":"`+id+`","cwd":"/tmp/cx","git":{"branch":"dev"}}}`,
		`{"timestamp":"`+t0+`","type":"turn_context","payload":{"model":"gpt-5.3-codex","cwd":"/tmp/cx"}}`,
		`{"timestamp":"`+t0+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>x</environment_context>"}]}}`,
		`{"timestamp":"`+t0+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Add tests"}]}}`,
		`{"timestamp":"`+t1+`","type":"response_item","payload":{"type":"reasoning","summary":[{"text":"thinking"}]}}`,
		`{"timestamp":"`+t1+`","type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{\"cmd\":\"ls\"}","call_id":"c1"}}`,
		`{"timestamp":"`+t1+`","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"{\"output\":\"a.go\"}"}}`,
		tc, tc, // streamed duplicate
		`{"timestamp":"`+t2+`","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Added."}]}}`,
	))
	writeFile(t, e.path(".codex", "session_index.jsonl"), jl(`{"id":"`+id+`","thread_name":"Codex tests"}`))
	ps := readAll(t, newCodex(), e)
	p := session(t, ps, id)
	checkParsed(t, p, want{title: "Codex tests", first: "Add tests", cwd: "/tmp/cx", model: "gpt-5.3-codex",
		roles: "user,assistant,tool,assistant",
		// two identical token_count events: same dedupe key, summed here
		usage: usageRec{Input: 120, Output: 100, CacheRead: 80, Reasoning: 20}})
	if p.Usage[0].Key != p.Usage[1].Key {
		t.Errorf("duplicate token_count events must share a key: %q %q", p.Usage[0].Key, p.Usage[1].Key)
	}
	if p.Messages[2].Parts[0].Output != "a.go" {
		t.Errorf("tool output = %q", p.Messages[2].Parts[0].Output)
	}
}

func TestGeminiReader(t *testing.T) {
	e := newTestEnv(t)
	dir := e.path(".gemini", "tmp", "abc123")
	writeFile(t, filepath.Join(dir, ".project_root"), "/tmp/gproj\n")
	writeFile(t, filepath.Join(dir, "chats", "session-2026-01-02T03-04-g1.json"), `{"sessionId":"g1","projectHash":"abc123",
	 "startTime":"`+t0+`","lastUpdated":"`+t2+`","messages":[
	 {"id":"m1","type":"user","content":"Explain the code","timestamp":"`+t0+`"},
	 {"id":"m0","type":"user","content":"/help","timestamp":"`+t0+`"},
	 {"id":"m2","type":"gemini","content":"It works","model":"gemini-2.5-pro","timestamp":"`+t1+`",
	  "thoughts":[{"subject":"S","description":"D"}],"tokens":{"input":100,"output":20,"cached":30,"thoughts":5},
	  "toolCalls":[{"name":"read_file","args":{"path":"a"},"result":[{"functionResponse":{"x":1}}],"status":"success"}]}]}`)
	p := session(t, readAll(t, newGemini(), e), "g1")
	checkParsed(t, p, want{first: "Explain the code", cwd: "/tmp/gproj", model: "gemini-2.5-pro",
		roles: "user,assistant,tool", usage: usageRec{Input: 70, Output: 25, CacheRead: 30, Reasoning: 5}})
	if got := partTypes(p.Messages[1]); got != "thinking,text,tool_call" {
		t.Errorf("parts = %q", got)
	}
}

func TestQwenReader(t *testing.T) {
	e := newTestEnv(t)
	writeFile(t, e.path(".qwen", "projects", "-tmp-q", "chats", "q1.jsonl"), jl(
		`{"uuid":"x1","sessionId":"q1","timestamp":"`+t0+`","type":"user","cwd":"/tmp/q","gitBranch":"main","message":{"role":"user","parts":[{"text":"Hello qwen"}]}}`,
		`{"uuid":"x2","sessionId":"q1","timestamp":"`+t1+`","type":"assistant","model":"qwen3-coder-plus","message":{"role":"model","parts":[{"text":"hm","thought":true},{"text":"Hi"},{"functionCall":{"name":"ls","args":{}}}]},"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":10,"cachedContentTokenCount":20,"thoughtsTokenCount":3}}`,
		`{"uuid":"x3","sessionId":"q1","timestamp":"`+t2+`","type":"tool_result","message":{"role":"user","parts":[{"functionResponse":{"name":"ls","response":{"out":"a"}}}]}}`,
	))
	p := session(t, readAll(t, newQwen(), e), "q1")
	checkParsed(t, p, want{first: "Hello qwen", cwd: "/tmp/q", model: "qwen3-coder-plus", roles: "user,assistant,tool",
		usage: usageRec{Input: 30, Output: 13, CacheRead: 20, Reasoning: 3}})
}

func TestKiroReader(t *testing.T) {
	e := newTestEnv(t)
	base := e.path(".kiro", "sessions", "cli", "k1")
	writeFile(t, base+".json", `{"session_id":"k1","cwd":"/tmp/k","created_at":"`+t0+`","updated_at":"`+t2+`","title":"Kiro task",
	 "session_state":{"conversation_metadata":{"user_turn_metadatas":[{"model":"claude-sonnet-4.5","end_timestamp":"`+t2+`",
	 "input_token_count":1000,"output_token_count":100,"cache_read_input_token_count":600,"cache_write_input_token_count":200,
	 "message_ids":["p1"],"metering_usage":[{"value":0.5,"unit":"credit"}]}]}}}`)
	writeFile(t, base+".jsonl", jl(
		`{"kind":"Prompt","data":{"message_id":"p1","content":[{"kind":"text","data":"Refactor module"}],"meta":{"timestamp":`+itoa64(t0s)+`}}}`,
		`{"kind":"AssistantMessage","data":{"message_id":"a1","content":[{"kind":"thinking","data":{"text":"plan"}},{"kind":"text","data":"Sure"},{"kind":"toolUse","data":{"toolUseId":"tu1","name":"fs_read","input":{"path":"x"}}}]}}`,
		`{"kind":"ToolResults","data":{"message_id":"r1","content":[{"kind":"toolResult","data":{"toolUseId":"tu1","status":"error","content":[{"kind":"text","data":"nope"}]}}]}}`,
	))
	// Legacy kiro-cli 1.x database.
	conv := `{"history":[{"user":{"content":{"Prompt":{"prompt":"Old prompt"}},"timestamp":"` + t0 + `"},
	 "assistant":{"Response":{"message_id":"m1","content":"Old answer"}},
	 "request_metadata":{"request_id":"rq1","model_id":"claude-sonnet-4","uncached_input_tokens":5,"output_tokens":6,"cache_read_input_tokens":7}}]}`
	sqliteFile(t, e.path(".local", "share", "kiro-cli", "data.sqlite3"),
		`CREATE TABLE conversations_v2 (key TEXT, conversation_id TEXT, value TEXT, created_at INTEGER, updated_at INTEGER)`,
		`INSERT INTO conversations_v2 VALUES ('/tmp/old', 'kv1', '`+conv+`', `+itoa64(t0ms)+`, `+itoa64(t0ms+1000)+`)`)
	ps := readAll(t, newKiro(), e)
	p := session(t, ps, "k1")
	checkParsed(t, p, want{title: "Kiro task", first: "Refactor module", cwd: "/tmp/k", model: "claude-sonnet-4.5",
		roles: "user,assistant,tool", usage: usageRec{Input: 200, Output: 100, CacheRead: 600, CacheWrite: 200, Credits: 0.5}})
	if !p.Messages[2].Parts[0].IsError || p.Messages[2].Parts[0].Tool != "fs_read" {
		t.Errorf("tool result = %+v", p.Messages[2].Parts[0])
	}
	old := session(t, ps, "kv1")
	checkParsed(t, old, want{first: "Old prompt", cwd: "/tmp/old", roles: "user,assistant",
		usage: usageRec{Input: 5, Output: 6, CacheRead: 7}})
	if old.Resumable {
		t.Error("legacy kiro sessions cannot be resumed by id")
	}
}

func TestOpenCodeReader(t *testing.T) {
	e := newTestEnv(t)
	sqliteFile(t, e.path(".local", "share", "opencode", "opencode.db"),
		`CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, directory TEXT, title TEXT, time_created INTEGER, time_updated INTEGER)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session VALUES ('s1', NULL, '/tmp/oc', 'Fix CSS', `+itoa64(t0ms)+`, `+itoa64(t0ms+2000)+`)`,
		`INSERT INTO session VALUES ('s2', 's1', '/tmp/oc', 'Child session - x', `+itoa64(t0ms)+`, `+itoa64(t0ms)+`)`,
		`INSERT INTO message VALUES ('m1', 's1', `+itoa64(t0ms)+`, 0, '{"role":"user"}')`,
		`INSERT INTO message VALUES ('m2', 's1', `+itoa64(t0ms+1000)+`, 0, '{"role":"assistant","modelID":"claude-sonnet-4-5","cost":0.01,"tokens":{"input":10,"output":5,"reasoning":1,"cache":{"read":3,"write":2}}}')`,
		`INSERT INTO part VALUES ('p1', 'm1', 's1', 1, 0, '{"type":"text","text":"Make it blue"}')`,
		`INSERT INTO part VALUES ('p2', 'm1', 's1', 2, 0, '{"type":"text","text":"synthetic","synthetic":true}')`,
		`INSERT INTO part VALUES ('p3', 'm2', 's1', 3, 0, '{"type":"reasoning","text":"t"}')`,
		`INSERT INTO part VALUES ('p4', 'm2', 's1', 4, 0, '{"type":"text","text":"Done"}')`,
		`INSERT INTO part VALUES ('p5', 'm2', 's1', 5, 0, '{"type":"tool","tool":"bash","state":{"status":"completed","input":{"cmd":"ls"},"output":"a"}}')`,
	)
	ps := readAll(t, newOpenCode(), e)
	p := session(t, ps, "s1")
	checkParsed(t, p, want{title: "Fix CSS", first: "Make it blue", cwd: "/tmp/oc", model: "claude-sonnet-4-5",
		roles: "user,assistant,tool", usage: usageRec{Input: 10, Output: 6, Reasoning: 1, CacheRead: 3, CacheWrite: 2, NativeCost: 0.01}})
	child := session(t, ps, "s2")
	if !child.Hidden || child.Title != "" {
		t.Errorf("child session: hidden=%v title=%q", child.Hidden, child.Title)
	}
}

func TestCursorReader(t *testing.T) {
	e := newTestEnv(t)
	meta := hex.EncodeToString([]byte(`{"name":"Cursor chat","createdAt":` + itoa64(t0ms) + `,"lastUsedModel":"gpt-5"}`))
	sqliteFile(t, e.path(".cursor", "chats", "h1", "c1", "store.db"),
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)`,
		`INSERT INTO meta VALUES ('0', '`+meta+`')`,
		`INSERT INTO blobs VALUES ('b0', X'0a0b0c')`,
		`INSERT INTO blobs VALUES ('b1', CAST('{"role":"user","content":"Build it"}' AS BLOB))`,
		`INSERT INTO blobs VALUES ('b2', CAST('{"role":"assistant","content":[{"type":"reasoning","text":"r"},{"type":"text","text":"Built"},{"type":"tool-call","toolName":"edit","args":{"a":1}}]}' AS BLOB))`,
		`INSERT INTO blobs VALUES ('b3', CAST('{"role":"tool","content":[{"type":"tool-result","toolName":"edit","result":"ok"}]}' AS BLOB))`,
		`INSERT INTO blobs VALUES ('b4', CAST('{"role":"system","content":"sys"}' AS BLOB))`,
	)
	p := session(t, readAll(t, newCursor(), e), "c1")
	checkParsed(t, p, want{title: "Cursor chat", first: "Build it", model: "gpt-5", roles: "user,assistant,tool"})
}

func TestGrokReader(t *testing.T) {
	e := newTestEnv(t)
	dir := e.path(".grok", "sessions", "%2Ftmp%2Fgk", "gs1")
	writeFile(t, filepath.Join(dir, "summary.json"), `{"generated_title":"Grok work","current_model_id":"grok-4","created_at":"`+t0+`","updated_at":"`+t2+`"}`)
	writeFile(t, filepath.Join(dir, "usage.json"), `{"turns":[{"turnNumber":1,"endedAt":"`+t2+`","inputTokens":100,"outputTokens":10,"cachedReadTokens":30,"cacheCreationTokens":0,"reasoningTokens":2,"costUsdTicks":100000000,"primaryModelId":"grok-4"}]}`)
	writeFile(t, filepath.Join(dir, "chat_history.jsonl"), jl(
		`{"type":"user","content":"Do grok things"}`,
		`{"type":"user","synthetic_reason":"reminder","content":"sys"}`,
		`{"type":"reasoning","summary":[{"text":"hm"}]}`,
		`{"type":"assistant","content":[{"type":"text","text":"ok"}],"tool_calls":[{"id":"tc1","name":"run","arguments":"{}"}],"model_id":"grok-4"}`,
		`{"type":"tool_result","tool_call_id":"tc1","content":"out"}`,
	))
	p := session(t, readAll(t, newGrok(), e), "gs1")
	checkParsed(t, p, want{title: "Grok work", first: "Do grok things", cwd: "/tmp/gk", model: "grok-4",
		roles: "user,assistant,tool", usage: usageRec{Input: 70, Output: 10, CacheRead: 30, Reasoning: 2, NativeCost: 0.01}})
	if p.Messages[2].Parts[0].Tool != "run" {
		t.Errorf("tool name = %q", p.Messages[2].Parts[0].Tool)
	}
}

func TestPiReader(t *testing.T) {
	e := newTestEnv(t)
	writeFile(t, e.path(".pi", "agent", "sessions", "--tmp-pi--", "2026-01-02T03-04-05_p1.jsonl"), jl(
		`{"type":"session","id":"p1","timestamp":"`+t0+`","cwd":"/tmp/pi"}`,
		`{"type":"message","id":"e1","timestamp":"`+t0+`","message":{"role":"user","content":"Pi please"}}`,
		`{"type":"message","id":"e2","timestamp":"`+t1+`","message":{"role":"assistant","model":"claude-opus-4-5","content":[{"type":"thinking","thinking":"t"},{"type":"text","text":"yes"},{"type":"toolCall","name":"bash","arguments":{"c":"ls"}}],"usage":{"input":5,"output":6,"cacheRead":7,"cacheWrite":8,"cost":{"total":0.02}}}}`,
		`{"type":"message","id":"e3","timestamp":"`+t2+`","message":{"role":"toolResult","toolName":"bash","content":[{"type":"text","text":"a"}],"isError":true}}`,
		`{"type":"session_info","id":"e4","timestamp":"`+t2+`","name":"Pi title"}`,
	))
	p := session(t, readAll(t, newPi(), e), "p1")
	checkParsed(t, p, want{title: "Pi title", first: "Pi please", cwd: "/tmp/pi", model: "claude-opus-4-5",
		roles: "user,assistant,tool", usage: usageRec{Input: 5, Output: 6, CacheRead: 7, CacheWrite: 8, NativeCost: 0.02}})
	if !p.Messages[2].Parts[0].IsError {
		t.Error("tool error flag lost")
	}
}

func TestHermesReader(t *testing.T) {
	e := newTestEnv(t)
	sqliteFile(t, e.path(".hermes", "state.db"),
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, title TEXT, cwd TEXT, git_branch TEXT, model TEXT, parent_session_id TEXT,
			started_at REAL, ended_at REAL, last_activity_at REAL, message_count INTEGER, hidden INTEGER, archived INTEGER)`,
		`CREATE TABLE messages (id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT, tool_calls TEXT, tool_name TEXT, reasoning TEXT, timestamp REAL)`,
		`CREATE TABLE session_model_usage (session_id TEXT, model TEXT, task TEXT, input_tokens INTEGER, output_tokens INTEGER,
			cache_read_tokens INTEGER, cache_write_tokens INTEGER, reasoning_tokens INTEGER, estimated_cost_usd REAL, actual_cost_usd REAL, first_seen REAL, last_seen REAL)`,
		`INSERT INTO sessions VALUES ('h1', 'Hermes chat', '/tmp/h', 'main', 'gpt-5', NULL, `+itoa64(t0s)+`, NULL, `+itoa64(t0s+2)+`, 3, 0, 0)`,
		`INSERT INTO sessions VALUES ('h2', 'sub', '/tmp/h', '', '', 'h1', `+itoa64(t0s)+`, NULL, NULL, 0, 0, 0)`,
		`INSERT INTO messages VALUES (1, 'h1', 'user', 'Hermes go', NULL, NULL, NULL, `+itoa64(t0s)+`)`,
		`INSERT INTO messages VALUES (2, 'h1', 'assistant', 'going', '[{"function":{"name":"terminal","arguments":"{}"}}]', NULL, 'why', `+itoa64(t0s+1)+`)`,
		`INSERT INTO messages VALUES (3, 'h1', 'tool', 'result', NULL, 'terminal', NULL, `+itoa64(t0s+2)+`)`,
		`INSERT INTO session_model_usage VALUES ('h1', 'gpt-5', 'chat', 10, 20, 30, 0, 5, 0.05, NULL, `+itoa64(t0s)+`, `+itoa64(t0s+2)+`)`,
	)
	ps := readAll(t, newHermes(), e)
	p := session(t, ps, "h1")
	checkParsed(t, p, want{title: "Hermes chat", first: "Hermes go", cwd: "/tmp/h", model: "gpt-5",
		roles: "user,assistant,tool", usage: usageRec{Input: 10, Output: 20, CacheRead: 30, Reasoning: 5, NativeCost: 0.05}})
	if got := partTypes(p.Messages[1]); got != "thinking,text,tool_call" {
		t.Errorf("parts = %q", got)
	}
	if !session(t, ps, "h2").Hidden {
		t.Error("child session must be hidden")
	}
}

func TestAmpReader(t *testing.T) {
	e := newTestEnv(t)
	writeFile(t, e.path(".local", "share", "amp", "threads", "T-abc.json"), `{"id":"T-abc","created":`+itoa64(t0ms)+`,"title":"Amp thread",
	 "env":{"initial":{"trees":[{"uri":"file:///tmp/amp"}]}},"messages":[
	 {"role":"user","content":[{"type":"text","text":"Amp go"}],"meta":{"sentAt":`+itoa64(t0ms)+`}},
	 {"role":"assistant","content":[{"type":"thinking","thinking":"t"},{"type":"text","text":"went"},{"type":"tool_use","id":"u1","name":"Bash","input":{"cmd":"ls"}}],
	  "usage":{"model":"claude-sonnet-4-5","inputTokens":3,"outputTokens":4,"cacheCreationInputTokens":5,"cacheReadInputTokens":6,"timestamp":"`+t1+`"}},
	 {"role":"user","content":[{"type":"tool_result","toolUseID":"u1","run":{"status":"error","result":{"out":"x"}}}]}]}`)
	p := session(t, readAll(t, newAmp(), e), "T-abc")
	checkParsed(t, p, want{title: "Amp thread", first: "Amp go", cwd: "/tmp/amp", model: "claude-sonnet-4-5",
		roles: "user,assistant,tool", usage: usageRec{Input: 3, Output: 4, CacheRead: 6, CacheWrite: 5}})
	if r := p.Messages[2].Parts[0]; r.Tool != "Bash" || !r.IsError {
		t.Errorf("tool result = %+v", r)
	}
}

func TestCopilotReader(t *testing.T) {
	e := newTestEnv(t)
	dir := e.path(".copilot", "session-state", "cp1")
	writeFile(t, filepath.Join(dir, "workspace.yaml"), "id: cp1\ncwd: /tmp/cp\n")
	writeFile(t, filepath.Join(dir, "events.jsonl"), jl(
		`{"type":"session.start","id":"e1","timestamp":"`+t0+`","data":{"sessionId":"cp1","selectedModel":"gpt-5"}}`,
		`{"type":"user.message","id":"e2","timestamp":"`+t0+`","data":{"content":"Copilot do"}}`,
		`{"type":"assistant.message","id":"e3","timestamp":"`+t1+`","data":{"content":"doing","messageId":"mm","outputTokens":42,"toolRequests":[{"toolCallId":"t1","name":"bash","arguments":{"c":1}}]}}`,
		`{"type":"tool.execution_complete","id":"e4","timestamp":"`+t2+`","data":{"toolCallId":"t1","success":false,"result":{"content":"err"}}}`,
	))
	p := session(t, readAll(t, newCopilot(), e), "cp1")
	checkParsed(t, p, want{first: "Copilot do", cwd: "/tmp/cp", model: "gpt-5", roles: "user,assistant,tool",
		usage: usageRec{Output: 42}})
	if r := p.Messages[2].Parts[0]; r.Tool != "bash" || !r.IsError {
		t.Errorf("tool result = %+v", r)
	}
}

func TestAiderReader(t *testing.T) {
	e := newTestEnv(t)
	ws := t.TempDir()
	e.workspaces = func(context.Context) []string { return []string{ws} }
	writeFile(t, filepath.Join(ws, aiderHistory), "# aider chat started at 2026-01-02 03:04:05\n\n#### Fix typo\n\nI fixed it.\n\n> Applied edit to a.go\n\n"+
		"# aider chat started at 2026-01-03 03:04:05\n\n#### Second run\n\nok\n")
	ps := readAll(t, newAider(), e)
	if len(ps) != 2 {
		t.Fatalf("runs = %d, want 2", len(ps))
	}
	if ps[0].p.NativeID == ps[1].p.NativeID {
		t.Fatal("runs must have distinct ids")
	}
	checkParsed(t, ps[0].p, want{first: "Fix typo", cwd: ws, roles: "user,assistant,tool"})
	checkParsed(t, ps[1].p, want{first: "Second run", cwd: ws, roles: "user,assistant"})
}

func TestCrushReader(t *testing.T) {
	e := newTestEnv(t)
	ws := t.TempDir()
	e.workspaces = func(context.Context) []string { return []string{ws} }
	sqliteFile(t, filepath.Join(ws, ".crush", "crush.db"),
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, parent_session_id TEXT, title TEXT, message_count INTEGER, prompt_tokens INTEGER,
			completion_tokens INTEGER, cost REAL, created_at INTEGER, updated_at INTEGER)`,
		`CREATE TABLE messages (id TEXT PRIMARY KEY, session_id TEXT, role TEXT, parts TEXT, model TEXT, created_at INTEGER)`,
		`INSERT INTO sessions VALUES ('cr1', NULL, 'Crush title', 3, 100, 50, 0.3, `+itoa64(t0s)+`, `+itoa64(t0s+2)+`)`,
		`INSERT INTO messages VALUES ('x1', 'cr1', 'user', '[{"type":"text","data":{"text":"Crush it"}}]', NULL, `+itoa64(t0s)+`)`,
		`INSERT INTO messages VALUES ('x2', 'cr1', 'assistant', '[{"type":"reasoning","data":{"thinking":"t"}},{"type":"text","data":{"text":"crushed"}},{"type":"tool_call","data":{"name":"bash","input":"ls"}}]', 'gpt-5', `+itoa64(t0s+1)+`)`,
		`INSERT INTO messages VALUES ('x3', 'cr1', 'tool', '[{"type":"tool_result","data":{"name":"bash","content":"x","is_error":true}}]', NULL, `+itoa64(t0s+2)+`)`,
	)
	p := session(t, readAll(t, newCrush(), e), "cr1")
	checkParsed(t, p, want{title: "Crush title", first: "Crush it", cwd: ws, model: "gpt-5", roles: "user,assistant,tool",
		usage: usageRec{Input: 100, Output: 50, NativeCost: 0.3}})
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
