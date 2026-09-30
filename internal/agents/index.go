package agents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/store"
)

// migrations for the "agents" feature (append-only).
var migrations = []string{
	`CREATE TABLE agent_sources (
		agent TEXT NOT NULL,
		skey TEXT NOT NULL,
		path TEXT NOT NULL,
		size INTEGER NOT NULL DEFAULT 0,
		mtime INTEGER NOT NULL DEFAULT 0,
		offset INTEGER NOT NULL DEFAULT 0,
		version TEXT NOT NULL DEFAULT '',
		session TEXT NOT NULL DEFAULT '',
		append INTEGER NOT NULL DEFAULT 0,
		usage_only INTEGER NOT NULL DEFAULT 0,
		range_start INTEGER NOT NULL DEFAULT 0,
		range_end INTEGER NOT NULL DEFAULT 0,
		native_id TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (agent, skey)
	)`,
	`CREATE TABLE agent_sessions (
		id TEXT PRIMARY KEY,
		agent TEXT NOT NULL,
		native_id TEXT NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		first_prompt TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		cwd TEXT NOT NULL DEFAULT '',
		git_branch TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		started INTEGER NOT NULL DEFAULT 0,
		updated INTEGER NOT NULL DEFAULT 0,
		messages INTEGER NOT NULL DEFAULT 0,
		resumable INTEGER NOT NULL DEFAULT 0,
		hidden INTEGER NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX agent_sessions_updated ON agent_sessions(updated DESC)`,
	`CREATE INDEX agent_sessions_agent ON agent_sessions(agent, updated DESC)`,
	`CREATE INDEX agent_sessions_cwd ON agent_sessions(cwd)`,
	`CREATE TABLE agent_usage (
		session TEXT NOT NULL,
		ukey TEXT NOT NULL,
		at INTEGER NOT NULL DEFAULT 0,
		model TEXT NOT NULL DEFAULT '',
		input INTEGER NOT NULL DEFAULT 0,
		output INTEGER NOT NULL DEFAULT 0,
		cache_read INTEGER NOT NULL DEFAULT 0,
		cache_write INTEGER NOT NULL DEFAULT 0,
		reasoning INTEGER NOT NULL DEFAULT 0,
		native_cost REAL NOT NULL DEFAULT 0,
		credits REAL NOT NULL DEFAULT 0,
		PRIMARY KEY (session, ukey)
	)`,
	`CREATE INDEX agent_usage_at ON agent_usage(at)`,
	`CREATE TABLE agent_user (
		id TEXT PRIMARY KEY,
		title TEXT,
		pinned INTEGER NOT NULL DEFAULT 0,
		archived INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE agent_text (
		rowid INTEGER PRIMARY KEY,
		session TEXT NOT NULL,
		msg TEXT NOT NULL,
		role TEXT NOT NULL,
		at INTEGER NOT NULL DEFAULT 0,
		body TEXT NOT NULL
	)`,
	`CREATE INDEX agent_text_session ON agent_text(session)`,
}

// ftsSetup creates the FTS5 index over agent_text (external content, kept
// in sync by triggers). It runs outside the migration list so a SQLite
// build without FTS5 degrades to LIKE search instead of failing startup.
var ftsSetup = []string{
	`CREATE VIRTUAL TABLE IF NOT EXISTS agent_fts USING fts5(body, content='agent_text', content_rowid='rowid', tokenize='unicode61 remove_diacritics 2')`,
	`CREATE TRIGGER IF NOT EXISTS agent_text_ai AFTER INSERT ON agent_text BEGIN
		INSERT INTO agent_fts(rowid, body) VALUES (new.rowid, new.body);
	END`,
	`CREATE TRIGGER IF NOT EXISTS agent_text_ad AFTER DELETE ON agent_text BEGIN
		INSERT INTO agent_fts(agent_fts, rowid, body) VALUES ('delete', old.rowid, old.body);
	END`,
}

// maxIndexedText caps the text indexed per message (FTS body).
const maxIndexedText = 4 << 10

// index is the SQLite side of the agents feature.
type index struct {
	db  *sql.DB
	st  *store.Store
	fts bool
}

func openIndex(ctx context.Context, st *store.Store) (*index, error) {
	if err := st.Migrate(ctx, "agents", migrations); err != nil {
		return nil, fmt.Errorf("agents migrations: %w", err)
	}
	ix := &index{db: st.DB, st: st, fts: true}
	for _, q := range ftsSetup {
		if _, err := st.DB.ExecContext(ctx, q); err != nil {
			ix.fts = false
			break
		}
	}
	if ix.fts {
		// Backfill when FTS was added after text rows existed.
		var nText, nFTS int64
		_ = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM agent_text`).Scan(&nText)
		_ = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM agent_fts_docsize`).Scan(&nFTS)
		if nText > 0 && nFTS == 0 {
			_, _ = st.DB.ExecContext(ctx, `INSERT INTO agent_fts(agent_fts) VALUES ('rebuild')`)
		}
	}
	return ix, nil
}

// knownSource is what the index remembers about a transcript source.
type knownSource struct {
	Size    int64
	MTime   int64
	Offset  int64
	Version string
	Session string
}

func (ix *index) sources(ctx context.Context, agent string) (map[string]knownSource, error) {
	rows, err := ix.db.QueryContext(ctx, `SELECT skey, size, mtime, offset, version, session FROM agent_sources WHERE agent=?`, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]knownSource{}
	for rows.Next() {
		var k string
		var s knownSource
		if err := rows.Scan(&k, &s.Size, &s.MTime, &s.Offset, &s.Version, &s.Session); err != nil {
			return nil, err
		}
		out[k] = s
	}
	return out, rows.Err()
}

// changed reports whether src differs from what was indexed.
func (k knownSource) changed(src source) bool {
	return k.Size != src.Size || k.MTime != src.ModTime.UnixNano() || k.Version != src.Version
}

// sessionRow is a stored session (index columns + user overrides).
type sessionRow struct {
	api.AgentSession
	FirstPrompt string
	Hidden      bool
	Source      string
	UserTitle   sql.NullString
}

const sessionCols = `s.id, s.agent, s.native_id, s.title, s.first_prompt, s.summary, s.cwd, s.git_branch, s.model,
	s.started, s.updated, s.messages, s.resumable, s.hidden, s.source,
	u.title, COALESCE(u.pinned,0), COALESCE(u.archived,0)`

func scanSession(sc interface{ Scan(...any) error }) (*sessionRow, error) {
	var r sessionRow
	var started, updated int64
	var resumable, hidden, pinned, archived int
	if err := sc.Scan(&r.ID, &r.Agent, &r.NativeID, &r.Title, &r.FirstPrompt, &r.Summary, &r.Cwd, &r.GitBranch, &r.Model,
		&started, &updated, &r.Messages, &resumable, &hidden, &r.Source, &r.UserTitle, &pinned, &archived); err != nil {
		return nil, err
	}
	r.StartedAt, r.UpdatedAt = msTime(started), msTime(updated)
	r.Resumable, r.Hidden, r.Pinned, r.Archived = resumable != 0, hidden != 0, pinned != 0, archived != 0
	r.Status = api.AgentHistory
	switch {
	case r.UserTitle.Valid && r.UserTitle.String != "":
		r.Title = r.UserTitle.String
	case r.Title != "":
	case r.FirstPrompt != "":
		r.Title = r.FirstPrompt
	case r.Summary != "":
		r.Title = r.Summary
	default:
		r.Title = "Untitled session"
	}
	return &r, nil
}

func (ix *index) session(ctx context.Context, id string) (*sessionRow, error) {
	row := ix.db.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM agent_sessions s LEFT JOIN agent_user u ON u.id=s.id WHERE s.id=?`, id)
	r, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

func msTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func toMS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// applyResult is what one apply call changed.
type applyResult struct {
	Session string
	New     bool
}

// apply stores one parse result (a whole source, or the next chunk of an
// append-only source when incremental) in a single transaction.
func (ix *index) apply(ctx context.Context, agent string, src source, known knownSource, p *parsed, incremental bool) (applyResult, error) {
	native := p.NativeID
	if native == "" && known.Session != "" {
		native = strings.TrimPrefix(known.Session, agent+":")
	}
	if native == "" {
		native = src.NativeID
	}
	if native == "" {
		return applyResult{}, fmt.Errorf("no session id in %s", src.Key)
	}
	id := agent + ":" + native
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return applyResult{}, err
	}
	defer tx.Rollback()
	res := applyResult{Session: id}

	if !p.UsageOnly {
		if res.New, err = upsertSession(ctx, tx, agent, native, id, src, p, incremental); err != nil {
			return res, err
		}
		if err := insertText(ctx, tx, id, p.Messages); err != nil {
			return res, err
		}
	}
	if err := upsertUsage(ctx, tx, id, p); err != nil {
		return res, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_sources(agent, skey, path, size, mtime, offset, version, session,
			append, usage_only, range_start, range_end, native_id)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(agent, skey) DO UPDATE SET path=excluded.path, size=excluded.size,
		mtime=excluded.mtime, offset=excluded.offset, version=excluded.version, session=excluded.session,
		append=excluded.append, usage_only=excluded.usage_only, range_start=excluded.range_start,
		range_end=excluded.range_end, native_id=excluded.native_id`,
		agent, src.Key, src.Path, src.Size, src.ModTime.UnixNano(), p.Consumed, src.Version, id,
		boolInt(src.Append), boolInt(src.UsageOnly), src.Start, src.End, src.NativeID); err != nil {
		return res, err
	}
	return res, tx.Commit()
}

func upsertSession(ctx context.Context, tx *sql.Tx, agent, native, id string, src source, p *parsed, incremental bool) (bool, error) {
	var exists bool
	var msgs int64
	var first string
	err := tx.QueryRowContext(ctx, `SELECT messages, first_prompt FROM agent_sessions WHERE id=?`, id).Scan(&msgs, &first)
	switch {
	case err == nil:
		exists = true
	case errors.Is(err, sql.ErrNoRows):
	default:
		return false, err
	}
	if !incremental {
		msgs, first = 0, ""
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_text WHERE session=?`, id); err != nil {
			return false, err
		}
	}
	msgs += int64(countConversational(p.Messages))
	if first == "" {
		first = p.FirstUser
	}
	hidden := p.Hidden || (msgs == 0 && first == "")
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_sessions(id, agent, native_id, title, first_prompt, summary, cwd, git_branch, model,
			started, updated, messages, resumable, hidden, source)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			title = CASE WHEN excluded.title<>'' THEN excluded.title ELSE agent_sessions.title END,
			first_prompt = excluded.first_prompt,
			summary = CASE WHEN excluded.summary<>'' THEN excluded.summary ELSE agent_sessions.summary END,
			cwd = CASE WHEN excluded.cwd<>'' THEN excluded.cwd ELSE agent_sessions.cwd END,
			git_branch = CASE WHEN excluded.git_branch<>'' THEN excluded.git_branch ELSE agent_sessions.git_branch END,
			model = CASE WHEN excluded.model<>'' THEN excluded.model ELSE agent_sessions.model END,
			started = CASE WHEN agent_sessions.started=0 OR (excluded.started>0 AND excluded.started<agent_sessions.started) THEN excluded.started ELSE agent_sessions.started END,
			updated = max(agent_sessions.updated, excluded.updated),
			messages = excluded.messages,
			resumable = excluded.resumable,
			hidden = excluded.hidden,
			source = excluded.source`,
		id, agent, native, p.Title, first, p.Summary, p.Cwd, p.GitBranch, p.Model,
		toMS(p.StartedAt), toMS(p.UpdatedAt), msgs, boolInt(p.Resumable), boolInt(hidden), src.Key)
	return !exists, err
}

// countConversational counts user and assistant messages.
func countConversational(ms []api.AgentMessage) int {
	n := 0
	for _, m := range ms {
		if m.Role == "user" || m.Role == "assistant" {
			n++
		}
	}
	return n
}

func insertText(ctx context.Context, tx *sql.Tx, id string, ms []api.AgentMessage) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO agent_text(session, msg, role, at, body) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, m := range ms {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		var b strings.Builder
		for _, pt := range m.Parts {
			if pt.Type == "text" && pt.Text != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(pt.Text)
				if b.Len() >= maxIndexedText {
					break
				}
			}
		}
		body := strings.TrimSpace(ansiRe.ReplaceAllString(truncate(b.String(), maxIndexedText), ""))
		if body == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, id, m.ID, m.Role, toMS(m.At), body); err != nil {
			return err
		}
	}
	return nil
}

func upsertUsage(ctx context.Context, tx *sql.Tx, id string, p *parsed) error {
	if len(p.Usage) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO agent_usage(session, ukey, at, model, input, output, cache_read, cache_write, reasoning, native_cost, credits)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(session, ukey) DO UPDATE SET
			at = CASE WHEN excluded.at>0 THEN excluded.at ELSE agent_usage.at END,
			model = CASE WHEN excluded.model<>'' THEN excluded.model ELSE agent_usage.model END,
			input = max(agent_usage.input, excluded.input),
			output = max(agent_usage.output, excluded.output),
			cache_read = max(agent_usage.cache_read, excluded.cache_read),
			cache_write = max(agent_usage.cache_write, excluded.cache_write),
			reasoning = max(agent_usage.reasoning, excluded.reasoning),
			native_cost = max(agent_usage.native_cost, excluded.native_cost),
			credits = max(agent_usage.credits, excluded.credits)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, u := range p.Usage {
		model := u.Model
		if model == "" {
			model = p.Model
		}
		at := u.At
		if at.IsZero() {
			at = p.UpdatedAt
		}
		if _, err := stmt.ExecContext(ctx, id, u.Key, toMS(at), model, u.Input, u.Output, u.CacheRead, u.CacheWrite,
			u.Reasoning, u.NativeCost, u.Credits); err != nil {
			return err
		}
	}
	return nil
}

// forget removes a source that disappeared from disk, and its session
// when the source was the session's own transcript.
func (ix *index) forget(ctx context.Context, agent, key string, k knownSource) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if k.Session != "" {
		var src string
		if tx.QueryRowContext(ctx, `SELECT source FROM agent_sessions WHERE id=?`, k.Session).Scan(&src) == nil && src == key {
			for _, q := range []string{`DELETE FROM agent_text WHERE session=?`, `DELETE FROM agent_usage WHERE session=?`, `DELETE FROM agent_sessions WHERE id=?`} {
				if _, err := tx.ExecContext(ctx, q, k.Session); err != nil {
					return err
				}
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_sources WHERE agent=? AND skey=?`, agent, key); err != nil {
		return err
	}
	return tx.Commit()
}

// resetAgent drops every indexed row of agent (full reindex).
func (ix *index) resetAgent(ctx context.Context, agent string) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	like := agent + ":%"
	for _, q := range []string{
		`DELETE FROM agent_text WHERE session LIKE ?`,
		`DELETE FROM agent_usage WHERE session LIKE ?`,
		`DELETE FROM agent_sessions WHERE id LIKE ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, like); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_sources WHERE agent=?`, agent); err != nil {
		return err
	}
	return tx.Commit()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
