package previews

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/aduthekaddu/relay/internal/server"
)

// No listener exists: 404 means admission reached listener lookup; 403 means
// the Origin guard refused admission before any upstream forwarding.
func TestPathWebSocketOriginGuard(t *testing.T) {
	tests := []struct {
		name, auth, origin, fetchSite string
		ws, local                     bool
		want                          int
	}{
		{"cookie allowed", "cookie", "http://relay.test", "", true, false, 404},
		{"cookie other origin", "cookie", "http://other.test", "", true, false, 403},
		{"cookie wrong port", "cookie", "http://relay.test:47780", "", true, false, 403},
		{"cookie missing origin", "cookie", "", "", true, false, 403},
		{"cookie null origin", "cookie", "null", "", true, false, 403},
		{"cookie malformed origin", "cookie", "://", "", true, false, 403},
		{"cookie cross site", "cookie", "http://relay.test", "cross-site", true, false, 403},
		{"cookie same site", "cookie", "http://relay.test", "same-site", true, false, 404},
		{"token missing origin", "token", "", "", true, false, 404},
		{"token other origin", "token", "http://other.test", "cross-site", true, false, 404},
		{"local missing origin", "", "", "", true, true, 404},
		{"anonymous upgrade", "", "http://relay.test", "", true, false, 401},
		{"cookie ordinary HTTP", "cookie", "", "", false, false, 404},
	}
	for _, mode := range []string{"path", "subdomain"} {
		for _, tt := range tests {
			t.Run(mode+"/"+tt.name, func(t *testing.T) {
				h := newHarness(t, mode, "relay.test", "http://relay.test")
				r := httptest.NewRequest(http.MethodGet, "http://relay.test/p/47780/hmr", nil)
				if tt.ws {
					r.Header.Set("Connection", "keep-alive")
					r.Header.Add("Connection", "UpGrAdE")
					r.Header.Set("Upgrade", "WebSocket")
				}
				if tt.auth == "cookie" {
					r.Header.Set("Cookie", "relay_session=good")
				}
				if tt.auth == "token" {
					r.Header.Set("Authorization", "Bearer rly_good")
				}
				if tt.local {
					r = r.WithContext(server.MarkLocal(r.Context()))
				}
				r.Header.Set("Origin", tt.origin)
				if tt.fetchSite != "" {
					r.Header.Set("Sec-Fetch-Site", tt.fetchSite)
				}
				w := httptest.NewRecorder()
				h.h.ServeHTTP(w, r)
				if w.Code != tt.want {
					t.Fatalf("status = %d, want %d", w.Code, tt.want)
				}
			})
		}
	}
}

func TestPathProtocolListOriginGuard(t *testing.T) {
	for _, upgrade := range []string{"websocket, example", "example, websocket", "example"} {
		t.Run(upgrade, func(t *testing.T) {
			h := newHarness(t, "path", "", "http://relay.test")
			r := httptest.NewRequest(http.MethodGet, "http://relay.test/p/47780/hmr", nil)
			r.Header.Set("Cookie", "relay_session=good")
			r.Header.Set("Connection", "keep-alive, Upgrade")
			r.Header.Set("Upgrade", upgrade)
			w := httptest.NewRecorder()
			h.h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", w.Code)
			}
			r.Header.Set("Origin", "http://relay.test")
			w = httptest.NewRecorder()
			h.h.ServeHTTP(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatalf("allowed origin: status = %d, want 404", w.Code)
			}
			r.Header.Del("Cookie")
			w = httptest.NewRecorder()
			h.h.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous: status = %d, want 401", w.Code)
			}
		})
	}
}

// Keep socket fixtures on the shared machine's reserved loopback range.
func previewTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	var lastErr error
	for port := 47700; port <= 47799; port++ {
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			lastErr = err
			continue
		}
		srv := &httptest.Server{Listener: ln, Config: &http.Server{Handler: handler}}
		srv.Start()
		t.Cleanup(srv.Close)
		return srv
	}
	t.Fatalf("no reserved loopback port available: %v", lastErr)
	return nil
}
