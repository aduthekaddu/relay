package notify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// browserKeys is a synthetic push subscription key pair, as a browser's
// PushManager would create it.
type browserKeys struct {
	priv *ecdh.PrivateKey
	auth []byte
}

func newBrowserKeys(t *testing.T) browserKeys {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return browserKeys{priv: priv, auth: auth}
}

func (k browserKeys) subscription(endpoint string) api.PushSubscription {
	var s api.PushSubscription
	s.Endpoint = endpoint
	s.Keys.P256dh = base64.RawURLEncoding.EncodeToString(k.priv.PublicKey().Bytes())
	s.Keys.Auth = base64.RawURLEncoding.EncodeToString(k.auth)
	return s
}

// decrypt implements the user-agent side of RFC 8291 (aes128gcm).
func (k browserKeys) decrypt(t *testing.T, body []byte) []byte {
	t.Helper()
	if len(body) < 21 {
		t.Fatalf("body too short: %d", len(body))
	}
	salt := body[:16]
	rs := binary.BigEndian.Uint32(body[16:20])
	idlen := int(body[20])
	asPub := body[21 : 21+idlen]
	ct := body[21+idlen:]
	if rs < 18 {
		t.Fatalf("record size %d", rs)
	}
	remote, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		t.Fatalf("server key: %v", err)
	}
	shared, err := k.priv.ECDH(remote)
	if err != nil {
		t.Fatal(err)
	}
	prkKey, _ := hkdf.Extract(sha256.New, shared, k.auth)
	info := append(append([]byte("WebPush: info\x00"), k.priv.PublicKey().Bytes()...), asPub...)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, string(info), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	pt = bytes.TrimRight(pt, "\x00")
	if len(pt) == 0 || pt[len(pt)-1] != 2 {
		t.Fatal("missing last-record delimiter")
	}
	return pt[:len(pt)-1]
}

type pushRequest struct {
	header http.Header
	body   []byte
}

type fakePushService struct {
	mu     sync.Mutex
	got    []pushRequest
	status int
	srv    *httptest.Server
}

func newFakePushService(t *testing.T, status int) *fakePushService {
	f := &fakePushService{status: status}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.got = append(f.got, pushRequest{header: r.Header.Clone(), body: b})
		st := f.status
		f.mu.Unlock()
		w.WriteHeader(st)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePushService) requests() []pushRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]pushRequest(nil), f.got...)
}

func TestWebPushEncryptionAndHeaders(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	push := newFakePushService(t, http.StatusCreated)
	keys := newBrowserKeys(t)
	sub := keys.subscription(push.srv.URL + "/push/abc")
	if err := s.Subscribe(ctx, sub, "Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) Safari/604.1"); err != nil {
		t.Fatal(err)
	}
	if !s.hasSubscriptions() {
		t.Fatal("subscription count not updated")
	}

	n := api.Notification{ID: "n_1", Kind: "attention", Title: "Codex needs you", Body: "Approve running tests?", Link: "/terminal/t_x", SessionID: "t_x"}
	sent, err := s.sendPush(ctx, n)
	if err != nil || sent != 1 {
		t.Fatalf("sent %d err %v", sent, err)
	}
	reqs := push.requests()
	if len(reqs) != 1 {
		t.Fatalf("push requests %d", len(reqs))
	}
	h := reqs[0].header
	if h.Get("Content-Encoding") != "aes128gcm" || h.Get("TTL") != "43200" || h.Get("Urgency") != "high" {
		t.Fatalf("headers: enc=%q ttl=%q urgency=%q", h.Get("Content-Encoding"), h.Get("TTL"), h.Get("Urgency"))
	}
	authz := h.Get("Authorization")
	if !strings.HasPrefix(authz, "vapid t=") || !strings.Contains(authz, "k="+s.VAPIDPublicKey()) {
		t.Fatalf("authorization %q", authz)
	}
	var p pushPayload
	if err := json.Unmarshal(keys.decrypt(t, reqs[0].body), &p); err != nil {
		t.Fatal(err)
	}
	want := pushPayload{Title: n.Title, Body: n.Body, Link: n.Link, Tag: "attention:t_x", Kind: "attention"}
	if p != want {
		t.Fatalf("payload %+v want %+v", p, want)
	}

	// Non-attention kinds use normal urgency and the id as tag.
	if _, err := s.sendPush(ctx, api.Notification{ID: "n_2", Kind: "done", Title: "Done"}); err != nil {
		t.Fatal(err)
	}
	reqs = push.requests()
	if reqs[1].header.Get("Urgency") != "normal" {
		t.Fatalf("urgency %q", reqs[1].header.Get("Urgency"))
	}
	_ = json.Unmarshal(keys.decrypt(t, reqs[1].body), &p)
	if p.Tag != "n_2" {
		t.Fatalf("tag %q", p.Tag)
	}
}

func TestWebPushGoneSubscriptionsAreDeleted(t *testing.T) {
	for _, status := range []int{http.StatusGone, http.StatusNotFound} {
		s, _ := newTestService(t)
		ctx := context.Background()
		gone := newFakePushService(t, status)
		alive := newFakePushService(t, http.StatusCreated)
		if err := s.Subscribe(ctx, newBrowserKeys(t).subscription(gone.srv.URL+"/gone"), ""); err != nil {
			t.Fatal(err)
		}
		if err := s.Subscribe(ctx, newBrowserKeys(t).subscription(alive.srv.URL+"/alive"), ""); err != nil {
			t.Fatal(err)
		}
		sent, err := s.sendPush(ctx, api.Notification{ID: "n", Kind: "done", Title: "x"})
		if err != nil || sent != 1 {
			t.Fatalf("status %d: sent %d err %v", status, sent, err)
		}
		if n, _ := s.countSubscriptions(ctx); n != 1 {
			t.Fatalf("status %d: %d subscriptions left, want 1", status, n)
		}
		// A server error keeps the subscription and counts a failure.
		alive.mu.Lock()
		alive.status = http.StatusInternalServerError
		alive.mu.Unlock()
		if _, err := s.sendPush(ctx, api.Notification{ID: "n", Kind: "done", Title: "x"}); err == nil {
			t.Fatal("5xx reported as success")
		}
		var failures int
		_ = s.d.Store.DB.QueryRow(`SELECT failures FROM push_subscriptions`).Scan(&failures)
		if failures != 1 {
			t.Fatalf("failures %d", failures)
		}
	}
}

func TestSubscribeValidation(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	good := newBrowserKeys(t).subscription("https://push.example/sub/1")
	if err := s.Subscribe(ctx, good, ""); err != nil {
		t.Fatal(err)
	}
	// Re-subscribing the same endpoint is an upsert.
	good.Device = "Kitchen iPad"
	if err := s.Subscribe(ctx, good, ""); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.countSubscriptions(ctx); n != 1 {
		t.Fatalf("count %d", n)
	}
	var dev string
	_ = s.d.Store.DB.QueryRow(`SELECT device FROM push_subscriptions`).Scan(&dev)
	if dev != "Kitchen iPad" {
		t.Fatalf("device %q", dev)
	}

	bad := []func(*api.PushSubscription){
		func(p *api.PushSubscription) { p.Endpoint = "http://push.example/x" },
		func(p *api.PushSubscription) { p.Endpoint = "not a url" },
		func(p *api.PushSubscription) { p.Keys.P256dh = "AAAA" },
		func(p *api.PushSubscription) { p.Keys.Auth = base64.RawURLEncoding.EncodeToString([]byte("short")) },
	}
	for i, mut := range bad {
		p := newBrowserKeys(t).subscription("https://push.example/sub/other")
		mut(&p)
		if err := s.Subscribe(ctx, p, ""); err == nil {
			t.Errorf("bad subscription %d accepted", i)
		}
	}
	if err := s.Unsubscribe(ctx, good.Endpoint); err != nil {
		t.Fatal(err)
	}
	if s.hasSubscriptions() {
		t.Fatal("still subscribed")
	}
}

func TestNtfyAndWebhookDelivery(t *testing.T) {
	type got struct {
		header http.Header
		body   []byte
	}
	ch := make(chan got, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ch <- got{r.Header.Clone(), b}
		if strings.HasSuffix(r.URL.Path, "/fail") {
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer srv.Close()

	s, d := newTestService(t)
	d.Cfg.Server.PublicURL = "https://relay.example.test"
	ctx := context.Background()
	n := api.Notification{ID: "n_9", Kind: "attention", Title: "Gemini wartet", Body: "Needs approval", Link: "/terminal/t_9", Agent: "gemini", Severity: "warning", At: time.Now()}

	if err := s.sendNtfy(ctx, srv.URL+"/relay-topic", n); err != nil {
		t.Fatal(err)
	}
	g := <-ch
	if string(g.body) != "Needs approval" {
		t.Fatalf("ntfy body %q", g.body)
	}
	if g.header.Get("Priority") != "4" || g.header.Get("Click") != "https://relay.example.test/terminal/t_9" {
		t.Fatalf("ntfy headers %v", g.header)
	}
	if title := g.header.Get("Title"); !strings.HasPrefix(title, "=?UTF-8?b?") && !strings.HasPrefix(title, "=?utf-8?b?") && title != "Gemini wartet" {
		t.Fatalf("ntfy title %q", title)
	}
	if tags := g.header.Get("Tags"); !strings.Contains(tags, "bell") || !strings.Contains(tags, "attention") {
		t.Fatalf("ntfy tags %q", tags)
	}

	if err := s.sendWebhook(ctx, srv.URL+"/hook", n); err != nil {
		t.Fatal(err)
	}
	g = <-ch
	if g.header.Get("Content-Type") != "application/json" {
		t.Fatalf("webhook content type %q", g.header.Get("Content-Type"))
	}
	var body map[string]any
	if err := json.Unmarshal(g.body, &body); err != nil {
		t.Fatal(err)
	}
	if body["title"] != n.Title || body["kind"] != "attention" || body["url"] != "https://relay.example.test/terminal/t_9" || body["text"] != "Gemini wartet — Needs approval" {
		t.Fatalf("webhook body %v", body)
	}

	if err := s.sendWebhook(ctx, srv.URL+"/fail", n); err == nil {
		t.Fatal("5xx webhook reported success")
	}
	<-ch
}

func TestWebhookTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	s, _ := newTestService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := s.sendWebhook(ctx, srv.URL, api.Notification{Title: "x"}); err == nil {
		t.Fatal("hung webhook succeeded")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("webhook did not honour its deadline")
	}
}

func TestWorkerDeliversAllChannels(t *testing.T) {
	hits := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- r.URL.Path
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	s, _ := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = s.Start(ctx); close(done) }()

	if err := s.Subscribe(ctx, newBrowserKeys(t).subscription(srv.URL+"/push"), ""); err != nil {
		t.Fatal(err)
	}
	str := func(v string) *string { return &v }
	if _, err := s.updateSettings(ctx, settingsPatch{NtfyURL: str(srv.URL + "/ntfy"), WebhookURL: str(srv.URL + "/hook")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Notify(ctx, api.NotifyRequest{Kind: "done", Title: "Tests passed"}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	timeout := time.After(5 * time.Second)
	for len(seen) < 3 {
		select {
		case p := <-hits:
			seen[p] = true
		case <-timeout:
			t.Fatalf("delivered to %v only", seen)
		}
	}
	cancel()
	<-done
}
