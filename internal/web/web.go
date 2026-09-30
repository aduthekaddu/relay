// Package web serves the embedded single-page app built from /web.
//
// `pnpm --dir web build` writes into internal/web/dist. Hashed assets under
// /assets/ are immutable; index.html and the service worker are no-cache.
// When a pre-compressed sibling exists (file.br / file.gz) and the client
// accepts it, that is served instead.
package web

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// FS returns the embedded dist directory.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// Handler serves static files, falling back to index.html for client-side
// routes (any path without a file extension that is not /api/).
func Handler() http.Handler {
	root := FS()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if strings.HasPrefix(p, "api/") {
			http.NotFound(w, r)
			return
		}
		if _, err := fs.Stat(root, p); err != nil {
			if path.Ext(p) != "" {
				http.NotFound(w, r)
				return
			}
			p = "index.html"
		}
		serveFile(w, r, root, p)
	})
}

func serveFile(w http.ResponseWriter, r *http.Request, root fs.FS, p string) {
	h := w.Header()
	switch {
	case strings.HasPrefix(p, "assets/"):
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	case p == "index.html" || p == "sw.js" || strings.HasSuffix(p, ".webmanifest"):
		h.Set("Cache-Control", "no-cache")
	default:
		h.Set("Cache-Control", "public, max-age=3600")
	}
	if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
		h.Set("Content-Type", ct)
	} else if strings.HasSuffix(p, ".webmanifest") {
		h.Set("Content-Type", "application/manifest+json")
	}
	h.Add("Vary", "Accept-Encoding")
	ae := r.Header.Get("Accept-Encoding")
	for _, enc := range []struct{ ext, name string }{{".br", "br"}, {".gz", "gzip"}} {
		if strings.Contains(ae, enc.name) {
			if f, err := root.Open(p + enc.ext); err == nil {
				defer f.Close()
				h.Set("Content-Encoding", enc.name)
				if rs, ok := f.(interface {
					Read([]byte) (int, error)
				}); ok {
					w.WriteHeader(http.StatusOK)
					if r.Method != http.MethodHead {
						buf := make([]byte, 32*1024)
						for {
							n, err := rs.Read(buf)
							if n > 0 {
								if _, werr := w.Write(buf[:n]); werr != nil {
									return
								}
							}
							if err != nil {
								return
							}
						}
					}
					return
				}
			}
		}
	}
	http.ServeFileFS(w, r, root, p)
}
