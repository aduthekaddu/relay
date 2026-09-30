// Package store owns the SQLite database (relay.db).
//
// Each feature package owns its tables and calls Migrate with its own
// feature name and an append-only list of SQL statements. Applied versions
// are recorded in schema_migrations, so adding a statement to the end of a
// list is how a feature evolves its schema. Never edit or reorder applied
// statements.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite" // pure-Go driver, registers "sqlite"
)

type Store struct {
	DB *sql.DB
	mu sync.Mutex
}

// Open opens (creating if needed) the database at path with WAL mode and
// sane pragmas. The file is created with mode 0600.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		f.Close()
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a small pool keeps readers concurrent.
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{DB: db}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		feature TEXT NOT NULL,
		version INTEGER NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		PRIMARY KEY (feature, version)
	)`); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenMemory opens a private in-memory database (tests).
func OpenMemory() (*Store, error) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (feature TEXT NOT NULL, version INTEGER NOT NULL, applied_at TEXT, PRIMARY KEY (feature, version))`)
	return s, err
}

// Migrate applies the not-yet-applied statements for feature, in order.
// Statement i (0-based) is version i+1.
func (s *Store) Migrate(ctx context.Context, feature string, stmts []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var cur int
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations WHERE feature=?`, feature).Scan(&cur); err != nil {
		return err
	}
	for i := cur; i < len(stmts); i++ {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, stmts[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrate %s v%d: %w", feature, i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(feature, version) VALUES(?,?)`, feature, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.DB.Close() }

// KV is a tiny key/value table for feature settings that do not deserve
// their own table (e.g. VAPID keys, UI preferences synced across devices).
func (s *Store) ensureKV(ctx context.Context) error {
	return s.Migrate(ctx, "kv", []string{
		`CREATE TABLE kv (k TEXT PRIMARY KEY, v TEXT NOT NULL, updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`,
	})
}

// GetKV returns the value for key, or "" and false.
func (s *Store) GetKV(ctx context.Context, key string) (string, bool, error) {
	if err := s.ensureKV(ctx); err != nil {
		return "", false, err
	}
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT v FROM kv WHERE k=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetKV upserts key.
func (s *Store) SetKV(ctx context.Context, key, value string) error {
	if err := s.ensureKV(ctx); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO kv(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`, key, value)
	return err
}
