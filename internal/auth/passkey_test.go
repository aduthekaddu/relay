package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
)

// virtualAuthenticator is a minimal ES256 platform authenticator that
// produces "none" attestations and assertions, like a phone's passkey
// manager would.
type virtualAuthenticator struct {
	t          *testing.T
	key        *ecdsa.PrivateKey
	credID     []byte
	userHandle []byte
	origin     string
	count      uint32
	flags      protocol.AuthenticatorFlags
}

func newVirtualAuthenticator(t *testing.T, origin string) *virtualAuthenticator {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &virtualAuthenticator{t: t, key: k, credID: id, origin: origin,
		flags: protocol.FlagUserPresent | protocol.FlagUserVerified | protocol.FlagBackupEligible | protocol.FlagBackupState}
}

var b64 = base64.RawURLEncoding

func (a *virtualAuthenticator) clientData(typ, challenge string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	return b
}

func (a *virtualAuthenticator) authData(rpID string, attested []byte) []byte {
	h := sha256.Sum256([]byte(rpID))
	d := append([]byte{}, h[:]...)
	f := a.flags
	if attested != nil {
		f |= protocol.FlagAttestedCredentialData
	}
	d = append(d, byte(f))
	d = binary.BigEndian.AppendUint32(d, a.count)
	return append(d, attested...)
}

// create answers navigator.credentials.create() for the given options JSON.
func (a *virtualAuthenticator) create(optionsJSON []byte) []byte {
	a.t.Helper()
	var opts struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			RP        struct {
				ID string `json:"id"`
			} `json:"rp"`
			User struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(optionsJSON, &opts); err != nil {
		a.t.Fatalf("creation options: %v", err)
	}
	uh, err := b64.DecodeString(opts.PublicKey.User.ID)
	if err != nil {
		a.t.Fatalf("user id: %v", err)
	}
	a.userHandle = uh
	cose, err := webauthncbor.Marshal(map[int64]any{
		1: int64(webauthncose.EllipticKey), 3: int64(webauthncose.AlgES256), -1: int64(webauthncose.P256),
		-2: a.key.X.FillBytes(make([]byte, 32)), -3: a.key.Y.FillBytes(make([]byte, 32)),
	})
	if err != nil {
		a.t.Fatal(err)
	}
	att := make([]byte, 16) // zero AAGUID
	att = binary.BigEndian.AppendUint16(att, uint16(len(a.credID)))
	att = append(att, a.credID...)
	att = append(att, cose...)
	obj, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(opts.PublicKey.RP.ID, att)})
	if err != nil {
		a.t.Fatal(err)
	}
	id := b64.EncodeToString(a.credID)
	body, _ := json.Marshal(map[string]any{
		"id": id, "rawId": id, "type": "public-key", "authenticatorAttachment": "platform",
		"response": map[string]any{
			"attestationObject": b64.EncodeToString(obj),
			"clientDataJSON":    b64.EncodeToString(a.clientData("webauthn.create", opts.PublicKey.Challenge)),
			"transports":        []string{"internal", "hybrid"},
		},
		"clientExtensionResults": map[string]any{},
	})
	return body
}

// get answers navigator.credentials.get() (discoverable) for options JSON.
func (a *virtualAuthenticator) get(optionsJSON []byte) []byte {
	a.t.Helper()
	var opts struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			RPID      string `json:"rpId"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(optionsJSON, &opts); err != nil {
		a.t.Fatalf("request options: %v", err)
	}
	a.count++
	ad := a.authData(opts.PublicKey.RPID, nil)
	cd := a.clientData("webauthn.get", opts.PublicKey.Challenge)
	cdh := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), cdh[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		a.t.Fatal(err)
	}
	id := b64.EncodeToString(a.credID)
	body, _ := json.Marshal(map[string]any{
		"id": id, "rawId": id, "type": "public-key",
		"response": map[string]any{
			"authenticatorData": b64.EncodeToString(ad),
			"clientDataJSON":    b64.EncodeToString(cd),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(a.userHandle),
		},
		"clientExtensionResults": map[string]any{},
	})
	return body
}

func TestPasskeyRegisterAndSignIn(t *testing.T) {
	e := newEnv(t, "localhost")
	owner := e.setup()
	va := newVirtualAuthenticator(t, e.origin)

	var st api.AuthStateResponse
	owner.expect(200, "GET", "/api/v1/auth/state", nil).decode(t, &st)
	if !st.PasskeysAvailable || st.Methods.Passkey {
		t.Fatalf("state before registration = %+v", st)
	}

	// Register.
	opts := owner.expect(200, "POST", "/api/v1/auth/passkeys/begin", api.NameRequest{Name: "Test phone"})
	var co struct {
		PublicKey struct {
			RP struct {
				ID string `json:"id"`
			} `json:"rp"`
			AuthenticatorSelection struct {
				ResidentKey string `json:"residentKey"`
			} `json:"authenticatorSelection"`
		} `json:"publicKey"`
	}
	opts.decode(t, &co)
	if co.PublicKey.RP.ID != "localhost" || co.PublicKey.AuthenticatorSelection.ResidentKey != "required" {
		t.Fatalf("creation options = %s", opts.body)
	}
	var pk api.PasskeyDetail
	owner.expect(201, "POST", "/api/v1/auth/passkeys/finish", va.create(opts.body)).decode(t, &pk)
	if pk.Name != "Test phone" || pk.ID == "" || !pk.Synced || len(pk.Transports) != 2 {
		t.Fatalf("passkey = %+v", pk)
	}
	// The ceremony is single use.
	owner.expect(400, "POST", "/api/v1/auth/passkeys/finish", va.create(opts.body))

	// Sign in on a fresh browser with the passkey (discoverable: no username).
	b := e.browser(uaIPhone)
	req := b.expect(200, "POST", "/api/v1/auth/passkey/begin", nil)
	var ro struct {
		PublicKey struct {
			Challenge        string `json:"challenge"`
			RPID             string `json:"rpId"`
			UserVerification string `json:"userVerification"`
			AllowCredentials []any  `json:"allowCredentials"`
		} `json:"publicKey"`
	}
	req.decode(t, &ro)
	if ro.PublicKey.RPID != "localhost" || ro.PublicKey.Challenge == "" || ro.PublicKey.UserVerification != "preferred" || len(ro.PublicKey.AllowCredentials) != 0 {
		t.Fatalf("request options = %s", req.body)
	}
	var lr api.LoginResponse
	b.expect(200, "POST", "/api/v1/auth/passkey/finish", va.get(req.body)).decode(t, &lr)
	if !lr.OK {
		t.Fatalf("passkey login = %+v", lr)
	}
	var list []api.DeviceSession
	b.expect(200, "GET", "/api/v1/auth/sessions", nil).decode(t, &list)
	methods := map[string]int{}
	for _, s := range list {
		methods[s.Method]++
	}
	if methods["passkey"] != 1 {
		t.Fatalf("sessions = %+v", list)
	}

	// Replaying the same assertion fails (challenge consumed).
	b2 := e.browser(uaIPhone)
	b2.expect(400, "POST", "/api/v1/auth/passkey/finish", va.get(req.body))

	// A different authenticator is refused.
	req2 := b2.expect(200, "POST", "/api/v1/auth/passkey/begin", nil)
	stranger := newVirtualAuthenticator(t, e.origin)
	stranger.userHandle = va.userHandle
	b2.expect(401, "POST", "/api/v1/auth/passkey/finish", stranger.get(req2.body))

	// Wrong origin in client data is refused.
	req3 := b2.expect(200, "POST", "/api/v1/auth/passkey/begin", nil)
	va.origin = "https://evil.example"
	b2.expect(401, "POST", "/api/v1/auth/passkey/finish", va.get(req3.body))
	va.origin = e.origin

	// Manage: list, rename, delete.
	var pks []api.PasskeyDetail
	owner.expect(200, "GET", "/api/v1/auth/passkeys", nil).decode(t, &pks)
	if len(pks) != 1 || pks[0].LastUsedAt.IsZero() {
		t.Fatalf("passkeys = %+v", pks)
	}
	owner.expect(200, "PATCH", "/api/v1/auth/passkeys/"+pk.ID, api.NameRequest{Name: "Work phone"}).decode(t, &pk)
	if pk.Name != "Work phone" {
		t.Fatalf("rename = %+v", pk)
	}
	owner.expect(400, "PATCH", "/api/v1/auth/passkeys/"+pk.ID, api.NameRequest{Name: "  "})
	owner.expect(204, "DELETE", "/api/v1/auth/passkeys/"+pk.ID, nil)
	owner.expect(404, "DELETE", "/api/v1/auth/passkeys/"+pk.ID, nil)

	req4 := b2.expect(200, "POST", "/api/v1/auth/passkey/begin", nil)
	b2.expect(401, "POST", "/api/v1/auth/passkey/finish", va.get(req4.body))

	acts := activity(t, owner)
	for _, ev := range []string{"passkey.add", "passkey.fail", "passkey.remove"} {
		if !hasEvent(acts, ev) {
			t.Errorf("activity lacks %s", ev)
		}
	}
}

func TestPasskeysUnavailableOnIPOrigin(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	c := e.setup()
	var st api.AuthStateResponse
	c.expect(200, "GET", "/api/v1/auth/state", nil).decode(t, &st)
	if st.PasskeysAvailable {
		t.Fatal("passkeys can't work on an IP origin")
	}
	r := c.expect(503, "POST", "/api/v1/auth/passkeys/begin", nil)
	if r.errCode(t) != "unavailable" {
		t.Fatalf("code = %s", r.body)
	}
	e.browser("").expect(503, "POST", "/api/v1/auth/passkey/begin", nil)
	e.browser("").expect(400, "POST", "/api/v1/auth/passkey/finish", []byte(`{}`))
}

func TestRPID(t *testing.T) {
	tests := []struct {
		name   string
		mut    func(*config.Config)
		want   string
		wantOK bool
	}{
		{"default loopback IP", func(c *config.Config) {}, "", false},
		{"localhost", func(c *config.Config) { c.Server.Listen = "localhost:7777" }, "localhost", true},
		{"https public url", func(c *config.Config) { c.Server.PublicURL = "https://relay.example.ts.net" }, "relay.example.ts.net", true},
		{"plain http domain", func(c *config.Config) { c.Server.PublicURL = "http://relay.example.com" }, "", false},
		{"domain with auto TLS", func(c *config.Config) { c.Server.Domain = "dev.example.com"; c.Server.Listen = ":443" }, "dev.example.com", true},
		{"public url under domain", func(c *config.Config) {
			c.Server.Domain = "example.com"
			c.Server.PublicURL = "https://relay.example.com"
		}, "example.com", true},
		{"https IP", func(c *config.Config) { c.Server.PublicURL = "https://192.0.2.1" }, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			tt.mut(cfg)
			s := &Service{d: newDepsWith(cfg)}
			got, ok := s.rpID()
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("rpID = %q,%v; want %q,%v (origin %s)", got, ok, tt.want, tt.wantOK, cfg.Origin())
			}
		})
	}
}
