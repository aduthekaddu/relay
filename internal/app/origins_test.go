package app

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/server"
)

func TestAppLocalOrigins(t *testing.T) {
	t.Setenv("RELAY_DEV", "0")
	cases := []struct {
		canonical string
		want      []string
	}{
		{"http://localhost:47733", []string{"http://localhost:47733", "http://127.0.0.1:47733", "http://[::1]:47733"}},
		{"http://127.0.0.1:47733", []string{"http://127.0.0.1:47733", "http://localhost:47733", "http://[::1]:47733"}},
		{"http://[::1]:47733", []string{"http://[::1]:47733", "http://localhost:47733", "http://127.0.0.1:47733"}},
		{"http://localhost:80", []string{"http://localhost", "http://127.0.0.1", "http://[::1]"}},
		{"https://relay.example.test:443", []string{"https://relay.example.test"}},
		{"https://relay.example.test:47733", []string{"https://relay.example.test:47733"}},
		{"https://localhost:47733", []string{"https://localhost:47733"}},
		{"http://other.localhost:47733", []string{"http://other.localhost:47733"}},
		{"http://192.0.2.1:47733", nil},
		{"http://[2001:db8::1]:47733", nil},
		{"http://*.localhost:47733", nil},
		{"http://localhost:47733/path", nil},
	}
	for _, tc := range cases {
		t.Run(tc.canonical, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Server.PublicURL = tc.canonical
			a := &App{D: &core.Deps{Cfg: cfg}}
			if got := a.Origins(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("origins = %v; want %v", got, tc.want)
			}
			rt := server.NewRouter(originFixtureAuth{}, a.Origins)
			rt.Handle("POST /fixture", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
			for _, origin := range []string{tc.canonical, "http://localhost:47733", "http://127.0.0.1:47733", "http://[::1]:47733", "http://localhost:47734", "http://localhost.evil.test:47733", "https://unrelated.example.test", "http://192.0.2.1:47733"} {
				r := httptest.NewRequest("POST", "/fixture", nil)
				r.Header.Set("Origin", origin)
				w := httptest.NewRecorder()
				rt.ServeHTTP(w, r)
				want := 403
				if server.OriginInList(origin, tc.want) {
					want = 204
				}
				if w.Code != want {
					t.Fatalf("Origin %q: status %d; want %d", origin, w.Code, want)
				}
			}
		})
	}
}

type originFixtureAuth struct{}

func (originFixtureAuth) Identify(*http.Request) *server.Principal {
	return &server.Principal{User: "fixture", Method: "cookie"}
}

func TestAppExplicitOrigins(t *testing.T) {
	t.Setenv("RELAY_DEV", "0")
	cfg := config.Defaults()
	cfg.Server.Listen = "127.0.0.1:47733"
	a := &App{D: &core.Deps{Cfg: cfg}}
	for _, raw := range []string{"*", "http://*.localhost:47733", "http://192.0.2.1:47733", "https://owner@relay.example.test", "https://relay.example.test/path"} {
		a.AllowOrigin(raw)
	}
	if got := len(a.Origins()); got != 3 {
		t.Fatalf("invalid extra origin was added: %v", a.Origins())
	}
	a.AllowOrigin("HTTPS://Relay.Example.Test:443/")
	if got := a.Origins(); len(got) != 4 || got[3] != "https://relay.example.test" {
		t.Fatalf("explicit origin = %v", got)
	}
	// Explicit development origins remain an opt-in exception.
	t.Setenv("RELAY_DEV", "1")
	if got := a.Origins(); len(got) != 24 || !server.OriginInList("http://localhost:47780", got) || server.OriginInList("http://localhost:47790", got) {
		t.Fatalf("development origins = %v", got)
	}
}
