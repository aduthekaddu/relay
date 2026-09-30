package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Crush (Charm): a registry at ~/.local/share/crush/projects.json lists
// projects; each keeps SQLite history in <project>/<data_dir>/crush.db
// (sessions with token totals and cost, messages with JSON parts).
func newCrush() *Adapter {
	return &Adapter{
		ID: "crush", Name: "Crush", Vendor: "Charm", Color: "#FF5FD2",
		Binaries: []string{"crush"}, InstallHint: "crush",
		Caps:        api.AgentCapabilities{Headless: true, History: true, Usage: true, Worktrees: true},
		Interactive: func(bin, _, _ string) []string { return []string{bin} },
		Headless: func(bin, prompt, _ string) []string {
			return []string{bin, "run", safeArg(prompt)}
		},
		History: crushReader{},
	}
}

type crushReader struct{}

// crushDBs lists project databases from the registry and from known
// workspaces (./.crush/crush.db).
func crushDBs(ctx context.Context, e *env) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	var reg struct {
		Projects []struct {
			Path    string `json:"path"`
			DataDir string `json:"data_dir"`
		} `json:"projects"`
	}
	if readJSONFile(e.path(".local", "share", "crush", "projects.json"), 4<<20, &reg) == nil {
		for _, pr := range reg.Projects {
			dd := pr.DataDir
			if dd == "" {
				dd = ".crush"
			}
			if !filepath.IsAbs(dd) {
				dd = filepath.Join(pr.Path, dd)
			}
			add(filepath.Join(dd, "crush.db"))
		}
	}
	if e.workspaces != nil {
		for _, ws := range e.workspaces(ctx) {
			add(filepath.Join(ws, ".crush", "crush.db"))
		}
	}
	return out
}

func (crushReader) sources(ctx context.Context, e *env) ([]source, error) {
	var out []source
	for _, path := range crushDBs(ctx, e) {
		db, err := e.dbs.get(ctx, e.tmp, path)
		if err != nil {
			continue
		}
		rows, err := db.QueryContext(ctx, `SELECT id, updated_at, message_count FROM sessions`)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id string
			var upd, n int64
			if rows.Scan(&id, &upd, &n) == nil {
				out = append(out, source{Key: path + "#" + id, Path: path, NativeID: id, ModTime: unixAny(float64(upd)),
					Version: strconv.FormatInt(upd, 10) + ":" + strconv.FormatInt(n, 10)})
			}
		}
		rows.Close()
	}
	return out, ctx.Err()
}

func (crushReader) parse(ctx context.Context, e *env, src source, _ int64) (*parsed, error) {
	db, err := e.dbs.get(ctx, e.tmp, src.Path)
	if err != nil {
		return nil, err
	}
	p := &parsed{NativeID: src.NativeID, Cwd: filepath.Dir(filepath.Dir(src.Path))}
	var title string
	var parent sql.NullString
	var prompt, completion, created, updated int64
	var cost float64
	err = db.QueryRowContext(ctx, `SELECT title, parent_session_id, prompt_tokens, completion_tokens, cost, created_at, updated_at FROM sessions WHERE id=?`, src.NativeID).
		Scan(&title, &parent, &prompt, &completion, &cost, &created, &updated)
	if err != nil {
		return nil, fmt.Errorf("crush session: %w", err)
	}
	p.Title = cleanTitle(title)
	p.Hidden = parent.Valid && parent.String != ""
	p.touch(unixAny(float64(created)))
	p.touch(unixAny(float64(updated)))
	rows, err := db.QueryContext(ctx, `SELECT id, role, parts, COALESCE(model,''), created_at FROM messages WHERE session_id=? ORDER BY created_at, id`, src.NativeID)
	if err != nil {
		return nil, fmt.Errorf("crush messages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, role, partsJSON, model string
		var ts int64
		if rows.Scan(&id, &role, &partsJSON, &model, &ts) != nil {
			continue
		}
		if model != "" {
			p.Model = model
		}
		crushMessage(p, id, role, model, unixAny(float64(ts)), partsJSON)
	}
	if prompt+completion > 0 || cost > 0 {
		p.Usage = append(p.Usage, usageRec{Key: "session", At: p.UpdatedAt, Model: p.Model,
			Input: prompt, Output: completion, NativeCost: cost})
	}
	return p, rows.Err()
}

func crushMessage(p *parsed, id, role, model string, at time.Time, partsJSON string) {
	var raw []struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal([]byte(partsJSON), &raw) != nil {
		return
	}
	var parts []api.Part
	for _, r := range raw {
		var d struct {
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
			Name     string `json:"name"`
			Input    string `json:"input"`
			Content  string `json:"content"`
			IsError  bool   `json:"is_error"`
		}
		_ = json.Unmarshal(r.Data, &d)
		switch r.Type {
		case "text":
			if strings.TrimSpace(d.Text) != "" {
				if role == "user" {
					p.addUser(d.Text)
				}
				parts = append(parts, textPart("text", d.Text))
			}
		case "reasoning":
			if strings.TrimSpace(d.Thinking) != "" {
				parts = append(parts, textPart("thinking", d.Thinking))
			}
		case "tool_call":
			parts = append(parts, toolCallPart(d.Name, d.Input))
		case "tool_result":
			parts = append(parts, toolResultPart(d.Name, d.Content, d.IsError))
		}
	}
	if len(parts) == 0 {
		return
	}
	p.touch(at)
	p.Messages = append(p.Messages, api.AgentMessage{ID: id, Role: role, At: at, Model: modelIf(role, model), Parts: parts})
}
