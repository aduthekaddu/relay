package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
)

// ControlSocket returns the path of the running server's control socket:
// $RELAY_SOCKET when set (inside Relay terminals), else the default path.
func ControlSocket() string {
	if s := os.Getenv("RELAY_SOCKET"); s != "" {
		return s
	}
	p, err := config.ResolvePaths()
	if err != nil {
		return ""
	}
	return p.CtlSocket
}

// LocalHTTP returns an http.Client that dials the control socket. Requests
// sent through it are authenticated as the local owner.
func LocalHTTP() *http.Client {
	sock := ControlSocket()
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
}

// CallLocal sends a JSON request to the server over the control socket.
// path is an absolute API path, e.g. "/api/v1/notify". body and out may be
// nil. Non-2xx responses become errors carrying the server's message.
func CallLocal(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://relay"+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := LocalHTTP().Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach the Relay server (is `relay serve` running?): %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var eb api.ErrorBody
		b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if json.Unmarshal(b, &eb) == nil && eb.Error.Message != "" {
			return fmt.Errorf("%s", eb.Error.Message)
		}
		return fmt.Errorf("server returned %s", res.Status)
	}
	if out != nil && res.StatusCode != http.StatusNoContent {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}
