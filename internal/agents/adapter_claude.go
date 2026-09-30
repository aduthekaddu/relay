package agents

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Claude Code: ~/.claude/projects/<encoded cwd>/<session uuid>.jsonl, one
// JSON object per line (user / assistant / summary / custom-title / ...).
// Sub-agent transcripts live in <session uuid>/subagents/*.jsonl and are
// indexed for usage only.
func newClaude() *Adapter {
	return &Adapter{
		ID: "claude", Name: "Claude Code", Vendor: "Anthropic", Color: "#D97757",
		Binaries: []string{"claude"}, ExtraDirs: []string{".claude/local"},
		InstallHint: "claude-code",
		Caps: api.AgentCapabilities{Resume: true, Fork: true, Headless: true, Hooks: true,
			History: true, Usage: true, Quota: true, Worktrees: true, Prompt: true},
		Interactive: func(bin, prompt, model string) []string {
			return withPrompt(withFlag([]string{bin}, "--model", model), prompt)
		},
		PresetID: func() ([]string, string) {
			id := newUUID()
			return []string{"--session-id", id}, id
		},
		Resume: func(bin, id string) []string { return []string{bin, "--resume", id} },
		Fork:   func(bin, id string) []string { return []string{bin, "--resume", id, "--fork-session"} },
		Headless: func(bin, prompt, model string) []string {
			return withPrompt(withFlag([]string{bin, "-p", "--output-format", "text"}, "--model", model), prompt)
		},
		History: claudeReader{},
		Hooks: &jsonHooks{agent: "claude", rel: []string{".claude", "settings.json"},
			events: map[string]string{"Notification": "notification", "Stop": "stop"},
			order:  []string{"Notification", "Stop"}},
		ParseHook: parseClaudeLikeHook,
	}
}

// parseClaudeLikeHook interprets Claude Code / Gemini CLI hook payloads:
// {"session_id","cwd","hook_event_name","message"}.
func parseClaudeLikeHook(event string, payload []byte) hookEvent {
	m := decodePayload(payload)
	ev := hookEvent{NativeID: payloadString(m, "session_id", "sessionId"), Cwd: payloadString(m, "cwd")}
	name := strings.ToLower(payloadString(m, "hook_event_name"))
	if name == "" {
		name = strings.ToLower(event)
	}
	switch name {
	case "notification":
		ev.Action = hookAttention
		ev.Message = payloadString(m, "message", "title")
		if ev.Message == "" {
			ev.Message = "Needs your input"
		}
	case "stop", "afteragent", "sessionend", "subagentstop":
		if name == "subagentstop" {
			return hookEvent{}
		}
		ev.Action = hookDone
		ev.Message = "Finished its turn"
	}
	return ev
}

type claudeReader struct{}

func (claudeReader) sources(ctx context.Context, e *env) ([]source, error) {
	var out []source
	for _, root := range []string{e.path(".claude", "projects"), e.path(".config", "claude", "projects")} {
		for _, s := range globSources(filepath.Join(root, "*", "*.jsonl"), true) {
			s.NativeID = strings.TrimSuffix(filepath.Base(s.Path), ".jsonl")
			out = append(out, s)
		}
		for _, s := range globSources(filepath.Join(root, "*", "*", "subagents", "*.jsonl"), true) {
			s.NativeID = filepath.Base(filepath.Dir(filepath.Dir(s.Path)))
			s.UsageOnly = true
			out = append(out, s)
		}
	}
	return out, ctx.Err()
}

type claudeLine struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	SessionID   string          `json:"sessionId"`
	Timestamp   string          `json:"timestamp"`
	Cwd         string          `json:"cwd"`
	GitBranch   string          `json:"gitBranch"`
	IsMeta      bool            `json:"isMeta"`
	IsSidechain bool            `json:"isSidechain"`
	IsCompact   bool            `json:"isCompactSummary"`
	RequestID   string          `json:"requestId"`
	Message     json.RawMessage `json:"message"`
	Summary     string          `json:"summary"`
	CustomTitle string          `json:"customTitle"`
	AITitle     string          `json:"aiTitle"`
}

type claudeMessage struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *struct {
		Input      int64 `json:"input_tokens"`
		Output     int64 `json:"output_tokens"`
		CacheRead  int64 `json:"cache_read_input_tokens"`
		CacheWrite int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    *struct {
		MediaType string `json:"media_type"`
	} `json:"source"`
}

func (claudeReader) parse(ctx context.Context, e *env, src source, offset int64) (*parsed, error) {
	p := &parsed{NativeID: src.NativeID, Resumable: true, UsageOnly: src.UsageOnly}
	tools := map[string]string{} // tool_use id -> tool name
	usageIdx := map[string]int{}
	consumed, err := scanJSONL(ctx, src.Path, offset, e.chunk, func(line []byte, off int64) error {
		var l claudeLine
		if json.Unmarshal(line, &l) != nil {
			return nil
		}
		at := parseTime(l.Timestamp)
		switch l.Type {
		case "custom-title":
			if l.CustomTitle != "" {
				p.Title = cleanTitle(l.CustomTitle)
			}
			return nil
		case "ai-title":
			if l.AITitle != "" {
				p.Summary = cleanTitle(l.AITitle)
			}
			return nil
		case "summary":
			if l.Summary != "" && p.Summary == "" {
				p.Summary = cleanTitle(l.Summary)
			}
			return nil
		case "user", "assistant":
		default:
			return nil
		}
		if p.NativeID == "" {
			p.NativeID = l.SessionID
		}
		if l.Cwd != "" {
			p.Cwd = l.Cwd
		}
		if l.GitBranch != "" && l.GitBranch != "HEAD" {
			p.GitBranch = l.GitBranch
		}
		p.touch(at)
		var m claudeMessage
		if json.Unmarshal(l.Message, &m) != nil {
			return nil
		}
		if l.Type == "assistant" {
			claudeAssistant(p, &l, &m, at, tools, usageIdx)
			return nil
		}
		if l.IsMeta || l.IsCompact {
			return nil
		}
		claudeUser(p, &l, &m, at, tools)
		return nil
	})
	p.Consumed = consumed
	return p, err
}

func claudeAssistant(p *parsed, l *claudeLine, m *claudeMessage, at time.Time, tools map[string]string, usageIdx map[string]int) {
	if m.Model != "" && m.Model != "<synthetic>" {
		p.Model = m.Model
	}
	if m.Usage != nil && m.Model != "<synthetic>" {
		key := m.ID + ":" + l.RequestID
		if m.ID == "" {
			key = l.UUID
		}
		rec := usageRec{Key: key, At: at, Model: m.Model, Input: m.Usage.Input, Output: m.Usage.Output,
			CacheRead: m.Usage.CacheRead, CacheWrite: m.Usage.CacheWrite}
		if i, ok := usageIdx[key]; ok {
			p.Usage[i] = maxUsage(p.Usage[i], rec)
		} else {
			usageIdx[key] = len(p.Usage)
			p.Usage = append(p.Usage, rec)
		}
	}
	if p.UsageOnly {
		return
	}
	var blocks []contentBlock
	if json.Unmarshal(m.Content, &blocks) != nil {
		return
	}
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
		case "tool_use":
			tools[b.ID] = b.Name
			parts = append(parts, toolCallPart(b.Name, rawString(b.Input)))
			if d := editDiff(b.Name, b.Input); d != nil {
				parts = append(parts, *d)
			}
		}
	}
	if len(parts) == 0 {
		return
	}
	// Streaming writes one line per content block with the same message id:
	// fold them back into one message.
	if n := len(p.Messages); n > 0 && m.ID != "" && p.Messages[n-1].Role == "assistant" && p.lastAPIID == m.ID {
		p.Messages[n-1].Parts = append(p.Messages[n-1].Parts, parts...)
		return
	}
	p.lastAPIID = m.ID
	p.Messages = append(p.Messages, api.AgentMessage{ID: l.UUID, Role: "assistant", At: at, Model: m.Model, Parts: parts})
}

func claudeUser(p *parsed, l *claudeLine, m *claudeMessage, at time.Time, tools map[string]string) {
	p.lastAPIID = ""
	if p.UsageOnly {
		return
	}
	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		if isInjectedPrompt(text) || strings.TrimSpace(text) == "" {
			return
		}
		p.addUser(text)
		p.Messages = append(p.Messages, api.AgentMessage{ID: l.UUID, Role: "user", At: at, Parts: []api.Part{textPart("text", text)}})
		return
	}
	var blocks []contentBlock
	if json.Unmarshal(m.Content, &blocks) != nil {
		return
	}
	var user, results []api.Part
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if isInjectedPrompt(b.Text) || strings.TrimSpace(b.Text) == "" {
				continue
			}
			p.addUser(b.Text)
			user = append(user, textPart("text", b.Text))
		case "image":
			mime := "image/png"
			if b.Source != nil && b.Source.MediaType != "" {
				mime = b.Source.MediaType
			}
			user = append(user, api.Part{Type: "image", MimeType: mime})
		case "tool_result":
			results = append(results, toolResultPart(tools[b.ToolUseID], blockText(b.Content), b.IsError))
		}
	}
	if len(user) > 0 {
		p.Messages = append(p.Messages, api.AgentMessage{ID: l.UUID, Role: "user", At: at, Parts: user})
	}
	if len(results) > 0 {
		id := l.UUID
		if len(user) > 0 {
			id += ":r"
		}
		p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "tool", At: at, Parts: results})
	}
}

// blockText flattens a tool_result content value (string or block list).
func blockText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return rawString(raw)
	}
	var b strings.Builder
	for _, x := range blocks {
		switch x.Type {
		case "text":
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(x.Text)
		case "image":
			b.WriteString("[image]")
		}
		if b.Len() > maxToolText {
			break
		}
	}
	return b.String()
}

// maxUsage merges two records for the same API message, keeping the
// largest value of each counter (streamed lines repeat or grow them).
func maxUsage(a, b usageRec) usageRec {
	a.Input = max(a.Input, b.Input)
	a.Output = max(a.Output, b.Output)
	a.CacheRead = max(a.CacheRead, b.CacheRead)
	a.CacheWrite = max(a.CacheWrite, b.CacheWrite)
	a.Reasoning = max(a.Reasoning, b.Reasoning)
	a.NativeCost = max(a.NativeCost, b.NativeCost)
	if a.Model == "" {
		a.Model = b.Model
	}
	return a
}

// editDiff renders file-edit tool calls (Edit, MultiEdit, Write) as a
// compact unified diff part.
func editDiff(tool string, input json.RawMessage) *api.Part {
	var in struct {
		Path    string `json:"file_path"`
		Old     string `json:"old_string"`
		New     string `json:"new_string"`
		Content string `json:"content"`
		Edits   []struct {
			Old string `json:"old_string"`
			New string `json:"new_string"`
		} `json:"edits"`
	}
	if json.Unmarshal(input, &in) != nil || in.Path == "" {
		return nil
	}
	var b strings.Builder
	b.WriteString("--- a/" + in.Path + "\n+++ b/" + in.Path + "\n")
	hunk := func(old, new string) {
		b.WriteString("@@\n")
		for _, l := range splitLines(old) {
			b.WriteString("-" + l + "\n")
		}
		for _, l := range splitLines(new) {
			b.WriteString("+" + l + "\n")
		}
	}
	switch tool {
	case "Edit":
		hunk(in.Old, in.New)
	case "MultiEdit":
		for _, ed := range in.Edits {
			hunk(ed.Old, ed.New)
		}
	case "Write":
		hunk("", in.Content)
	default:
		return nil
	}
	return &api.Part{Type: "diff", Path: in.Path, Text: truncate(b.String(), maxToolText)}
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
