package previews

import (
	"context"
	"html"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// probeResult is what an HTTP check learned about a port.
type probeResult struct {
	HTTP      bool
	Status    int
	Title     string
	Framework string
}

const (
	probeTimeout = time.Second
	probeMaxBody = 64 << 10
)

// prober issues the HTTP check. dial reaches the port on loopback.
type prober struct {
	client *http.Client
}

func newProber() *prober {
	tr := &http.Transport{
		Proxy:                  nil, // never through an HTTP(S)_PROXY
		DialContext:            (&net.Dialer{Timeout: probeTimeout}).DialContext,
		DisableKeepAlives:      true,
		ResponseHeaderTimeout:  probeTimeout,
		MaxResponseHeaderBytes: 32 << 10,
	}
	return &prober{client: &http.Client{
		Transport: tr,
		Timeout:   probeTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Follow a couple of redirects, but only on the same loopback port.
			if len(via) >= 3 || req.URL.Host != via[0].URL.Host {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}}
}

// probe GETs / on host:port (host is a loopback literal) and inspects the
// answer. Any HTTP response, whatever the status, counts as HTTP.
func (p *prober) probe(ctx context.Context, host string, port int) probeResult {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	u := "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return probeResult{}
	}
	req.Header.Set("User-Agent", "relay-preview-probe/1")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	res, err := p.client.Do(req)
	if err != nil {
		return probeResult{}
	}
	defer res.Body.Close()
	// A body read error (slow stream, timeout) is fine: the headers
	// arrived, so it speaks HTTP, and a partial page is enough.
	body, _ := io.ReadAll(io.LimitReader(res.Body, probeMaxBody))
	r := probeResult{HTTP: true, Status: res.StatusCode}
	if isHTMLish(res.Header.Get("Content-Type"), body) {
		r.Title = extractTitle(body)
	}
	r.Framework = detectFramework(res.Header, body, "")
	return r
}

func isHTMLish(ct string, body []byte) bool {
	ct = strings.ToLower(ct)
	if strings.Contains(ct, "html") {
		return true
	}
	if ct == "" {
		head := strings.ToLower(string(body[:min(len(body), 512)]))
		return strings.Contains(head, "<html") || strings.Contains(head, "<!doctype html")
	}
	return false
}

var (
	titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	spaceRe = regexp.MustCompile(`\s+`)
)

// extractTitle returns the decoded, whitespace-collapsed <title>.
func extractTitle(body []byte) string {
	m := titleRe.FindSubmatch(body)
	if m == nil {
		return ""
	}
	t := html.UnescapeString(string(m[1]))
	t = strings.TrimSpace(spaceRe.ReplaceAllString(t, " "))
	return capString(t, 200)
}

// framework signatures, most specific first. A signature matches when any
// of its header, body or command-line needles is present.
type signature struct {
	name    string
	headers [][2]string // header name, lower-case substring of its value ("" = present)
	body    []string    // substrings of the first 64 KiB of the page
	cmd     []string    // substrings of the process command line (lower-case)
}

var signatures = []signature{
	{name: "Storybook", body: []string{"storybook-root", "@storybook/", "sb-show-main"}, cmd: []string{"storybook"}},
	{name: "Jupyter", body: []string{"jupyter-config-data", "jupyterlab", "jupyter notebook"}, cmd: []string{"jupyter-lab", "jupyter-notebook", "jupyter lab", "jupyter notebook", "jupyter-server"}},
	{name: "Next.js", headers: [][2]string{{"X-Powered-By", "next.js"}, {"X-Nextjs-Cache", ""}}, body: []string{"/_next/static", "__next_f", `id="__next"`}, cmd: []string{"next dev", "next start", "next-server", "next-router-worker"}},
	{name: "Nuxt", body: []string{"/_nuxt/", "__nuxt", "window.__NUXT__"}, cmd: []string{"nuxt dev", "nuxi dev", "nuxt.mjs"}},
	{name: "Remix", body: []string{"__remixContext", "__remixManifest"}, cmd: []string{"remix dev", "remix vite:dev"}},
	{name: "SvelteKit", body: []string{"__sveltekit", "data-sveltekit"}, cmd: []string{"svelte-kit"}},
	{name: "Astro", headers: [][2]string{{"X-Astro", ""}}, body: []string{"astro-island", `content="astro`, "/@id/astro:", "astro:scripts"}, cmd: []string{"astro dev", "astro preview", "/astro.js"}},
	{name: "Gatsby", body: []string{"___gatsby"}, cmd: []string{"gatsby develop"}},
	{name: "Angular", body: []string{"ng-version=", "<app-root"}, cmd: []string{"ng serve"}},
	{name: "Vite", body: []string{"/@vite/client"}, cmd: []string{"vite"}},
	{name: "webpack", body: []string{"webpack-dev-server", "/webpack-dev-server.js"}, cmd: []string{"webpack serve", "webpack-dev-server"}},
	{name: "Django", headers: [][2]string{{"Server", "wsgiserver"}}, body: []string{"csrfmiddlewaretoken", "django"}, cmd: []string{"manage.py runserver", "django"}},
	{name: "Rails", headers: [][2]string{{"X-Runtime", ""}}, body: []string{"rails-ujs", "ruby on rails", "turbo-rails", "csrf-param\" content=\"authenticity_token"}, cmd: []string{"rails server", "rails s", "bin/rails", "puma"}},
	{name: "FastAPI", body: []string{"fastapi", "swagger-ui"}, cmd: []string{"fastapi", "uvicorn"}},
	{name: "Flask", headers: [][2]string{{"Server", "werkzeug"}}, cmd: []string{"flask run"}},
	{name: "Hugo", body: []string{`content="hugo`}, cmd: []string{"hugo server"}},
	{name: "Jekyll", body: []string{`content="jekyll`}, cmd: []string{"jekyll serve"}},
	{name: "Express", headers: [][2]string{{"X-Powered-By", "express"}}},
	{name: "Phoenix", body: []string{"phx-", "phoenix_live"}, cmd: []string{"phx.server"}},
	{name: "Laravel", headers: [][2]string{{"Set-Cookie", "laravel_session"}}, cmd: []string{"artisan serve"}},
	{name: "Python http.server", headers: [][2]string{{"Server", "simplehttp"}}, cmd: []string{"http.server"}},
}

// detectFramework names the dev server from response headers, the page
// body and/or the owning process command line. Any of them may be empty.
func detectFramework(h http.Header, body []byte, cmdline string) string {
	lbody := strings.ToLower(string(body))
	lcmd := strings.ToLower(cmdline)
	for _, s := range signatures {
		for _, hv := range s.headers {
			if h == nil {
				break
			}
			vals := h.Values(hv[0])
			for _, v := range vals {
				if hv[1] == "" || strings.Contains(strings.ToLower(v), hv[1]) {
					return s.name
				}
			}
		}
		if lbody != "" {
			for _, b := range s.body {
				if strings.Contains(lbody, strings.ToLower(b)) {
					return s.name
				}
			}
		}
	}
	if lcmd == "" {
		return ""
	}
	for _, s := range signatures {
		for _, c := range s.cmd {
			if cmdMatches(lcmd, c) {
				return s.name
			}
		}
	}
	return ""
}

// cmdMatches finds needle as a whole word sequence in the command line
// ("vite" matches "node /x/node_modules/.bin/vite --port 5173" but not
// "invite-server").
func cmdMatches(cmd, needle string) bool {
	for i := 0; ; {
		j := strings.Index(cmd[i:], needle)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(needle)
		if (start == 0 || isSep(cmd[start-1])) && (end == len(cmd) || isSep(cmd[end])) {
			return true
		}
		i = start + 1
	}
}

func isSep(b byte) bool {
	switch b {
	case ' ', '/', '\\', '=', '"', '\'', '@', ':':
		return true
	}
	return false
}
