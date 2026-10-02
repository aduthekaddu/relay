package auth

import (
	"context"
	"fmt"

	"github.com/aduthekaddu/relay/internal/store"
)

// migrations are append-only: never edit or reorder applied statements.
// Timestamps are Unix milliseconds (UTC).
var migrations = []string{
	`CREATE TABLE auth_user (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		username TEXT NOT NULL,
		password_hash TEXT NOT NULL,
		webauthn_id BLOB NOT NULL,
		totp_secret TEXT NOT NULL DEFAULT '',
		totp_pending TEXT NOT NULL DEFAULT '',
		totp_last_step INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE auth_sessions (
		id TEXT PRIMARY KEY,
		token_hash TEXT NOT NULL UNIQUE,
		username TEXT NOT NULL,
		method TEXT NOT NULL,
		remember INTEGER NOT NULL DEFAULT 0,
		ip TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT '',
		device TEXT NOT NULL DEFAULT '',
		browser TEXT NOT NULL DEFAULT '',
		os TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	)`,
	`CREATE INDEX auth_sessions_expires ON auth_sessions(expires_at)`,
	`CREATE TABLE auth_passkeys (
		id TEXT PRIMARY KEY,
		credential_id BLOB NOT NULL UNIQUE,
		public_key BLOB NOT NULL,
		attestation_type TEXT NOT NULL DEFAULT '',
		sign_count INTEGER NOT NULL DEFAULT 0,
		aaguid BLOB,
		transports TEXT NOT NULL DEFAULT '',
		user_present INTEGER NOT NULL DEFAULT 0,
		user_verified INTEGER NOT NULL DEFAULT 0,
		backup_eligible INTEGER NOT NULL DEFAULT 0,
		backup_state INTEGER NOT NULL DEFAULT 0,
		name TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		last_used_at INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE auth_tokens (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		prefix TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL,
		last_used_at INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE auth_audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		at INTEGER NOT NULL,
		event TEXT NOT NULL,
		actor TEXT NOT NULL DEFAULT '',
		ip TEXT NOT NULL DEFAULT '',
		device TEXT NOT NULL DEFAULT '',
		detail TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX auth_audit_at ON auth_audit(at)`,
	`CREATE TABLE auth_devices (
		id_hash TEXT PRIMARY KEY,
		label TEXT NOT NULL DEFAULT '',
		first_seen_at INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL
	)`,
	`ALTER TABLE auth_user ADD COLUMN recovery_generation INTEGER NOT NULL DEFAULT 0`,
}

// Migrate creates or upgrades the auth tables. Safe to call repeatedly
// (the server and the CLI both call it).
func Migrate(ctx context.Context, st *store.Store) error {
	if err := st.Migrate(ctx, "auth", migrations); err != nil {
		return fmt.Errorf("auth migrations: %w", err)
	}
	return nil
}
