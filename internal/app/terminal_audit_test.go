package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/auth"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/ptyd"
	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
	"github.com/aduthekaddu/relay/internal/terminal"
)

// Exercise the actual router, auth consumer, SQLite and activity response.
// Only the fixture daemon owns PTYs. HTTP requests stay in-process.
func TestTerminalMutationActivity(t *testing.T) {
	root, err := os.MkdirTemp("", "r25-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	t.Setenv("RELAY_HOME", root)
	t.Setenv("RELAY_CONFIG", "")
	t.Setenv("RELAY_NO_PTYD", "1")
	paths, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	paths.Home = root
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Server.Listen = "127.0.0.1:47725"
	cfg.Server.TLS = "off"
	cfg.Terminal.Shell = "/bin/sh"
	cfg.Terminal.DefaultCwd = root
	cfg.Terminal.Record = "off"
	cfg.Terminal.ImportTmux = false
	cfg.Desktop.Enabled = false
	cfg.Files.Root = filepath.Join(root, "files")
	if err := os.Mkdir(cfg.Files.Root, 0700); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &core.Deps{Cfg: cfg, Paths: paths, Store: st, Bus: events.New(), Log: log, Pty: ptyclient.New(paths.PtydSocket)}
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
			t.Error("owned daemon did not stop")
		}
	})
	waitREL023(t, 5*time.Second, func() bool { return d.Pty.Health(ctx) == nil })
	a := &App{D: d}
	a.Router = server.NewRouter(nil, a.Origins)
	authSvc, err := auth.New(d)
	if err != nil {
		t.Fatal(err)
	}
	authSvc.SetOrigins(a.Origins)
	authSvc.Routes(a.Router)
	a.Router.SetAuthenticator(authSvc)
	a.OnStart("auth", authSvc.Start)
	if err := wireTerminal(ctx, a); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body any, header http.Header) *httptest.ResponseRecorder {
		t.Helper()
		var rd io.Reader
		if b, ok := body.([]byte); ok {
			rd = bytes.NewReader(b)
		} else if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			rd = bytes.NewReader(b)
		}
		r := httptest.NewRequest(method, cfg.Origin()+path, rd)
		r.RemoteAddr = "127.0.0.1:47725"
		for k, vs := range header {
			r.Header[k] = append([]string(nil), vs...)
		}
		w := httptest.NewRecorder()
		a.Router.ServeHTTP(w, r)
		return w
	}
	expect := func(code int, method, path string, body any, header http.Header, out any) *httptest.ResponseRecorder {
		t.Helper()
		w := request(method, path, body, header)
		if w.Code != code {
			t.Fatalf("%s %s = %d, want %d", method, path, w.Code, code)
		}
		if out != nil {
			if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
		}
		return w
	}
	password := "synthetic audit password"
	w := expect(200, "POST", "/api/v1/auth/setup", api.SetupRequest{Username: "fixture", Password: password}, nil, nil)
	cookieHeader := http.Header{"Origin": []string{cfg.Origin()}}
	var cookieValue string
	for _, c := range w.Result().Cookies() {
		if strings.Contains(c.Name, "relay_session") {
			cookieHeader.Set("Cookie", c.Name+"="+c.Value)
			cookieValue = c.Value
		}
	}
	if cookieValue == "" {
		t.Fatal("missing session cookie")
	}
	var tok api.CreatedToken
	expect(201, "POST", "/api/v1/auth/tokens", api.NameRequest{Name: "synthetic"}, cookieHeader, &tok)
	tokenHeader := http.Header{"Authorization": []string{"Bearer " + tok.Token}}
	done := make(chan error, 1)
	for _, start := range a.starters {
		if start.name == "auth" {
			go func() { done <- start.fn(ctx) }()
		}
	}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("audit consumer did not stop")
		}
	})
	activity := func() []api.AuditEntry {
		var rows []api.AuditEntry
		expect(200, "GET", "/api/v1/auth/activity?limit=500", nil, tokenHeader, &rows)
		return rows
	}
	// A persisted barrier proves subscription readiness and drains prior events.
	barrier := 0
	flush := func() {
		t.Helper()
		barrier++
		marker := fmt.Sprint(barrier)
		waitREL023(t, 3*time.Second, func() bool {
			d.Bus.Publish(core.BusAudit, core.AuditEvent{Event: "test.barrier", Detail: marker})
			for _, e := range activity() {
				if e.Event == "test.barrier" && e.Detail == marker {
					return true
				}
			}
			return false
		})
	}
	flush()
	want := map[string]int{}
	add := func(action, id string) { want[action+":"+id]++ }
	create := func(script string) *api.TerminalSession {
		var s api.TerminalSession
		expect(201, "POST", "/api/v1/terminals", api.CreateTerminalRequest{Name: "synthetic-name-secret", Command: []string{"/bin/sh", "-c", script}, Env: map[string]string{"SYNTHETIC_SECRET": "synthetic-env-secret"}}, tokenHeader, &s)
		return &s
	}
	s := create("echo synthetic-prompt-secret; read x")
	waitREL023(t, 3*time.Second, func() bool {
		snap, err := d.Pty.Snapshot(ctx, s.ID, 10)
		return err == nil && strings.Contains(snap.Text, "synthetic-prompt-secret")
	})
	for _, tc := range []struct {
		name, path string
		code       int
		header     http.Header
	}{
		{"anonymous", "/api/v1/terminals/" + s.ID, 401, nil},
		{"CSRF", "/api/v1/terminals/" + s.ID, 403, http.Header{"Cookie": cookieHeader.Values("Cookie"), "Origin": []string{"https://cross-site.invalid"}}},
		{"invalid id", "/api/v1/terminals/synthetic-session-secret", 404, tokenHeader},
		{"invalid signal", "/api/v1/terminals/" + s.ID + "?signal=synthetic-token-secret", 400, tokenHeader},
		{"unknown", "/api/v1/terminals/t_aaaaaaaaaa", 404, tokenHeader},
	} {
		t.Run(tc.name, func(t *testing.T) { expect(tc.code, "DELETE", tc.path, nil, tc.header, nil) })
	}
	expect(204, "DELETE", "/api/v1/terminals/"+s.ID+"?signal=KILL", nil, tokenHeader, nil)
	add("terminal.kill", s.ID+" KILL")
	waitREL023(t, 3*time.Second, func() bool { s, err := d.Pty.Get(ctx, s.ID); return err == nil && s.Activity == api.ActivityExited })
	expect(204, "DELETE", "/api/v1/terminals/"+s.ID+"?signal=KILL", nil, tokenHeader, nil)
	expect(204, "DELETE", "/api/v1/terminals/"+s.ID+"?forget=1", nil, cookieHeader, nil)
	add("terminal.forget", s.ID)
	expect(404, "DELETE", "/api/v1/terminals/"+s.ID+"?forget=1", nil, tokenHeader, nil)
	live := create("echo ready; read x")
	expect(204, "DELETE", "/api/v1/terminals/"+live.ID+"?forget=1", nil, tokenHeader, nil)
	add("terminal.forget", live.ID)
	closing := create(`trap "" HUP TERM; echo closing-ready; read x`)
	waitREL023(t, 3*time.Second, func() bool {
		snap, err := d.Pty.Snapshot(ctx, closing.ID, 10)
		return err == nil && strings.Contains(snap.Text, "closing-ready")
	})
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := request("DELETE", "/api/v1/terminals/"+closing.ID, nil, tokenHeader)
			if w.Code != 204 {
				t.Errorf("concurrent close = %d", w.Code)
			}
		}()
	}
	wg.Wait()
	add("terminal.kill", closing.ID+" HUP")
	expect(204, "DELETE", "/api/v1/terminals/"+closing.ID+"?signal=KILL", nil, tokenHeader, nil)
	add("terminal.kill", closing.ID+" KILL")
	expect(204, "DELETE", "/api/v1/terminals/"+closing.ID+"?forget=1", nil, tokenHeader, nil)
	add("terminal.forget", closing.ID)
	start := func(size int, dir string) api.Upload {
		var up api.Upload
		expect(201, "POST", "/api/v1/uploads", api.StartUploadRequest{Name: "synthetic-filename-secret.txt", Mime: "synthetic-mime-secret", Size: int64(size), Dir: dir}, tokenHeader, &up)
		return up
	}
	body := []byte("synthetic-file-body-secret")
	up := start(len(body), "")
	for _, denied := range []struct {
		method, suffix string
		header         http.Header
		code           int
	}{
		{"POST", "/complete", nil, 401},
		{"DELETE", "", nil, 401},
		{"DELETE", "", http.Header{"Cookie": cookieHeader.Values("Cookie"), "Origin": []string{"https://cross-site.invalid"}}, 403},
	} {
		expect(denied.code, denied.method, "/api/v1/uploads/"+up.ID+denied.suffix, nil, denied.header, nil)
	}
	expect(200, "PUT", "/api/v1/uploads/"+up.ID+"?offset=0", body[:5], tokenHeader, nil)
	expect(409, "POST", "/api/v1/uploads/"+up.ID+"/complete", nil, tokenHeader, nil)
	// Reconstruct the upload service against persisted staging before resuming.
	svc, err := terminal.New(d)
	if err != nil {
		t.Fatal(err)
	}
	rt := server.NewRouter(authSvc, a.Origins)
	authSvc.Routes(rt)
	svc.Routes(rt)
	a.Router = rt
	var status api.Upload
	expect(200, "GET", "/api/v1/uploads/"+up.ID, nil, tokenHeader, &status)
	if status.Received != 5 {
		t.Fatalf("partial bytes = %d", status.Received)
	}
	expect(409, "PUT", "/api/v1/uploads/"+up.ID+"?offset=10", body[10:], tokenHeader, nil)
	expect(200, "PUT", "/api/v1/uploads/"+up.ID+"?offset=0", body, tokenHeader, nil)
	var res api.UploadResult
	expect(200, "POST", "/api/v1/uploads/"+up.ID+"/complete", nil, tokenHeader, &res)
	add("upload.complete", up.ID)
	got, err := os.ReadFile(res.Path)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatal("completed file differs")
	}
	for _, tc := range []struct{ method, suffix string }{{"POST", "/complete"}, {"DELETE", ""}, {"PUT", "?offset=0"}} {
		expect(404, tc.method, "/api/v1/uploads/"+up.ID+tc.suffix, body, tokenHeader, nil)
	}
	// Consumed IDs stay consumed in a freshly constructed service.
	svc, err = terminal.New(d)
	if err != nil {
		t.Fatal(err)
	}
	rt = server.NewRouter(authSvc, a.Origins)
	authSvc.Routes(rt)
	svc.Routes(rt)
	a.Router = rt
	expect(404, "POST", "/api/v1/uploads/"+up.ID+"/complete", nil, tokenHeader, nil)
	for _, size := range []int{0, len(body)} {
		canceled := start(size, cfg.Files.Root)
		if size > 0 {
			expect(200, "PUT", "/api/v1/uploads/"+canceled.ID+"?offset=0", body[:5], tokenHeader, nil)
		}
		expect(204, "DELETE", "/api/v1/uploads/"+canceled.ID, nil, cookieHeader, nil)
		add("upload.cancel", canceled.ID)
		expect(404, "DELETE", "/api/v1/uploads/"+canceled.ID, nil, tokenHeader, nil)
		expect(404, "POST", "/api/v1/uploads/"+canceled.ID+"/complete", nil, tokenHeader, nil)
	}
	zero := start(0, cfg.Files.Root)
	expect(200, "POST", "/api/v1/uploads/"+zero.ID+"/complete", nil, tokenHeader, nil)
	add("upload.complete", zero.ID)
	expect(403, "POST", "/api/v1/uploads", api.StartUploadRequest{Name: "synthetic", Size: 0, Dir: root}, tokenHeader, nil)
	flush()
	actual := map[string]int{}
	var sanitized []api.AuditEntry
	for _, e := range activity() {
		if strings.HasPrefix(e.Event, "terminal.") || strings.HasPrefix(e.Event, "upload.") {
			actual[e.Event+":"+e.Detail]++
			sanitized = append(sanitized, e)
			if e.Actor != "fixture" || e.IP != "127.0.0.1" {
				t.Fatalf("actor/IP = %+v", e)
			}
		}
	}
	if len(actual) != len(want) {
		t.Fatalf("activity count = %v, want %v", actual, want)
	}
	for key, n := range want {
		if actual[key] != n {
			t.Fatalf("activity %s = %d, want %d", key, actual[key], n)
		}
	}
	b, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{password, tok.Token, cookieValue, "synthetic-session-secret", "synthetic-token-secret", "synthetic-name-secret", "synthetic-env-secret", "synthetic-prompt-secret", "synthetic-filename-secret", "synthetic-mime-secret", string(body), root} {
		if strings.Contains(string(b), secret) {
			t.Fatal("private fixture value entered activity")
		}
	}
	// Log only approved fields, with generated IDs normalized for portable evidence.
	for _, e := range sanitized {
		e.ID = 0
		e.At = time.Time{}
		id, signal, _ := strings.Cut(e.Detail, " ")
		e.Detail = strings.Split(id, "_")[0] + "_synthetic"
		if signal != "" {
			e.Detail += " " + signal
		}
		row, _ := json.Marshal(e)
		t.Logf("sanitized activity %s", row)
	}
}
