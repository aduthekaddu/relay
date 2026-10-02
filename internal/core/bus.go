package core

// Backend-only bus topics. They connect features without imports and are
// never forwarded to browsers by internal/live.
const (
	// BusAudit carries an AuditEvent (value or pointer). Publish after a
	// destructive or security-relevant action; internal/auth records it in
	// the audit log shown under Settings → Security → Activity.
	BusAudit = "audit"
	// BusClipCapture carries a ClipCapture: text for the universal
	// clipboard that arrived from a non-HTTP source (OSC 52, desktop).
	BusClipCapture = "clip.capture"
	// BusSessionRevoked carries a SessionRevoked. internal/auth publishes
	// it after browser-session or API-token revocation so
	// long-lived connections (WebSockets) authenticated by them can close.
	BusSessionRevoked = "auth.session.revoked"
)

// AuditEvent is the payload of BusAudit.
type AuditEvent struct {
	Event  string // dotted name, e.g. "file.delete", "process.kill", "git.discard"
	Actor  string // principal user, "local" or "system"
	IP     string // client IP when the action came from HTTP
	Detail string // short human-readable detail; never secrets or file contents
}

// ClipCapture is the payload of BusClipCapture.
type ClipCapture struct {
	Text   string
	Source string // "terminal" | "desktop" | ...
	// SessionID is the terminal session that produced the text, if any.
	SessionID string
}

// SessionRevoked is the payload of BusSessionRevoked. SessionIDs are the
// public session ids (server.Principal.SessionID). TokenIDs are public
// API-token ids (server.Principal.TokenID). No credential values or hashes.
type SessionRevoked struct {
	SessionIDs []string
	TokenIDs   []string
}

// IsBackendTopic reports whether t is a backend-only bus topic that must
// not be streamed to browsers.
func IsBackendTopic(t string) bool {
	switch t {
	case BusAudit, BusClipCapture, BusSessionRevoked:
		return true
	}
	return false
}

// Presence tells features what connected browsers are looking at, so they
// can avoid redundant work or notifications. Implemented by internal/live.
type Presence interface {
	// Watching reports whether a visible browser is showing the terminal
	// with this id (route /terminal/<id>).
	Watching(terminalID string) bool
	// Online reports whether at least one browser has the app open and
	// visible.
	Online() bool
	// Subscribed reports whether any visible connected browser subscribed
	// to the topic (e.g. "metrics"). Hidden pages do not keep high-frequency
	// streams active.
	Subscribed(topic string) bool
}
