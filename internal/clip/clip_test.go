package clip

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
)

func newTestService(t *testing.T) (*Service, *core.Deps) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	d := &core.Deps{Store: st, Bus: events.New()}
	s, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	n := 0
	s.now = func() time.Time { n++; return base.Add(time.Duration(n) * time.Second) }
	return s, d
}

func TestAddDedupeAndRing(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()

	a, err := s.Add(ctx, "hello", "web")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Add(ctx, "hello", "osc52")
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != a.ID || b.Source != "osc52" || !b.At.After(a.At) {
		t.Fatalf("consecutive duplicate not collapsed: %+v vs %+v", a, b)
	}
	if _, err := s.Add(ctx, "world", "cli"); err != nil {
		t.Fatal(err)
	}
	// Non-consecutive repeat creates a new entry.
	c, _ := s.Add(ctx, "hello", "bogus-source")
	if c.ID == a.ID || c.Source != "web" {
		t.Fatalf("non-consecutive repeat: %+v", c)
	}
	list, _ := s.List(ctx, 10)
	if len(list) != 3 || list[0].ID != c.ID || list[1].Text != "world" {
		t.Fatalf("list %+v", list)
	}

	for i := 0; i < Retain+10; i++ {
		if _, err := s.Add(ctx, strings.Repeat("x", i+1), "cli"); err != nil {
			t.Fatal(err)
		}
	}
	list, _ = s.List(ctx, Retain+50)
	if len(list) != Retain {
		t.Fatalf("kept %d want %d", len(list), Retain)
	}
	if list[0].Size != Retain+10 {
		t.Fatalf("newest size %d", list[0].Size)
	}
}

func TestAddValidation(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	for _, text := range []string{"", "   \n", strings.Repeat("a", MaxBytes+1)} {
		if _, err := s.Add(ctx, text, "web"); err == nil {
			t.Errorf("accepted %d bytes", len(text))
		}
	}
	if _, err := s.Add(ctx, strings.Repeat("a", MaxBytes), "web"); err != nil {
		t.Fatalf("max size refused: %v", err)
	}
	c, err := s.Add(ctx, "bad \xff utf8", "web")
	if err != nil || !strings.Contains(c.Text, "\uFFFD") {
		t.Fatalf("invalid utf8: %v %q", err, c.Text)
	}
}

func TestBusCapture(t *testing.T) {
	s, d := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Start(ctx); close(done) }()
	evs := d.Bus.Subscribe(8, func(e api.Event) bool { return e.Type == api.EvClip })
	defer evs.Close()

	// Wait for the service's subscription to exist.
	for d.Bus.Subscribers() < 2 {
		time.Sleep(time.Millisecond)
	}
	type capture struct {
		Text      string
		Source    string
		SessionID string
	}
	d.Bus.Publish(core.BusClipCapture, capture{Text: "copied in vim", Source: "osc52", SessionID: "t_1"})
	d.Bus.Publish(core.BusClipCapture, map[string]string{"text": "from desktop", "source": "desktop"})
	d.Bus.Publish(core.BusClipCapture, "plain string")

	want := []struct{ text, source string }{{"copied in vim", "osc52"}, {"from desktop", "desktop"}, {"plain string", "terminal"}}
	for _, w := range want {
		select {
		case ev := <-evs.C:
			c := ev.Data.(api.Clip)
			if c.Text != w.text || c.Source != w.source {
				t.Fatalf("got %+v want %+v", c, w)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("capture not stored")
		}
	}
	cancel()
	<-done
}

type auth struct{}

func (auth) Identify(r *http.Request) *server.Principal {
	return &server.Principal{User: "owner", Method: "token"}
}

func TestHTTP(t *testing.T) {
	s, _ := newTestService(t)
	rt := server.NewRouter(auth{}, func() []string { return nil })
	s.Routes(rt)
	call := func(method, path, body string, local bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if local {
			req = req.WithContext(server.MarkLocal(req.Context()))
		}
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, req)
		return rec
	}
	rec := call("POST", "/api/v1/clip", `{"text":"from phone"}`, false)
	var c api.Clip
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	if rec.Code != 200 || c.Source != "web" {
		t.Fatalf("post %d %s", rec.Code, rec.Body)
	}
	rec = call("POST", "/api/v1/clip", `{"text":"from cli"}`, true)
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	if c.Source != "cli" {
		t.Fatalf("local source %q", c.Source)
	}
	big, _ := json.Marshal(map[string]string{"text": strings.Repeat("é", MaxBytes)})
	if rec := call("POST", "/api/v1/clip", string(big), false); rec.Code != 413 {
		t.Fatalf("oversized %d", rec.Code)
	}
	rec = call("GET", "/api/v1/clip?limit=1", "", false)
	var list []api.Clip
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Text != "from cli" {
		t.Fatalf("list %s", rec.Body)
	}
	if rec := call("DELETE", "/api/v1/clip/"+list[0].ID, "", false); rec.Code != 204 {
		t.Fatalf("delete %d", rec.Code)
	}
	if rec := call("DELETE", "/api/v1/clip/"+list[0].ID, "", false); rec.Code != 404 {
		t.Fatalf("delete again %d", rec.Code)
	}
	if rec := call("DELETE", "/api/v1/clip", "", false); rec.Code != 204 {
		t.Fatalf("clear %d", rec.Code)
	}
	rec = call("GET", "/api/v1/clip", "", false)
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("after clear %s", rec.Body)
	}
}
