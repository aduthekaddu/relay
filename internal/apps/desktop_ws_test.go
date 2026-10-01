package apps

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// rfbInit is what a client learns from the RFB 3.8 handshake.
type rfbInit struct {
	Width, Height int
	Name          string
}

// rfbHandshake runs the client side of RFB 3.8 with security type None
// and a shared ClientInit, then requests one full framebuffer update and
// reads its header. It returns the server init and the rectangle count.
func rfbHandshake(c net.Conn) (rfbInit, int, error) {
	var init rfbInit
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	ver := make([]byte, 12)
	if _, err := io.ReadFull(c, ver); err != nil {
		return init, 0, fmt.Errorf("read version: %w", err)
	}
	if !strings.HasPrefix(string(ver), "RFB 003.") {
		return init, 0, fmt.Errorf("bad version %q", ver)
	}
	if _, err := c.Write([]byte("RFB 003.008\n")); err != nil {
		return init, 0, err
	}
	var n [1]byte
	if _, err := io.ReadFull(c, n[:]); err != nil || n[0] == 0 {
		return init, 0, fmt.Errorf("security types: n=%d err=%v", n[0], err)
	}
	types := make([]byte, n[0])
	if _, err := io.ReadFull(c, types); err != nil {
		return init, 0, err
	}
	if !strings.ContainsRune(string(types), 1) {
		return init, 0, fmt.Errorf("no None security type in %v", types)
	}
	if _, err := c.Write([]byte{1}); err != nil {
		return init, 0, err
	}
	var res [4]byte
	if _, err := io.ReadFull(c, res[:]); err != nil || binary.BigEndian.Uint32(res[:]) != 0 {
		return init, 0, fmt.Errorf("security result %v err=%v", res, err)
	}
	if _, err := c.Write([]byte{1}); err != nil { // ClientInit: shared
		return init, 0, err
	}
	var si [24]byte
	if _, err := io.ReadFull(c, si[:]); err != nil {
		return init, 0, fmt.Errorf("server init: %w", err)
	}
	init.Width = int(binary.BigEndian.Uint16(si[0:2]))
	init.Height = int(binary.BigEndian.Uint16(si[2:4]))
	name := make([]byte, binary.BigEndian.Uint32(si[20:24]))
	if _, err := io.ReadFull(c, name); err != nil {
		return init, 0, err
	}
	init.Name = string(name)
	req := []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(req[6:8], uint16(init.Width))
	binary.BigEndian.PutUint16(req[8:10], uint16(init.Height))
	if _, err := c.Write(req); err != nil {
		return init, 0, err
	}
	var hdr [4]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return init, 0, fmt.Errorf("framebuffer update: %w", err)
	}
	if hdr[0] != 0 {
		return init, 0, fmt.Errorf("unexpected server message type %d", hdr[0])
	}
	return init, int(binary.BigEndian.Uint16(hdr[2:4])), nil
}

// fakeRFB serves the server side of the same exchange on a unix socket,
// writing the ServerInit in small pieces to exercise reassembly.
func fakeRFB(t *testing.T, sock string, w, h int) *sync.WaitGroup {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer ln.Close()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 64)
		c.Write([]byte("RFB 003.008\n"))
		io.ReadFull(c, buf[:12])
		c.Write([]byte{1, 1})
		io.ReadFull(c, buf[:1])
		c.Write([]byte{0, 0, 0, 0})
		io.ReadFull(c, buf[:1])
		si := make([]byte, 24)
		binary.BigEndian.PutUint16(si[0:2], uint16(w))
		binary.BigEndian.PutUint16(si[2:4], uint16(h))
		binary.BigEndian.PutUint32(si[20:24], 5)
		si = append(si, "Relay"...)
		for i := 0; i < len(si); i += 7 {
			c.Write(si[i:min(i+7, len(si))])
		}
		io.ReadFull(c, buf[:10])
		c.Write([]byte{0, 0, 0, 1})
		io.Copy(io.Discard, c) // until the bridge closes us
	}()
	return &wg
}

func testDesktop(t *testing.T, sock string, ensure func(context.Context) error) *desktop {
	t.Helper()
	return &desktop{
		d: testDeps(t, nil), log: slog.New(slog.NewTextHandler(io.Discard, nil)), sock: sock,
		conns: map[net.Conn]struct{}{}, ensure: ensure, unavailable: "test",
		scanner: &runningScanner{procDir: t.TempDir(), ttl: time.Second},
	}
}

func dialDesktopWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{"binary"}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if resp.Header.Get("Sec-WebSocket-Protocol") != "binary" {
		t.Errorf("subprotocol = %q", resp.Header.Get("Sec-WebSocket-Protocol"))
	}
	return ws
}

func TestDesktopBridgeHandshake(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "vnc.sock")
	srv := fakeRFB(t, sock, 1600, 1000)
	k := testDesktop(t, sock, func(context.Context) error { return nil })
	hs := httptest.NewServer(http.HandlerFunc(k.ServeWS))
	defer hs.Close()

	ws := dialDesktopWS(t, "ws"+strings.TrimPrefix(hs.URL, "http"))
	conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	init, rects, err := rfbHandshake(conn)
	if err != nil {
		t.Fatal(err)
	}
	if init.Width != 1600 || init.Height != 1000 || init.Name != "Relay" || rects != 1 {
		t.Fatalf("got %+v rects=%d", init, rects)
	}
	if v := k.State().Viewers; v != 1 {
		t.Errorf("viewers during session = %d, want 1", v)
	}
	conn.Close()
	srv.Wait() // the bridge closed the unix side
	waitFor(t, func() bool { return k.State().Viewers == 0 })
}

func TestDesktopBridgeServerClose(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "vnc.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Write([]byte("RFB 003.008\n"))
			c.Close() // desktop stops
		}
		ln.Close()
	}()
	k := testDesktop(t, sock, func(context.Context) error { return nil })
	hs := httptest.NewServer(http.HandlerFunc(k.ServeWS))
	defer hs.Close()
	ws := dialDesktopWS(t, "ws"+strings.TrimPrefix(hs.URL, "http"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, msg, err := ws.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || string(msg) != "RFB 003.008\n" {
		t.Fatalf("first frame: %v %q %v", typ, msg, err)
	}
	_, _, err = ws.Read(ctx)
	if code := websocket.CloseStatus(err); code != websocket.StatusGoingAway {
		t.Fatalf("close status = %v (%v), want going away", code, err)
	}
	waitFor(t, func() bool { return k.State().Viewers == 0 })
}

func TestDesktopBridgeRejectsText(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "vnc.sock")
	fakeRFB(t, sock, 800, 600)
	k := testDesktop(t, sock, func(context.Context) error { return nil })
	hs := httptest.NewServer(http.HandlerFunc(k.ServeWS))
	defer hs.Close()
	ws := dialDesktopWS(t, "ws"+strings.TrimPrefix(hs.URL, "http"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageText, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	var err error
	for err == nil {
		_, _, err = ws.Read(ctx)
	}
	if code := websocket.CloseStatus(err); code != websocket.StatusUnsupportedData {
		t.Fatalf("close status = %v (%v)", code, err)
	}
}

func TestDesktopBridgeErrors(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name   string
		ensure func(context.Context) error
		status int
	}{
		{"start fails", func(context.Context) error { return errors.New("boom") }, http.StatusInternalServerError},
		{"socket missing", func(context.Context) error { return nil }, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := testDesktop(t, filepath.Join(dir, "none.sock"), tt.ensure)
			rec := httptest.NewRecorder()
			k.ServeWS(rec, httptest.NewRequest("GET", "/api/v1/desktop/ws", nil))
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
		})
	}
}

func TestDesktopViewerLimit(t *testing.T) {
	k := testDesktop(t, "", nil)
	var conns []net.Conn
	for i := 0; i < maxViewers; i++ {
		a, b := net.Pipe()
		defer b.Close()
		if !k.addViewer(a) {
			t.Fatalf("viewer %d refused", i)
		}
		conns = append(conns, a)
	}
	extra, other := net.Pipe()
	defer other.Close()
	if k.addViewer(extra) {
		t.Fatal("viewer over the limit accepted")
	}
	k.closeViewers()
	for _, c := range conns {
		k.removeViewer(c)
	}
	k.removeViewer(conns[0]) // idempotent
	if k.viewers != 0 {
		t.Fatalf("viewers = %d", k.viewers)
	}
}

// TestDesktopLiveRFB drives a real desktop through a running Relay: set
// RELAY_DESKTOP_TEST_SOCKET to its control socket (relay.sock).
func TestDesktopLiveRFB(t *testing.T) {
	ctl := os.Getenv("RELAY_DESKTOP_TEST_SOCKET")
	if ctl == "" {
		t.Skip("RELAY_DESKTOP_TEST_SOCKET not set")
	}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", ctl)
		},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://relay/api/v1/desktop/ws", &websocket.DialOptions{HTTPClient: client, Subprotocols: []string{"binary"}})
	if err != nil {
		t.Fatal(err)
	}
	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	defer conn.Close()
	init, rects, err := rfbHandshake(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("desktop %dx%d %q, first update with %d rectangles", init.Width, init.Height, init.Name, rects)
	if init.Width == 0 || rects == 0 {
		t.Fatal("empty framebuffer update")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
