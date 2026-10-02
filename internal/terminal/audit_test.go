package terminal

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/server"
)

func auditSub(e *env) *events.Sub {
	s := e.d.Bus.Subscribe(128, func(ev api.Event) bool { return ev.Type == core.BusAudit })
	e.t.Cleanup(s.Close)
	return s
}

func expectAudit(t *testing.T, sub *events.Sub, action, id string) {
	t.Helper()
	select {
	case ev := <-sub.C:
		e, ok := ev.Data.(core.AuditEvent)
		if !ok || e.Event != action || e.Detail != id || e.Actor != "owner" {
			t.Fatalf("audit = %+v", ev.Data)
		}
	default:
		t.Fatalf("missing %s", action)
	}
	select {
	case ev := <-sub.C:
		t.Fatalf("extra audit = %+v", ev.Data)
	default:
	}
}

func TestUploadAuditConcurrency(t *testing.T) {
	for _, operation := range []string{"complete", "cancel", "mixed"} {
		t.Run(operation, func(t *testing.T) {
			e := newEnv(t, false, nil)
			sub := auditSub(e)
			var up api.Upload
			if e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: "synthetic.txt", Size: 1}, &up) != 201 {
				t.Fatal("start")
			}
			if e.put(up.ID, 0, []byte("x"), nil) != 200 {
				t.Fatal("put")
			}
			var wg sync.WaitGroup
			codes := make(chan int, 20)
			for i := range 20 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if operation == "cancel" || operation == "mixed" && i%2 == 0 {
						codes <- e.call("DELETE", "/api/v1/uploads/"+up.ID, nil, nil)
					} else {
						codes <- e.call("POST", "/api/v1/uploads/"+up.ID+"/complete", nil, nil)
					}
				}()
			}
			wg.Wait()
			close(codes)
			n := 0
			for code := range codes {
				if code == 200 || code == 204 {
					n++
				} else if code != 404 {
					t.Errorf("retry status = %d", code)
				}
			}
			if n != 1 {
				t.Fatalf("successful mutations = %d", n)
			}
			select {
			case ev := <-sub.C:
				a := ev.Data.(core.AuditEvent)
				if a.Detail != up.ID || a.Event != "upload.complete" && a.Event != "upload.cancel" {
					t.Fatalf("audit = %+v", a)
				}
			default:
				t.Fatal("missing audit")
			}
			select {
			case ev := <-sub.C:
				t.Fatalf("duplicate audit = %+v", ev.Data)
			default:
			}
			e.svc.up.mu.Lock()
			nlocks := len(e.svc.up.locks)
			e.svc.up.mu.Unlock()
			if nlocks != 0 {
				t.Fatalf("retained locks = %d", nlocks)
			}
		})
	}
}

func TestUploadCancelFailureAndRetryAudit(t *testing.T) {
	for _, failedFile := range []string{"data", "metadata"} {
		t.Run(failedFile, func(t *testing.T) {
			e := newEnv(t, false, nil)
			sub := auditSub(e)
			var up api.Upload
			e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: "synthetic", Size: 1}, &up)
			path := e.svc.up.dataPath(up.ID)
			if failedFile == "metadata" {
				path = e.svc.up.metaPath(up.ID)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			blocker := filepath.Join(path, "synthetic-blocker")
			if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
			if code := e.call("DELETE", "/api/v1/uploads/"+up.ID, nil, nil); code != 500 {
				t.Fatalf("failed cancellation = %d", code)
			}
			select {
			case ev := <-sub.C:
				t.Fatalf("failed success audit = %+v", ev.Data)
			default:
			}
			if err := os.Remove(blocker); err != nil {
				t.Fatal(err)
			}
			if code := e.call("DELETE", "/api/v1/uploads/"+up.ID, nil, nil); code != 204 {
				t.Fatalf("retry = %d", code)
			}
			expectAudit(t, sub, "upload.cancel", up.ID)
			if code := e.call("DELETE", "/api/v1/uploads/"+up.ID, nil, nil); code != 404 {
				t.Fatalf("consumed retry = %d", code)
			}
		})
	}
}

type interruptedUpload struct{ sent bool }

func (r *interruptedUpload) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "partial"), nil
	}
	return 0, io.ErrUnexpectedEOF
}

func TestInterruptedUploadNoSuccessAudit(t *testing.T) {
	e := newEnv(t, false, nil)
	sub := auditSub(e)
	var up api.Upload
	e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: "synthetic", Size: 10}, &up)
	r := httptest.NewRequest("PUT", "/api/v1/uploads/"+up.ID+"?offset=0", &interruptedUpload{})
	r.SetPathValue("id", up.ID)
	w := httptest.NewRecorder()
	e.svc.hUploadPut(w, r)
	if w.Code != 400 {
		t.Fatalf("interrupted PUT = %d", w.Code)
	}
	var status api.Upload
	if code := e.call("GET", "/api/v1/uploads/"+up.ID, nil, &status); code != 200 || status.Received != 7 {
		t.Fatalf("resume = %d %+v", code, status)
	}
	if e.call("POST", "/api/v1/uploads/"+up.ID+"/complete", nil, nil) != 409 {
		t.Fatal("partial complete")
	}
	select {
	case ev := <-sub.C:
		t.Fatalf("partial success audit = %+v", ev.Data)
	default:
	}
	if e.put(up.ID, 7, []byte("end"), nil) != 200 {
		t.Fatal("resume put")
	}
	if e.call("POST", "/api/v1/uploads/"+up.ID+"/complete", nil, nil) != 200 {
		t.Fatal("complete")
	}
	expectAudit(t, sub, "upload.complete", up.ID)
}

func TestTerminalDaemonFailureNoSuccessAudit(t *testing.T) {
	e := newEnv(t, false, nil)
	sub := auditSub(e)
	for _, suffix := range []string{"", "?forget=1"} {
		if code := e.call("DELETE", "/api/v1/terminals/t_aaaaaaaaaa"+suffix, nil, nil); code != http.StatusServiceUnavailable {
			t.Fatalf("daemon failure = %d", code)
		}
	}
	select {
	case ev := <-sub.C:
		t.Fatalf("failure success audit = %+v", ev.Data)
	default:
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.svc.pty.Delete(ctx, "t_aaaaaaaaaa", ptyclient.DeleteSpec{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request = %v", err)
	}
}

func TestUploadCompleteFailureNoSuccessAudit(t *testing.T) {
	e := newEnv(t, false, nil)
	sub := auditSub(e)
	var up api.Upload
	e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: "synthetic", Size: 1}, &up)
	e.put(up.ID, 0, []byte("x"), nil)
	m, _, err := e.svc.up.load(up.ID)
	if err != nil {
		t.Fatal(err)
	}
	day := filepath.Join(e.paths.Uploads, m.Day)
	if err := os.WriteFile(day, []byte("synthetic blocker"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := e.call("POST", "/api/v1/uploads/"+up.ID+"/complete", nil, nil); code != 500 {
		t.Fatalf("failed completion = %d", code)
	}
	select {
	case ev := <-sub.C:
		t.Fatalf("failed success audit = %+v", ev.Data)
	default:
	}
	if err := os.Remove(day); err != nil {
		t.Fatal(err)
	}
	if code := e.call("POST", "/api/v1/uploads/"+up.ID+"/complete", nil, nil); code != 200 {
		t.Fatalf("retry = %d", code)
	}
	expectAudit(t, sub, "upload.complete", up.ID)
}

func TestUploadRetargetedDirectoryNoSuccessAudit(t *testing.T) {
	e := newEnv(t, false, nil)
	sub := auditSub(e)
	dir := filepath.Join(e.files, "synthetic-dir")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	var up api.Upload
	e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: "synthetic", Size: 0, Dir: dir}, &up)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	if code := e.call("POST", "/api/v1/uploads/"+up.ID+"/complete", nil, nil); code != 403 {
		t.Fatalf("retargeted directory = %d", code)
	}
	select {
	case ev := <-sub.C:
		t.Fatalf("denied success audit = %+v", ev.Data)
	default:
	}
	if code := e.call("DELETE", "/api/v1/uploads/"+up.ID, nil, nil); code != 204 {
		t.Fatalf("cancel = %d", code)
	}
	expectAudit(t, sub, "upload.cancel", up.ID)
}

func TestCompletedUploadMetadataIsNotCancellation(t *testing.T) {
	e := newEnv(t, false, nil)
	sub := auditSub(e)
	var up api.Upload
	e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: "synthetic", Size: 0}, &up)
	metadata, err := os.ReadFile(e.svc.up.metaPath(up.ID))
	if err != nil {
		t.Fatal(err)
	}
	if code := e.call("POST", "/api/v1/uploads/"+up.ID+"/complete", nil, nil); code != 200 {
		t.Fatalf("complete = %d", code)
	}
	expectAudit(t, sub, "upload.complete", up.ID)
	// Model failed metadata cleanup after the source was consumed.
	if err := os.WriteFile(e.svc.up.metaPath(up.ID), metadata, 0600); err != nil {
		t.Fatal(err)
	}
	if code := e.call("DELETE", "/api/v1/uploads/"+up.ID, nil, nil); code != 404 {
		t.Fatalf("completed cancel = %d", code)
	}
	select {
	case ev := <-sub.C:
		t.Fatalf("consumed success audit = %+v", ev.Data)
	default:
	}
}

func TestUploadSourceCleanupFailureDoesNotCommit(t *testing.T) {
	e := newEnv(t, false, nil)
	sub := auditSub(e)
	var up api.Upload
	e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: "synthetic", Size: 0}, &up)
	if err := os.Chmod(e.svc.up.partial(), 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(e.svc.up.partial(), 0700) })
	if code := e.call("POST", "/api/v1/uploads/"+up.ID+"/complete", nil, nil); code != 500 {
		t.Fatalf("source cleanup failure = %d", code)
	}
	select {
	case ev := <-sub.C:
		t.Fatalf("failed success audit = %+v", ev.Data)
	default:
	}
	m, _, err := e.svc.up.load(up.ID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join(e.paths.Uploads, m.Day))
	if err != nil || len(files) != 0 {
		t.Fatalf("rollback files = %d, %v", len(files), err)
	}
	if err := os.Chmod(e.svc.up.partial(), 0700); err != nil {
		t.Fatal(err)
	}
	if code := e.call("POST", "/api/v1/uploads/"+up.ID+"/complete", nil, nil); code != 200 {
		t.Fatalf("retry = %d", code)
	}
	expectAudit(t, sub, "upload.complete", up.ID)
}

func TestAuditDoesNotCopyRequestSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, remote, cf, wantIP string
		local                    bool
	}{
		{"trusted invalid proxy IP", "127.0.0.1:47725", "synthetic-token-secret", "", false},
		{"trusted valid proxy IP", "127.0.0.1:47725", "192.0.2.25", "192.0.2.25", false},
		{"invalid remote address", "synthetic-session-secret", "", "", false},
		{"IPv6", "[::1]:47725", "", "::1", false},
		{"local socket", "", "synthetic-token-secret", "local", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, false, nil)
			sub := auditSub(e)
			r := httptest.NewRequest("DELETE", "/api/v1/terminals/t_aaaaaaaaaa?secret=synthetic-prompt-secret", nil)
			r.RemoteAddr = tc.remote
			r.Header.Set("CF-Connecting-IP", tc.cf)
			r.Header.Set("Authorization", "Bearer synthetic-token-secret")
			r.Header.Set("Cookie", "relay_session=synthetic-session-secret")
			r.Header.Set("User-Agent", "synthetic-prompt-secret")
			ctx := server.WithPrincipal(r.Context(), &server.Principal{User: "owner", Method: "token", SessionID: "synthetic-session-secret", TokenID: "synthetic-token-secret"})
			if tc.local {
				ctx = server.MarkLocal(ctx)
			}
			r = r.WithContext(ctx)
			h := server.WithRequestInfo(func(net.IP) bool { return tc.cf != "" }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { e.svc.audit(r, "terminal.kill", "t_aaaaaaaaaa HUP") }))
			h.ServeHTTP(httptest.NewRecorder(), r)
			select {
			case ev := <-sub.C:
				a := ev.Data.(core.AuditEvent)
				if a != (core.AuditEvent{Event: "terminal.kill", Actor: "owner", IP: tc.wantIP, Detail: "t_aaaaaaaaaa HUP"}) {
					t.Fatalf("request secrets entered audit: %+v", a)
				}
			default:
				t.Fatal("missing audit")
			}
		})
	}
}
