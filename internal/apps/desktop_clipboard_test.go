package apps

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aduthekaddu/relay/internal/core"
)

func TestClipboardBufferLimit(t *testing.T) {
	for _, tt := range []struct {
		text string
		over bool
	}{{"12", false}, {"123", false}, {"1234", true}} {
		t.Run(tt.text, func(t *testing.T) {
			b := limitedBuffer{limit: 3}
			n, err := io.Copy(&b, io.LimitReader(strings.NewReader(tt.text), int64(len(tt.text))))
			if err != nil || n != int64(len(tt.text)) || b.Len() > 3 || b.over != tt.over {
				t.Fatalf("copy count=%d length=%d over=%v err=%v", n, b.Len(), b.over, err)
			}
		})
	}
}

// Exercise desktop handlers with a synthetic xclip, never the user's display.
func TestDesktopClipboardCanonicalCapture(t *testing.T) {
	d := testDeps(t, nil)
	d.Cfg.Desktop.Enabled = true
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$REL030_XCLIP_FAIL\" = 1 ]; then exit 1; fi\ncase \"$3\" in\n-o) /bin/cat \"$RELAY_DESKTOP_DIR/copy\";;\n-i) /bin/cat >/dev/null; if [ \"$2\" = primary ] && [ \"$REL030_PRIMARY_FAIL\" = 1 ]; then exit 1; fi;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "xclip"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "copy"), []byte("desktop fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REL030_XCLIP_FAIL", "0")
	t.Setenv("REL030_PRIMARY_FAIL", "0")
	k := &desktop{d: d, dir: dir, display: ":fixture", xvnc: &proc{state: stateRunning}, session: &proc{state: stateRunning}}
	s := &Service{d: d, desk: k}
	sub := d.Bus.Subscribe(16, nil)
	defer sub.Close()
	for _, tt := range []struct {
		name, method, body     string
		fail, primary, stopped bool
		wantStatus             int
		capture                string
	}{
		{"read", "GET", "", false, false, false, 200, "desktop fixture"},
		{"write", "POST", `{"text":"written fixture"}`, false, false, false, 204, "written fixture"},
		{"empty write", "POST", `{"text":""}`, false, false, false, 204, ""},
		{"invalid JSON", "POST", `{"text":false}`, false, false, false, 400, ""},
		{"failed read", "GET", "", true, false, false, 200, ""},
		{"failed write", "POST", `{"text":"failed fixture"}`, true, false, false, 503, ""},
		{"partial write", "POST", `{"text":"partial fixture"}`, false, true, false, 503, ""},
		{"stopped read", "GET", "", false, false, true, 409, ""},
		{"stopped write", "POST", `{"text":"stopped fixture"}`, false, false, true, 409, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("REL030_XCLIP_FAIL", map[bool]string{true: "1", false: "0"}[tt.fail])
			t.Setenv("REL030_PRIMARY_FAIL", map[bool]string{true: "1", false: "0"}[tt.primary])
			k.session.state = stateRunning
			if tt.stopped {
				k.session.state = stateStopped
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(tt.method, "/api/v1/desktop/clipboard", strings.NewReader(tt.body))
			if tt.method == "GET" {
				s.handleClipboardGet(w, r)
			} else {
				s.handleClipboardSet(w, r)
			}
			if w.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			select {
			case ev := <-sub.C:
				c, ok := ev.Data.(core.ClipCapture)
				if tt.capture == "" || !ok || ev.Type != core.BusClipCapture || c.Text != tt.capture || c.Source != "desktop" || c.SessionID != "" {
					t.Fatalf("unexpected capture: %+v", ev)
				}
			default:
				if tt.capture != "" {
					t.Fatal("desktop did not publish canonical capture")
				}
			}
		})
	}
	k.session.state = stateRunning
	if err := os.WriteFile(filepath.Join(dir, "copy"), []byte(strings.Repeat("x", maxClipboard+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Clipboard(t.Context()); err == nil {
		t.Fatal("oversized desktop read accepted")
	}
	if err := k.SetClipboard(t.Context(), strings.Repeat("x", maxClipboard+1)); err == nil {
		t.Fatal("oversized desktop write accepted")
	}
	select {
	case ev := <-sub.C:
		t.Fatalf("failed size check published %s", ev.Type)
	default:
	}
	if err := os.WriteFile(filepath.Join(dir, "copy"), []byte("nil bus fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	d.Bus = nil
	if _, err := k.Clipboard(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := k.SetClipboard(context.Background(), "nil bus fixture"); err != nil {
		t.Fatal(err)
	}
}
