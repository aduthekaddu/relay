package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/aduthekaddu/relay/internal/api"
)

// Gemini CLI: ~/.gemini/tmp/<project>/chats/session-*.json (one JSON
// document) or session-*.jsonl (newer: one record per line). <project> is
// the SHA-256 of the project root or, in newer versions, a directory with
// a .project_root file naming it.
func newGemini() *Adapter {
	return &Adapter{
		ID: "gemini", Name: "Gemini CLI", Vendor: "Google", Color: "#4285F4",
		Binaries: []string{"gemini"}, InstallHint: "gemini-cli",
		Caps: api.AgentCapabilities{Resume: true, Headless: true, Hooks: true, History: true,
			Usage: true, Worktrees: true, Prompt: true},
		Interactive: func(bin, prompt, model string) []string {
			argv := withFlag([]string{bin}, "-m", model)
			if prompt != "" {
				argv = append(argv, "-i", safeArg(prompt))
			}
			return argv
		},
		Resume: func(bin, id string) []string { return []string{bin, "--resume", id} },
		Headless: func(bin, prompt, model string) []string {
			return withFlag([]string{bin, "-p", safeArg(prompt)}, "-m", model)
		},
		History: geminiReader{root: ".gemini"},
		Hooks: &jsonHooks{agent: "gemini", rel: []string{".gemini", "settings.json"},
			events: map[string]string{"Notification": "notification", "AfterAgent": "stop"},
			order:  []string{"Notification", "AfterAgent"}},
		ParseHook: parseClaudeLikeHook,
	}
}

type geminiReader struct{ root string }

func (r geminiReader) sources(ctx context.Context, e *env) ([]source, error) {
	var out []source
	for _, pat := range []string{"session-*.json", "session-*.jsonl"} {
		out = append(out, globSources(e.path(r.root, "tmp", "*", "chats", pat), false)...)
	}
	return out, ctx.Err()
}

type geminiDoc struct {
	SessionID   string          `json:"sessionId"`
	ProjectHash string          `json:"projectHash"`
	StartTime   string          `json:"startTime"`
	LastUpdated string          `json:"lastUpdated"`
	Summary     string          `json:"summary"`
	Messages    []geminiMessage `json:"messages"`
}

type geminiMessage struct {
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Content   json.RawMessage `json:"content"`
	Model     string          `json:"model"`
	Thoughts  []struct {
		Subject     string `json:"subject"`
		Description string `json:"description"`
	} `json:"thoughts"`
	Tokens *struct {
		Input    int64 `json:"input"`
		Output   int64 `json:"output"`
		Cached   int64 `json:"cached"`
		Thoughts int64 `json:"thoughts"`
		Tool     int64 `json:"tool"`
	} `json:"tokens"`
	ToolCalls []struct {
		Name   string          `json:"name"`
		Args   json.RawMessage `json:"args"`
		Result json.RawMessage `json:"result"`
		Status string          `json:"status"`
	} `json:"toolCalls"`
}

func (r geminiReader) parse(ctx context.Context, e *env, src source, _ int64) (*parsed, error) {
	b, err := os.ReadFile(src.Path)
	if err != nil {
		return nil, err
	}
	var doc geminiDoc
	if json.Unmarshal(b, &doc) != nil || (doc.SessionID == "" && len(doc.Messages) == 0) {
		doc = geminiDoc{}
		for _, line := range bytes.Split(b, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var m geminiMessage
			if json.Unmarshal(line, &m) == nil && m.Type != "" {
				doc.Messages = append(doc.Messages, m)
				continue
			}
			var hdr geminiDoc
			if json.Unmarshal(line, &hdr) == nil && hdr.SessionID != "" {
				doc.SessionID, doc.ProjectHash, doc.StartTime = hdr.SessionID, hdr.ProjectHash, hdr.StartTime
				if hdr.Summary != "" {
					doc.Summary = hdr.Summary
				}
			}
		}
	}
	p := &parsed{NativeID: doc.SessionID, Resumable: true, Summary: cleanTitle(doc.Summary)}
	if p.NativeID == "" {
		p.NativeID = strings.TrimSuffix(strings.TrimPrefix(filepath.Base(src.Path), "session-"), filepath.Ext(src.Path))
	}
	p.touch(parseTime(doc.StartTime))
	p.touch(parseTime(doc.LastUpdated))
	p.Cwd = geminiProjectRoot(ctx, e, filepath.Dir(filepath.Dir(src.Path)), doc.ProjectHash)
	for i, m := range doc.Messages {
		geminiAdd(p, i, &m)
	}
	return p, ctx.Err()
}

func geminiAdd(p *parsed, i int, m *geminiMessage) {
	at := parseTime(m.Timestamp)
	p.touch(at)
	id := m.ID
	if id == "" {
		id = "m" + itoa(i)
	}
	text := geminiText(m.Content)
	switch m.Type {
	case "user":
		if strings.TrimSpace(text) == "" || strings.HasPrefix(strings.TrimSpace(text), "/") && len(text) < 40 {
			return
		}
		p.addUser(text)
		p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "user", At: at, Parts: []api.Part{textPart("text", text)}})
	case "gemini", "model", "assistant":
		if m.Model != "" {
			p.Model = m.Model
		}
		var parts []api.Part
		for _, t := range m.Thoughts {
			if s := strings.TrimSpace(t.Subject + "\n" + t.Description); s != "" {
				parts = append(parts, textPart("thinking", s))
			}
		}
		if strings.TrimSpace(text) != "" {
			parts = append(parts, textPart("text", text))
		}
		var results []api.Part
		for _, tc := range m.ToolCalls {
			parts = append(parts, toolCallPart(tc.Name, rawString(tc.Args)))
			results = append(results, toolResultPart(tc.Name, geminiText(tc.Result), tc.Status == "error"))
		}
		if len(parts) > 0 {
			p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "assistant", At: at, Model: m.Model, Parts: parts})
		}
		if len(results) > 0 {
			p.Messages = append(p.Messages, api.AgentMessage{ID: id + ":r", Role: "tool", At: at, Parts: results})
		}
		if t := m.Tokens; t != nil {
			// Gemini counts cached tokens inside input; thoughts are billed as output.
			p.Usage = append(p.Usage, usageRec{Key: id, At: at, Model: m.Model, Input: max(0, t.Input-t.Cached),
				Output: t.Output + t.Thoughts, CacheRead: t.Cached, Reasoning: t.Thoughts})
		}
	}
}

// geminiText flattens content that is a string, a list of {text} parts or
// function responses.
func geminiText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text             string          `json:"text"`
		FunctionResponse json.RawMessage `json:"functionResponse"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return rawString(raw)
	}
	var b strings.Builder
	for _, pt := range parts {
		switch {
		case pt.Text != "":
			b.WriteString(pt.Text)
		case len(pt.FunctionResponse) > 0:
			b.WriteString(rawString(pt.FunctionResponse))
		}
	}
	return b.String()
}

// geminiProjectRoot resolves the project directory of a Gemini tmp dir.
func geminiProjectRoot(ctx context.Context, e *env, dir, hash string) string {
	if b, err := os.ReadFile(filepath.Join(dir, ".project_root")); err == nil {
		if p := strings.TrimSpace(string(b)); filepath.IsAbs(p) {
			return p
		}
	}
	if hash == "" {
		hash = filepath.Base(dir)
	}
	candidates := []string{e.home}
	if e.workspaces != nil {
		candidates = append(candidates, e.workspaces(ctx)...)
	}
	for _, c := range candidates {
		sum := sha256.Sum256([]byte(c))
		if hex.EncodeToString(sum[:]) == hash {
			return c
		}
	}
	return ""
}

// Qwen Code (Gemini CLI fork): ~/.qwen/projects/<sanitised cwd>/chats/<session>.jsonl,
// append-only records {uuid, sessionId, timestamp, type, cwd, gitBranch,
// model, message:{role, parts}, usageMetadata}.
func newQwen() *Adapter {
	return &Adapter{
		ID: "qwen", Name: "Qwen Code", Vendor: "Alibaba", Color: "#615CED",
		Binaries: []string{"qwen"}, InstallHint: "qwen-code",
		Caps: api.AgentCapabilities{Resume: true, Headless: true, History: true, Usage: true,
			Worktrees: true, Prompt: true},
		Interactive: func(bin, prompt, model string) []string {
			argv := withFlag([]string{bin}, "-m", model)
			if prompt != "" {
				argv = append(argv, "-i", safeArg(prompt))
			}
			return argv
		},
		Resume: func(bin, id string) []string { return []string{bin, "--resume", id} },
		Headless: func(bin, prompt, model string) []string {
			return withFlag([]string{bin, "-p", safeArg(prompt)}, "-m", model)
		},
		History: qwenReader{},
	}
}

type qwenReader struct{}

func (qwenReader) sources(ctx context.Context, e *env) ([]source, error) {
	var out []source
	for _, s := range globSources(e.path(".qwen", "projects", "*", "chats", "*.jsonl"), true) {
		s.NativeID = strings.TrimSuffix(filepath.Base(s.Path), ".jsonl")
		out = append(out, s)
	}
	return out, ctx.Err()
}

type qwenRecord struct {
	UUID      string `json:"uuid"`
	SessionID string `json:"sessionId"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Cwd       string `json:"cwd"`
	GitBranch string `json:"gitBranch"`
	Model     string `json:"model"`
	Message   *struct {
		Role  string `json:"role"`
		Parts []struct {
			Text         string `json:"text"`
			Thought      bool   `json:"thought"`
			FunctionCall *struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"functionCall"`
			FunctionResponse *struct {
				Name     string          `json:"name"`
				Response json.RawMessage `json:"response"`
			} `json:"functionResponse"`
		} `json:"parts"`
	} `json:"message"`
	Usage *struct {
		Prompt   int64 `json:"promptTokenCount"`
		Output   int64 `json:"candidatesTokenCount"`
		Cached   int64 `json:"cachedContentTokenCount"`
		Thoughts int64 `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
}

func (qwenReader) parse(ctx context.Context, e *env, src source, offset int64) (*parsed, error) {
	p := &parsed{NativeID: src.NativeID, Resumable: true}
	consumed, err := scanJSONL(ctx, src.Path, offset, e.chunk, func(line []byte, off int64) error {
		var r qwenRecord
		if json.Unmarshal(line, &r) != nil {
			return nil
		}
		at := parseTime(r.Timestamp)
		p.touch(at)
		if r.Cwd != "" {
			p.Cwd = r.Cwd
		}
		if r.GitBranch != "" {
			p.GitBranch = r.GitBranch
		}
		if r.Model != "" {
			p.Model = r.Model
		}
		id := r.UUID
		if id == "" {
			id = lineID(off)
		}
		if u := r.Usage; u != nil {
			p.Usage = append(p.Usage, usageRec{Key: id, At: at, Model: r.Model, Input: max(0, u.Prompt-u.Cached),
				Output: u.Output + u.Thoughts, CacheRead: u.Cached, Reasoning: u.Thoughts})
		}
		if r.Message == nil {
			return nil
		}
		var parts []api.Part
		role := map[string]string{"user": "user", "assistant": "assistant", "tool_result": "tool"}[r.Type]
		for _, pt := range r.Message.Parts {
			switch {
			case pt.FunctionCall != nil:
				parts = append(parts, toolCallPart(pt.FunctionCall.Name, rawString(pt.FunctionCall.Args)))
			case pt.FunctionResponse != nil:
				role = "tool"
				parts = append(parts, toolResultPart(pt.FunctionResponse.Name, rawString(pt.FunctionResponse.Response), false))
			case pt.Thought && strings.TrimSpace(pt.Text) != "":
				parts = append(parts, textPart("thinking", pt.Text))
			case strings.TrimSpace(pt.Text) != "":
				if role == "user" {
					if isInjectedPrompt(pt.Text) {
						continue
					}
					p.addUser(pt.Text)
				}
				parts = append(parts, textPart("text", pt.Text))
			}
		}
		if role != "" && len(parts) > 0 {
			p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: role, At: at, Model: modelIf(role, r.Model), Parts: parts})
		}
		return nil
	})
	p.Consumed = consumed
	return p, err
}
