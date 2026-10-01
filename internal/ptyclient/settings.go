package ptyclient

import (
	"context"
	"net/http"

	"github.com/aduthekaddu/relay/internal/api"
)

// TerminalSettings is private to the owner-only daemon protocol. ConfigID
// proves that serve and ptyd use the same saved file and expansion home.
type TerminalSettings struct {
	api.TerminalDefaults
	ConfigID string `json:"configId"`
}

// Defaults asks the owning daemon about defaults for its next session.
// Old daemons return ErrNotFound; callers must not assume hot adoption.
func (c *Client) Defaults(ctx context.Context) (*TerminalSettings, error) {
	var out TerminalSettings
	if err := c.impl.doJSON(ctx, http.MethodGet, "/v1/settings", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
