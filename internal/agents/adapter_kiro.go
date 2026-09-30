package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Kiro CLI. Current versions keep ~/.kiro/sessions/cli/<uuid>.json
// (metadata: title, cwd, per-turn token/credit metering) next to
// <uuid>.jsonl (append-only Prompt / AssistantMessage / ToolResults
// records). Older versions stored conversations in the conversations_v2
// table of ~/.local/share/kiro-cli/data.sqlite3.
func newKiro() *Adapter {
	return &Adapter{
		ID: "kiro", Name: "Kiro CLI", Vendor: "AWS", Color: "#7B61FF",
		Binaries: []string{"kiro-cli", "kiro"}, InstallHint: "kiro-cli",
		Caps: api.AgentCapabilities{Resume: true, Headless: true, History: true, Usage: true,
			Worktrees: true, Prompt: true},
		Interactive: func(bin, prompt, model string) []string {
			return withPrompt(withFlag([]string{bin, "chat"}, "--model", model), prompt)
		},
		Resume: func(bin, id string) []string { return []string{bin, "chat", "--resume-id", id} },
		Headless: func(bin, prompt, model string) []string {
			return withPrompt(withFlag([]string{bin, "chat", "--no-interactive", "--wrap", "never"}, "--model", model), prompt)
		},
		History: kiroReader{},
	}
}

type kiroReader struct{}

func kiroV1DB(e *env) string { return e.path(".local", "share", "kiro-cli", "data.sqlite3") }

func (kiroReader) sources(ctx context.Context, e *env) ([]source, error) {
	var out []source
	for _, s := range globSources(e.path(".kiro", "sessions", "cli", "*.jsonl"), true) {
		s.NativeID = strings.TrimSuffix(filepath.Base(s.Path), ".jsonl")
		// The sibling .json is rewritten as turns finish (title, usage):
		// its stamp makes the indexer revisit the source.
		if st, ok := fileStamp(strings.TrimSuffix(s.Path, "l")); ok {
			s.Version = st
		}
		out = append(out, s)
	}
	v1, err := kiroV1Sources(ctx, e)
	if err != nil {
		return out, err
	}
	return append(out, v1...), ctx.Err()
}

type kiroMeta struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Title     string `json:"title"`
	State     struct {
		Conversation struct {
			Turns []struct {
				Model      string   `json:"model"`
				End        string   `json:"end_timestamp"`
				Input      int64    `json:"input_token_count"`
				Output     int64    `json:"output_token_count"`
				CacheRead  int64    `json:"cache_read_input_token_count"`
				CacheWrite int64    `json:"cache_write_input_token_count"`
				MessageIDs []string `json:"message_ids"`
				Metering   []struct {
					Value float64 `json:"value"`
					Unit  string  `json:"unit"`
				} `json:"metering_usage"`
			} `json:"user_turn_metadatas"`
		} `json:"conversation_metadata"`
		Model struct {
			Info *struct {
				ModelID string `json:"model_id"`
			} `json:"model_info"`
		} `json:"rts_model_state"`
	} `json:"session_state"`
}

type kiroRecord struct {
	Kind string `json:"kind"`
	Data struct {
		MessageID string            `json:"message_id"`
		Content   []json.RawMessage `json:"content"`
		Meta      *struct {
			Timestamp any `json:"timestamp"`
		} `json:"meta"`
	} `json:"data"`
}

type kiroBlock struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

func (r kiroReader) parse(ctx context.Context, e *env, src source, offset int64) (*parsed, error) {
	if strings.Contains(src.Key, "#") {
		return kiroV1Parse(ctx, e, src)
	}
	p := &parsed{NativeID: src.NativeID, Resumable: true}
	kiroApplyMeta(p, strings.TrimSuffix(src.Path, "l"))
	var at time.Time
	tools := map[string]string{}
	consumed, err := scanJSONL(ctx, src.Path, offset, e.chunk, func(line []byte, off int64) error {
		var rec kiroRecord
		if json.Unmarshal(line, &rec) != nil {
			return nil
		}
		if rec.Data.Meta != nil {
			if t := parseTime(rec.Data.Meta.Timestamp); !t.IsZero() {
				at = t
			}
		}
		id := rec.Data.MessageID
		if id == "" {
			id = lineID(off)
		}
		switch rec.Kind {
		case "Prompt":
			text := kiroText(rec.Data.Content)
			if strings.TrimSpace(text) == "" || isInjectedPrompt(text) {
				return nil
			}
			p.addUser(text)
			p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "user", At: at, Parts: []api.Part{textPart("text", text)}})
		case "AssistantMessage":
			var parts []api.Part
			for _, raw := range rec.Data.Content {
				var b kiroBlock
				if json.Unmarshal(raw, &b) != nil {
					continue
				}
				switch b.Kind {
				case "text":
					var s string
					if json.Unmarshal(b.Data, &s) == nil && strings.TrimSpace(s) != "" {
						parts = append(parts, textPart("text", s))
					}
				case "thinking":
					var t struct {
						Text string `json:"text"`
					}
					if json.Unmarshal(b.Data, &t) == nil && strings.TrimSpace(t.Text) != "" {
						parts = append(parts, textPart("thinking", t.Text))
					}
				case "toolUse":
					var t struct {
						ID    string          `json:"toolUseId"`
						Name  string          `json:"name"`
						Input json.RawMessage `json:"input"`
					}
					if json.Unmarshal(b.Data, &t) == nil {
						tools[t.ID] = t.Name
						parts = append(parts, toolCallPart(t.Name, rawString(t.Input)))
					}
				}
			}
			if len(parts) > 0 {
				p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "assistant", At: at, Model: p.Model, Parts: parts})
			}
		case "ToolResults":
			var parts []api.Part
			for _, raw := range rec.Data.Content {
				var b kiroBlock
				if json.Unmarshal(raw, &b) != nil || b.Kind != "toolResult" {
					continue
				}
				var t struct {
					ID      string            `json:"toolUseId"`
					Status  string            `json:"status"`
					Content []json.RawMessage `json:"content"`
				}
				if json.Unmarshal(b.Data, &t) == nil {
					parts = append(parts, toolResultPart(tools[t.ID], kiroText(t.Content), strings.EqualFold(t.Status, "error")))
				}
			}
			if len(parts) > 0 {
				p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: "tool", At: at, Parts: parts})
			}
		}
		return nil
	})
	p.Consumed = consumed
	return p, err
}

// kiroApplyMeta reads the session's .json sidecar: title, cwd, times and
// per-turn metering (tokens + credits).
func kiroApplyMeta(p *parsed, path string) {
	var m kiroMeta
	if err := readJSONFile(path, 32<<20, &m); err != nil {
		return
	}
	if m.SessionID != "" {
		p.NativeID = m.SessionID
	}
	p.Cwd = m.Cwd
	if t := cleanTitle(m.Title); t != "" {
		p.Title = t
	}
	p.touch(parseTime(m.CreatedAt))
	p.touch(parseTime(m.UpdatedAt))
	if info := m.State.Model.Info; info != nil && info.ModelID != "" {
		p.Model = info.ModelID
	}
	for i, t := range m.State.Conversation.Turns {
		key := "turn:" + strconv.Itoa(i)
		if len(t.MessageIDs) > 0 {
			key = t.MessageIDs[0]
		}
		in := t.Input
		// Kiro's input count includes cache traffic when it is large enough to.
		if in >= t.CacheRead+t.CacheWrite {
			in -= t.CacheRead + t.CacheWrite
		}
		var credits float64
		for _, mu := range t.Metering {
			credits += mu.Value
		}
		model := t.Model
		if model == "" {
			model = p.Model
		} else {
			p.Model = model
		}
		if in+t.Output+t.CacheRead+t.CacheWrite == 0 && credits == 0 {
			continue
		}
		p.Usage = append(p.Usage, usageRec{Key: key, At: parseTime(t.End), Model: model, Input: in, Output: t.Output,
			CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, Credits: credits})
	}
}

// kiroText joins text/json blocks ({"kind":"text","data":"…"}).
func kiroText(blocks []json.RawMessage) string {
	var b strings.Builder
	for _, raw := range blocks {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			b.WriteString(s)
			continue
		}
		var k kiroBlock
		if json.Unmarshal(raw, &k) != nil {
			continue
		}
		switch k.Kind {
		case "text":
			var t string
			if json.Unmarshal(k.Data, &t) == nil {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(t)
			}
		case "json":
			b.WriteString(rawString(k.Data))
		case "image":
			b.WriteString("[image]")
		}
		if b.Len() > maxPartText {
			break
		}
	}
	return b.String()
}

// --- legacy conversations_v2 (kiro-cli ≤ 1.x) ---

func kiroV1Sources(ctx context.Context, e *env) ([]source, error) {
	path := kiroV1DB(e)
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	db, err := e.dbs.get(ctx, e.tmp, path)
	if err != nil {
		return nil, err
	}
	if !tableColumns(ctx, db, "conversations_v2")["conversation_id"] {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT conversation_id, updated_at FROM conversations_v2`)
	if err != nil {
		return nil, fmt.Errorf("kiro conversations: %w", err)
	}
	defer rows.Close()
	var out []source
	for rows.Next() {
		var id string
		var upd int64
		if rows.Scan(&id, &upd) == nil {
			out = append(out, source{Key: path + "#" + id, Path: path, NativeID: id,
				ModTime: unixAny(float64(upd)), Version: strconv.FormatInt(upd, 10)})
		}
	}
	return out, rows.Err()
}

type kiroV1Conv struct {
	History []struct {
		User *struct {
			Content struct {
				Prompt *struct {
					Prompt string `json:"prompt"`
				} `json:"Prompt"`
				ToolUseResults *struct {
					Results []struct {
						ID      string            `json:"tool_use_id"`
						Content []json.RawMessage `json:"content"`
						Status  string            `json:"status"`
					} `json:"tool_use_results"`
				} `json:"ToolUseResults"`
			} `json:"content"`
			Timestamp string `json:"timestamp"`
		} `json:"user"`
		Assistant *struct {
			Response *struct {
				ID      string `json:"message_id"`
				Content string `json:"content"`
			} `json:"Response"`
			ToolUse *struct {
				ID       string `json:"message_id"`
				Content  string `json:"content"`
				ToolUses []struct {
					ID   string          `json:"id"`
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				} `json:"tool_uses"`
			} `json:"ToolUse"`
		} `json:"assistant"`
		Meta *struct {
			RequestID  string `json:"request_id"`
			Model      string `json:"model_id"`
			Uncached   int64  `json:"uncached_input_tokens"`
			CacheRead  int64  `json:"cache_read_input_tokens"`
			CacheWrite int64  `json:"cache_write_input_tokens"`
			Output     int64  `json:"output_tokens"`
			EndMs      int64  `json:"stream_end_timestamp_ms"`
		} `json:"request_metadata"`
	} `json:"history"`
}

func kiroV1Parse(ctx context.Context, e *env, src source) (*parsed, error) {
	db, err := e.dbs.get(ctx, e.tmp, src.Path)
	if err != nil {
		return nil, err
	}
	var cwd, value string
	var created, updated int64
	if err := db.QueryRowContext(ctx, `SELECT key, value, created_at, updated_at FROM conversations_v2 WHERE conversation_id=?`, src.NativeID).
		Scan(&cwd, &value, &created, &updated); err != nil {
		return nil, fmt.Errorf("kiro conversation: %w", err)
	}
	p := &parsed{NativeID: src.NativeID, Cwd: cwd, Resumable: false}
	p.touch(unixAny(float64(created)))
	p.touch(unixAny(float64(updated)))
	var c kiroV1Conv
	if err := json.Unmarshal([]byte(value), &c); err != nil {
		return nil, fmt.Errorf("kiro conversation json: %w", err)
	}
	tools := map[string]string{}
	for i, h := range c.History {
		at := time.Time{}
		idx := strconv.Itoa(i)
		if u := h.User; u != nil {
			at = parseTime(u.Timestamp)
			if pr := u.Content.Prompt; pr != nil && strings.TrimSpace(pr.Prompt) != "" {
				p.addUser(pr.Prompt)
				p.Messages = append(p.Messages, api.AgentMessage{ID: "u" + idx, Role: "user", At: at, Parts: []api.Part{textPart("text", pr.Prompt)}})
			}
			if tr := u.Content.ToolUseResults; tr != nil {
				var parts []api.Part
				for _, r := range tr.Results {
					parts = append(parts, toolResultPart(tools[r.ID], kiroText(r.Content), strings.EqualFold(r.Status, "error")))
				}
				if len(parts) > 0 {
					p.Messages = append(p.Messages, api.AgentMessage{ID: "t" + idx, Role: "tool", At: at, Parts: parts})
				}
			}
		}
		if h.Meta != nil {
			if h.Meta.EndMs > 0 {
				at = unixAny(float64(h.Meta.EndMs))
			}
			if h.Meta.Model != "" {
				p.Model = h.Meta.Model
			}
			key := h.Meta.RequestID
			if key == "" {
				key = "h" + idx
			}
			p.Usage = append(p.Usage, usageRec{Key: key, At: at, Model: h.Meta.Model, Input: h.Meta.Uncached,
				Output: h.Meta.Output, CacheRead: h.Meta.CacheRead, CacheWrite: h.Meta.CacheWrite})
		}
		if a := h.Assistant; a != nil {
			var parts []api.Part
			if a.Response != nil && strings.TrimSpace(a.Response.Content) != "" {
				parts = append(parts, textPart("text", a.Response.Content))
			}
			if a.ToolUse != nil {
				if strings.TrimSpace(a.ToolUse.Content) != "" {
					parts = append(parts, textPart("text", a.ToolUse.Content))
				}
				for _, tu := range a.ToolUse.ToolUses {
					tools[tu.ID] = tu.Name
					parts = append(parts, toolCallPart(tu.Name, rawString(tu.Args)))
				}
			}
			if len(parts) > 0 {
				p.Messages = append(p.Messages, api.AgentMessage{ID: "a" + idx, Role: "assistant", At: at, Model: p.Model, Parts: parts})
			}
		}
	}
	return p, nil
}
