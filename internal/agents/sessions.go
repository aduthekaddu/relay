package agents

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

// sessionQuery holds list filters (GET /api/v1/agents/sessions).
type sessionQuery struct {
	Agent    string
	Q        string
	Cwd      string
	Status   string // live | history | all
	Pinned   *bool
	Archived string // "" (exclude archived) | "1" (only) | "all"
	Limit    int
	Cursor   string
}

// cursor encodes the keyset position (updated ms, id).
func encodeCursor(updated int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(updated, 10) + "|" + id))
}

func decodeCursor(c string) (int64, string, bool) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, "", false
	}
	ts, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return 0, "", false
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	return n, id, err == nil
}

// listSessions returns one page: live sessions first (first page only),
// then history by updatedAt desc.
func (s *Service) listSessions(ctx context.Context, q sessionQuery) (*api.Page[api.AgentSession], error) {
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	live := s.live.snapshot(ctx)
	liveIDs := make([]string, 0, len(live.bySession))
	for id := range live.bySession {
		liveIDs = append(liveIDs, id)
	}
	page := &api.Page[api.AgentSession]{Items: []api.AgentSession{}}
	var rows []*sessionRow

	if q.Cursor == "" && q.Status != "history" {
		lr, err := s.queryRows(ctx, q, liveIDs, true, 0, "", 500)
		if err != nil {
			return nil, err
		}
		rows = append(rows, lr...)
		page.Items = append(page.Items, s.pendingLive(q, live)...)
	}
	if q.Status != "live" {
		var cu int64
		var cid string
		if q.Cursor != "" {
			var ok bool
			if cu, cid, ok = decodeCursor(q.Cursor); !ok {
				return nil, httpx.BadRequest("invalid cursor")
			}
		}
		hr, err := s.queryRows(ctx, q, liveIDs, false, cu, cid, q.Limit+1)
		if err != nil {
			return nil, err
		}
		if len(hr) > q.Limit {
			last := hr[q.Limit-1]
			page.NextCursor = encodeCursor(toMS(last.UpdatedAt), last.ID)
			hr = hr[:q.Limit]
		}
		rows = append(rows, hr...)
	}
	page.Items = append(page.Items, s.decorate(ctx, rows)...)
	return page, nil
}

// queryRows runs the filtered session query. With onlyIDs it returns just
// those sessions (live ones); otherwise it excludes them.
func (s *Service) queryRows(ctx context.Context, q sessionQuery, ids []string, only bool, cu int64, cid string, limit int) ([]*sessionRow, error) {
	if only && len(ids) == 0 {
		return nil, nil
	}
	var where []string
	var args []any
	where = append(where, "s.hidden=0")
	if q.Agent != "" {
		where = append(where, "s.agent=?")
		args = append(args, q.Agent)
	}
	if q.Q != "" {
		like := "%" + escapeLike(q.Q) + "%"
		where = append(where, `(s.title LIKE ? ESCAPE '\' OR s.first_prompt LIKE ? ESCAPE '\' OR s.summary LIKE ? ESCAPE '\' OR u.title LIKE ? ESCAPE '\' OR s.cwd LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like, like, like)
	}
	if q.Cwd != "" {
		c := filepath.Clean(q.Cwd)
		where = append(where, `(s.cwd=? OR s.cwd LIKE ? ESCAPE '\')`)
		args = append(args, c, escapeLike(strings.TrimSuffix(c, "/"))+"/%")
	}
	if q.Pinned != nil {
		where = append(where, "COALESCE(u.pinned,0)=?")
		args = append(args, boolInt(*q.Pinned))
	}
	switch q.Archived {
	case "all":
	case "1", "true":
		where = append(where, "COALESCE(u.archived,0)=1")
	default:
		if !only {
			where = append(where, "COALESCE(u.archived,0)=0")
		}
	}
	if len(ids) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		if only {
			where = append(where, "s.id IN ("+ph+")")
		} else {
			where = append(where, "s.id NOT IN ("+ph+")")
		}
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if cid != "" {
		where = append(where, "(s.updated < ? OR (s.updated = ? AND s.id < ?))")
		args = append(args, cu, cu, cid)
	}
	query := `SELECT ` + sessionCols + ` FROM agent_sessions s LEFT JOIN agent_user u ON u.id=s.id WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY s.updated DESC, s.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.ix.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []*sessionRow
	for rows.Next() {
		r, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// decorate fills cost, tokens, workspace and live state.
func (s *Service) decorate(ctx context.Context, rows []*sessionRow) []api.AgentSession {
	out := make([]api.AgentSession, 0, len(rows))
	if len(rows) == 0 {
		return out
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	costs := s.sessionCosts(ctx, ids)
	live := s.live.snapshot(ctx)
	for _, r := range rows {
		as := r.AgentSession
		if c, ok := costs[r.ID]; ok {
			t := c.Tokens
			as.Tokens = &t
			cost := c.Cost
			as.CostUSD = &cost
		}
		if s.d.Workspaces != nil && as.Cwd != "" {
			as.Workspace = s.d.Workspaces.RootOf(as.Cwd)
		}
		if t, ok := live.bySession[r.ID]; ok {
			as.Status = api.AgentLive
			as.TerminalID = t.ID
			as.Activity = t.Activity
		}
		if a, ok := s.byID[r.Agent]; ok && (a.Resume == nil) {
			as.Resumable = false
		}
		out = append(out, as)
	}
	return out
}

// pendingLive returns live agent terminals whose transcript is not
// indexed yet, as synthetic sessions ("<agent>:term-<terminal id>").
func (s *Service) pendingLive(q sessionQuery, live liveSnapshot) []api.AgentSession {
	var out []api.AgentSession
	for _, t := range live.unpaired {
		if q.Agent != "" && t.Agent != q.Agent {
			continue
		}
		if q.Cwd != "" && !within(t.Cwd, filepath.Clean(q.Cwd)) {
			continue
		}
		title := t.Title
		if title == "" {
			title = t.Name
		}
		if q.Q != "" && !strings.Contains(strings.ToLower(title+" "+t.Cwd), strings.ToLower(q.Q)) {
			continue
		}
		out = append(out, api.AgentSession{
			ID: t.Agent + ":term-" + t.ID, Agent: t.Agent, Title: title, Cwd: t.Cwd, Workspace: t.Workspace,
			StartedAt: t.CreatedAt, UpdatedAt: latest(t.LastOutputAt, t.CreatedAt), Status: api.AgentLive,
			TerminalID: t.ID, Activity: t.Activity,
		})
	}
	return out
}

func latest(ts ...time.Time) time.Time {
	var m time.Time
	for _, t := range ts {
		if t.After(m) {
			m = t
		}
	}
	return m
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	path = filepath.Clean(path)
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

// getSession returns one decorated session or a 404.
func (s *Service) getSession(ctx context.Context, id string) (*api.AgentSession, *sessionRow, error) {
	row, err := s.ix.session(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if row == nil {
		return nil, nil, httpx.NotFound("agent session not found")
	}
	out := s.decorate(ctx, []*sessionRow{row})
	return &out[0], row, nil
}

// patchSession applies user overrides (title, pin, archive).
func (s *Service) patchSession(ctx context.Context, id string, req api.UpdateAgentSessionRequest) (*api.AgentSession, error) {
	if _, _, err := s.getSession(ctx, id); err != nil {
		return nil, err
	}
	if req.Title != nil {
		t := strings.TrimSpace(*req.Title)
		if len(t) > 200 {
			return nil, httpx.BadRequest("title too long")
		}
		var v any
		if t != "" {
			v = t
		}
		if _, err := s.ix.db.ExecContext(ctx, `INSERT INTO agent_user(id, title) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET title=excluded.title`, id, v); err != nil {
			return nil, err
		}
	}
	for col, val := range map[string]*bool{"pinned": req.Pinned, "archived": req.Archived} {
		if val == nil {
			continue
		}
		if _, err := s.ix.db.ExecContext(ctx, `INSERT INTO agent_user(id, `+col+`) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET `+col+`=excluded.`+col, id, boolInt(*val)); err != nil {
			return nil, err
		}
	}
	as, _, err := s.getSession(ctx, id)
	if err == nil {
		s.publish(api.EvAgentSession, as)
	}
	return as, err
}

// transcript returns the newest page of messages (or the page before
// `before`), oldest to newest. The transcript is re-read from disk in
// bounded chunks; only a window of limit messages is kept in memory.
func (s *Service) transcript(ctx context.Context, id string, limit int, before string) (*api.Transcript, error) {
	as, row, err := s.getSession(ctx, id)
	if err != nil {
		return nil, err
	}
	a, err := s.adapter(row.Agent)
	if err != nil || a.History == nil {
		return nil, httpx.NotFound("transcript not available")
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	src, err := s.sourceFor(ctx, a, row)
	if err != nil {
		return nil, err
	}
	e := s.env()
	if src.Append {
		e.chunk = indexChunk
	}
	var window []api.AgentMessage
	dropped := false
	found := false
	offset := int64(0)
	for {
		p, perr := a.History.parse(ctx, e, src, offset)
		if perr != nil && p == nil {
			return nil, fmt.Errorf("read transcript: %w", perr)
		}
		for _, m := range p.Messages {
			if before != "" && m.ID == before {
				found = true
				break
			}
			window = append(window, m)
		}
		if len(window) > limit {
			window = append(window[:0:0], window[len(window)-limit:]...)
			dropped = true
		}
		if found || perr != nil || !src.Append || p.Consumed <= offset || p.Consumed >= src.Size {
			break
		}
		offset = p.Consumed
	}
	if window == nil {
		window = []api.AgentMessage{}
	}
	return &api.Transcript{Session: *as, Messages: window, HasMore: dropped}, nil
}

// sourceFor rebuilds the transcript source of a session from the index
// and re-stats it, so the read sees the file as it is now.
func (s *Service) sourceFor(ctx context.Context, a *Adapter, row *sessionRow) (source, error) {
	var src source
	var mtime int64
	var appendOnly, usageOnly int
	err := s.ix.db.QueryRowContext(ctx, `SELECT path, size, mtime, version, append, usage_only, range_start, range_end, native_id
		FROM agent_sources WHERE agent=? AND skey=?`, a.ID, row.Source).
		Scan(&src.Path, &src.Size, &mtime, &src.Version, &appendOnly, &usageOnly, &src.Start, &src.End, &src.NativeID)
	if err != nil {
		return source{}, httpx.NotFound("transcript source not found")
	}
	src.Key, src.ModTime = row.Source, time.Unix(0, mtime)
	src.Append, src.UsageOnly = appendOnly != 0, usageOnly != 0
	if src.NativeID == "" {
		src.NativeID = row.NativeID
	}
	if st, ok := fileSource(src.Path, false); ok {
		if src.Append || !strings.Contains(src.Key, "#") {
			src.Size, src.ModTime = st.Size, st.ModTime
		}
	} else {
		return source{}, httpx.NotFound("transcript file no longer exists")
	}
	return src, nil
}

// AgentCwds returns the most recently used working directories of indexed
// sessions (at most limit), newest first. internal/workspaces uses it to
// discover projects and count sessions per workspace.
func (s *Service) AgentCwds(ctx context.Context, limit int) []api.CwdUsage {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	rows, err := s.ix.db.QueryContext(ctx, `SELECT cwd, count(*), max(updated) FROM agent_sessions
		WHERE hidden=0 AND cwd<>'' GROUP BY cwd ORDER BY max(updated) DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []api.CwdUsage
	for rows.Next() {
		var u api.CwdUsage
		var updated int64
		if rows.Scan(&u.Path, &u.Sessions, &updated) == nil {
			u.LastUsedAt = msTime(updated)
			out = append(out, u)
		}
	}
	return out
}
