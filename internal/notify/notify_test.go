package notify

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/store"
)

type fakePresence struct {
	mu       sync.Mutex
	watching map[string]bool
}

func (f *fakePresence) Watching(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.watching[id]
}

func (f *fakePresence) Online() bool           { return false }
func (f *fakePresence) Subscribed(string) bool { return false }

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestService(t *testing.T, opts ...Option) (*Service, *core.Deps) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	d := &core.Deps{Store: st, Bus: events.New(), Cfg: config.Defaults()}
	s, err := New(d, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s, d
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name    string
		in      api.NotifyRequest
		want    api.Notification
		wantErr string
	}{
		{"defaults", api.NotifyRequest{Title: " Build done "}, api.Notification{Kind: "custom", Title: "Build done", Severity: "info"}, ""},
		{"body becomes title", api.NotifyRequest{Body: "first line\nrest of it"}, api.Notification{Kind: "custom", Title: "first line", Body: "rest of it", Severity: "info"}, ""},
		{"empty", api.NotifyRequest{}, api.Notification{}, "title"},
		{"unknown kind", api.NotifyRequest{Title: "x", Kind: "Weird"}, api.Notification{Kind: "custom", Title: "x", Severity: "info"}, ""},
		{"attention severity", api.NotifyRequest{Title: "x", Kind: "ATTENTION"}, api.Notification{Kind: "attention", Title: "x", Severity: "warning"}, ""},
		{"session link", api.NotifyRequest{Title: "x", SessionID: "t_abc"}, api.Notification{Kind: "custom", Title: "x", Severity: "info", SessionID: "t_abc", Link: "/terminal/t_abc"}, ""},
		{"external link refused", api.NotifyRequest{Title: "x", Link: "https://evil.example"}, api.Notification{}, "link"},
		{"protocol-relative refused", api.NotifyRequest{Title: "x", Link: "//evil.example/x"}, api.Notification{}, "link"},
		{"backslash refused", api.NotifyRequest{Title: "x", Link: "/\\evil.example"}, api.Notification{}, "link"},
		{"bad severity", api.NotifyRequest{Title: "x", Severity: "loud"}, api.Notification{}, "severity"},
		{"explicit severity", api.NotifyRequest{Title: "x", Kind: "done", Severity: "Danger", Link: "/files?path=%2F"}, api.Notification{Kind: "done", Title: "x", Severity: "danger", Link: "/files?path=%2F"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalize(tc.in)
			if tc.wantErr != "" {
				e, ok := err.(*httpx.Err)
				if !ok || e.Field != tc.wantErr {
					t.Fatalf("want error on %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("héllo wörld", 5); got != "héll…" {
		t.Fatalf("got %q", got)
	}
	if got := truncate("abc", 5); got != "abc" {
		t.Fatalf("got %q", got)
	}
}

func TestQuietHours(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 3, 4, h, m, 0, 0, time.Local) }
	tests := []struct {
		start, end string
		t          time.Time
		want       bool
	}{
		{"22:00", "07:00", at(23, 30), true},
		{"22:00", "07:00", at(3, 0), true},
		{"22:00", "07:00", at(7, 0), false},
		{"22:00", "07:00", at(12, 0), false},
		{"22:00", "07:00", at(22, 0), true},
		{"09:00", "17:00", at(12, 0), true},
		{"09:00", "17:00", at(17, 0), false},
		{"09:00", "17:00", at(8, 59), false},
		{"09:00", "09:00", at(9, 0), false},
		{"", "", at(3, 0), false},
		{"25:00", "07:00", at(3, 0), false},
	}
	for _, tc := range tests {
		st := settings{QuietStart: tc.start, QuietEnd: tc.end}
		if got := st.inQuietHours(tc.t); got != tc.want {
			t.Errorf("%s-%s at %s: got %v want %v", tc.start, tc.end, tc.t.Format("15:04"), got, tc.want)
		}
	}
}

func TestParseClock(t *testing.T) {
	for in, want := range map[string]int{"00:00": 0, "7:05": 425, "23:59": 1439} {
		if got, ok := parseClock(in); !ok || got != want {
			t.Errorf("%q: got %d %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "24:00", "12:60", "12", "1:2", "aa:bb", "123:00"} {
		if _, ok := parseClock(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestShouldDeliver(t *testing.T) {
	pres := &fakePresence{watching: map[string]bool{"t_seen": true}}
	s, _ := newTestService(t, WithPresence(func() core.Presence { return pres }))
	s.subs.Store(1) // pretend a device is subscribed
	quiet := func(h int) time.Time { return time.Date(2026, 1, 1, h, 0, 0, 0, time.Local) }
	base := settings{Rules: defaultRules(), QuietStart: "22:00", QuietEnd: "07:00"}
	off := base.clone()
	off.Rules["done"] = false

	tests := []struct {
		name string
		n    api.Notification
		st   settings
		t    time.Time
		want bool
	}{
		{"plain", api.Notification{Kind: "done"}, base, quiet(12), true},
		{"rule off", api.Notification{Kind: "done"}, off, quiet(12), false},
		{"default off kind", api.Notification{Kind: "exited"}, base, quiet(12), false},
		{"quiet hours", api.Notification{Kind: "done"}, base, quiet(23), false},
		{"attention bypasses quiet", api.Notification{Kind: "attention"}, base, quiet(23), true},
		{"security bypasses quiet", api.Notification{Kind: "security"}, base, quiet(2), true},
		{"watched session suppressed", api.Notification{Kind: "attention", SessionID: "t_seen"}, base, quiet(12), false},
		{"other session delivered", api.Notification{Kind: "attention", SessionID: "t_other"}, base, quiet(12), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.shouldDeliver(tc.n, tc.st, tc.t); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}

	s.subs.Store(0)
	if s.shouldDeliver(api.Notification{Kind: "done"}, base, quiet(12)) {
		t.Fatal("delivered with no channel configured")
	}
	withNtfy := base.clone()
	withNtfy.NtfyURL = "https://ntfy.example/topic"
	if !s.shouldDeliver(api.Notification{Kind: "done"}, withNtfy, quiet(12)) {
		t.Fatal("ntfy channel ignored")
	}
}

func TestNotifyInboxDedupeAndEvents(t *testing.T) {
	c := &clock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	pres := &fakePresence{watching: map[string]bool{"t_seen": true}}
	s, d := newTestService(t, WithClock(c.now), WithPresence(func() core.Presence { return pres }))
	s.subs.Store(1)
	sub := d.Bus.Subscribe(16, nil)
	defer sub.Close()
	ctx := context.Background()

	n1, err := s.Notify(ctx, api.NotifyRequest{Kind: "attention", Title: "Claude needs you", SessionID: "t_a"})
	if err != nil {
		t.Fatal(err)
	}
	if n1.ID == "" || n1.At.IsZero() || n1.Read {
		t.Fatalf("bad notification %+v", n1)
	}
	ev := <-sub.C
	if ev.Type != api.EvNotification {
		t.Fatalf("event %q", ev.Type)
	}
	if len(s.queue) != 1 {
		t.Fatalf("queued %d, want 1", len(s.queue))
	}

	// Same session + kind within 30 s: deduped, nothing stored or queued.
	c.add(10 * time.Second)
	n2, err := s.Notify(ctx, api.NotifyRequest{Kind: "attention", Title: "again", SessionID: "t_a"})
	if err != nil {
		t.Fatal(err)
	}
	if n2.ID != n1.ID {
		t.Fatal("duplicate not collapsed")
	}
	// Different kind for the same session is not a duplicate.
	if _, err := s.Notify(ctx, api.NotifyRequest{Kind: "done", Title: "finished", SessionID: "t_a"}); err != nil {
		t.Fatal(err)
	}
	// After the window it is delivered again.
	c.add(31 * time.Second)
	n3, err := s.Notify(ctx, api.NotifyRequest{Kind: "attention", Title: "third", SessionID: "t_a"})
	if err != nil || n3.ID == n1.ID {
		t.Fatalf("not re-notified after window: %v", err)
	}
	// Watched session: stored in the inbox, not queued.
	queued := len(s.queue)
	if _, err := s.Notify(ctx, api.NotifyRequest{Kind: "attention", Title: "seen", SessionID: "t_seen"}); err != nil {
		t.Fatal(err)
	}
	if len(s.queue) != queued {
		t.Fatal("watched session was queued for push")
	}

	list, err := s.List(ctx, 50, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 4 {
		t.Fatalf("inbox has %d, want 4", len(list))
	}
	if list[0].Title != "seen" || list[len(list)-1].ID != n1.ID {
		t.Fatalf("order wrong: %q ... %q", list[0].Title, list[len(list)-1].Title)
	}

	changed, err := s.MarkRead(ctx, []string{n1.ID, "n_missing"}, false)
	if err != nil || len(changed) != 1 {
		t.Fatalf("mark read: %v %v", changed, err)
	}
	if u, _ := s.Unread(ctx); u != 3 {
		t.Fatalf("unread %d", u)
	}
	unread, _ := s.List(ctx, 50, true)
	if len(unread) != 3 {
		t.Fatalf("unread list %d", len(unread))
	}
	changed, _ = s.MarkRead(ctx, nil, true)
	if len(changed) != 3 {
		t.Fatalf("mark all changed %d", len(changed))
	}
	if err := s.Delete(ctx, n1.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, n1.ID); err == nil {
		t.Fatal("double delete succeeded")
	}
}

func TestNotifyQueueFullNeverBlocks(t *testing.T) {
	c := &clock{t: time.Now()}
	s, _ := newTestService(t, WithClock(c.now))
	s.subs.Store(1)
	ctx := context.Background()
	start := time.Now()
	for i := 0; i < QueueSize+20; i++ {
		c.add(time.Minute) // defeat dedupe
		if _, err := s.Notify(ctx, api.NotifyRequest{Title: "n", SessionID: "t_x"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.queue) != QueueSize {
		t.Fatalf("queue %d", len(s.queue))
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("Notify blocked")
	}
}

func TestInboxRetention(t *testing.T) {
	c := &clock{t: time.Now()}
	s, _ := newTestService(t, WithClock(c.now))
	ctx := context.Background()
	for i := 0; i < InboxRetain+5; i++ {
		c.add(time.Minute)
		if _, err := s.Notify(ctx, api.NotifyRequest{Title: "n", SessionID: "t_r"}); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := s.d.Store.DB.QueryRow(`SELECT COUNT(*) FROM notifications`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != InboxRetain {
		t.Fatalf("kept %d, want %d", n, InboxRetain)
	}
}

func TestSettingsPatch(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	str := func(v string) *string { return &v }

	st, err := s.updateSettings(ctx, settingsPatch{
		Rules:      map[string]bool{"done": false},
		QuietStart: str("22:30"),
		QuietEnd:   str("06:45"),
		NtfyURL:    str("https://ntfy.example/relay-topic"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.Rules["done"] || !st.Rules["attention"] || st.QuietStart != "22:30" || st.NtfyURL == "" {
		t.Fatalf("unexpected %+v", st)
	}
	// Persisted: a fresh service over the same store sees it.
	s2, err := New(s.d)
	if err != nil {
		t.Fatal(err)
	}
	st2, _ := s2.loadSettings(ctx)
	if st2.Rules["done"] || st2.QuietEnd != "06:45" {
		t.Fatalf("not persisted: %+v", st2)
	}
	// VAPID keys are stable across restarts.
	if s2.VAPIDPublicKey() != s.VAPIDPublicKey() || s.VAPIDPublicKey() == "" {
		t.Fatal("VAPID key regenerated")
	}

	bad := []settingsPatch{
		{Rules: map[string]bool{"nope": true}},
		{QuietStart: str("25:00"), QuietEnd: str("01:00")},
		{QuietStart: str("22:00"), QuietEnd: str("")},
		{NtfyURL: str("ftp://x")},
		{WebhookURL: str("not a url")},
	}
	for i, p := range bad {
		if _, err := s.updateSettings(ctx, p); err == nil {
			t.Errorf("bad patch %d accepted", i)
		}
	}
	// Clearing works.
	st, err = s.updateSettings(ctx, settingsPatch{QuietStart: str(""), QuietEnd: str(""), NtfyURL: str("")})
	if err != nil || st.QuietStart != "" || st.NtfyURL != "" {
		t.Fatalf("clear failed: %+v %v", st, err)
	}
}

func TestDeviceLabel(t *testing.T) {
	tests := map[string]string{
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Mobile/15E148 Safari/604.1": "iPhone · Safari",
		"Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Mobile Safari/537.36":                                "Android · Chrome",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7; rv:125.0) Gecko/20100101 Firefox/125.0":                                                  "Mac · Firefox",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36 Edg/124.0":                   "Windows · Edge",
		"curl/8.0": "Device",
	}
	for ua, want := range tests {
		if got := deviceLabel(ua); got != want {
			t.Errorf("%q: got %q want %q", ua, got, want)
		}
	}
}
