package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/cli"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/ptyd"
	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
	"github.com/coder/websocket"
)

const revocationPassword = "synthetic revocation password"

// A subprocess runs the real CLI against the fixture's WAL database, without
// sharing the server's Accounts, bus or memory. No server or daemon is launched.
func TestRevocationCLIHelper(t *testing.T) {
	if os.Getenv("RELAY_REVOCATION_CLI_HELPER") != "1" {
		return
	}
	args := strings.Split(os.Getenv("RELAY_REVOCATION_CLI_ARGS"), "\n")
	os.Exit(cli.Run(append([]string{"relay"}, args...)))
}

type revocationSocket struct {
	conn   *websocket.Conn
	frames chan []byte
	done   chan error
}

func (s *revocationSocket) write(typ websocket.MessageType, b []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return s.conn.Write(ctx, typ, b)
}
func (s *revocationSocket) waitFrame(t *testing.T, want string) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case b := <-s.frames:
			if strings.Contains(string(b), want) {
				return
			}
		case err := <-s.done:
			t.Fatalf("socket closed while waiting for frame: %v", err)
		case <-timer.C:
			t.Fatalf("frame %q not received", want)
		}
	}
}

func TestAuthenticatedSocketRevocationMatrix(t *testing.T) {
	cases := []struct {
		name, method string
		affected     []int
	}{
		{"server-session", "cookie", []int{1}},
		{"logout", "cookie", []int{1}},
		{"server-token", "token", []int{2}},
		{"self-token", "token", []int{2}},
		{"revoke-others-cookie", "cookie", []int{1}},
		{"revoke-others-token", "token", []int{0, 1}},
		{"password-change", "cookie", []int{1}},
		{"cli-password-recovery", "cookie", []int{0, 1}},
		{"cli-keep-sessions", "cookie", nil},
		{"cli-token", "token", []int{2}},
		{"offline-session", "cookie", []int{1}},
		{"offline-token", "token", []int{2}},
		{"offline-token-rotation", "token", []int{2}},
		{"expiry", "cookie", []int{1}},
		{"natural-expiry", "cookie", []int{1}},
		{"account-removed", "all", []int{0, 1, 2, 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := os.MkdirTemp("", "r24-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(root) })
			t.Setenv("RELAY_HOME", root)
			t.Setenv("RELAY_CONFIG", "")
			paths, err := config.ResolvePaths()
			if err != nil {
				t.Fatal(err)
			}
			paths.Home = root
			if err := paths.Ensure(); err != nil {
				t.Fatal(err)
			}
			cfg := config.Defaults()
			cfg.Terminal.Shell = "/bin/sh"
			cfg.Terminal.DefaultCwd = root
			cfg.Terminal.Record = "off"
			cfg.Terminal.ImportTmux = false
			cfg.Desktop.Enabled = os.Getenv("RELAY_REVOCATION_DESKTOP") == "1"
			cfg.Desktop.Display = ":93"
			cfg.Desktop.Geometry = "640x480"
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			st, err := store.Open(paths.DB)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })
			d := &core.Deps{Cfg: cfg, Paths: paths, Store: st, Bus: events.New(), Log: log, Pty: ptyclient.New(paths.PtydSocket), Search: core.NewSearchRegistry()}
			d.InitSettings()
			dm, err := ptyd.New(ptyd.Options{Cfg: cfg, Paths: paths, Log: log, LoginEnv: new(bool)})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			daemonDone := make(chan error, 1)
			go func() { daemonDone <- dm.Run(ctx) }()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-daemonDone:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(8 * time.Second):
					t.Error("owned ptyd did not stop")
				}
			})
			waitREL023(t, 5*time.Second, func() bool { return d.Pty.Health(context.Background()) == nil })
			a := &App{D: d}
			a.Router = server.NewRouter(nil, a.Origins)
			for _, wire := range []func(context.Context, *App) error{wireAuth, wireLive, wireTerminal, wireSystem, wireApps} {
				if err := wire(ctx, a); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(a.Close)
			var ln net.Listener
			for port := 47700; port <= 47799; port++ {
				ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
				if err == nil {
					break
				}
			}
			if err != nil {
				t.Fatal("no reserved loopback test port available")
			}
			hs := httptest.NewUnstartedServer(a.Router)
			hs.Listener.Close()
			hs.Listener = ln
			cfg.Server.TLS = "off"
			cfg.Server.Listen = ln.Addr().String()
			hs.Start()
			t.Cleanup(hs.Close)
			// Only live needs a loop for these probes. Socket enforcement must work
			// even before auth.Start and independently of live's bus fanout queue.
			liveDone := make(chan error, 1)
			for _, starter := range a.starters {
				if starter.name == "live" {
					go func() { liveDone <- starter.fn(ctx) }()
				}
			}
			t.Cleanup(func() {
				cancel()
				select {
				case <-liveDone:
				case <-time.After(5 * time.Second):
					t.Error("live did not stop")
				}
			})
			request := func(index int, method, path string, body any, headers []http.Header) *http.Response {
				t.Helper()
				var buf bytes.Buffer
				if body != nil {
					if err := json.NewEncoder(&buf).Encode(body); err != nil {
						t.Fatal(err)
					}
				}
				req, err := http.NewRequest(method, hs.URL+path, &buf)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Origin", hs.URL)
				req.Header.Set("Content-Type", "application/json")
				if index >= 0 {
					for k, vs := range headers[index] {
						req.Header[k] = append([]string(nil), vs...)
					}
				}
				hc := http.Client{Timeout: 8 * time.Second}
				res, err := hc.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				return res
			}
			expect := func(index int, code int, method, path string, body any, headers []http.Header, out any) http.Header {
				t.Helper()
				res := request(index, method, path, body, headers)
				defer res.Body.Close()
				if res.StatusCode != code {
					t.Fatalf("%s %s status %d want %d", method, path, res.StatusCode, code)
				}
				if out != nil {
					if err := json.NewDecoder(res.Body).Decode(out); err != nil {
						t.Fatal(err)
					}
				}
				return res.Header
			}
			headers := make([]http.Header, 4)
			for i := 0; i < 2; i++ {
				path := "/api/v1/auth/login"
				body := any(api.LoginRequest{Username: "fixture", Password: revocationPassword})
				if i == 0 {
					path = "/api/v1/auth/setup"
					body = api.SetupRequest{Username: "fixture", Password: revocationPassword}
				}
				h := expect(-1, 200, "POST", path, body, headers, nil)
				cookies := (&http.Response{Header: h}).Cookies()
				headers[i] = http.Header{}
				for _, c := range cookies {
					if strings.Contains(c.Name, "relay_session") {
						headers[i].Set("Cookie", c.Name+"="+c.Value)
					}
				}
			}
			var tokens [2]api.CreatedToken
			for i := 0; i < 2; i++ {
				expect(0, 201, "POST", "/api/v1/auth/tokens", api.NameRequest{Name: "synthetic"}, headers, &tokens[i])
				headers[i+2] = http.Header{"Authorization": []string{"Bearer " + tokens[i].Token}}
			}
			// Resolve public principals through the real request authenticator.
			var sessions []api.DeviceSession
			expect(1, 200, "GET", "/api/v1/auth/sessions", nil, headers, &sessions)
			var targetID string
			for _, s := range sessions {
				if s.Current {
					targetID = s.ID
				}
			}
			term, err := d.Pty.Create(ctx, ptyclient.CreateSpec{Command: []string{"/bin/sh", "-c", "stty -echo; printf 'fixture-ready\\n'; while IFS= read -r line; do printf 'accepted:%s\\n' \"$line\"; done"}, Cwd: root, Cols: 80, Rows: 24})
			if err != nil {
				t.Fatal(err)
			}
			waitREL023(t, 3*time.Second, func() bool {
				snap, e := d.Pty.Snapshot(ctx, term.ID, 40)
				return e == nil && strings.Contains(snap.Text, "fixture-ready")
			})
			logfile := filepath.Join(root, "fixture.log")
			if err := os.WriteFile(logfile, []byte("log-ready\n"), 0600); err != nil {
				t.Fatal(err)
			}
			sources := []struct{ name, path, ready string }{{"live", "/api/v1/events", "hello"}, {"terminal", "/api/v1/terminals/" + term.ID + "/attach?replay=0", "hello"}, {"log", "/api/v1/system/logs?file=" + logfile, "log-ready"}}
			if cfg.Desktop.Enabled {
				sources = append(sources, struct{ name, path, ready string }{"desktop", "/api/v1/desktop/ws", "RFB 003."})
			}
			sockets := make([][]*revocationSocket, len(sources))
			for si, src := range sources {
				for i := range headers {
					h := headers[i].Clone()
					h.Set("Origin", hs.URL)
					dialCtx, dcancel := context.WithTimeout(ctx, 30*time.Second)
					c, resp, err := websocket.Dial(dialCtx, "ws"+strings.TrimPrefix(hs.URL, "http")+src.path, &websocket.DialOptions{HTTPHeader: h, Subprotocols: []string{"binary"}})
					dcancel()
					if err != nil {
						status := 0
						if resp != nil {
							status = resp.StatusCode
						}
						t.Fatalf("%s dial status %d: %v", src.name, status, err)
					}
					s := &revocationSocket{conn: c, frames: make(chan []byte, 256), done: make(chan error, 1)}
					t.Cleanup(func() { c.CloseNow() })
					go func() {
						for {
							_, b, err := c.Read(ctx)
							if err != nil {
								s.done <- err
								return
							}
							select {
							case s.frames <- b:
							default:
							}
						}
					}()
					s.waitFrame(t, src.ready)
					sockets[si] = append(sockets[si], s)
				}
			}
			runCLI := func(args []string, password string) {
				exe, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				cctx, ccancel := context.WithTimeout(ctx, 8*time.Second)
				defer ccancel()
				cmd := exec.CommandContext(cctx, exe, "-test.run=^TestRevocationCLIHelper$")
				cmd.Env = append(os.Environ(), "RELAY_REVOCATION_CLI_HELPER=1", "RELAY_REVOCATION_CLI_ARGS="+strings.Join(args, "\n"))
				cmd.Stdin = strings.NewReader(password + "\n")
				if err := cmd.Run(); err != nil {
					t.Fatalf("isolated CLI: %v", err)
				}
			}
			started := time.Now()
			switch tc.name {
			case "server-session":
				expect(0, 204, "DELETE", "/api/v1/auth/sessions/"+targetID, nil, headers, nil)
			case "logout":
				expect(1, 204, "POST", "/api/v1/auth/logout", nil, headers, nil)
			case "server-token":
				expect(0, 204, "DELETE", "/api/v1/auth/tokens/"+tokens[0].ID, nil, headers, nil)
			case "self-token":
				expect(2, 204, "DELETE", "/api/v1/auth/tokens/"+tokens[0].ID, nil, headers, nil)
			case "revoke-others-cookie":
				expect(0, 200, "POST", "/api/v1/auth/sessions/revoke-others", nil, headers, nil)
			case "revoke-others-token":
				expect(2, 200, "POST", "/api/v1/auth/sessions/revoke-others", nil, headers, nil)
			case "password-change":
				expect(0, 204, "POST", "/api/v1/auth/password", api.ChangePasswordRequest{Current: revocationPassword, Next: "another synthetic password"}, headers, nil)
			case "cli-password-recovery":
				runCLI([]string{"passwd", "--stdin", "--user", "recovered"}, "another synthetic password")
			case "cli-keep-sessions":
				runCLI([]string{"passwd", "--stdin", "--keep-sessions"}, "another synthetic password")
			case "cli-token":
				runCLI([]string{"token", "revoke", tokens[0].ID}, "")
			case "offline-session", "offline-token", "offline-token-rotation", "expiry", "natural-expiry", "account-removed":
				other, err := store.Open(paths.DB)
				if err != nil {
					t.Fatal(err)
				}
				query := "DELETE FROM auth_sessions WHERE id=?"
				id := targetID
				switch tc.name {
				case "offline-token":
					query = "DELETE FROM auth_tokens WHERE id=?"
					id = tokens[0].ID
				case "offline-token-rotation":
					query = "UPDATE auth_tokens SET token_hash='synthetic-replacement-hash' WHERE id=?"
					id = tokens[0].ID
				case "expiry":
					query = "UPDATE auth_sessions SET expires_at=0 WHERE id=?"
				case "natural-expiry":
					query = fmt.Sprintf("UPDATE auth_sessions SET expires_at=%d WHERE id=?", time.Now().Add(800*time.Millisecond).UnixMilli())
				case "account-removed":
					query = "DELETE FROM auth_user WHERE id=1 AND ? IS NOT NULL"
				}
				_, err = other.DB.ExecContext(ctx, query, id)
				other.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "natural-expiry" {
				time.Sleep(850 * time.Millisecond)
			}
			revoked := map[int]bool{}
			for _, i := range tc.affected {
				revoked[i] = true
			}
			// Frames sent after the mutation must never reach the PTY. The socket can
			// still be awaiting its close handshake when this input is sent.
			for i := range headers {
				if revoked[i] {
					_ = sockets[1][i].write(websocket.MessageBinary, []byte(fmt.Sprintf("denied-%d\r", i)))
				}
			}
			for si, src := range sources {
				for i, s := range sockets[si] {
					if !revoked[i] {
						continue
					}
					select {
					case err := <-s.done:
						if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
							t.Fatalf("%s/%d close: %v", src.name, i, err)
						}
						var ce websocket.CloseError
						if !errors.As(err, &ce) || ce.Reason != "authentication ended" {
							t.Fatalf("unsafe/unexpected reason: %v", err)
						}
						elapsed := time.Since(started)
						if elapsed > server.SocketRevocationBound+500*time.Millisecond {
							t.Fatalf("%s close took %s", src.name, elapsed)
						}
						t.Logf("%s/%d closed in %s", src.name, i, elapsed.Round(time.Millisecond))
					case <-time.After(server.SocketRevocationBound + 500*time.Millisecond):
						t.Fatalf("%s/%d stayed open", src.name, i)
					}
				}
			}
			for i := range headers {
				code := 200
				if revoked[i] {
					code = 401
				}
				expect(i, code, "GET", "/api/v1/terminals", nil, headers, nil)
				if revoked[i] {
					continue
				}
				if err := sockets[0][i].write(websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
					t.Fatal(err)
				}
				sockets[0][i].waitFrame(t, "pong")
				marker := fmt.Sprintf("allowed-%d", i)
				if err := sockets[1][i].write(websocket.MessageBinary, []byte(marker+"\r")); err != nil {
					t.Fatal(err)
				}
				sockets[1][i].waitFrame(t, "accepted:"+marker)
				f, err := os.OpenFile(logfile, os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteString(marker + "\n")
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
				sockets[2][i].waitFrame(t, marker)
				if cfg.Desktop.Enabled {
					if err := sockets[3][i].write(websocket.MessageBinary, []byte("RFB 003.008\n")); err != nil {
						t.Fatal(err)
					}
					sockets[3][i].waitFrame(t, "\x01\x01")
				}
			}
			snap, err := d.Pty.Snapshot(ctx, term.ID, 100)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(snap.Text, "denied-") {
				t.Fatal("revoked input reached durable PTY")
			}
			for _, group := range sockets {
				for _, s := range group {
					s.conn.CloseNow()
				}
			}
			waitREL023(t, 3*time.Second, func() bool { return d.Bus.Subscribers() == 1 }) // only live fanout remains
		})
	}
}
