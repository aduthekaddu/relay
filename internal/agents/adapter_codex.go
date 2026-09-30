package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Codex CLI: ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl with
// {"timestamp","type","payload"} lines (session_meta, turn_context,
// response_item, event_msg). Thread names live in ~/.codex/session_index.jsonl.
func newCodex() *Adapter {
	return &Adapter{
		ID: "codex", Name: "Codex", Vendor: "OpenAI", Color: "#10A37F",
		Binaries: []string{"codex"}, InstallHint: "codex",
		Caps: api.AgentCapabilities{Resume: true, Fork: true, Headless: true, Hooks: true,
			History: true, Usage: true, Quota: true, Worktrees: true, Prompt: true},
		Interactive: func(bin, prompt, model string) []string {
			return withPrompt(withFlag([]string{bin}, "-m", model), prompt)
		},
		Resume: func(bin, id string) []string { return []string{bin, "resume", id} },
		Fork:   func(bin, id string) []string { return []string{bin, "fork", id} },
		Headless: func(bin, prompt, model string) []string {
			return withPrompt(withFlag([]string{bin, "exec", "--skip-git-repo-check"}, "-m", model), prompt)
		},
		History:   &codexReader{},
		Hooks:     &codexHooks{},
		ParseHook: parseCodexHook,
	}
}

// parseCodexHook handles the JSON Codex passes to its notify program:
// {"type":"agent-turn-complete","thread-id","cwd","last-assistant-message"}.
func parseCodexHook(_ string, payload []byte) hookEvent {
	m := decodePayload(payload)
	ev := hookEvent{NativeID: payloadString(m, "thread-id", "thread_id", "session_id"), Cwd: payloadString(m, "cwd")}
	switch payloadString(m, "type") {
	case "agent-turn-complete":
		ev.Action = hookDone
		ev.Message = cleanTitle(payloadString(m, "last-assistant-message"))
		if ev.Message == "" {
			ev.Message = "Finished its turn"
		}
	case "approval-requested", "user-input-requested", "elicitation-requested":
		ev.Action = hookAttention
		ev.Message = "Needs your approval"
	}
	return ev
}

type codexReader struct {
	mu      sync.Mutex
	names   map[string]string
	namesAt time.Time
}

func (r *codexReader) sources(ctx context.Context, e *env) ([]source, error) {
	var out []source
	add := func(ss []source) {
		for _, s := range ss {
			base := strings.TrimSuffix(filepath.Base(s.Path), ".jsonl")
			if len(base) >= 36 {
				s.NativeID = base[len(base)-36:]
			}
			out = append(out, s)
		}
	}
	add(walkSources(ctx, e.path(".codex", "sessions"), 4, true, func(n string) bool {
		return strings.HasPrefix(n, "rollout-") && strings.HasSuffix(n, ".jsonl")
	}))
	add(globSources(e.path(".codex", "archived_sessions", "rollout-*.jsonl"), true))
	return out, ctx.Err()
}

// threadName returns the user-visible thread name Codex stored for id.
func (r *codexReader) threadName(e *env, id string) string {
	path := e.path(".codex", "session_index.jsonl")
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.names == nil || !st.ModTime().Equal(r.namesAt) {
		names := map[string]string{}
		_, _ = scanJSONL(context.Background(), path, 0, 0, func(line []byte, off int64) error {
			var x struct {
				ID   string `json:"id"`
				Name string `json:"thread_name"`
			}
			if json.Unmarshal(line, &x) == nil && x.ID != "" && x.Name != "" {
				names[x.ID] = x.Name
			}
			return nil
		})
		r.names, r.namesAt = names, st.ModTime()
	}
	return r.names[id]
}

type codexLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexPayload struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Cwd       string          `json:"cwd"`
	Model     string          `json:"model"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Summary   json.RawMessage `json:"summary"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Input     string          `json:"input"`
	CallID    string          `json:"call_id"`
	Output    json.RawMessage `json:"output"`
	Source    json.RawMessage `json:"source"`
	Git       *struct {
		Branch string `json:"branch"`
	} `json:"git"`
	Info *struct {
		Total *codexTokens `json:"total_token_usage"`
		Last  *codexTokens `json:"last_token_usage"`
	} `json:"info"`
}

type codexTokens struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_output_tokens"`
	Total      int64 `json:"total_tokens"`
}

func (r *codexReader) parse(ctx context.Context, e *env, src source, offset int64) (*parsed, error) {
	p := &parsed{NativeID: src.NativeID, Resumable: true}
	tools := map[string]string{}
	consumed, err := scanJSONL(ctx, src.Path, offset, e.chunk, func(line []byte, off int64) error {
		var l codexLine
		if json.Unmarshal(line, &l) != nil {
			return nil
		}
		var pl codexPayload
		if json.Unmarshal(l.Payload, &pl) != nil {
			return nil
		}
		at := parseTime(l.Timestamp)
		p.touch(at)
		switch l.Type {
		case "session_meta":
			if pl.ID != "" {
				p.NativeID = pl.ID
			}
			if pl.Cwd != "" {
				p.Cwd = pl.Cwd
			}
			if pl.Git != nil && pl.Git.Branch != "" {
				p.GitBranch = pl.Git.Branch
			}
			// Guardian / review sub-agents are bookkeeping, not conversations.
			if len(pl.Source) > 0 && pl.Source[0] == '{' && strings.Contains(string(pl.Source), "subagent") {
				p.Hidden = true
			}
		case "turn_context":
			if pl.Model != "" {
				p.Model = pl.Model
			}
			if pl.Cwd != "" && p.Cwd == "" {
				p.Cwd = pl.Cwd
			}
		case "response_item":
			codexItem(p, &pl, at, off, tools)
		case "event_msg":
			if pl.Type == "token_count" && pl.Info != nil && pl.Info.Last != nil && pl.Info.Total != nil {
				t := pl.Info.Last
				// token_count repeats the same totals several times per
				// turn; the cumulative total is a natural dedupe key.
				p.Usage = append(p.Usage, usageRec{
					Key: "tc:" + strconv.FormatInt(pl.Info.Total.Total, 10), At: at, Model: p.Model,
					Input: max(0, t.Input-t.Cached-t.CacheWrite), Output: t.Output,
					CacheRead: t.Cached, CacheWrite: t.CacheWrite, Reasoning: t.Reasoning,
				})
			}
		}
		return nil
	})
	p.Consumed = consumed
	if name := r.threadName(e, p.NativeID); name != "" {
		p.Title = cleanTitle(name)
	}
	return p, err
}

func codexItem(p *parsed, pl *codexPayload, at time.Time, off int64, tools map[string]string) {
	id := lineID(off)
	if pl.ID != "" {
		id = pl.ID
	}
	switch pl.Type {
	case "message":
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(pl.Content, &blocks)
		var parts []api.Part
		for _, b := range blocks {
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			if pl.Role == "user" {
				if isInjectedPrompt(b.Text) {
					continue
				}
				p.addUser(b.Text)
			}
			switch b.Type {
			case "input_text", "output_text", "text":
				parts = append(parts, textPart("text", b.Text))
			case "input_image":
				parts = append(parts, api.Part{Type: "image", MimeType: "image/png"})
			}
		}
		if len(parts) == 0 || (pl.Role != "user" && pl.Role != "assistant") {
			return
		}
		p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: pl.Role, At: at, Model: modelIf(pl.Role, p.Model), Parts: parts})
	case "reasoning":
		var sum []struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(pl.Summary, &sum)
		var b strings.Builder
		for _, s := range sum {
			b.WriteString(s.Text)
			b.WriteString("\n")
		}
		if t := strings.TrimSpace(b.String()); t != "" {
			appendAssistantPart(p, id, at, textPart("thinking", t))
		}
	case "function_call", "custom_tool_call", "local_shell_call":
		tools[pl.CallID] = pl.Name
		input := pl.Arguments
		if input == "" {
			input = pl.Input
		}
		appendAssistantPart(p, id, at, toolCallPart(pl.Name, input))
		if pl.Name == "apply_patch" && strings.Contains(input, "*** Begin Patch") {
			appendAssistantPart(p, id, at, api.Part{Type: "diff", Text: truncate(input, maxToolText)})
		}
	case "function_call_output", "custom_tool_call_output":
		out := rawString(pl.Output)
		var wrapped struct {
			Output string `json:"output"`
		}
		if json.Unmarshal([]byte(out), &wrapped) == nil && wrapped.Output != "" {
			out = wrapped.Output
		}
		p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "tool", At: at,
			Parts: []api.Part{toolResultPart(tools[pl.CallID], out, false)}})
	}
}

// appendAssistantPart adds a part to the trailing assistant message, or
// starts a new one, so reasoning + tool calls of one turn stay together.
func appendAssistantPart(p *parsed, id string, at time.Time, part api.Part) {
	if n := len(p.Messages); n > 0 && p.Messages[n-1].Role == "assistant" {
		p.Messages[n-1].Parts = append(p.Messages[n-1].Parts, part)
		return
	}
	p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "assistant", At: at, Model: p.Model, Parts: []api.Part{part}})
}

func modelIf(role, model string) string {
	if role == "assistant" {
		return model
	}
	return ""
}
