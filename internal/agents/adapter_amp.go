package agents

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Amp (Sourcegraph): threads are JSON documents in
// ~/.local/share/amp/threads/T-<id>.json with {id, created, title,
// messages[{role, content[], usage}], env.initial.trees[{uri}]}.
func newAmp() *Adapter {
	return &Adapter{
		ID: "amp", Name: "Amp", Vendor: "Sourcegraph", Color: "#F34E3F",
		Binaries: []string{"amp"}, ExtraDirs: []string{".amp/bin"}, InstallHint: "amp",
		Caps:        api.AgentCapabilities{Resume: true, Headless: true, History: true, Usage: true, Worktrees: true},
		Interactive: func(bin, _, _ string) []string { return []string{bin} },
		Resume:      func(bin, id string) []string { return []string{bin, "threads", "continue", id} },
		Headless: func(bin, prompt, _ string) []string {
			return []string{bin, "-x", safeArg(prompt)}
		},
		History: ampReader{},
	}
}

type ampReader struct{}

func (ampReader) sources(ctx context.Context, e *env) ([]source, error) {
	var out []source
	for _, s := range globSources(e.path(".local", "share", "amp", "threads", "T-*.json"), false) {
		s.NativeID = strings.TrimSuffix(filepath.Base(s.Path), ".json")
		out = append(out, s)
	}
	return out, ctx.Err()
}

type ampThread struct {
	ID       string  `json:"id"`
	Created  float64 `json:"created"`
	Title    string  `json:"title"`
	Messages []struct {
		Role    string `json:"role"`
		Content []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Thinking  string          `json:"thinking"`
			Name      string          `json:"name"`
			ID        string          `json:"id"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"toolUseID"`
			Run       *struct {
				Status string          `json:"status"`
				Result json.RawMessage `json:"result"`
			} `json:"run"`
		} `json:"content"`
		Meta *struct {
			SentAt float64 `json:"sentAt"`
		} `json:"meta"`
		Usage *struct {
			Model      string `json:"model"`
			Input      int64  `json:"inputTokens"`
			Output     int64  `json:"outputTokens"`
			CacheWrite int64  `json:"cacheCreationInputTokens"`
			CacheRead  int64  `json:"cacheReadInputTokens"`
			Timestamp  string `json:"timestamp"`
		} `json:"usage"`
	} `json:"messages"`
	Env *struct {
		Initial struct {
			Trees []struct {
				URI string `json:"uri"`
			} `json:"trees"`
		} `json:"initial"`
	} `json:"env"`
}

func (ampReader) parse(ctx context.Context, e *env, src source, _ int64) (*parsed, error) {
	var t ampThread
	if err := readJSONFile(src.Path, 64<<20, &t); err != nil {
		return nil, err
	}
	p := &parsed{NativeID: src.NativeID, Resumable: true, Title: cleanTitle(t.Title)}
	if t.ID != "" {
		p.NativeID = t.ID
	}
	p.touch(unixAny(t.Created))
	p.touch(src.ModTime)
	if t.Env != nil && len(t.Env.Initial.Trees) > 0 {
		if u, err := url.Parse(t.Env.Initial.Trees[0].URI); err == nil && u.Scheme == "file" {
			p.Cwd = u.Path
		}
	}
	tools := map[string]string{}
	for i, m := range t.Messages {
		id := "m" + strconv.Itoa(i)
		var at time.Time
		if m.Meta != nil {
			at = unixAny(m.Meta.SentAt)
		}
		if m.Usage != nil {
			if ut := parseTime(m.Usage.Timestamp); !ut.IsZero() {
				at = ut
			}
			p.Model = m.Usage.Model
			p.Usage = append(p.Usage, usageRec{Key: id, At: at, Model: m.Usage.Model, Input: m.Usage.Input,
				Output: m.Usage.Output, CacheRead: m.Usage.CacheRead, CacheWrite: m.Usage.CacheWrite})
		}
		p.touch(at)
		var parts, results []api.Part
		for _, c := range m.Content {
			switch c.Type {
			case "text":
				if strings.TrimSpace(c.Text) == "" {
					continue
				}
				if m.Role == "user" {
					p.addUser(c.Text)
				}
				parts = append(parts, textPart("text", c.Text))
			case "thinking":
				if strings.TrimSpace(c.Thinking) != "" {
					parts = append(parts, textPart("thinking", c.Thinking))
				}
			case "tool_use":
				tools[c.ID] = c.Name
				parts = append(parts, toolCallPart(c.Name, rawString(c.Input)))
			case "tool_result":
				if c.Run != nil {
					results = append(results, toolResultPart(tools[c.ToolUseID], rawString(c.Run.Result), c.Run.Status == "error"))
				}
			}
		}
		if len(parts) > 0 && (m.Role == "user" || m.Role == "assistant") {
			p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: m.Role, At: at, Model: modelIf(m.Role, p.Model), Parts: parts})
		}
		if len(results) > 0 {
			p.Messages = append(p.Messages, api.AgentMessage{ID: id + ":r", Role: "tool", At: at, Parts: results})
		}
	}
	return p, ctx.Err()
}
