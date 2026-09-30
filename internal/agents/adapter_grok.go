package agents

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aduthekaddu/relay/internal/api"
)

// Grok (xAI "grok build" CLI): ~/.grok/sessions/<url-encoded cwd>/<id>/
// with summary.json (title, times, model), chat_history.jsonl (append-only
// system/user/reasoning/assistant/tool_result records) and usage.json
// (per-turn tokens and cost in USD ticks of 1e-10).
func newGrok() *Adapter {
	return &Adapter{
		ID: "grok", Name: "Grok", Vendor: "xAI", Color: "#1D1D1F",
		Binaries: []string{"grok"}, ExtraDirs: []string{".grok/bin"}, InstallHint: "grok",
		Caps: api.AgentCapabilities{Resume: true, Fork: true, Headless: true, History: true,
			Usage: true, Worktrees: true, Prompt: true},
		Interactive: func(bin, prompt, model string) []string {
			return withPrompt(withFlag([]string{bin}, "-m", model), prompt)
		},
		// grok --session-id pins the id of a new conversation (verified with
		// `grok --help`), so Relay-launched terminals pair exactly.
		PresetID: func() ([]string, string) {
			id := newUUID()
			return []string{"--session-id", id}, id
		},
		Resume: func(bin, id string) []string { return []string{bin, "--resume", id} },
		Fork:   func(bin, id string) []string { return []string{bin, "--resume", id, "--fork-session"} },
		Headless: func(bin, prompt, model string) []string {
			return withFlag([]string{bin, "-p", safeArg(prompt)}, "-m", model)
		},
		History: grokReader{},
	}
}

type grokReader struct{}

func (grokReader) sources(ctx context.Context, e *env) ([]source, error) {
	var out []source
	for _, s := range globSources(e.path(".grok", "sessions", "*", "*", "chat_history.jsonl"), true) {
		dir := filepath.Dir(s.Path)
		s.NativeID = filepath.Base(dir)
		a, _ := fileStamp(filepath.Join(dir, "summary.json"))
		b, _ := fileStamp(filepath.Join(dir, "usage.json"))
		s.Version = a + "|" + b
		out = append(out, s)
	}
	return out, ctx.Err()
}

type grokSummary struct {
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	LastActive  string `json:"last_active_at"`
	Title       string `json:"generated_title"`
	Summary     string `json:"session_summary"`
	Model       string `json:"current_model_id"`
	SessionKind string `json:"session_kind"`
	WorkingDir  string `json:"working_directory"`
	Info        struct {
		Cwd string `json:"cwd"`
		ID  string `json:"id"`
	} `json:"info"`
}

type grokUsage struct {
	Turns []struct {
		TurnNumber int     `json:"turnNumber"`
		EndedAt    string  `json:"endedAt"`
		Input      int64   `json:"inputTokens"`
		Output     int64   `json:"outputTokens"`
		CacheRead  int64   `json:"cachedReadTokens"`
		CacheWrite int64   `json:"cacheCreationTokens"`
		Reasoning  int64   `json:"reasoningTokens"`
		Ticks      float64 `json:"costUsdTicks"`
		Model      string  `json:"primaryModelId"`
	} `json:"turns"`
}

type grokRecord struct {
	Type      string          `json:"type"`
	Content   json.RawMessage `json:"content"`
	ModelID   string          `json:"model_id"`
	ToolCalls []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"tool_calls"`
	ToolCallID string `json:"tool_call_id"`
	Summary    []struct {
		Text string `json:"text"`
	} `json:"summary"`
	Synthetic string `json:"synthetic_reason"`
}

func (grokReader) parse(ctx context.Context, e *env, src source, offset int64) (*parsed, error) {
	dir := filepath.Dir(src.Path)
	p := &parsed{NativeID: src.NativeID, Resumable: true}
	var sum grokSummary
	if readJSONFile(filepath.Join(dir, "summary.json"), 4<<20, &sum) == nil {
		p.Title = cleanTitle(sum.Title)
		p.Summary = cleanTitle(sum.Summary)
		p.Model = sum.Model
		p.Cwd = sum.Info.Cwd
		p.touch(parseTime(sum.CreatedAt))
		p.touch(parseTime(sum.UpdatedAt))
		p.touch(parseTime(sum.LastActive))
	}
	if p.Cwd == "" {
		if cwd, err := url.PathUnescape(filepath.Base(filepath.Dir(dir))); err == nil && filepath.IsAbs(cwd) {
			p.Cwd = cwd
		}
	}
	var u grokUsage
	if readJSONFile(filepath.Join(dir, "usage.json"), 8<<20, &u) == nil {
		for _, t := range u.Turns {
			p.Usage = append(p.Usage, usageRec{Key: "turn:" + strconv.Itoa(t.TurnNumber), At: parseTime(t.EndedAt),
				Model: t.Model, Input: max(0, t.Input-t.CacheRead-t.CacheWrite), Output: t.Output,
				CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, Reasoning: t.Reasoning,
				NativeCost: t.Ticks / 1e10})
		}
	}
	at := p.UpdatedAt
	tools := map[string]string{}
	consumed, err := scanJSONL(ctx, src.Path, offset, e.chunk, func(line []byte, off int64) error {
		var r grokRecord
		if json.Unmarshal(line, &r) != nil {
			return nil
		}
		id := lineID(off)
		switch r.Type {
		case "user":
			if r.Synthetic != "" {
				return nil
			}
			text := grokText(r.Content)
			if strings.TrimSpace(text) == "" || isInjectedPrompt(text) {
				return nil
			}
			p.addUser(text)
			p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "user", At: at, Parts: []api.Part{textPart("text", text)}})
		case "reasoning":
			var b strings.Builder
			for _, s := range r.Summary {
				b.WriteString(s.Text)
			}
			if t := strings.TrimSpace(b.String()); t != "" {
				appendAssistantPart(p, id, at, textPart("thinking", t))
			}
		case "assistant":
			if r.ModelID != "" {
				p.Model = r.ModelID
			}
			if t := grokText(r.Content); strings.TrimSpace(t) != "" {
				appendAssistantPart(p, id, at, textPart("text", t))
			}
			for _, tc := range r.ToolCalls {
				tools[tc.ID] = tc.Name
				appendAssistantPart(p, id, at, toolCallPart(tc.Name, tc.Arguments))
			}
		case "tool_result":
			p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "tool", At: at,
				Parts: []api.Part{toolResultPart(tools[r.ToolCallID], grokText(r.Content), false)}})
		}
		return nil
	})
	p.Consumed = consumed
	return p, err
}

// grokText flattens string or [{type:"text",text}] content.
func grokText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, x := range blocks {
		if x.Text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(x.Text)
		}
	}
	return b.String()
}
