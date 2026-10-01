package system

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/server"
)

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	d := &core.Deps{Cfg: &config.Config{}, Paths: config.Paths{Home: home}, Bus: events.New()}
	s, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(home)
	return s, real
}

func do(t *testing.T, h http.HandlerFunc, method, target string, body any, pathVals map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, target, &buf)
	r = r.WithContext(server.WithPrincipal(r.Context(), &server.Principal{User: "tester", Method: "cookie"}))
	for k, v := range pathVals {
		r.SetPathValue(k, v)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestHandleSignalProtectedAndAudited(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("needs /proc")
	}
	s, _ := newTestService(t)
	var audits []api.AuditEntry
	s.Audit = func(_ context.Context, e api.AuditEntry) { audits = append(audits, e) }

	if w := do(t, s.handleSignal, "POST", "/x", api.SignalRequest{Signal: "KILL"}, map[string]string{"pid": "1"}); w.Code != 403 {
		t.Fatalf("pid 1: %d %s", w.Code, w.Body)
	}
	if w := do(t, s.handleSignal, "POST", "/x", api.SignalRequest{Signal: "TERM"}, map[string]string{"pid": "abc"}); w.Code != 400 {
		t.Fatalf("bad pid: %d", w.Code)
	}
	if len(audits) != 0 {
		t.Fatalf("refused signals must not be audited: %+v", audits)
	}

	child := startSleep(t)
	w := do(t, s.handleSignal, "POST", "/x", api.SignalRequest{Signal: "TERM"}, map[string]string{"pid": strconv.Itoa(child)})
	if w.Code != 204 {
		t.Fatalf("child: %d %s", w.Code, w.Body)
	}
	if len(audits) != 1 || audits[0].Event != "process.signal" || audits[0].Actor != "tester" {
		t.Fatalf("audit = %+v", audits)
	}
}

func startSleep(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skip("cannot start sleep:", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	return cmd.Process.Pid
}

func TestHandleServiceActionPolicy(t *testing.T) {
	s, _ := newTestService(t)
	u, f := newFakeUnits()
	s.units = u
	var audits []api.AuditEntry
	s.Audit = func(_ context.Context, e api.AuditEntry) { audits = append(audits, e) }

	tests := []struct {
		name, unit, action string
		all                bool
		status             int
	}{
		{"unmanaged refused", "dbus", "restart", false, 403},
		{"managed allowed", "relay", "restart", false, 200},
		{"opt-in allows all", "broken.service", "stop", true, 200},
		{"bad action", "relay", "enable", false, 404},
		{"bad name", "-x", "start", false, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s.ManageAllUnits = tt.all
			before := len(f.calls)
			w := do(t, s.handleServiceAction, "POST", "/x", nil, map[string]string{"name": tt.unit, "action": tt.action})
			if w.Code != tt.status {
				t.Fatalf("status %d want %d: %s", w.Code, tt.status, w.Body)
			}
			if tt.status != 200 && len(f.calls) != before {
				t.Fatalf("refused request ran systemctl: %v", f.calls[before:])
			}
		})
	}
	if len(audits) != 2 || audits[0].Event != "service.restart" || audits[0].Detail != "relay.service" {
		t.Fatalf("audits = %+v", audits)
	}
}

func TestHandleServicesWithoutSystemd(t *testing.T) {
	s, _ := newTestService(t)
	s.units = &Units{}
	w := do(t, s.handleServices, "GET", "/x", nil, nil)
	if w.Code != 200 || bytes.TrimSpace(w.Body.Bytes())[0] != '[' {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func TestHandleProcessesBadSort(t *testing.T) {
	s, _ := newTestService(t)
	if w := do(t, s.handleProcesses, "GET", "/x?sort=evil", nil, nil); w.Code != 400 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestMetricsAndHistory(t *testing.T) {
	f := newFixture(t)
	s, _ := newTestService(t)
	s.col = f.collector()
	w := do(t, s.handleMetrics, "GET", "/x", nil, nil)
	var m api.Metrics
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || w.Code != 200 {
		t.Fatalf("metrics: %d %v", w.Code, err)
	}
	if m.CPU.Cores != 2 || m.Host.Kernel != "6.1.0-synthetic" {
		t.Fatalf("metrics = %+v", m)
	}
	// Cached within the freshness window: no second sample.
	at := s.lastAt
	do(t, s.handleMetrics, "GET", "/x", nil, nil)
	if !s.lastAt.Equal(at) {
		t.Fatal("metrics resampled inside freshness window")
	}

	old := historySample(m)
	old.At = time.Now().Add(-30 * time.Minute)
	s.hist.push(old)
	s.hist.push(historySample(m))
	var hist []api.Metrics
	w = do(t, s.handleHistory, "GET", "/x?minutes=10", nil, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &hist); err != nil || len(hist) != 1 {
		t.Fatalf("history(10m) = %d samples, %v", len(hist), err)
	}
	if hist[0].Host.Hostname != "" || hist[0].CPU.Model != "" {
		t.Errorf("history keeps static fields: %+v", hist[0].Host)
	}
	w = do(t, s.handleHistory, "GET", "/x", nil, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &hist); err != nil || len(hist) != 2 {
		t.Fatalf("history(60m) = %d samples", len(hist))
	}
}

func TestStartPublishesOnlyWhenSubscribed(t *testing.T) {
	f := newFixture(t)
	s, _ := newTestService(t)
	s.col = f.collector()
	sub := s.d.Bus.Subscribe(16, func(e api.Event) bool { return e.Type == api.EvMetrics })
	defer sub.Close()

	subscribed := make(chan bool, 1)
	subscribed <- false
	var want bool
	s.Subscribed = func(topic string) bool {
		select {
		case v := <-subscribed:
			want = v
		default:
		}
		return topic == "metrics" && want
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	startAt := time.Now()
	go func() { done <- s.Start(ctx) }()

	select {
	case <-sub.C:
		t.Fatal("published metrics without a subscriber")
	case <-time.After(1500 * time.Millisecond):
	}
	subscribed <- true
	select {
	case <-sub.C:
	case <-time.After(3 * time.Second):
		t.Fatal("no metrics event while subscribed")
	}
	subscribed <- false
	select {
	case ev := <-sub.C:
		t.Fatalf("published metrics after unsubscribe: %+v", ev)
	case <-time.After(1200 * time.Millisecond):
	}
	historyCount := func() int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.hist.since(time.Time{}))
	}
	if historyCount() == 0 {
		t.Fatal("history ring was not fed without a subscriber")
	}
	deadline := startAt.Add(historyInterval + 2*time.Second)
	for historyCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := historyCount(); n < 2 {
		t.Fatalf("history ring stopped while unsubscribed: got %d sample(s)", n)
	}
	cancel()
	<-done
}

func TestParseLogRequest(t *testing.T) {
	s, home := newTestService(t)
	s.journalctl = "/usr/bin/journalctl"
	writeFile(t, filepath.Join(home, "logs/app.log"), "x\n")
	outside := filepath.Join(t.TempDir(), "secret.log")
	writeFile(t, outside, "x\n")
	if err := os.Symlink(outside, filepath.Join(home, "escape.log")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, query string
		status      int
	}{
		{"unit", "unit=relay", 0},
		{"file in home", "file=~/logs/app.log", 0},
		{"both", "unit=relay&file=~/logs/app.log", 400},
		{"neither", "", 400},
		{"bad unit", "unit=-f", 400},
		{"outside", "file=" + outside, 403},
		{"dotdot", "file=~/../../etc/passwd", 403},
		{"symlink escape", "file=~/escape.log", 403},
		{"directory", "file=~/logs", 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/x?"+tt.query, nil)
			src, err := s.parseLogRequest(r)
			if statusOf(err) != tt.status {
				t.Fatalf("err = %v (status %d want %d)", err, statusOf(err), tt.status)
			}
			if tt.status == 0 && src.unit == "" && src.file == "" {
				t.Fatal("no source")
			}
		})
	}
}

func TestProcessSearchProvider(t *testing.T) {
	f := newFixture(t)
	s, _ := newTestService(t)
	s.col = f.collector()
	s.procs = fixtureLister(f)
	p := s.SearchProvider()
	if p.Scope() != "processes" {
		t.Fatal(p.Scope())
	}
	res := p.Search(context.Background(), "http.server", 5)
	if len(res) != 1 || res[0].ID != "4002" || res[0].Link != "/system/processes?pid=4002" {
		t.Fatalf("results = %+v", res)
	}
	if res := p.Search(context.Background(), "b", 5); res != nil {
		t.Fatalf("single letter should not search: %+v", res)
	}
	if res := p.Search(context.Background(), "4001", 5); len(res) != 1 || res[0].Score != 1 {
		t.Fatalf("pid search = %+v", res)
	}
}
