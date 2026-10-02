package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/aduthekaddu/relay/internal/secret"
)

// RecoverTOTP replaces an existing account's password, clears active/pending
// TOTP and revokes ALL browser sessions in one transaction. The CLI must verify
// filesystem ownership before calling it. There is deliberately no HTTP route.
// Tokens and passkeys survive. A generation prevents stale sign-ins or credential
// writes from undoing recovery. SQL errors are fixed text, since a damaged
// database or a trigger can put credential values into a driver error.
func (a *Accounts) RecoverTOTP(ctx context.Context, username, password string) (name string, err error) {
	const failure = "TOTP recovery failed; nothing changed"
	// Hash before taking SQLite's write lock. A running server can continue
	// touching sessions while Argon2 runs; the transaction starts with a write.
	if err := a.st.DB.QueryRowContext(ctx, `SELECT username FROM auth_user WHERE id=1`).Scan(&name); err != nil {
		return "", errors.New("TOTP recovery needs an existing account; nothing changed")
	}
	existing := name
	if username = strings.TrimSpace(username); username != "" {
		name = username
	}
	if err := ValidateUsername(name); err != nil {
		return "", err
	}
	if err := CheckPassword(name, password); err != nil {
		return "", err
	}
	hash, err := secret.HashPassword(password)
	if err != nil {
		return "", errors.New(failure)
	}
	tx, err := a.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", errors.New(failure)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE auth_user SET username=?, password_hash=?, totp_secret='', totp_pending='', totp_last_step=0, recovery_generation=recovery_generation+1, updated_at=? WHERE id=1 AND username=?`, name, hash, ms(a.now()), existing)
	if err := credentialWrite(res, err); err != nil {
		return "", errors.New(failure)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_sessions`); err != nil {
		return "", errors.New(failure)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_audit(at,event,actor,ip,detail) VALUES(?,'totp.recover',?,'local',?)`, ms(a.now()), name, "relay passwd --reset-totp; all browser sessions revoked; API tokens and passkeys retained"); err != nil {
		return "", errors.New(failure)
	}
	if err := tx.Commit(); err != nil {
		return "", errors.New(failure)
	}
	return name, nil
}
