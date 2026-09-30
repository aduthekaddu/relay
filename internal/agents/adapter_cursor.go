package agents

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aduthekaddu/relay/internal/api"
)

// Cursor Agent CLI: ~/.cursor/chats/<workspace hash>/<chat id>/store.db
// (SQLite). The meta table holds hex-encoded JSON ({agentId, name,
// createdAt, lastUsedModel, ...}); the blobs table is content-addressed and
// mixes JSON message blobs ({role, content}) with binary tree nodes. Only
// JSON message blobs are read. Transcripts carry no token usage.
func newCursor() *Adapter {
	return &Adapter{
		ID: "cursor", Name: "Cursor Agent", Vendor: "Anysphere", Color: "#3B82F6",
		Binaries: []string{"cursor-agent", "agent"}, ExtraDirs: []string{".cursor/bin"}, InstallHint: "cursor-agent",
		Caps: api.AgentCapabilities{Resume: true, Headless: true, Hooks: true, History: true,
			Worktrees: true, Prompt: true},
		Interactive: func(bin, prompt, model string) []string {
			return withPrompt(withFlag([]string{bin}, "--model", model), prompt)
		},
		Resume: func(bin, id string) []string { return []string{bin, "--resume", id} },
		Headless: func(bin, prompt, model string) []string {
			return withPrompt(withFlag([]string{bin, "-p", "--output-format", "text"}, "--model", model), prompt)
		},
		History: cursorReader{},
		Hooks: &jsonHooks{agent: "cursor", rel: []string{".cursor", "hooks.json"}, flat: true,
			events: map[string]string{"stop": "stop"}, order: []string{"stop"}},
		ParseHook: func(event string, payload []byte) hookEvent {
			m := decodePayload(payload)
			ev := hookEvent{NativeID: payloadString(m, "conversation_id", "chatId"), Cwd: firstString(m, "workspace_roots")}
			if event == "stop" || payloadString(m, "hook_event_name") == "stop" {
				ev.Action, ev.Message = hookDone, "Finished its turn"
			}
			return ev
		},
	}
}

// firstString returns the first element of a string array field.
func firstString(m map[string]json.RawMessage, key string) string {
	var arr []string
	if json.Unmarshal(m[key], &arr) == nil && len(arr) > 0 {
		return arr[0]
	}
	return ""
}

type cursorReader struct{}

func (cursorReader) sources(ctx context.Context, e *env) ([]source, error) {
	var out []source
	for _, s := range globSources(e.path(".cursor", "chats", "*", "*", "store.db"), false) {
		s.NativeID = filepath.Base(filepath.Dir(s.Path))
		if wal, ok := fileStamp(s.Path + "-wal"); ok {
			s.Version = wal
		}
		out = append(out, s)
	}
	return out, ctx.Err()
}

type cursorMeta struct {
	Name      string `json:"name"`
	CreatedAt any    `json:"createdAt"`
	Model     string `json:"lastUsedModel"`
}

func (cursorReader) parse(ctx context.Context, e *env, src source, _ int64) (*parsed, error) {
	db, err := e.dbs.get(ctx, e.tmp, src.Path)
	if err != nil {
		return nil, err
	}
	p := &parsed{NativeID: src.NativeID, Resumable: true}
	p.touch(src.ModTime)
	var metaHex string
	if db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='0'`).Scan(&metaHex) == nil {
		if raw, err := hex.DecodeString(metaHex); err == nil {
			var m cursorMeta
			if json.Unmarshal(raw, &m) == nil {
				if m.Name != "" && m.Name != "New Agent" {
					p.Title = cleanTitle(m.Name)
				}
				p.Model = m.Model
				p.touch(parseTime(m.CreatedAt))
			}
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT id, data FROM blobs ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("cursor blobs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var data []byte
		if rows.Scan(&id, &data) != nil || len(data) == 0 || data[0] != '{' {
			continue
		}
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		cursorBlob(p, id, m.Role, m.Content)
	}
	return p, rows.Err()
}

func cursorBlob(p *parsed, id, role string, content json.RawMessage) {
	var blocks []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		ToolName string          `json:"toolName"`
		Args     json.RawMessage `json:"args"`
		Result   json.RawMessage `json:"result"`
	}
	var text string
	if json.Unmarshal(content, &text) != nil {
		if json.Unmarshal(content, &blocks) != nil {
			return
		}
	}
	var parts []api.Part
	if text != "" {
		parts = append(parts, textPart("text", text))
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				parts = append(parts, textPart("text", b.Text))
			}
		case "reasoning":
			if strings.TrimSpace(b.Text) != "" {
				parts = append(parts, textPart("thinking", b.Text))
			}
		case "tool-call":
			parts = append(parts, toolCallPart(b.ToolName, rawString(b.Args)))
		case "tool-result":
			parts = append(parts, toolResultPart(b.ToolName, rawString(b.Result), false))
		}
	}
	if len(parts) == 0 {
		return
	}
	switch role {
	case "user":
		t := parts[0].Text
		if isInjectedPrompt(t) || strings.HasPrefix(strings.TrimSpace(t), "<user_info>") {
			return
		}
		p.addUser(t)
	case "assistant", "tool":
	default:
		return
	}
	p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: role, Parts: parts})
}
