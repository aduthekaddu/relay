package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
)

// passkeyRow is a stored WebAuthn credential.
type passkeyRow struct {
	ID         string
	Cred       webauthn.Credential
	Name       string
	CreatedAt  time.Time
	LastUsedAt time.Time
}

func (p *passkeyRow) toAPI() api.PasskeyDetail {
	out := api.PasskeyDetail{
		Passkey: api.Passkey{ID: p.ID, Name: p.Name, CreatedAt: p.CreatedAt, LastUsedAt: p.LastUsedAt},
		Synced:  p.Cred.Flags.BackupState,
	}
	for _, t := range p.Cred.Transport {
		out.Transports = append(out.Transports, string(t))
	}
	if g := p.Cred.Authenticator.AAGUID; len(g) == 16 && !bytes.Equal(g, make([]byte, 16)) {
		h := hex.EncodeToString(g)
		out.AAGUID = h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
	}
	return out
}

const passkeyCols = `id, credential_id, public_key, attestation_type, sign_count, aaguid, transports, user_present, user_verified, backup_eligible, backup_state, name, created_at, last_used_at`

func scanPasskey(sc interface{ Scan(...any) error }) (*passkeyRow, error) {
	var p passkeyRow
	var transports string
	var signCount int64
	var up, uv, be, bs bool
	var c, l int64
	if err := sc.Scan(&p.ID, &p.Cred.ID, &p.Cred.PublicKey, &p.Cred.AttestationType, &signCount, &p.Cred.Authenticator.AAGUID,
		&transports, &up, &uv, &be, &bs, &p.Name, &c, &l); err != nil {
		return nil, err
	}
	p.Cred.Authenticator.SignCount = uint32(signCount)
	for _, t := range strings.Split(transports, ",") {
		if t != "" {
			p.Cred.Transport = append(p.Cred.Transport, protocol.AuthenticatorTransport(t))
		}
	}
	p.Cred.Flags = webauthn.NewCredentialFlags(credentialFlags(up, uv, be, bs))
	p.CreatedAt, p.LastUsedAt = fromMS(c), fromMS(l)
	return &p, nil
}

func credentialFlags(up, uv, be, bs bool) protocol.AuthenticatorFlags {
	var f protocol.AuthenticatorFlags
	if up {
		f |= protocol.FlagUserPresent
	}
	if uv {
		f |= protocol.FlagUserVerified
	}
	if be {
		f |= protocol.FlagBackupEligible
	}
	if bs {
		f |= protocol.FlagBackupState
	}
	return f
}

func (a *Accounts) listPasskeys(ctx context.Context) ([]*passkeyRow, error) {
	rows, err := a.st.DB.QueryContext(ctx, `SELECT `+passkeyCols+` FROM auth_passkeys ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list passkeys: %w", err)
	}
	defer rows.Close()
	var out []*passkeyRow
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (a *Accounts) passkeyCount(ctx context.Context) (int, error) {
	var n int
	err := a.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_passkeys`).Scan(&n)
	return n, err
}

func (a *Accounts) passkeyByCredID(ctx context.Context, credID []byte) (*passkeyRow, error) {
	p, err := scanPasskey(a.st.DB.QueryRowContext(ctx, `SELECT `+passkeyCols+` FROM auth_passkeys WHERE credential_id=?`, credID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (a *Accounts) passkeyByID(ctx context.Context, id string) (*passkeyRow, error) {
	p, err := scanPasskey(a.st.DB.QueryRowContext(ctx, `SELECT `+passkeyCols+` FROM auth_passkeys WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("No passkey with that id.")
	}
	return p, err
}

func (a *Accounts) insertPasskey(ctx context.Context, p *passkeyRow) error {
	c := p.Cred
	ts := make([]string, 0, len(c.Transport))
	for _, t := range c.Transport {
		ts = append(ts, string(t))
	}
	_, err := a.st.DB.ExecContext(ctx, `INSERT INTO auth_passkeys(`+passkeyCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, c.ID, c.PublicKey, c.AttestationType, int64(c.Authenticator.SignCount), c.Authenticator.AAGUID, strings.Join(ts, ","),
		c.Flags.UserPresent, c.Flags.UserVerified, c.Flags.BackupEligible, c.Flags.BackupState, p.Name, ms(p.CreatedAt), ms(p.LastUsedAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return httpx.Conflict("That passkey is already registered.")
		}
		return fmt.Errorf("store passkey: %w", err)
	}
	return nil
}

// passkeyUsed records a successful assertion.
func (a *Accounts) passkeyUsed(ctx context.Context, id string, c *webauthn.Credential, at time.Time) error {
	_, err := a.st.DB.ExecContext(ctx, `UPDATE auth_passkeys SET sign_count=?, user_verified=?, backup_state=?, last_used_at=? WHERE id=?`,
		int64(c.Authenticator.SignCount), c.Flags.UserVerified, c.Flags.BackupState, ms(at), id)
	return err
}

func (a *Accounts) renamePasskey(ctx context.Context, id, name string) error {
	res, err := a.st.DB.ExecContext(ctx, `UPDATE auth_passkeys SET name=? WHERE id=?`, name, id)
	if err != nil {
		return fmt.Errorf("rename passkey: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.NotFound("No passkey with that id.")
	}
	return nil
}

func (a *Accounts) deletePasskey(ctx context.Context, id string) (*passkeyRow, error) {
	p, err := a.passkeyByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := a.st.DB.ExecContext(ctx, `DELETE FROM auth_passkeys WHERE id=?`, id); err != nil {
		return nil, fmt.Errorf("delete passkey: %w", err)
	}
	return p, nil
}

func validPasskeyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "Give the passkey a name.", Field: "name"}
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "Use 64 characters or fewer.", Field: "name"}
	}
	return name, nil
}

// waUser adapts the account to webauthn.User.
type waUser struct {
	u     *user
	creds []webauthn.Credential
}

func (w *waUser) WebAuthnID() []byte                         { return w.u.WebAuthnID }
func (w *waUser) WebAuthnName() string                       { return w.u.Username }
func (w *waUser) WebAuthnDisplayName() string                { return w.u.Username }
func (w *waUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }

// --- relying party -----------------------------------------------------------

// rpID returns the WebAuthn relying-party id for this installation and
// whether passkeys can work at all: the canonical origin must be HTTPS
// with a host name, or http(s)://localhost (a secure context). IP
// addresses cannot be RP ids.
func (s *Service) rpID() (string, bool) {
	origin, ok := server.NormalizeOrigin(s.d.Cfg.Origin())
	if !ok {
		return "", false
	}
	o, _ := url.Parse(origin)
	host := strings.ToLower(o.Hostname())
	if host == "" || net.ParseIP(host) != nil {
		return "", false
	}
	local := host == "localhost" || strings.HasSuffix(host, ".localhost")
	if o.Scheme != "https" && !local {
		return "", false
	}
	if d := strings.ToLower(strings.TrimSpace(s.d.Cfg.Server.Domain)); d != "" && (host == d || strings.HasSuffix(host, "."+d)) {
		return d, true
	}
	return host, true
}

// PasskeysAvailable reports whether WebAuthn can be used at the canonical
// origin.
func (s *Service) PasskeysAvailable() bool {
	_, ok := s.rpID()
	return ok
}

// passkeysAvailableAt also checks the accessed browser origin. A loopback IP
// alias can use password auth but cannot use the canonical hostname's RP ID.
func (s *Service) passkeysAvailableAt(r *http.Request) bool {
	rp, ok := s.rpID()
	if !ok {
		return false
	}
	origin, ok := server.ExternalOrigin(r)
	return ok && server.OriginInList(origin, s.origins()) && passkeyOrigin(origin, rp)
}

func passkeyOrigin(origin, rp string) bool {
	u, _ := url.Parse(origin) // callers supply normalized origins
	host := u.Hostname()
	if net.ParseIP(host) != nil || host != rp && !strings.HasSuffix(host, "."+rp) {
		return false
	}
	return u.Scheme == "https" || host == "localhost" || strings.HasSuffix(host, ".localhost")
}

func (s *Service) webAuthnAt(r *http.Request) (*webauthn.WebAuthn, error) {
	if !s.passkeysAvailableAt(r) {
		return nil, httpx.Unavailable("Passkeys need Relay's canonical HTTPS hostname, or a canonical localhost address. IP addresses cannot use passkeys.")
	}
	return s.webAuthn()
}

// webAuthn builds the relying party for the current configuration. The
// accepted origins are the allowed browser origins within the RP id.
func (s *Service) webAuthn() (*webauthn.WebAuthn, error) {
	rp, ok := s.rpID()
	if !ok {
		return nil, httpx.Unavailable("Passkeys need Relay on HTTPS with a domain name, or on localhost.")
	}
	var origins []string
	for _, o := range s.origins() {
		norm, ok := server.NormalizeOrigin(strings.TrimSuffix(o, "/"))
		if !ok {
			continue
		}
		if passkeyOrigin(norm, rp) {
			origins = append(origins, norm)
		}
	}
	if len(origins) == 0 {
		return nil, httpx.Unavailable("Passkeys aren't available at this address.")
	}
	w, err := webauthn.New(&webauthn.Config{
		RPID:          rp,
		RPDisplayName: "Relay",
		RPOrigins:     origins,
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: ceremonyTTL, TimeoutUVD: ceremonyTTL},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: ceremonyTTL, TimeoutUVD: ceremonyTTL},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("webauthn config: %w", err)
	}
	return w, nil
}

// discoverableHandler resolves the credential presented at passkey sign-in.
// The user handle must match the account's WebAuthn id.
func (s *Service) discoverableHandler(ctx context.Context, found **passkeyRow) webauthn.DiscoverableUserHandler {
	return func(rawID, userHandle []byte) (webauthn.User, error) {
		u, err := s.acc.user(ctx)
		if err != nil || u == nil {
			return nil, errors.New("no account")
		}
		if !bytes.Equal(userHandle, u.WebAuthnID) {
			return nil, errors.New("unknown user handle")
		}
		p, err := s.acc.passkeyByCredID(ctx, rawID)
		if err != nil || p == nil {
			return nil, errors.New("unknown credential")
		}
		*found = p
		return &waUser{u: u, creds: []webauthn.Credential{p.Cred}}, nil
	}
}
