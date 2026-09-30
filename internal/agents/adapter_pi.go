package agents

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// pi (pi-coding-agent): ~/.pi/agent/sessions/--<cwd>--/<ts>_<uuid>.jsonl,
// append-only entries: a "session" header, then "message" entries whose
// message has role user | assistant | toolResult | bashExecution, plus
// session_info (display name), compaction and model_change entries.
func newPi() *Adapter {
	return &Adapter{
		ID: "pi", Name: "pi", Vendor: "Earendil", Color: "#E4572E",
		Binaries: []string{"pi"}, ExtraDirs: []string{".pi/agent/bin"}, InstallHint: "pi",
		Caps: api.AgentCapabilities{Resume: true, Fork: true, Headless: true, History: true,
			Usage: true, Worktrees: true, Prompt: true},
		Interactive: func(bin, prompt, model string) []string {
			return withPrompt(withFlag([]string{bin}, "--model", model), prompt)
		},
		PresetID: func() ([]string, string) {
			id := newUUID()
			return []string{"--session-id", id}, id
		},
		Resume: func(bin, id string) []string { return []string{bin, "--session", id} },
		Fork:   func(bin, id string) []string { return []string{bin, "--fork", id} },
		Headless: func(bin, prompt, model string) []string {
			return withPrompt(withFlag([]string{bin, "-p"}, "--model", model), prompt)
		},
		History: piReader{},
	}
}

type piReader struct{}

func (piReader) sources(ctx context.Context, e *env) ([]source, error) {
	var out []source
	for _, s := range globSources(e.path(".pi", "agent", "sessions", "*", "*.jsonl"), true) {
		base := strings.TrimSuffix(filepath.Base(s.Path), ".jsonl")
		if i := strings.LastIndex(base, "_"); i >= 0 {
			base = base[i+1:]
		}
		s.NativeID = base
		out = append(out, s)
	}
	return out, ctx.Err()
}

type piEntry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Cwd       string          `json:"cwd"`
	Name      string          `json:"name"`
	Summary   string          `json:"summary"`
	ModelID   string          `json:"modelId"`
	Message   json.RawMessage `json:"message"`
}

type piMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Model      string          `json:"model"`
	ToolName   string          `json:"toolName"`
	IsError    bool            `json:"isError"`
	Command    string          `json:"command"`
	Output     string          `json:"output"`
	Timestamp  any             `json:"timestamp"`
	Usage      *piUsage        `json:"usage"`
	ResponseID string          `json:"responseId"`
}

type piUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Reasoning  int64 `json:"reasoning"`
	Cost       struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

type piBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	MimeType  string          `json:"mimeType"`
}

func (piReader) parse(ctx context.Context, e *env, src source, offset int64) (*parsed, error) {
	p := &parsed{NativeID: src.NativeID, Resumable: true}
	consumed, err := scanJSONL(ctx, src.Path, offset, e.chunk, func(line []byte, off int64) error {
		var en piEntry
		if json.Unmarshal(line, &en) != nil {
			return nil
		}
		at := parseTime(en.Timestamp)
		p.touch(at)
		id := en.ID
		if id == "" {
			id = lineID(off)
		}
		switch en.Type {
		case "session":
			if en.ID != "" {
				p.NativeID = en.ID
			}
			p.Cwd = en.Cwd
		case "session_info":
			if t := cleanTitle(en.Name); t != "" {
				p.Title = t
			}
		case "compaction", "branch_summary":
			if en.Summary != "" {
				p.Summary = cleanTitle(en.Summary)
			}
		case "model_change":
			if en.ModelID != "" {
				p.Model = en.ModelID
			}
		case "message":
			var m piMessage
			if json.Unmarshal(en.Message, &m) == nil {
				piMessageEntry(p, id, at, &m)
			}
		}
		return nil
	})
	p.Consumed = consumed
	return p, err
}

func piMessageEntry(p *parsed, id string, at time.Time, m *piMessage) {
	if m.Usage != nil {
		u := m.Usage
		p.Usage = append(p.Usage, usageRec{Key: id, At: at, Model: m.Model, Input: u.Input, Output: u.Output,
			CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Reasoning: u.Reasoning, NativeCost: u.Cost.Total})
	}
	switch m.Role {
	case "user":
		text := grokText(m.Content) // string or [{type:text,text}]
		if strings.TrimSpace(text) == "" || isInjectedPrompt(text) {
			return
		}
		p.addUser(text)
		p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "user", At: at, Parts: []api.Part{textPart("text", text)}})
	case "assistant":
		if m.Model != "" {
			p.Model = m.Model
		}
		var blocks []piBlock
		_ = json.Unmarshal(m.Content, &blocks)
		var parts []api.Part
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) != "" {
					parts = append(parts, textPart("text", b.Text))
				}
			case "thinking":
				if strings.TrimSpace(b.Thinking) != "" {
					parts = append(parts, textPart("thinking", b.Thinking))
				}
			case "toolCall":
				parts = append(parts, toolCallPart(b.Name, rawString(b.Arguments)))
			}
		}
		if len(parts) > 0 {
			p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "assistant", At: at, Model: m.Model, Parts: parts})
		}
	case "toolResult":
		p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "tool", At: at,
			Parts: []api.Part{toolResultPart(m.ToolName, grokText(m.Content), m.IsError)}})
	case "bashExecution":
		p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "tool", At: at,
			Parts: []api.Part{toolCallPart("bash", m.Command), toolResultPart("bash", m.Output, false)}})
	}
}
