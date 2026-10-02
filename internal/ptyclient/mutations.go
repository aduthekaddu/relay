package ptyclient

import (
	"context"
	"time"
)

// DeleteSpec and DeleteResult belong to the private daemon protocol. Changed
// distinguishes a delivered signal/removed record from an idempotent no-op.
type DeleteSpec struct {
	Signal string `json:"signal,omitempty"`
	Forget bool   `json:"forget,omitempty"`
}

type DeleteResult struct {
	Changed bool `json:"changed"`
}

// Delete requests a mutation with an authoritative outcome for auditing.
// The separate endpoint makes older daemons reject it before any mutation.
// Neither this method nor the compatibility Kill/Remove wrappers publishes
// audit events; the authenticated terminal handler owns publication.
func (c *Client) Delete(ctx context.Context, id string, spec DeleteSpec) (*DeleteResult, error) {
	if spec.Forget {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
	}
	var result DeleteResult
	if err := c.impl.doJSON(ctx, "POST", sessionPath(id, "/delete"), spec, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
