package files

import (
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

// inlineTypes may be displayed by the browser directly. Everything else is
// served as an attachment. Script-capable formats (HTML, SVG, XML, JS)
// are never inline even though the sandbox CSP would neuter them.
var inlineTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"image/avif": true, "image/bmp": true, "image/x-icon": true,
	"application/pdf": true, "text/plain": true,
}

// scriptable types are always downloaded.
var scriptable = map[string]bool{
	"text/html": true, "application/xhtml+xml": true, "image/svg+xml": true,
	"text/xml": true, "application/xml": true, "text/javascript": true,
	"application/javascript": true, "application/x-javascript": true,
	"text/xsl": true, "application/xslt+xml": true, "application/wasm": true,
}

// textual types (other than the scriptable ones) are shown inline as
// text/plain so code, markdown, JSON and logs open as readable text.
func textual(t string) bool {
	if strings.HasPrefix(t, "text/") {
		return true
	}
	switch t {
	case "application/json", "application/x-ndjson", "application/yaml", "application/toml",
		"application/sql", "application/x-asciicast", "application/x-sh":
		return true
	}
	return false
}

// rawDisposition decides the Content-Type and whether a response is inline.
// name is the file name, sniffed the http.DetectContentType result for the
// first bytes (may be "").
func rawDisposition(name, sniffed string, download bool) (ctype string, inline bool) {
	t := mimeByName(name)
	if t == "" {
		t = sniffed
		if i := strings.IndexByte(t, ';'); i > 0 {
			t = strings.TrimSpace(t[:i])
		}
	}
	if t == "" {
		t = "application/octet-stream"
	}
	// A sniffed HTML/XML body under a harmless extension is still HTML.
	sn := sniffed
	if i := strings.IndexByte(sn, ';'); i > 0 {
		sn = strings.TrimSpace(sn[:i])
	}
	if scriptable[t] || scriptable[sn] {
		return t, false
	}
	switch {
	case download:
		return t, false
	case strings.HasPrefix(t, "video/"), strings.HasPrefix(t, "audio/"), inlineTypes[t]:
		if t == "text/plain" {
			return "text/plain; charset=utf-8", true
		}
		return t, true
	case textual(t):
		return "text/plain; charset=utf-8", true
	}
	return t, false
}

// contentDisposition formats a Content-Disposition value with an RFC 2231
// encoded file name when needed.
func contentDisposition(kind, name string) string {
	if v := mime.FormatMediaType(kind, map[string]string{"filename": name}); v != "" {
		return v
	}
	return kind
}

// setSandboxHeaders marks a response as untrusted content.
func setSandboxHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Referrer-Policy", "no-referrer")
}

func (s *Service) handleRaw(w http.ResponseWriter, r *http.Request) {
	real, err := s.res.Resolve(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	f, fi, err := openRegular(real)
	if err != nil {
		fail(w, err)
		return
	}
	defer f.Close()
	var head [512]byte
	n, _ := io.ReadFull(f, head[:])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		fail(w, err)
		return
	}
	sniffed := ""
	if n > 0 {
		sniffed = http.DetectContentType(head[:n])
	}
	name := filepath.Base(real)
	ctype, inline := rawDisposition(name, sniffed, r.URL.Query().Get("download") == "1")
	h := w.Header()
	setSandboxHeaders(h)
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", `W/"`+strconv.FormatInt(fi.Size(), 36)+"-"+strconv.FormatInt(fi.ModTime().UnixNano(), 36)+`"`)
	if inline {
		h.Set("Content-Disposition", contentDisposition("inline", name))
	} else {
		h.Set("Content-Disposition", contentDisposition("attachment", name))
	}
	http.ServeContent(w, r, name, fi.ModTime(), f)
}
