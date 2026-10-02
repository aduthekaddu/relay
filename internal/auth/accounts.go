package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/secret"
	"github.com/aduthekaddu/relay/internal/store"
)

// Password policy.
const (
	MinPasswordLen = 10
	maxPasswordLen = 1024 // bytes; argon2 input cap
	maxUsernameLen = 64
	maxNameLen     = 64 // passkey and token names
)

// ErrUserExists is returned when creating the account a second time.
var ErrUserExists = errors.New("an account already exists")

// Accounts is the persistence layer for credentials, sessions, tokens and
// the audit log. It needs only the store, so the CLI (`relay passwd`,
// `relay token`) uses it directly whether or not the server is running.
type Accounts struct {
	st  *store.Store
	now func() time.Time
}

// OpenAccounts migrates the auth tables and returns an Accounts.
func OpenAccounts(ctx context.Context, st *store.Store) (*Accounts, error) {
	if err := Migrate(ctx, st); err != nil {
		return nil, err
	}
	return &Accounts{st: st, now: func() time.Time { return time.Now().UTC() }}, nil
}

type user struct {
	Username     string
	PasswordHash string
	WebAuthnID   []byte
	TOTPSecret   string
	TOTPPending  string
	TOTPLastStep int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

// randomHex returns n random bytes as hex.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error()) // unrecoverable
	}
	return hex.EncodeToString(b)
}

// ValidateUsername checks a username for the single account.
func ValidateUsername(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return &httpx.Err{Status: 400, Code: "bad_request", Message: "Choose a username.", Field: "username"}
	}
	if utf8.RuneCountInString(name) > maxUsernameLen {
		return &httpx.Err{Status: 400, Code: "bad_request", Message: "Use 64 characters or fewer.", Field: "username"}
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return &httpx.Err{Status: 400, Code: "bad_request", Message: "Usernames can't contain spaces.", Field: "username"}
		}
	}
	return nil
}

// commonPasswords are rejected outright even when long enough.
var commonPasswords = map[string]bool{
	"1234567890": true, "12345678910": true, "123456789012": true, "0987654321": true,
	"password12": true, "password123": true, "password1234": true, "passw0rd123": true,
	"qwertyuiop": true, "1q2w3e4r5t": true, "qwerty12345": true, "iloveyou12": true,
	"administrator": true, "letmein123": true, "changeme123": true, "welcome123": true,
	"aaaaaaaaaa": true, "abcdefghij": true, "0000000000": true, "1111111111": true,
}

// CheckPassword enforces the password policy: at least MinPasswordLen
// characters, not trivially guessable, not the username.
func CheckPassword(username, password string) error {
	field := func(msg string) error {
		return &httpx.Err{Status: 400, Code: "bad_request", Message: msg, Field: "password"}
	}
	if utf8.RuneCountInString(password) < MinPasswordLen {
		return field(fmt.Sprintf("Use at least %d characters.", MinPasswordLen))
	}
	if len(password) > maxPasswordLen {
		return field("That password is too long.")
	}
	lower := strings.ToLower(password)
	if commonPasswords[lower] || (username != "" && strings.EqualFold(password, username)) {
		return field("That password is too easy to guess.")
	}
	distinct := map[rune]bool{}
	for _, r := range password {
		distinct[r] = true
	}
	if len(distinct) < 4 {
		return field("That password is too easy to guess.")
	}
	return nil
}

// user returns the account, or nil when none exists yet.
func (a *Accounts) user(ctx context.Context) (*user, error) {
	var u user
	var created, updated int64
	err := a.st.DB.QueryRowContext(ctx, `SELECT username, password_hash, webauthn_id, totp_secret, totp_pending, totp_last_step, created_at, updated_at FROM auth_user WHERE id=1`).
		Scan(&u.Username, &u.PasswordHash, &u.WebAuthnID, &u.TOTPSecret, &u.TOTPPending, &u.TOTPLastStep, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load account: %w", err)
	}
	u.CreatedAt, u.UpdatedAt = fromMS(created), fromMS(updated)
	return &u, nil
}

// HasUser reports whether the account has been created.
func (a *Accounts) HasUser(ctx context.Context) (bool, error) {
	u, err := a.user(ctx)
	return u != nil, err
}

// Username returns the account's username ("" before setup).
func (a *Accounts) Username(ctx context.Context) (string, error) {
	u, err := a.user(ctx)
	if err != nil || u == nil {
		return "", err
	}
	return u.Username, nil
}

// createUser inserts the account with an existing argon2id hash.
func (a *Accounts) createUser(ctx context.Context, username, hash string) error {
	now := ms(a.now())
	wid := make([]byte, 32)
	if _, err := rand.Read(wid); err != nil {
		return err
	}
	res, err := a.st.DB.ExecContext(ctx, `INSERT INTO auth_user(id, username, password_hash, webauthn_id, created_at, updated_at)
		VALUES(1,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, strings.TrimSpace(username), hash, wid, now, now)
	if err != nil {
		return fmt.Errorf("create account: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserExists
	}
	return nil
}

// SetPassword sets the account password, creating the account (with
// username) when none exists. A non-empty username on an existing account
// renames it. It reports whether the account was created. Callers should
// revoke sessions afterwards (see RevokeAllSessions).
func (a *Accounts) SetPassword(ctx context.Context, username, password string) (created bool, err error) {
	u, err := a.user(ctx)
	if err != nil {
		return false, err
	}
	name := strings.TrimSpace(username)
	if name == "" && u != nil {
		name = u.Username
	}
	if err := ValidateUsername(name); err != nil {
		return false, err
	}
	if err := CheckPassword(name, password); err != nil {
		return false, err
	}
	hash, err := secret.HashPassword(password)
	if err != nil {
		return false, fmt.Errorf("hash password: %w", err)
	}
	if u == nil {
		if err := a.createUser(ctx, name, hash); err != nil {
			return false, err
		}
		return true, nil
	}
	_, err = a.st.DB.ExecContext(ctx, `UPDATE auth_user SET username=?, password_hash=?, updated_at=? WHERE id=1`, name, hash, ms(a.now()))
	if err != nil {
		return false, fmt.Errorf("update password: %w", err)
	}
	return false, nil
}

func (a *Accounts) setPasswordHash(ctx context.Context, hash string) error {
	_, err := a.st.DB.ExecContext(ctx, `UPDATE auth_user SET password_hash=?, updated_at=? WHERE id=1`, hash, ms(a.now()))
	return err
}

// --- TOTP state -------------------------------------------------------------

func (a *Accounts) setTOTPPending(ctx context.Context, secretB32 string) error {
	_, err := a.st.DB.ExecContext(ctx, `UPDATE auth_user SET totp_pending=?, updated_at=? WHERE id=1`, secretB32, ms(a.now()))
	return err
}

func (a *Accounts) enableTOTP(ctx context.Context, secretB32 string, step int64) error {
	_, err := a.st.DB.ExecContext(ctx, `UPDATE auth_user SET totp_secret=?, totp_pending='', totp_last_step=?, updated_at=? WHERE id=1`, secretB32, step, ms(a.now()))
	return err
}

func (a *Accounts) disableTOTP(ctx context.Context) error {
	_, err := a.st.DB.ExecContext(ctx, `UPDATE auth_user SET totp_secret='', totp_pending='', totp_last_step=0, updated_at=? WHERE id=1`, ms(a.now()))
	return err
}

// claimTOTPStep atomically records step as used. It returns false when the
// step (or a later one) was already used: replay protection that also
// holds across concurrent requests.
func (a *Accounts) claimTOTPStep(ctx context.Context, step int64) (bool, error) {
	res, err := a.st.DB.ExecContext(ctx, `UPDATE auth_user SET totp_last_step=? WHERE id=1 AND totp_last_step < ?`, step, step)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// --- API tokens -------------------------------------------------------------

// CreateToken mints an API token. The plaintext is returned exactly once;
// only its SHA-256 is stored.
func (a *Accounts) CreateToken(ctx context.Context, name string) (*api.CreatedToken, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "Give the token a name.", Field: "name"}
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "Use 64 characters or fewer.", Field: "name"}
	}
	tok, err := secret.Token(TokenPrefix, 32)
	if err != nil {
		return nil, err
	}
	t := api.APIToken{ID: "tok_" + randomHex(8), Name: name, Prefix: tok[:len(TokenPrefix)+4], CreatedAt: a.now().Truncate(time.Millisecond)}
	_, err = a.st.DB.ExecContext(ctx, `INSERT INTO auth_tokens(id, name, prefix, token_hash, created_at) VALUES(?,?,?,?,?)`,
		t.ID, t.Name, t.Prefix, secret.HashToken(tok), ms(t.CreatedAt))
	if err != nil {
		return nil, fmt.Errorf("store token: %w", err)
	}
	return &api.CreatedToken{APIToken: t, Token: tok}, nil
}

// ListTokens returns every API token, newest first.
func (a *Accounts) ListTokens(ctx context.Context) ([]api.APIToken, error) {
	rows, err := a.st.DB.QueryContext(ctx, `SELECT id, name, prefix, created_at, last_used_at FROM auth_tokens ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer rows.Close()
	out := []api.APIToken{}
	for rows.Next() {
		var t api.APIToken
		var c, l int64
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &c, &l); err != nil {
			return nil, err
		}
		t.CreatedAt, t.LastUsedAt = fromMS(c), fromMS(l)
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeToken deletes a token by id. Unknown ids return a NotFound *httpx.Err.
func (a *Accounts) RevokeToken(ctx context.Context, id string) (*api.APIToken, error) {
	var t api.APIToken
	var c, l int64
	err := a.st.DB.QueryRowContext(ctx, `DELETE FROM auth_tokens WHERE id=? RETURNING id, name, prefix, created_at, last_used_at`, id).
		Scan(&t.ID, &t.Name, &t.Prefix, &c, &l)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("No token with that id.")
	}
	if err != nil {
		return nil, fmt.Errorf("revoke token: %w", err)
	}
	t.CreatedAt, t.LastUsedAt = fromMS(c), fromMS(l)
	return &t, nil
}

type tokenRow struct {
	ID       string
	Name     string
	LastUsed time.Time
}

func (a *Accounts) lookupToken(ctx context.Context, hash string) (*tokenRow, error) {
	var t tokenRow
	var l int64
	err := a.st.DB.QueryRowContext(ctx, `SELECT id, name, last_used_at FROM auth_tokens WHERE token_hash=?`, hash).Scan(&t.ID, &t.Name, &l)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.LastUsed = fromMS(l)
	return &t, nil
}

func (a *Accounts) touchToken(ctx context.Context, id string, at time.Time) error {
	_, err := a.st.DB.ExecContext(ctx, `UPDATE auth_tokens SET last_used_at=? WHERE id=? AND last_used_at<=?`, ms(at), id, ms(at.Add(-tokenTouchEvery)))
	return err
}

// --- Audit log --------------------------------------------------------------

// auditRetention is the number of audit rows kept.
const auditRetention = 5000

// Record appends an audit entry (At defaults to now).
func (a *Accounts) Record(ctx context.Context, e api.AuditEntry) error {
	if e.At.IsZero() {
		e.At = a.now()
	}
	_, err := a.st.DB.ExecContext(ctx, `INSERT INTO auth_audit(at, event, actor, ip, device, detail) VALUES(?,?,?,?,?,?)`,
		ms(e.At), clip(e.Event, 64), clip(e.Actor, 128), clip(e.IP, 64), clip(e.Device, 128), clip(e.Detail, 512))
	if err != nil {
		return fmt.Errorf("record audit: %w", err)
	}
	return nil
}

// Activity returns the newest audit entries.
func (a *Accounts) Activity(ctx context.Context, limit int) ([]api.AuditEntry, error) {
	rows, err := a.st.DB.QueryContext(ctx, `SELECT id, at, event, actor, ip, device, detail FROM auth_audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list activity: %w", err)
	}
	defer rows.Close()
	out := []api.AuditEntry{}
	for rows.Next() {
		var e api.AuditEntry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Event, &e.Actor, &e.IP, &e.Device, &e.Detail); err != nil {
			return nil, err
		}
		e.At = fromMS(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (a *Accounts) pruneAudit(ctx context.Context) error {
	_, err := a.st.DB.ExecContext(ctx, `DELETE FROM auth_audit WHERE id <= (SELECT id FROM auth_audit ORDER BY id DESC LIMIT 1 OFFSET ?)`, auditRetention)
	return err
}

// clip truncates s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
