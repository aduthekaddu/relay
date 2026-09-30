package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Health is the answer of /api/v1/health (server) or /v1/health (ptyd).
type Health struct {
	OK       bool   `json:"ok"`
	Version  string `json:"version"`
	Sessions int    `json:"sessions"`
}

// SocketHealth GETs path on a unix socket with a 3 second timeout.
func SocketHealth(ctx context.Context, socket, path string) (*Health, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://relay"+path, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("health: %s", res.Status)
	}
	var h Health
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&h); err != nil {
		return nil, fmt.Errorf("health: %w", err)
	}
	return &h, nil
}

// WaitHealthy polls the control socket until the server answers or the
// timeout passes.
func WaitHealthy(ctx context.Context, socket string, timeout time.Duration) (*Health, error) {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		h, err := SocketHealth(ctx, socket, "/api/v1/health")
		if err == nil {
			return h, nil
		}
		last = err
		if time.Now().After(deadline) {
			return nil, last
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}
