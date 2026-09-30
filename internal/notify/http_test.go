package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/server"
)

// headerAuth authenticates by the X-Test-Method header (cookie|token).
type headerAuth struct{}

func (headerAuth) Identify(r *http.Request) *server.Principal {
	m := r.Header.Get("X-Test-Method")
	if m == "" {
		return nil
	}
	return &server.Principal{User: "owner", Method: m}
}

const testOrigin = "https://relay.example.test"

func newTestRouter(t *testing.T) (*Service, http.Handler) {
	s, _ := newTestService(t)
	rt := server.NewRouter(headerAuth{}, func() []string { return []string{testOrigin} })
	s.Routes(rt)
	return s, rt
}

func do(t *testing.T, h http.Handler, method, path, body, authMethod string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if authMethod != "" {
		req.Header.Set("X-Test-Method", authMethod)
	}
	req.Header.Set("Origin", testOrigin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPNotifyRequiresLocalOrToken(t *testing.T) {
	_, h := newTestRouter(t)
	body := `{"title":"hello","kind":"done"}`
	if rec := do(t, h, "POST", "/api/v1/notify", body, ""); rec.Code != 401 {
		t.Fatalf("anonymous: %d", rec.Code)
	}
	if rec := do(t, h, "POST", "/api/v1/notify", body, "cookie"); rec.Code != 403 {
		t.Fatalf("cookie: %d", rec.Code)
	}
	rec := do(t, h, "POST", "/api/v1/notify", body, "token")
	if rec.Code != 200 {
		t.Fatalf("token: %d %s", rec.Code, rec.Body)
	}
	var n api.Notification
	if err := json.Unmarshal(rec.Body.Bytes(), &n); err != nil || n.Title != "hello" || n.Kind != "done" {
		t.Fatalf("response %s", rec.Body)
	}
	if rec := do(t, h, "POST", "/api/v1/notify", `{"title":""}`, "token"); rec.Code != 400 {
		t.Fatalf("empty title: %d", rec.Code)
	}
	if rec := do(t, h, "POST", "/api/v1/notify", `{"title":"x","bogus":1}`, "token"); rec.Code != 400 {
		t.Fatalf("unknown field: %d", rec.Code)
	}
}

func TestHTTPInboxFlow(t *testing.T) {
	_, h := newTestRouter(t)
	for _, title := range []string{"one", "two"} {
		if rec := do(t, h, "POST", "/api/v1/notify", `{"title":"`+title+`"}`, "token"); rec.Code != 200 {
			t.Fatal(rec.Body)
		}
	}
	rec := do(t, h, "GET", "/api/v1/notifications?limit=1", "", "cookie")
	var list []api.Notification
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != 200 || len(list) != 1 || list[0].Title != "two" {
		t.Fatalf("list %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "POST", "/api/v1/notifications/read", `{}`, "cookie"); rec.Code != 400 {
		t.Fatalf("empty read: %d", rec.Code)
	}
	if rec := do(t, h, "POST", "/api/v1/notifications/read", `{"ids":["`+list[0].ID+`"]}`, "cookie"); rec.Code != 204 {
		t.Fatalf("read: %d", rec.Code)
	}
	rec = do(t, h, "GET", "/api/v1/notifications?unread=1", "", "cookie")
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Title != "one" {
		t.Fatalf("unread %s", rec.Body)
	}
	if rec := do(t, h, "DELETE", "/api/v1/notifications/"+list[0].ID, "", "cookie"); rec.Code != 204 {
		t.Fatalf("delete %d", rec.Code)
	}
	if rec := do(t, h, "DELETE", "/api/v1/notifications/n_nope", "", "cookie"); rec.Code != 404 {
		t.Fatalf("delete missing %d", rec.Code)
	}
	// Cookie + unsafe method from a foreign origin is refused by the router.
	req := httptest.NewRequest("POST", "/api/v1/notifications/read", strings.NewReader(`{"all":true}`))
	req.Header.Set("X-Test-Method", "cookie")
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("cross-origin: %d", rr.Code)
	}
}

func TestHTTPSettingsAndPush(t *testing.T) {
	s, h := newTestRouter(t)
	rec := do(t, h, "GET", "/api/v1/notify/settings", "", "cookie")
	var st api.NotifySettings
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if rec.Code != 200 || !st.Rules["attention"] || st.VAPIDKey == "" || st.Devices != 0 {
		t.Fatalf("settings %d %s", rec.Code, rec.Body)
	}
	// Round-tripping the GET body (with read-only fields) is accepted.
	rec = do(t, h, "PATCH", "/api/v1/notify/settings", `{"rules":{"done":false},"quietStart":"23:00","quietEnd":"07:00","devices":3,"vapidKey":"x"}`, "cookie")
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if rec.Code != 200 || st.Rules["done"] || st.QuietStart != "23:00" || st.VAPIDKey != s.VAPIDPublicKey() {
		t.Fatalf("patch %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "PATCH", "/api/v1/notify/settings", `{"quietStart":"7pm"}`, "cookie"); rec.Code != 400 {
		t.Fatalf("bad patch %d", rec.Code)
	}

	rec = do(t, h, "GET", "/api/v1/push/key", "", "cookie")
	var key map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &key)
	if key["publicKey"] != s.VAPIDPublicKey() {
		t.Fatalf("key %s", rec.Body)
	}
	if rec := do(t, h, "POST", "/api/v1/push/test", "", "cookie"); rec.Code != 409 {
		t.Fatalf("test without devices: %d", rec.Code)
	}

	push := newFakePushService(t, http.StatusCreated)
	sub, _ := json.Marshal(newBrowserKeys(t).subscription(push.srv.URL + "/p"))
	if rec := do(t, h, "POST", "/api/v1/push/subscribe", string(sub), "cookie"); rec.Code != 204 {
		t.Fatalf("subscribe %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "POST", "/api/v1/push/test", "", "cookie"); rec.Code != 204 {
		t.Fatalf("test %d %s", rec.Code, rec.Body)
	}
	if len(push.requests()) != 1 {
		t.Fatal("test push not sent")
	}
	push.mu.Lock()
	push.status = http.StatusGone
	push.mu.Unlock()
	if rec := do(t, h, "POST", "/api/v1/push/test", "", "cookie"); rec.Code != 502 {
		t.Fatalf("test to gone device %d", rec.Code)
	}
	if s.hasSubscriptions() {
		t.Fatal("gone subscription kept")
	}
	if rec := do(t, h, "POST", "/api/v1/push/unsubscribe", `{"endpoint":"https://push.example/none"}`, "cookie"); rec.Code != 204 {
		t.Fatalf("unsubscribe %d", rec.Code)
	}
}
