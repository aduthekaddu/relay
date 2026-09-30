package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// OpenCode: ~/.local/share/opencode/opencode.db (SQLite): session, message
// (JSON data per message) and part (JSON data per content part) tables.
func newOpenCode() *Adapter {
	return &Adapter{
		ID: "opencode", Name: "OpenCode", Vendor: "SST", Color: "#F5A623",
		Binaries: []string{"opencode"}, ExtraDirs: []string{".opencode/bin"}, InstallHint: "opencode",
		Caps: api.AgentCapabilities{Resume: true, Fork: true, Headless: true, Hooks: true,
			History: true, Usage: true, Worktrees: true, Prompt: true},
		Interactive: func(bin, prompt, model string) []string {
			argv := withFlag([]string{bin}, "--model", model)
			if prompt != "" {
				argv = append(argv, "--prompt", safeArg(prompt))
			}
			return argv
		},
		Resume: func(bin, id string) []string { return []string{bin, "--session", id} },
		Fork:   func(bin, id string) []string { return []string{bin, "--session", id, "--fork"} },
		Headless: func(bin, prompt, model string) []string {
			return withPrompt(withFlag([]string{bin, "run"}, "--model", model), prompt)
		},
		History:   opencodeReader{},
		Hooks:     opencodePlugin{},
		ParseHook: parseOpenCodeHook,
	}
}

func parseOpenCodeHook(event string, payload []byte) hookEvent {
	m := decodePayload(payload)
	ev := hookEvent{}
	if props, ok := m["properties"]; ok {
		pm := decodePayload(props)
		ev.NativeID = payloadString(pm, "sessionID", "sessionId")
		if info, ok := pm["info"]; ok && ev.NativeID == "" {
			ev.NativeID = payloadString(decodePayload(info), "sessionID", "id")
		}
	}
	switch event {
	case "idle":
		ev.Action, ev.Message = hookDone, "Finished its turn"
	case "permission":
		ev.Action, ev.Message = hookAttention, "Needs your permission"
	}
	return ev
}

func opencodeDB(e *env) string { return e.path(".local", "share", "opencode", "opencode.db") }

type opencodeReader struct{}

func (opencodeReader) sources(ctx context.Context, e *env) ([]source, error) {
	path := opencodeDB(e)
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	db, err := e.dbs.get(ctx, e.tmp, path)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, time_updated FROM session`)
	if err != nil {
		return nil, fmt.Errorf("opencode sessions: %w", err)
	}
	defer rows.Close()
	var out []source
	for rows.Next() {
		var id string
		var updated int64
		if rows.Scan(&id, &updated) != nil {
			continue
		}
		out = append(out, source{Key: path + "#" + id, Path: path, NativeID: id,
			ModTime: unixAny(float64(updated)), Version: strconv.FormatInt(updated, 10)})
	}
	return out, rows.Err()
}

type ocMessage struct {
	Role    string  `json:"role"`
	ModelID string  `json:"modelID"`
	Cost    float64 `json:"cost"`
	Tokens  *struct {
		Input     int64 `json:"input"`
		Output    int64 `json:"output"`
		Reasoning int64 `json:"reasoning"`
		Cache     struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
	Path *struct {
		Cwd string `json:"cwd"`
	} `json:"path"`
}

type ocPart struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Synthetic bool   `json:"synthetic"`
	Tool      string `json:"tool"`
	Mime      string `json:"mime"`
	Filename  string `json:"filename"`
	State     *struct {
		Status string          `json:"status"`
		Input  json.RawMessage `json:"input"`
		Output string          `json:"output"`
		Error  string          `json:"error"`
	} `json:"state"`
	Files []string `json:"files"`
}

func (opencodeReader) parse(ctx context.Context, e *env, src source, _ int64) (*parsed, error) {
	db, err := e.dbs.get(ctx, e.tmp, src.Path)
	if err != nil {
		return nil, err
	}
	p := &parsed{NativeID: src.NativeID, Resumable: true}
	var dir, title string
	var parent sql.NullString
	var created, updated int64
	err = db.QueryRowContext(ctx, `SELECT directory, title, parent_id, time_created, time_updated FROM session WHERE id=?`, src.NativeID).
		Scan(&dir, &title, &parent, &created, &updated)
	if err != nil {
		return nil, fmt.Errorf("opencode session: %w", err)
	}
	p.Cwd = dir
	if !strings.HasPrefix(title, "New session - ") && !strings.HasPrefix(title, "Child session - ") {
		p.Title = cleanTitle(title)
	}
	p.Hidden = parent.Valid && parent.String != ""
	p.touch(unixAny(float64(created)))
	p.touch(unixAny(float64(updated)))

	parts, err := opencodeParts(ctx, db, src.NativeID)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, time_created, data FROM message WHERE session_id=? ORDER BY time_created, id`, src.NativeID)
	if err != nil {
		return nil, fmt.Errorf("opencode messages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, data string
		var ts int64
		if rows.Scan(&id, &ts, &data) != nil {
			continue
		}
		var m ocMessage
		if json.Unmarshal([]byte(data), &m) != nil {
			continue
		}
		at := unixAny(float64(ts))
		p.touch(at)
		if m.Role == "assistant" {
			if m.ModelID != "" {
				p.Model = m.ModelID
			}
			if m.Tokens != nil {
				p.Usage = append(p.Usage, usageRec{Key: id, At: at, Model: m.ModelID,
					Input: m.Tokens.Input, Output: m.Tokens.Output + m.Tokens.Reasoning, Reasoning: m.Tokens.Reasoning,
					CacheRead: m.Tokens.Cache.Read, CacheWrite: m.Tokens.Cache.Write, NativeCost: m.Cost})
			}
		}
		msg, results := opencodeMessage(p, id, m, at, parts[id])
		if len(msg.Parts) > 0 {
			p.Messages = append(p.Messages, msg)
		}
		if len(results.Parts) > 0 {
			p.Messages = append(p.Messages, results)
		}
	}
	return p, rows.Err()
}

func opencodeParts(ctx context.Context, db *sql.DB, session string) (map[string][]ocPart, error) {
	rows, err := db.QueryContext(ctx, `SELECT message_id, data FROM part WHERE session_id=? ORDER BY time_created, id`, session)
	if err != nil {
		return nil, fmt.Errorf("opencode parts: %w", err)
	}
	defer rows.Close()
	out := map[string][]ocPart{}
	for rows.Next() {
		var mid, data string
		if rows.Scan(&mid, &data) != nil {
			continue
		}
		var pt ocPart
		if json.Unmarshal([]byte(data), &pt) == nil {
			out[mid] = append(out[mid], pt)
		}
	}
	return out, rows.Err()
}

func opencodeMessage(p *parsed, id string, m ocMessage, at time.Time, parts []ocPart) (api.AgentMessage, api.AgentMessage) {
	msg := api.AgentMessage{ID: id, Role: m.Role, At: at, Model: modelIf(m.Role, m.ModelID)}
	res := api.AgentMessage{ID: id + ":r", Role: "tool", At: at}
	for _, pt := range parts {
		switch pt.Type {
		case "text":
			if pt.Synthetic || strings.TrimSpace(pt.Text) == "" {
				continue
			}
			if m.Role == "user" {
				p.addUser(pt.Text)
			}
			msg.Parts = append(msg.Parts, textPart("text", pt.Text))
		case "reasoning":
			if strings.TrimSpace(pt.Text) != "" {
				msg.Parts = append(msg.Parts, textPart("thinking", pt.Text))
			}
		case "file":
			if strings.HasPrefix(pt.Mime, "image/") {
				msg.Parts = append(msg.Parts, api.Part{Type: "image", MimeType: pt.Mime, Path: pt.Filename})
			}
		case "tool":
			if pt.State == nil {
				continue
			}
			msg.Parts = append(msg.Parts, toolCallPart(pt.Tool, rawString(pt.State.Input)))
			switch pt.State.Status {
			case "completed":
				res.Parts = append(res.Parts, toolResultPart(pt.Tool, pt.State.Output, false))
			case "error":
				res.Parts = append(res.Parts, toolResultPart(pt.Tool, pt.State.Error, true))
			}
		case "patch":
			if len(pt.Files) > 0 {
				msg.Parts = append(msg.Parts, api.Part{Type: "diff", Path: pt.Files[0], Text: strings.Join(pt.Files, "\n")})
			}
		}
	}
	return msg, res
}

// opencodePlugin installs a tiny OpenCode plugin that calls `relay hook`
// when a session goes idle or asks for permission. It is a file Relay
// owns outright, so install/remove create or delete it.
type opencodePlugin struct{}

const opencodePluginMarker = "// Installed by Relay (relay hook opencode)."

func (opencodePlugin) path(e *env) string {
	return e.path(".config", "opencode", "plugins", "relay-attention.js")
}

func (h opencodePlugin) status(e *env) api.HookStatus {
	st := api.HookStatus{Path: h.path(e)}
	b, err := os.ReadFile(h.path(e))
	if err == nil && strings.Contains(string(b), opencodePluginMarker) {
		st.Installed = true
		st.Detail = "Relay plugin: session.idle, permission"
	}
	return st
}

func (h opencodePlugin) install(e *env, relay string) (api.HookStatus, error) {
	relayJS, _ := json.Marshal(relay)
	src := opencodePluginMarker + `
// Tells Relay when OpenCode finishes a turn or needs permission.
export const RelayAttention = async ({ $ }) => {
  const relay = ` + string(relayJS) + `
  const send = async (event, payload) => {
    try {
      await $` + "`${relay} hook opencode ${event} < ${new Response(JSON.stringify(payload))}`" + `.quiet().nothrow()
    } catch {}
  }
  return {
    event: async ({ event }) => {
      if (event.type === "session.idle") await send("idle", event)
      if (event.type === "permission.updated" || event.type === "permission.asked") await send("permission", event)
    },
  }
}
`
	if cur, err := os.ReadFile(h.path(e)); err == nil {
		if string(cur) == src {
			return h.status(e), nil
		}
		if !strings.Contains(string(cur), opencodePluginMarker) {
			if _, err := backupFile(h.path(e), time.Now()); err != nil {
				return h.status(e), err
			}
		}
	}
	if err := writeFileAtomic(h.path(e), []byte(src)); err != nil {
		return h.status(e), err
	}
	return h.status(e), nil
}

func (h opencodePlugin) remove(e *env) (api.HookStatus, error) {
	b, err := os.ReadFile(h.path(e))
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !strings.Contains(string(b), opencodePluginMarker)) {
		return h.status(e), nil
	}
	if err != nil {
		return h.status(e), err
	}
	if err := os.Remove(h.path(e)); err != nil {
		return h.status(e), err
	}
	return h.status(e), nil
}
