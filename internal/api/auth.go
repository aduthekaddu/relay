package api

// Auth contract additions (mirrored in web/src/api/auth.ts).

// AuthStateResponse is returned by GET /api/v1/auth/state: AuthState plus
// first-run details.
type AuthStateResponse struct {
	AuthState
	// SetupCodeRequired is true during first-run setup when the request is
	// not from the machine itself: the setup code printed by `relay serve`
	// (and stored in <data>/setup-code) must be sent with SetupRequest.
	SetupCodeRequired bool `json:"setupCodeRequired,omitempty"`
	// PasskeysAvailable reports whether WebAuthn can be used at this origin.
	PasskeysAvailable bool `json:"passkeysAvailable"`
}

// SetupRequest is the body of POST /api/v1/auth/setup.
type SetupRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Code     string `json:"code,omitempty"` // one-time setup code (see SetupCodeRequired)
	Remember bool   `json:"remember,omitempty"`
}

// NameRequest is {name} (passkey register/rename, token create).
type NameRequest struct {
	Name string `json:"name"`
}

// CodeRequest is {code} (TOTP enable/disable).
type CodeRequest struct {
	Code string `json:"code"`
}

// TOTPStatus is returned by GET /api/v1/auth/totp.
type TOTPStatus struct {
	Enabled bool `json:"enabled"`
}

// RevokedCount is returned by POST /api/v1/auth/sessions/revoke-others.
type RevokedCount struct {
	Revoked int `json:"revoked"`
}

// PasskeyDetail extends Passkey with authenticator metadata shown in
// Settings → Security. Returned by the passkey list/register/rename routes.
type PasskeyDetail struct {
	Passkey
	Synced     bool     `json:"synced"`               // backup state (iCloud Keychain, Google Password Manager…)
	Transports []string `json:"transports,omitempty"` // internal, hybrid, usb, nfc, ble
	AAGUID     string   `json:"aaguid,omitempty"`     // authenticator model id (UUID)
}
