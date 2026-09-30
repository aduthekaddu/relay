package apps

import (
	"html/template"
	"net/http"
	"strconv"
)

// pageData fills the small self-contained status page shown while an app
// starts, or when it cannot.
type pageData struct {
	Title   string
	Heading string
	Body    string
	Detail  string
	Retry   int  // seconds until the page reloads itself; 0 = never
	Spinner bool // show the progress indicator
}

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="dark light">
<title>{{.Title}} — Relay</title>
<style>
:root{--bg:#0b0b0c;--surface:#111113;--line:#2a2a2f;--text:#ede9e0;--text2:#b3aea4;--signal:#ff5b1f}
@media (prefers-color-scheme:light){:root{--bg:#f3f0e8;--surface:#faf8f3;--line:#e6e2d8;--text:#16150f;--text2:#4b4840;--signal:#e44a0f}}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--text);font:15px/1.5 ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif}
main{width:min(28rem,calc(100vw - 2rem));padding:1.75rem;border:1px solid var(--line);border-radius:14px;background:var(--surface)}
h1{margin:0 0 .5rem;font-size:1.125rem;display:flex;align-items:center;gap:.6rem}
p{margin:.25rem 0;color:var(--text2)}
pre{margin:.75rem 0 0;padding:.6rem .75rem;border-radius:8px;background:var(--bg);color:var(--text2);white-space:pre-wrap;word-break:break-word;font:12px/1.45 ui-monospace,monospace}
.spin{width:1rem;height:1rem;border-radius:50%;border:2px solid var(--line);border-top-color:var(--signal);animation:s .8s linear infinite}
@keyframes s{to{transform:rotate(1turn)}}
@media (prefers-reduced-motion:reduce){.spin{animation:none;border-color:var(--signal)}}
a{color:var(--signal)}
</style></head>
<body><main role="status" aria-live="polite">
<h1>{{if .Spinner}}<span class="spin" aria-hidden="true"></span>{{end}}{{.Heading}}</h1>
<p>{{.Body}}</p>
{{if .Detail}}<pre>{{.Detail}}</pre>{{end}}
{{if not .Retry}}<p><a href="">Try again</a></p>{{end}}
</main></body></html>`))

// writePage renders a status page. Retry > 0 adds a Refresh header so
// the browser reloads once the app is up.
func writePage(w http.ResponseWriter, status int, d pageData) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	if d.Retry > 0 {
		h.Set("Refresh", strconv.Itoa(d.Retry))
		h.Set("Retry-After", strconv.Itoa(d.Retry))
	}
	w.WriteHeader(status)
	_ = pageTmpl.Execute(w, d)
}
