package clip

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
)

func TestDecodeCaptureCompatibility(t *testing.T) {
	legacy := struct{ Text, Source string }{"legacy", "desktop"}
	for _, tt := range []struct {
		name         string
		data         any
		text, source string
		bad          bool
	}{
		{"canonical", core.ClipCapture{Text: "copy", Source: "terminal", SessionID: "t_fixture"}, "copy", "terminal", false},
		{"pointer", &core.ClipCapture{Text: "copy", Source: "desktop"}, "copy", "desktop", false},
		{"default source", core.ClipCapture{Text: "copy"}, "copy", "terminal", false},
		{"legacy struct", legacy, "legacy", "desktop", false},
		{"lowercase map", map[string]any{"text": "map", "source": "osc52", "extra": true}, "map", "osc52", false},
		{"uppercase map", map[string]string{"Text": "map", "Source": "desktop"}, "map", "desktop", false},
		{"ignored session metadata", map[string]any{"text": "legacy metadata", "SessionID": 42}, "legacy metadata", "terminal", false},
		{"string", "string", "string", "terminal", false},
		{"bytes", []byte("bytes"), "bytes", "terminal", false},
		{"nil pointer", (*core.ClipCapture)(nil), "", "", true},
		{"number", 42, "", "", true},
		{"array", []string{"copy"}, "", "", true},
		{"wrong field", map[string]any{"text": 42}, "", "", true},
		{"unserializable", make(chan int), "", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			text, source, err := decodeCapture(tt.data)
			if (err != nil) != tt.bad || text != tt.text || source != tt.source {
				t.Fatalf("decode = %q, %q, %v", text, source, err)
			}
		})
	}
}

func captureEvent(t *testing.T, ch <-chan api.Event) api.Clip {
	t.Helper()
	select {
	case ev := <-ch:
		c, ok := ev.Data.(api.Clip)
		if !ok || ev.Type != api.EvClip {
			t.Fatalf("unexpected event: %+v", ev)
		}
		return c
	case <-time.After(3 * time.Second):
		t.Fatal("capture not stored")
		return api.Clip{}
	}
}

func TestCaptureStartupValidationAndTeardown(t *testing.T) {
	s, d := newTestService(t)
	evs := d.Bus.Subscribe(16, func(e api.Event) bool { return e.Type == api.EvClip })
	defer evs.Close()
	// These captures arrive before the background loop is scheduled.
	for _, data := range []any{nil, (*core.ClipCapture)(nil), 42, make(chan int), map[string]any{"text": false}, core.ClipCapture{Text: " \n"}, core.ClipCapture{Text: strings.Repeat("x", MaxBytes+1)}, core.ClipCapture{Text: strings.Repeat("\xffa", MaxBytes/2)}} {
		d.Bus.Publish(core.BusClipCapture, data)
	}
	d.Bus.Publish(api.EvClip, "public events must not become captures")
	// Drain the public event; it must not be processed again as a capture.
	<-evs.C
	d.Bus.Publish(core.BusClipCapture, core.ClipCapture{Text: "same copy", Source: "terminal", SessionID: "t_fixture"})
	d.Bus.Publish(core.BusClipCapture, &core.ClipCapture{Text: "same copy", Source: "desktop"})
	d.Bus.Publish(core.BusClipCapture, core.ClipCapture{Text: strings.Repeat("x", MaxBytes), Source: "desktop"})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("capture loop did not stop")
		}
	})
	first, second, boundary := captureEvent(t, evs.C), captureEvent(t, evs.C), captureEvent(t, evs.C)
	if first.Text != "same copy" || first.Source != "terminal" || second.ID != first.ID || second.Source != "desktop" || !second.At.After(first.At) || boundary.Size != MaxBytes {
		t.Fatalf("unexpected captures: first=%+v second=%+v boundary size=%d", first, second, boundary.Size)
	}
	list, err := s.List(ctx, 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("history length=%d, err=%v", len(list), err)
	}
	// Closing twice also stops a running loop and prevents new captures.
	_ = s.Close()
	_ = s.Close()
	d.Bus.Publish(core.BusClipCapture, core.ClipCapture{Text: "after close"})
	if d.Bus.Subscribers() != 1 {
		t.Fatal("capture subscription leaked")
	}
	select {
	case ev := <-evs.C:
		t.Fatalf("unexpected extra stored event: %s", ev.Type)
	default:
	}
}

func TestCaptureCloseBeforeStart(t *testing.T) {
	s, d := newTestService(t)
	_ = s.Close()
	if d.Bus.Subscribers() != 0 {
		t.Fatal("construction subscription leaked")
	}
	if err := s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureNilBus(t *testing.T) {
	s, d := newTestService(t)
	_ = s.Close()
	d.Bus = nil
	s, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()
	if _, err := s.Add(ctx, "HTTP without capture bus", "web"); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nil-bus loop did not stop")
	}
}

func TestCaptureConcurrentDedupe(t *testing.T) {
	s, _ := newTestService(t)
	// Add owns the clock call under its serialization lock.
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Add(t.Context(), "concurrent copy", "desktop"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	list, err := s.List(t.Context(), 20)
	if err != nil || len(list) != 1 {
		t.Fatalf("history length=%d, err=%v", len(list), err)
	}
}
