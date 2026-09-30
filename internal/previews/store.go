package previews

import (
	"context"
	"fmt"

	"github.com/aduthekaddu/relay/internal/store"
)

// meta is the user's per-port customisation, persisted in SQLite.
type meta struct {
	Label  string
	Pinned bool
	Hidden bool
}

var migrations = []string{
	`CREATE TABLE preview_meta (
		port INTEGER PRIMARY KEY,
		label TEXT NOT NULL DEFAULT '',
		pinned INTEGER NOT NULL DEFAULT 0,
		hidden INTEGER NOT NULL DEFAULT 0,
		updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	)`,
}

// metaStore reads and writes preview_meta.
type metaStore struct{ st *store.Store }

func (m metaStore) migrate(ctx context.Context) error {
	return m.st.Migrate(ctx, "previews", migrations)
}

// all loads every row.
func (m metaStore) all(ctx context.Context) (map[int]meta, error) {
	rows, err := m.st.DB.QueryContext(ctx, `SELECT port, label, pinned, hidden FROM preview_meta`)
	if err != nil {
		return nil, fmt.Errorf("load preview meta: %w", err)
	}
	defer rows.Close()
	out := map[int]meta{}
	for rows.Next() {
		var port int
		var v meta
		if err := rows.Scan(&port, &v.Label, &v.Pinned, &v.Hidden); err != nil {
			return nil, fmt.Errorf("scan preview meta: %w", err)
		}
		out[port] = v
	}
	return out, rows.Err()
}

// put upserts a row; an all-default row is deleted instead.
func (m metaStore) put(ctx context.Context, port int, v meta) error {
	if v == (meta{}) {
		_, err := m.st.DB.ExecContext(ctx, `DELETE FROM preview_meta WHERE port=?`, port)
		return err
	}
	_, err := m.st.DB.ExecContext(ctx, `INSERT INTO preview_meta(port, label, pinned, hidden) VALUES(?,?,?,?)
		ON CONFLICT(port) DO UPDATE SET label=excluded.label, pinned=excluded.pinned, hidden=excluded.hidden,
		updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, port, v.Label, v.Pinned, v.Hidden)
	if err != nil {
		return fmt.Errorf("save preview meta: %w", err)
	}
	return nil
}
