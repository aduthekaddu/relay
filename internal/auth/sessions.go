package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/secret"
)

// sessionRow is a browser session. The secret cookie value is never stored;
// TokenHash is its SHA-256.
type sessionRow struct {
	ID         string // public id, safe to show (DeviceSession.ID, Principal.SessionID)
	TokenHash  string
	Username   string
	Method     string // password | passkey | setup
	Remember   bool
	IP         string
	UserAgent  string
	Device     string
	Browser    string
	OS         string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// newSessionSecret returns a 256-bit random cookie value.
func newSessionSecret() (string, error) { return secret.Token("", 32) }

func (a *Accounts) insertSession(ctx context.Context, s *sessionRow) error {
	_, err := a.st.DB.ExecContext(ctx, `INSERT INTO auth_sessions(id, token_hash, username, method, remember, ip, user_agent, device, browser, os, created_at, last_seen_at, expires_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.ID, s.TokenHash, s.Username, s.Method, s.Remember, s.IP, clip(s.UserAgent, 512), s.Device, s.Browser, s.OS,
		ms(s.CreatedAt), ms(s.LastSeenAt), ms(s.ExpiresAt))
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

const sessionCols = `id, token_hash, username, method, remember, ip, user_agent, device, browser, os, created_at, last_seen_at, expires_at`

func scanSession(sc interface{ Scan(...any) error }) (*sessionRow, error) {
	var s sessionRow
	var c, l, e int64
	if err := sc.Scan(&s.ID, &s.TokenHash, &s.Username, &s.Method, &s.Remember, &s.IP, &s.UserAgent, &s.Device, &s.Browser, &s.OS, &c, &l, &e); err != nil {
		return nil, err
	}
	s.CreatedAt, s.LastSeenAt, s.ExpiresAt = fromMS(c), fromMS(l), fromMS(e)
	return &s, nil
}

// sessionByHash returns an unexpired session, or nil.
func (a *Accounts) sessionByHash(ctx context.Context, hash string) (*sessionRow, error) {
	row := a.st.DB.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM auth_sessions WHERE token_hash=? AND expires_at > ?`, hash, ms(a.now()))
	s, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

// touchSession slides the expiry and records the last request.
func (a *Accounts) touchSession(ctx context.Context, id string, seen, expires time.Time, ip string) error {
	_, err := a.st.DB.ExecContext(ctx, `UPDATE auth_sessions SET last_seen_at=?, expires_at=?, ip=? WHERE id=? AND expires_at>? AND last_seen_at<=?`, ms(seen), ms(expires), ip, id, ms(seen), ms(seen.Add(-touchEvery)))
	return err
}

// listSessions returns unexpired sessions, most recently used first.
func (a *Accounts) listSessions(ctx context.Context) ([]*sessionRow, error) {
	rows, err := a.st.DB.QueryContext(ctx, `SELECT `+sessionCols+` FROM auth_sessions WHERE expires_at > ? ORDER BY last_seen_at DESC`, ms(a.now()))
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []*sessionRow
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// deleteSession removes one session by public id and returns it.
func (a *Accounts) deleteSession(ctx context.Context, id string) (*sessionRow, error) {
	row := a.st.DB.QueryRowContext(ctx, `DELETE FROM auth_sessions WHERE id=? RETURNING `+sessionCols, id)
	s, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("That session has already ended.")
	}
	if err != nil {
		return nil, fmt.Errorf("revoke session: %w", err)
	}
	return s, nil
}

// RevokeAllSessions ends every browser session except keepID (may be "")
// and returns the public ids it removed.
func (a *Accounts) RevokeAllSessions(ctx context.Context, keepID string) ([]string, error) {
	rows, err := a.st.DB.QueryContext(ctx, `DELETE FROM auth_sessions WHERE id != ? RETURNING id`, keepID)
	if err != nil {
		return nil, fmt.Errorf("revoke sessions: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (a *Accounts) deleteExpiredSessions(ctx context.Context) error {
	_, err := a.st.DB.ExecContext(ctx, `DELETE FROM auth_sessions WHERE expires_at <= ?`, ms(a.now()))
	return err
}

func (s *sessionRow) toAPI(currentID string) api.DeviceSession {
	return api.DeviceSession{
		ID: s.ID, Current: s.ID == currentID, Device: s.Device, Browser: s.Browser, OS: s.OS,
		IP: s.IP, Method: s.Method, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, ExpiresAt: s.ExpiresAt,
	}
}

// deviceLabel is "Chrome on Mac" style text for audit rows and notices.
func deviceLabel(ua UserAgent) string {
	switch {
	case ua.Browser != "" && ua.Device != "":
		return ua.Browser + " on " + ua.Device
	case ua.Browser != "":
		return ua.Browser
	case ua.Device != "":
		return ua.Device
	}
	return "Unknown device"
}

// --- Known devices (new-device sign-in notices) ------------------------------

// seenDevice records a device cookie and reports whether it was known.
func (a *Accounts) seenDevice(ctx context.Context, deviceSecret, label string) (known bool, err error) {
	h := secret.HashToken(deviceSecret)
	now := ms(a.now())
	res, err := a.st.DB.ExecContext(ctx, `UPDATE auth_devices SET last_seen_at=?, label=? WHERE id_hash=?`, now, label, h)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return true, nil
	}
	_, err = a.st.DB.ExecContext(ctx, `INSERT INTO auth_devices(id_hash, label, first_seen_at, last_seen_at) VALUES(?,?,?,?) ON CONFLICT(id_hash) DO NOTHING`, h, label, now, now)
	return false, err
}

func (a *Accounts) deviceCount(ctx context.Context) (int, error) {
	var n int
	err := a.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_devices`).Scan(&n)
	return n, err
}

// cleanIP keeps audit/session IP columns short and printable.
func cleanIP(ip string) string {
	ip = strings.TrimSpace(ip)
	return clip(ip, 64)
}
