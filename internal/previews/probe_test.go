package previews

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	_, ps, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(ps)
	return p
}

func TestProbe(t *testing.T) {
	tests := []struct {
		name      string
		handler   http.HandlerFunc
		wantHTTP  bool
		title     string
		framework string
	}{
		{"vite", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<!doctype html><html><head><script type="module" src="/@vite/client"></script><title> My  &amp; App </title></head></html>`))
		}, true, "My & App", "Vite"},
		{"next header", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Powered-By", "Next.js")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(`<html><head><title>Home</title></head><body><div id="__next"></div></body></html>`))
		}, true, "Home", "Next.js"},
		{"astro beats vite", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><head><meta name="generator" content="Astro v4"><script src="/@vite/client"></script></head></html>`))
		}, true, "", "Astro"},
		{"flask 404 json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Server", "Werkzeug/3.0 Python/3.12")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			w.Write([]byte(`{"title":"<title>not html</title>"}`))
		}, true, "", "Flask"},
		{"redirect followed", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				http.Redirect(w, r, "/tree", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<title>Jupyter</title><script id="jupyter-config-data"></script>`))
		}, true, "Jupyter", "Jupyter"},
		{"huge body capped", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(strings.Repeat("x", 200<<10) + "<title>late</title>"))
		}, true, "", ""},
	}
	p := newProber()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			r := p.probe(t.Context(), "127.0.0.1", serverPort(t, srv))
			if r.HTTP != tt.wantHTTP || r.Title != tt.title || r.Framework != tt.framework {
				t.Errorf("probe = %+v, want http=%v title=%q fw=%q", r, tt.wantHTTP, tt.title, tt.framework)
			}
		})
	}
}

func TestProbeNonHTTPAndTimeout(t *testing.T) {
	// A raw TCP server that never speaks HTTP.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				c.Write([]byte("-ERR not http\r\n"))
				select {
				case <-done:
				case <-time.After(3 * time.Second):
				}
				c.Close()
			}()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	start := time.Now()
	r := newProber().probe(t.Context(), "127.0.0.1", port)
	if r.HTTP {
		t.Errorf("non-HTTP server detected as HTTP: %+v", r)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("probe took %v, want ≤ ~1s", el)
	}
}

func TestDetectFrameworkFromCmdline(t *testing.T) {
	tests := []struct{ cmd, want string }{
		{"node /w/node_modules/.bin/vite --port 5173", "Vite"},
		{"node /w/node_modules/.bin/next dev", "Next.js"},
		{"python manage.py runserver 0.0.0.0:8000", "Django"},
		{"/usr/bin/python3 -m http.server 8080", "Python http.server"},
		{"python -m uvicorn main:app --reload", "FastAPI"},
		{"node invite-server.js", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := detectFramework(nil, nil, tt.cmd); got != tt.want {
			t.Errorf("detectFramework(%q) = %q, want %q", tt.cmd, got, tt.want)
		}
	}
}
