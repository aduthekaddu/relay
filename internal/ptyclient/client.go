// Package ptyclient talks to the ptyd session daemon over its owner-only
// unix socket. ptyd owns every terminal process so the web server can be
// restarted or upgraded without killing anyone's shell or agent.
//
// The wire protocol is HTTP + WebSocket over the unix socket; see
// docs/dev/PTYD.md. This package is the only thing outside internal/ptyd
// that knows about it.
package ptyclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/api"
)

// ErrUnavailable is returned when the daemon socket cannot be reached.
var ErrUnavailable = errors.New("ptyd unavailable")

// ErrNotFound is returned for unknown session ids.
var ErrNotFound = errors.New("terminal session not found")

// StatusError is a non-2xx answer from the daemon other than 404. Status,
// Code and Message come from the standard error envelope and are safe to
// show to users.
type StatusError struct {
	Status  int
	Code    string
	Message string
}

func (e *StatusError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("ptyd: %s", http.StatusText(e.Status))
}

// CreateSpec describes a new session. Command empty = login shell.
type CreateSpec struct {
	api.CreateTerminalRequest
	// Env is merged over the daemon's environment. RELAY_SESSION,
	// RELAY_SOCKET, TERM, COLORTERM and TERM_PROGRAM are always set by ptyd.
	ExtraEnv map[string]string
	// Scrollback overrides the replay buffer size in bytes (0 = default).
	Scrollback int
	// AgentSessionID links the session to a known agent transcript.
	AgentSessionID string
	// Workspace is the resolved workspace root for grouping in the UI.
	Workspace string
}

// AttachOptions control a WebSocket attach.
type AttachOptions struct {
	Cols, Rows int
	Replay     bool // send the scrollback ring buffer first
	ReadOnly   bool // drop client input (share links, previews)
}

// PtyEvent is a lifecycle/activity event from the daemon.
type PtyEvent struct {
	Type    string               `json:"type"` // created | updated | exited | removed | notify | bell | clip | open
	Session *api.TerminalSession `json:"session,omitempty"`
	ID      string               `json:"id,omitempty"`
	Title   string               `json:"title,omitempty"` // notify
	Body    string               `json:"body,omitempty"`  // notify
	Text    string               `json:"text,omitempty"`  // clip (OSC 52)
	Path    string               `json:"path,omitempty"`  // open
}

// Client is safe for concurrent use.
type Client struct {
	socket string
	impl   *httpImpl
}

// New returns a client for the daemon listening on socket.
func New(socket string) *Client {
	return &Client{socket: socket, impl: newImpl(socket)}
}

// Socket returns the daemon socket path.
func (c *Client) Socket() string { return c.socket }

// Health reports nil when the daemon answers.
func (c *Client) Health(ctx context.Context) error { return c.impl.Health(ctx) }

// List returns every session (live first, then exited).
func (c *Client) List(ctx context.Context) ([]api.TerminalSession, error) {
	return c.impl.List(ctx)
}

// Get returns one session.
func (c *Client) Get(ctx context.Context, id string) (*api.TerminalSession, error) {
	return c.impl.Get(ctx, id)
}

// Create starts a session.
func (c *Client) Create(ctx context.Context, spec CreateSpec) (*api.TerminalSession, error) {
	return c.impl.Create(ctx, spec)
}

// Update renames or (un)pins a session.
func (c *Client) Update(ctx context.Context, id string, req api.UpdateTerminalRequest) (*api.TerminalSession, error) {
	return c.impl.Update(ctx, id, req)
}

// Kill signals the session's process group. signal: TERM, KILL, INT, HUP,
// QUIT, USR1, USR2; empty closes the terminal (HUP, TERM after 2 s, KILL
// after 5 s).
func (c *Client) Kill(ctx context.Context, id, signal string) error {
	return c.impl.Kill(ctx, id, signal)
}

// Remove forgets an exited session (kills it first if still running).
func (c *Client) Remove(ctx context.Context, id string) error { return c.impl.Remove(ctx, id) }

// Input writes raw bytes to the session's pty.
func (c *Client) Input(ctx context.Context, id string, data []byte) error {
	return c.impl.Input(ctx, id, data, false)
}

// Paste writes text as a paste: wrapped in bracketed-paste markers when
// the application enabled them, with embedded end markers removed.
func (c *Client) Paste(ctx context.Context, id string, data []byte) error {
	return c.impl.Input(ctx, id, data, true)
}

// Resize sets the pty size (the HTTP caller becomes the size owner).
func (c *Client) Resize(ctx context.Context, id string, cols, rows int) error {
	return c.impl.Resize(ctx, id, cols, rows)
}

// Snapshot returns the last lines of the screen as plain text.
func (c *Client) Snapshot(ctx context.Context, id string, lines int) (*api.TerminalSnapshot, error) {
	return c.impl.Snapshot(ctx, id, lines)
}

// Attach opens the session stream. The returned conn speaks the protocol
// documented on api.TermServerMsg / api.TermClientMsg.
func (c *Client) Attach(ctx context.Context, id string, opts AttachOptions) (*websocket.Conn, error) {
	return c.impl.Attach(ctx, id, opts)
}

// Events streams daemon events until ctx is cancelled. The channel is
// closed when the connection drops; callers should reconnect.
func (c *Client) Events(ctx context.Context) (<-chan PtyEvent, error) { return c.impl.Events(ctx) }

// SetAttention marks (a != nil) or clears (a == nil) "needs you".
func (c *Client) SetAttention(ctx context.Context, id string, a *api.Attention) error {
	return c.impl.SetAttention(ctx, id, a)
}

// SetMeta merges meta into the session's metadata (empty values delete).
func (c *Client) SetMeta(ctx context.Context, id string, meta map[string]string) error {
	return c.impl.SetMeta(ctx, id, meta)
}

// Recording returns the asciicast v2 stream for a recorded session.
func (c *Client) Recording(ctx context.Context, id string) (io.ReadCloser, error) {
	return c.impl.Recording(ctx, id)
}

// Restore starts a new session with the command, cwd and environment of
// an exited (or lost) one. The new session's meta has restoredFrom=<id>.
func (c *Client) Restore(ctx context.Context, id string) (*api.TerminalSession, error) {
	return c.impl.Restore(ctx, id)
}
