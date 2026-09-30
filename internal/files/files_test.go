package files

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
	"github.com/aduthekaddu/relay/internal/server"
)

type tokenAuth struct{}

func (tokenAuth) Identify(*http.Request) *server.Principal {
	return &server.Principal{User: "tester", Method: "token"}
}

type env struct {
	t    *testing.T
	home string // root
	out  string // a directory outside the root
	svc  *Service
	h    http.Handler
	bus  *events.Bus
}

func newEnv(t *testing.T) *env {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	out := filepath.Join(base, "outside")
	for _, d := range []string{home, out} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	cfg := config.Defaults()
	cfg.Files.Root = home
	cfg.Agents.WorkspaceRoots = nil
	bus := events.New()
	d := &core.Deps{
		Cfg:    cfg,
		Paths:  config.Paths{Home: home, CacheDir: filepath.Join(base, "cache")},
		Bus:    bus,
		Search: core.NewSearchRegistry(),
	}
	svc, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	rt := server.NewRouter(tokenAuth{}, func() []string { return nil })
	svc.Routes(rt)
	// Resolve through symlinks (macOS /var → /private/var).
	realHome, _ := filepath.EvalSymlinks(home)
	realOut, _ := filepath.EvalSymlinks(out)
	return &env{t: t, home: realHome, out: realOut, svc: svc, h: rt, bus: bus}
}

func (e *env) write(rel, content string) string {
	e.t.Helper()
	p := filepath.Join(e.home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) do(method, target string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, target, rd)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %T: %v (%s)", v, err, rec.Body.String())
	}
	return v
}

func TestResolver(t *testing.T) {
	e := newEnv(t)
	e.write("docs/a.txt", "a")
	secret := filepath.Join(e.out, "secret.txt")
	os.WriteFile(secret, []byte("x"), 0o600)
	must(t, os.Symlink(e.out, filepath.Join(e.home, "escape")))
	must(t, os.Symlink(secret, filepath.Join(e.home, "secret-link")))
	must(t, os.Symlink(filepath.Join(e.home, "docs"), filepath.Join(e.home, "docs-link")))

	r := e.svc.res
	tests := []struct {
		name string
		in   string
		want string // "" = error
		code int
	}{
		{"root by empty", "", e.home, 0},
		{"tilde", "~/docs/a.txt", filepath.Join(e.home, "docs/a.txt"), 0},
		{"absolute inside", filepath.Join(e.home, "docs"), filepath.Join(e.home, "docs"), 0},
		{"dotdot escape", e.home + "/docs/../../outside/secret.txt", "", 403},
		{"tilde dotdot", "~/../outside", "", 403},
		{"absolute outside", secret, "", 403},
		{"missing outside is still 403", "/nonexistent-relay-test/x", "", 403},
		{"missing inside is 404", "~/nope.txt", "", 404},
		{"relative refused", "docs/a.txt", "", 400},
		{"nul refused", "~/a\x00b", "", 400},
		{"symlinked dir escape", "~/escape/secret.txt", "", 403},
		{"symlink to outside file", "~/secret-link", "", 403},
		{"symlink inside ok", "~/docs-link/a.txt", filepath.Join(e.home, "docs/a.txt"), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.Resolve(tc.in)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("Resolve(%q) = %q, want error", tc.in, got)
				}
				if code := statusOf(err); code != tc.code {
					t.Fatalf("Resolve(%q) status %d, want %d (%v)", tc.in, code, tc.code, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Resolve(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}

	// ResolveEntry acts on the link itself, even when it points outside.
	full, fi, err := r.ResolveEntry("~/secret-link")
	if err != nil || full != filepath.Join(e.home, "secret-link") || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("ResolveEntry(link) = %q %v %v", full, fi, err)
	}
	if _, _, err := r.ResolveEntry("~"); statusOf(err) != 403 {
		t.Fatalf("ResolveEntry(root) err = %v, want 403", err)
	}
	if _, _, err := r.ResolveEntry("~/escape/secret.txt"); statusOf(err) != 403 {
		t.Fatalf("ResolveEntry through escaping parent = %v, want 403", err)
	}
	// ResolveCreate refuses new paths under an escaping symlink.
	if _, err := r.ResolveCreate("~/escape/new/dir"); statusOf(err) != 403 {
		t.Fatalf("ResolveCreate via escape = %v, want 403", err)
	}
	if got, err := r.ResolveCreate("~/docs-link/new/deep"); err != nil || got != filepath.Join(e.home, "docs/new/deep") {
		t.Fatalf("ResolveCreate via inside link = %q %v", got, err)
	}
}

func TestValidName(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"a.txt", true}, {".env", true}, {"with space", true}, {"ünï", true},
		{"", false}, {".", false}, {"..", false}, {"a/b", false}, {"a\x00", false},
		{strings.Repeat("x", 256), false}, {"\xff", false},
	} {
		if err := ValidName(tc.name); (err == nil) != tc.ok {
			t.Errorf("ValidName(%q) err=%v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestListing(t *testing.T) {
	e := newEnv(t)
	e.write("file10.txt", "0123456789")
	e.write("file2.txt", "01")
	e.write(".hidden", "h")
	e.write("sub/one", "1")
	e.write("sub/two", "2")
	must(t, os.Mkdir(filepath.Join(e.home, "empty"), 0o755))

	rec := e.do("GET", "/api/v1/files/list?path=~", nil)
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	l := decode[api.DirListing](t, rec)
	names := entryNames(l.Entries)
	if want := "empty,sub,file2.txt,file10.txt"; names != want {
		t.Fatalf("order = %s, want %s", names, want)
	}
	if l.Hidden != 1 || l.Total != 4 {
		t.Fatalf("hidden=%d total=%d", l.Hidden, l.Total)
	}
	for _, en := range l.Entries {
		if en.Name == "sub" && (en.Children != 2 || en.Mime != MimeDirectory) {
			t.Fatalf("sub children=%d mime=%q", en.Children, en.Mime)
		}
		if en.Name == "file10.txt" && (en.Size != 10 || en.Mime != "text/plain" || en.Mode != "-rw-r--r--") {
			t.Fatalf("file10 = %+v", en)
		}
	}

	l = decode[api.DirListing](t, e.do("GET", "/api/v1/files/list?hidden=1&sort=size&desc=1", nil))
	if names := entryNames(l.Entries); names != "sub,empty,file10.txt,file2.txt,.hidden" {
		t.Fatalf("size desc = %s", names)
	}
	l = decode[api.DirListing](t, e.do("GET", "/api/v1/files/list?offset=1&limit=2", nil))
	if names := entryNames(l.Entries); names != "sub,file2.txt" || l.Offset != 1 || l.Total != 4 {
		t.Fatalf("page = %s offset=%d total=%d", names, l.Offset, l.Total)
	}
	if rec := e.do("GET", "/api/v1/files/list?path=~/file2.txt", nil); rec.Code != 400 {
		t.Fatalf("list file = %d", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/files/list?sort=bogus", nil); rec.Code != 400 {
		t.Fatalf("bad sort = %d", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/files/list?path="+e.out, nil); rec.Code != 403 {
		t.Fatalf("outside = %d", rec.Code)
	}
}

func entryNames(es []api.FileEntry) string {
	n := make([]string, len(es))
	for i, e := range es {
		n[i] = e.Name
	}
	return strings.Join(n, ",")
}

func TestNaturalLess(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"file2", "file10", true}, {"file10", "file2", false}, {"a", "B", true},
		{"img001", "img1", true}, {"x", "x1", true}, {"abc", "abd", true},
	} {
		if got := naturalLess(tc.a, tc.b); got != tc.want {
			t.Errorf("naturalLess(%q,%q)=%v", tc.a, tc.b, got)
		}
	}
}

func TestParsePorcelainZ(t *testing.T) {
	in := " M src/a.go\x00?? new/\x00R  moved.txt\x00orig.txt\x00A  added.go\x00UU conflict.go\x00 D gone.txt\x00"
	got := parsePorcelainZ([]byte(in))
	want := map[string]string{"src/a.go": "M", "new/": "?", "moved.txt": "R", "added.go": "A", "conflict.go": "U", "gone.txt": "D"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	st := newGitStatus(got)
	es := []api.FileEntry{
		{Name: "src", Path: "/r/src", Type: "dir"},
		{Name: "new", Path: "/r/new", Type: "dir"},
		{Name: "moved.txt", Path: "/r/moved.txt", Type: "file"},
		{Name: "clean", Path: "/r/clean", Type: "dir"},
	}
	applyGit(es, "/r", st)
	if es[0].Git != "M" || es[1].Git != "?" || es[2].Git != "R" || es[3].Git != "" {
		t.Fatalf("applyGit = %+v", es)
	}
}

func TestGitStatusInListing(t *testing.T) {
	e := newEnv(t)
	if e.svc.git.gitPath == "" {
		t.Skip("git not installed")
	}
	repo := filepath.Join(e.home, "repo")
	e.write("repo/tracked.txt", "v1")
	e.write("repo/lib/x.go", "package x")
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "init")
	e.write("repo/tracked.txt", "v2")
	e.write("repo/untracked.txt", "u")
	e.write("repo/lib/x.go", "package x // changed")

	l := decode[api.DirListing](t, e.do("GET", "/api/v1/files/list?path=~/repo", nil))
	if l.GitRoot != repo {
		t.Fatalf("gitRoot = %q", l.GitRoot)
	}
	got := map[string]string{}
	for _, en := range l.Entries {
		got[en.Name] = en.Git
	}
	if got["tracked.txt"] != "M" || got["untracked.txt"] != "?" || got["lib"] != "M" {
		t.Fatalf("git letters = %v", got)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestRaw(t *testing.T) {
	e := newEnv(t)
	e.write("notes.txt", "0123456789abcdef")
	e.write("page.html", "<html><script>alert(1)</script></html>")
	e.write("pic.svg", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	e.write("code.go", "package main")
	e.write("sneaky.txt", "<!DOCTYPE html><html><body>x</body></html>")
	e.write("noext", "\x00\x01\x02binary")
	e.write("ünïcode name.txt", "u")

	tests := []struct {
		name, path, extra string
		ctype, disp       string
	}{
		{"text inline", "notes.txt", "", "text/plain; charset=utf-8", "inline"},
		{"html attachment", "page.html", "", "text/html", "attachment"},
		{"svg attachment", "pic.svg", "", "image/svg+xml", "attachment"},
		{"code as text", "code.go", "", "text/plain; charset=utf-8", "inline"},
		{"sniffed html attachment", "sneaky.txt", "", "text/plain", "attachment"},
		{"binary attachment", "noext", "", "application/octet-stream", "attachment"},
		{"download forces attachment", "notes.txt", "&download=1", "text/plain", "attachment"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.do("GET", "/api/v1/files/raw?path=~/"+tc.path+tc.extra, nil)
			if rec.Code != 200 {
				t.Fatalf("status %d %s", rec.Code, rec.Body)
			}
			h := rec.Header()
			if !strings.HasPrefix(h.Get("Content-Security-Policy"), "sandbox") {
				t.Errorf("CSP = %q", h.Get("Content-Security-Policy"))
			}
			if h.Get("X-Content-Type-Options") != "nosniff" {
				t.Error("missing nosniff")
			}
			if !strings.HasPrefix(h.Get("Content-Type"), tc.ctype) {
				t.Errorf("Content-Type = %q, want %q", h.Get("Content-Type"), tc.ctype)
			}
			if !strings.HasPrefix(h.Get("Content-Disposition"), tc.disp) {
				t.Errorf("Content-Disposition = %q, want %s", h.Get("Content-Disposition"), tc.disp)
			}
		})
	}

	// Range request.
	req := httptest.NewRequest("GET", "/api/v1/files/raw?path=~/notes.txt", nil)
	req.Header.Set("Range", "bytes=4-7")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "4567" || rec.Header().Get("Content-Range") != "bytes 4-7/16" {
		t.Fatalf("range: %d %q %q", rec.Code, rec.Body, rec.Header().Get("Content-Range"))
	}
	// Non-ASCII names use RFC 2231 encoding.
	rec = e.do("GET", "/api/v1/files/raw?download=1&path=~/"+strings.ReplaceAll("ünïcode name.txt", " ", "%20"), nil)
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "filename*=utf-8''") {
		t.Fatalf("disposition = %q", cd)
	}
	// Folders, missing and outside paths.
	must(t, os.Mkdir(filepath.Join(e.home, "d"), 0o755))
	for p, code := range map[string]int{"~/d": 400, "~/missing": 404, e.out: 403} {
		if rec := e.do("GET", "/api/v1/files/raw?path="+p, nil); rec.Code != code {
			t.Errorf("raw %s = %d, want %d", p, rec.Code, code)
		}
	}
}

func TestText(t *testing.T) {
	e := newEnv(t)
	p := e.write("a.txt", "hello")
	e.write("bin.dat", "ab\x00cd")
	os.WriteFile(filepath.Join(e.home, "bom.txt"), append(append([]byte{}, utf8BOM...), "bom"...), 0o644)

	tf := decode[api.TextFile](t, e.do("GET", "/api/v1/files/text?path=~/a.txt", nil))
	if tf.Text != "hello" || tf.Encoding != "utf-8" || tf.Size != 5 || tf.Truncated {
		t.Fatalf("text = %+v", tf)
	}
	if b := decode[api.TextFile](t, e.do("GET", "/api/v1/files/text?path=~/bin.dat", nil)); b.Encoding != "binary" || b.Text != "" {
		t.Fatalf("binary = %+v", b)
	}
	bom := decode[api.TextFile](t, e.do("GET", "/api/v1/files/text?path=~/bom.txt", nil))
	if bom.Encoding != "utf-8-bom" || bom.Text != "bom" {
		t.Fatalf("bom = %+v", bom)
	}

	// Save with the right mtime works; a stale mtime conflicts.
	mt := tf.ModTime.Format(time.RFC3339Nano)
	rec := e.do("PUT", "/api/v1/files/text?path=~/a.txt&mtime="+mt, api.SaveTextRequest{Text: "v2"})
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if b, _ := os.ReadFile(p); string(b) != "v2" {
		t.Fatalf("content = %q", b)
	}
	rec = e.do("PUT", "/api/v1/files/text?path=~/a.txt&mtime="+mt, api.SaveTextRequest{Text: "v3"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale save = %d, want 409", rec.Code)
	}
	if b, _ := os.ReadFile(p); string(b) != "v2" {
		t.Fatalf("conflict overwrote: %q", b)
	}
	// Millisecond precision (JavaScript dates) is accepted.
	fi, _ := os.Stat(p)
	ms := fi.ModTime().Truncate(time.Millisecond).UTC().Format(time.RFC3339Nano)
	if rec := e.do("PUT", "/api/v1/files/text?path=~/a.txt&mtime="+ms, api.SaveTextRequest{Text: "v4"}); rec.Code != 200 {
		t.Fatalf("ms save = %d %s", rec.Code, rec.Body)
	}
	// BOM is preserved on save.
	if rec := e.do("PUT", "/api/v1/files/text?path=~/bom.txt", api.SaveTextRequest{Text: "new"}); rec.Code != 200 {
		t.Fatalf("bom save = %d", rec.Code)
	}
	if b, _ := os.ReadFile(filepath.Join(e.home, "bom.txt")); !bytes.Equal(b, append(append([]byte{}, utf8BOM...), "new"...)) {
		t.Fatalf("bom lost: %q", b)
	}
	// New file in an existing folder; missing folder is 404.
	if rec := e.do("PUT", "/api/v1/files/text?path=~/new.md", api.SaveTextRequest{Text: "# hi"}); rec.Code != 200 {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("PUT", "/api/v1/files/text?path=~/nodir/new.md", api.SaveTextRequest{Text: "x"}); rec.Code != 404 {
		t.Fatalf("create in missing dir = %d", rec.Code)
	}
	// Writing through a symlink that escapes the root is refused.
	must(t, os.Symlink(filepath.Join(e.out, "victim"), filepath.Join(e.home, "evil")))
	if rec := e.do("PUT", "/api/v1/files/text?path=~/evil", api.SaveTextRequest{Text: "x"}); rec.Code != 403 {
		t.Fatalf("write via escaping link = %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(e.out, "victim")); err == nil {
		t.Fatal("file created outside root")
	}
}

func TestDecodeTextTruncatedRune(t *testing.T) {
	s := "héllo"
	b := []byte(s)[:2] // cut inside é
	if text, enc := decodeText(b, true); enc != "utf-8" || text != "h" {
		t.Fatalf("decodeText = %q %q", text, enc)
	}
	if _, enc := decodeText(b, false); enc != "binary" {
		t.Fatalf("untruncated invalid = %q", enc)
	}
}

func TestOpenPublishesEvent(t *testing.T) {
	e := newEnv(t)
	p := e.write("src/app.ts", "x")
	sub := e.bus.Subscribe(4, func(ev api.Event) bool { return ev.Type == api.EvOpen })
	defer sub.Close()
	rec := e.do("POST", "/api/v1/open", api.OpenRequest{Path: "~/src/app.ts", Line: 42})
	if rec.Code != 200 {
		t.Fatalf("open: %d %s", rec.Code, rec.Body)
	}
	select {
	case ev := <-sub.C:
		o := ev.Data.(api.OpenRequest)
		if o.Path != p || o.Line != 42 {
			t.Fatalf("event = %+v", o)
		}
	case <-time.After(time.Second):
		t.Fatal("no open event")
	}
	if rec := e.do("POST", "/api/v1/open", api.OpenRequest{Path: e.out}); rec.Code != 403 {
		t.Fatalf("open outside = %d", rec.Code)
	}
}

func statusOf(err error) int {
	if err == nil {
		return 0
	}
	rec := httptest.NewRecorder()
	fail(rec, err)
	return rec.Code
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// waitFor polls cond until it is true or the deadline passes.
func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	for !cond() {
		select {
		case <-ctx.Done():
			t.Fatal("condition not met in time")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
