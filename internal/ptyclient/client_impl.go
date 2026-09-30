package ptyclient

import (
	"context"
	"io"

	"github.com/coder/websocket"

	"github.com/aduthekaddu/relay/internal/api"
)

// stubImpl is a placeholder until the real implementation lands. Every
// call reports ErrUnavailable.
type stubImpl struct{}

func newImpl(string) clientImpl { return stubImpl{} }

func (stubImpl) Health(context.Context) error                        { return ErrUnavailable }
func (stubImpl) List(context.Context) ([]api.TerminalSession, error) { return nil, ErrUnavailable }
func (stubImpl) Get(context.Context, string) (*api.TerminalSession, error) {
	return nil, ErrUnavailable
}
func (stubImpl) Create(context.Context, CreateSpec) (*api.TerminalSession, error) {
	return nil, ErrUnavailable
}
func (stubImpl) Update(context.Context, string, api.UpdateTerminalRequest) (*api.TerminalSession, error) {
	return nil, ErrUnavailable
}
func (stubImpl) Kill(context.Context, string, string) error     { return ErrUnavailable }
func (stubImpl) Remove(context.Context, string) error           { return ErrUnavailable }
func (stubImpl) Input(context.Context, string, []byte) error    { return ErrUnavailable }
func (stubImpl) Resize(context.Context, string, int, int) error { return ErrUnavailable }
func (stubImpl) Snapshot(context.Context, string, int) (*api.TerminalSnapshot, error) {
	return nil, ErrUnavailable
}
func (stubImpl) Attach(context.Context, string, AttachOptions) (*websocket.Conn, error) {
	return nil, ErrUnavailable
}
func (stubImpl) Events(context.Context) (<-chan PtyEvent, error) { return nil, ErrUnavailable }
func (stubImpl) SetAttention(context.Context, string, *api.Attention) error {
	return ErrUnavailable
}
func (stubImpl) SetMeta(context.Context, string, map[string]string) error { return ErrUnavailable }
func (stubImpl) Recording(context.Context, string) (io.ReadCloser, error) {
	return nil, ErrUnavailable
}
