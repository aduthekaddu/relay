package agents

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/aduthekaddu/relay/internal/api"
)

// GitHub Copilot CLI: ~/.copilot/session-state/<session id>/events.jsonl
// (append-only {type, data, timestamp} events: session.start,
// user.message, assistant.message, tool.execution_complete,
// session.model_change) plus workspace.yaml with the cwd. Older builds
// wrote ~/.copilot/session-state/<session id>.jsonl. Only output tokens are
// recorded, so costs are estimates.
func newCopilot() *Adapter {
	return &Adapter{
		ID: "copilot", Name: "Copilot CLI", Vendor: "GitHub", Color: "#6E40C9",
		Binaries: []string{"copilot"}, InstallHint: "copilot-cli",
		Caps: api.AgentCapabilities{Resume: true, Headless: true, History: true, Usage: true,
			Worktrees: true, Prompt: true},
		Interactive: func(bin, prompt, model string) []string {
			argv := withFlag([]string{bin}, "--model", model)
			if prompt != "" {
				argv = append(argv, "-i", safeArg(prompt))
			}
			return argv
		},
		Resume: func(bin, id string) []string { return []string{bin, "--resume", id} },
		Headless: func(bin, prompt, model string) []string {
			return withFlag([]string{bin, "-p", safeArg(prompt)}, "--model", model)
		},
		History: copilotReader{},
	}
}

type copilotReader struct{}

func (copilotReader) sources(ctx context.Context, e *env) ([]source, error) {
	root := e.path(".copilot", "session-state")
	var out []source
	for _, s := range globSources(filepath.Join(root, "*", "events.jsonl"), true) {
		s.NativeID = filepath.Base(filepath.Dir(s.Path))
		out = append(out, s)
	}
	for _, s := range globSources(filepath.Join(root, "*.jsonl"), true) {
		s.NativeID = strings.TrimSuffix(filepath.Base(s.Path), ".jsonl")
		out = append(out, s)
	}
	return out, ctx.Err()
}

type copilotEvent struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Data      struct {
		SessionID     string `json:"sessionId"`
		SelectedModel string `json:"selectedModel"`
		NewModel      string `json:"newModel"`
		Content       string `json:"content"`
		MessageID     string `json:"messageId"`
		Model         string `json:"model"`
		OutputTokens  int64  `json:"outputTokens"`
		ToolRequests  []struct {
			ToolCallID string          `json:"toolCallId"`
			Name       string          `json:"name"`
			Arguments  json.RawMessage `json:"arguments"`
		} `json:"toolRequests"`
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		Success    *bool  `json:"success"`
		Result     *struct {
			Content string `json:"content"`
		} `json:"result"`
		Context *struct {
			Cwd string `json:"cwd"`
		} `json:"context"`
	} `json:"data"`
}

func (copilotReader) parse(ctx context.Context, e *env, src source, offset int64) (*parsed, error) {
	p := &parsed{NativeID: src.NativeID, Resumable: true}
	if filepath.Base(src.Path) == "events.jsonl" {
		p.Cwd = yamlValue(filepath.Join(filepath.Dir(src.Path), "workspace.yaml"), "cwd")
	}
	tools := map[string]string{}
	consumed, err := scanJSONL(ctx, src.Path, offset, e.chunk, func(line []byte, off int64) error {
		var ev copilotEvent
		if json.Unmarshal(line, &ev) != nil {
			return nil
		}
		at := parseTime(ev.Timestamp)
		p.touch(at)
		id := ev.ID
		if id == "" {
			id = lineID(off)
		}
		d := &ev.Data
		switch ev.Type {
		case "session.start":
			if d.SessionID != "" {
				p.NativeID = d.SessionID
			}
			if d.SelectedModel != "" {
				p.Model = d.SelectedModel
			}
			if d.Context != nil && d.Context.Cwd != "" && p.Cwd == "" {
				p.Cwd = d.Context.Cwd
			}
		case "session.model_change":
			if d.NewModel != "" {
				p.Model = d.NewModel
			}
		case "user.message":
			if strings.TrimSpace(d.Content) == "" || isInjectedPrompt(d.Content) {
				return nil
			}
			p.addUser(d.Content)
			p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "user", At: at, Parts: []api.Part{textPart("text", d.Content)}})
		case "assistant.message":
			model := d.Model
			if model == "" {
				model = p.Model
			}
			var parts []api.Part
			if strings.TrimSpace(d.Content) != "" {
				parts = append(parts, textPart("text", d.Content))
			}
			for _, tr := range d.ToolRequests {
				tools[tr.ToolCallID] = tr.Name
				parts = append(parts, toolCallPart(tr.Name, rawString(tr.Arguments)))
			}
			if len(parts) > 0 {
				p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "assistant", At: at, Model: model, Parts: parts})
			}
			if d.OutputTokens > 0 {
				key := d.MessageID
				if key == "" {
					key = id
				}
				p.Usage = append(p.Usage, usageRec{Key: key, At: at, Model: model, Output: d.OutputTokens})
			}
		case "tool.execution_complete":
			out := ""
			if d.Result != nil {
				out = d.Result.Content
			}
			name := d.ToolName
			if name == "" {
				name = tools[d.ToolCallID]
			}
			p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "tool", At: at,
				Parts: []api.Part{toolResultPart(name, out, d.Success != nil && !*d.Success)}})
		}
		return nil
	})
	p.Consumed = consumed
	return p, err
}

// yamlValue reads a top-level `key: value` scalar from a small YAML file.
func yamlValue(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 0; sc.Scan() && n < 200; n++ {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}
