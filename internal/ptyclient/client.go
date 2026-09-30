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
	"io"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/api"
)

// ErrUnavailable is returned when the daemon socket cannot be reached.
var ErrUnavailable = errors.New("ptyd unavailable")

// ErrNotFound is returned for unknown session ids.
var ErrNotFound = errors.New("terminal session not found")

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
	impl   clientImpl
}

// clientImpl is implemented in client_impl.go by the ptyd owner. Keeping
// the exported surface here stable lets other packages compile against it.
type clientImpl interface {
	Health(ctx context.Context) error
	List(ctx context.Context) ([]api.TerminalSession, error)
	Get(ctx context.Context, id string) (*api.TerminalSession, error)
	Create(ctx context.Context, spec CreateSpec) (*api.TerminalSession, error)
	Update(ctx context.Context, id string, req api.UpdateTerminalRequest) (*api.TerminalSession, error)
	Kill(ctx context.Context, id, signal string) error
	Remove(ctx context.Context, id string) error
	Input(ctx context.Context, id string, data []byte) error
	Resize(ctx context.Context, id string, cols, rows int) error
	Snapshot(ctx context.Context, id string, lines int) (*api.TerminalSnapshot, error)
	Attach(ctx context.Context, id string, opts AttachOptions) (*websocket.Conn, error)
	Events(ctx context.Context) (<-chan PtyEvent, error)
	SetAttention(ctx context.Context, id string, a *api.Attention) error
	SetMeta(ctx context.Context, id string, meta map[string]string) error
	Recording(ctx context.Context, id string) (io.ReadCloser, error)
}

// New returns a client for the daemon listening on socket.
func New(socket string) *Client {
	return &Client{socket: socket, impl: newImpl(socket)}
}

// Socket returns the daemon socket path.
func (c *Client) Socket() string { return c.socket }

func (c *Client) Health(ctx context.Context) error { return c.impl.Health(ctx) }
func (c *Client) List(ctx context.Context) ([]api.TerminalSession, error) {
	return c.impl.List(ctx)
}
func (c *Client) Get(ctx context.Context, id string) (*api.TerminalSession, error) {
	return c.impl.Get(ctx, id)
}
func (c *Client) Create(ctx context.Context, spec CreateSpec) (*api.TerminalSession, error) {
	return c.impl.Create(ctx, spec)
}
func (c *Client) Update(ctx context.Context, id string, req api.UpdateTerminalRequest) (*api.TerminalSession, error) {
	return c.impl.Update(ctx, id, req)
}

// Kill signals the session's process group. signal: TERM (default), KILL, INT, HUP.
func (c *Client) Kill(ctx context.Context, id, signal string) error {
	return c.impl.Kill(ctx, id, signal)
}

// Remove forgets an exited session (kills it first if still running).
func (c *Client) Remove(ctx context.Context, id string) error { return c.impl.Remove(ctx, id) }
func (c *Client) Input(ctx context.Context, id string, data []byte) error {
	return c.impl.Input(ctx, id, data)
}
func (c *Client) Resize(ctx context.Context, id string, cols, rows int) error {
	return c.impl.Resize(ctx, id, cols, rows)
}
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
func (c *Client) SetMeta(ctx context.Context, id string, meta map[string]string) error {
	return c.impl.SetMeta(ctx, id, meta)
}

// Recording returns the asciicast v2 stream for a recorded session.
func (c *Client) Recording(ctx context.Context, id string) (io.ReadCloser, error) {
	return c.impl.Recording(ctx, id)
}
