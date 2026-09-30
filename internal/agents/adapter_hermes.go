package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/aduthekaddu/relay/internal/api"
)

// Hermes Agent: ~/.hermes/state.db (SQLite): sessions (title, cwd, git
// branch, model, token totals, cost), messages (role, content, tool calls,
// reasoning) and session_model_usage (per-model tokens and cost).
func newHermes() *Adapter {
	return &Adapter{
		ID: "hermes", Name: "Hermes", Vendor: "Nous Research", Color: "#8B5CF6",
		Binaries: []string{"hermes"}, ExtraDirs: []string{".hermes/bin"}, InstallHint: "hermes",
		Caps: api.AgentCapabilities{Resume: true, Headless: true, History: true, Usage: true, Worktrees: true},
		Interactive: func(bin, _, model string) []string {
			return withFlag([]string{bin}, "-m", model)
		},
		Resume: func(bin, id string) []string { return []string{bin, "--resume", id} },
		Headless: func(bin, prompt, model string) []string {
			return withFlag([]string{bin, "-z", safeArg(prompt)}, "-m", model)
		},
		History: hermesReader{},
	}
}

type hermesReader struct{}

func hermesDB(e *env) string { return e.path(".hermes", "state.db") }

func (hermesReader) sources(ctx context.Context, e *env) ([]source, error) {
	path := hermesDB(e)
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	db, err := e.dbs.get(ctx, e.tmp, path)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, COALESCE(last_activity_at, ended_at, started_at, 0), COALESCE(message_count, 0) FROM sessions`)
	if err != nil {
		return nil, fmt.Errorf("hermes sessions: %w", err)
	}
	defer rows.Close()
	var out []source
	for rows.Next() {
		var id string
		var last float64
		var n int64
		if rows.Scan(&id, &last, &n) == nil {
			out = append(out, source{Key: path + "#" + id, Path: path, NativeID: id, ModTime: unixAny(last),
				Version: strconv.FormatFloat(last, 'f', 3, 64) + ":" + strconv.FormatInt(n, 10)})
		}
	}
	return out, rows.Err()
}

func (hermesReader) parse(ctx context.Context, e *env, src source, _ int64) (*parsed, error) {
	db, err := e.dbs.get(ctx, e.tmp, src.Path)
	if err != nil {
		return nil, err
	}
	p := &parsed{NativeID: src.NativeID, Resumable: true}
	var title, cwd, branch, model, parent sql.NullString
	var started, ended, last sql.NullFloat64
	var hidden, archived sql.NullInt64
	cols := tableColumns(ctx, db, "sessions")
	hiddenCol, archivedCol := "0", "0"
	if cols["hidden"] {
		hiddenCol = "hidden"
	}
	if cols["archived"] {
		archivedCol = "archived"
	}
	err = db.QueryRowContext(ctx, `SELECT title, cwd, git_branch, model, parent_session_id, started_at, ended_at, last_activity_at, `+
		hiddenCol+`, `+archivedCol+` FROM sessions WHERE id=?`, src.NativeID).
		Scan(&title, &cwd, &branch, &model, &parent, &started, &ended, &last, &hidden, &archived)
	if err != nil {
		return nil, fmt.Errorf("hermes session: %w", err)
	}
	p.Title, p.Cwd, p.GitBranch, p.Model = cleanTitle(title.String), cwd.String, branch.String, model.String
	p.Hidden = hidden.Int64 != 0 || parent.String != ""
	for _, t := range []sql.NullFloat64{started, ended, last} {
		if t.Valid {
			p.touch(unixAny(t.Float64))
		}
	}
	if err := hermesUsage(ctx, db, p); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT CAST(id AS TEXT), role, COALESCE(content,''), COALESCE(tool_calls,''), COALESCE(tool_name,''), COALESCE(reasoning,''), COALESCE(timestamp,0) FROM messages WHERE session_id=? ORDER BY timestamp, id`, src.NativeID)
	if err != nil {
		return nil, fmt.Errorf("hermes messages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var mid, role, content, calls, tool, reasoning string
		var ts float64
		if rows.Scan(&mid, &role, &content, &calls, &tool, &reasoning, &ts) != nil {
			continue
		}
		at := unixAny(ts)
		p.touch(at)
		switch role {
		case "user":
			if strings.TrimSpace(content) == "" || isInjectedPrompt(content) {
				continue
			}
			p.addUser(content)
			p.Messages = append(p.Messages, api.AgentMessage{ID: mid, Role: "user", At: at, Parts: []api.Part{textPart("text", content)}})
		case "assistant":
			var parts []api.Part
			if strings.TrimSpace(reasoning) != "" {
				parts = append(parts, textPart("thinking", reasoning))
			}
			if strings.TrimSpace(content) != "" {
				parts = append(parts, textPart("text", content))
			}
			var tc []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			}
			if calls != "" && json.Unmarshal([]byte(calls), &tc) == nil {
				for _, c := range tc {
					parts = append(parts, toolCallPart(c.Function.Name, c.Function.Arguments))
				}
			}
			if len(parts) > 0 {
				p.Messages = append(p.Messages, api.AgentMessage{ID: mid, Role: "assistant", At: at, Model: p.Model, Parts: parts})
			}
		case "tool":
			p.Messages = append(p.Messages, api.AgentMessage{ID: mid, Role: "tool", At: at, Parts: []api.Part{toolResultPart(tool, content, false)}})
		}
	}
	return p, rows.Err()
}

func hermesUsage(ctx context.Context, db *sql.DB, p *parsed) error {
	if len(tableColumns(ctx, db, "session_model_usage")) == 0 {
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(model,''), COALESCE(input_tokens,0), COALESCE(output_tokens,0), COALESCE(cache_read_tokens,0), COALESCE(cache_write_tokens,0), COALESCE(reasoning_tokens,0), COALESCE(actual_cost_usd, estimated_cost_usd, 0), COALESCE(last_seen, first_seen, 0), COALESCE(task,'') FROM session_model_usage WHERE session_id=?`, p.NativeID)
	if err != nil {
		return fmt.Errorf("hermes usage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var model, task string
		var in, out, cr, cw, rs int64
		var cost float64
		var seen any
		if rows.Scan(&model, &in, &out, &cr, &cw, &rs, &cost, &seen, &task) != nil {
			continue
		}
		p.Usage = append(p.Usage, usageRec{Key: "model:" + model + ":" + task, At: parseTime(seen), Model: model,
			Input: in, Output: out, CacheRead: cr, CacheWrite: cw, Reasoning: rs, NativeCost: cost})
	}
	return rows.Err()
}
