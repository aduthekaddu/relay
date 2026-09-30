package snippets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/server"
	"github.com/aduthekaddu/relay/internal/store"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := New(&core.Deps{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	n := 0
	s.now = func() time.Time { n++; return base.Add(time.Duration(n) * time.Second) }
	return s
}

func ptr[T any](v T) *T { return &v }

func TestVariables(t *testing.T) {
	tests := []struct {
		body string
		want []string
	}{
		{"no vars", nil},
		{"Review {{file}} for {{ issue }} then {{file}} again", []string{"file", "issue"}},
		{"{{a.b}} {{c-d}} {{_x}} {{9bad}} {{}} {{ spaced out }}", []string{"a.b", "c-d", "_x"}},
	}
	for _, tc := range tests {
		if got := Variables(tc.body); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %v want %v", tc.body, got, tc.want)
		}
	}
	if got := Expand("Fix {{ bug }} in {{file}} ({{other}})", map[string]string{"bug": "#12", "file": "main.go"}); got != "Fix #12 in main.go ({{other}})" {
		t.Errorf("expand: %q", got)
	}
}

func TestSnippetCRUD(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	sn, err := s.CreateSnippet(ctx, snippetPatch{Name: ptr(" Review PR "), Body: ptr("Review {{pr}} carefully"), Tags: ptr([]string{"Git", "git", " review "})})
	if err != nil {
		t.Fatal(err)
	}
	if sn.Name != "Review PR" || sn.Kind != "prompt" || !reflect.DeepEqual(sn.Tags, []string{"git", "review"}) || sn.ID == "" {
		t.Fatalf("created %+v", sn)
	}
	upd, err := s.UpdateSnippet(ctx, sn.ID, snippetPatch{Kind: ptr("command"), Body: ptr("gh pr view {{pr}}"), Uses: ptr(99)})
	if err != nil {
		t.Fatal(err)
	}
	if upd.Kind != "command" || upd.Name != "Review PR" || upd.Uses != 0 || !upd.UpdatedAt.After(sn.UpdatedAt) {
		t.Fatalf("updated %+v", upd)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.UseSnippet(ctx, sn.ID); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.GetSnippet(ctx, sn.ID)
	if got.Uses != 3 {
		t.Fatalf("uses %d", got.Uses)
	}
	other, _ := s.CreateSnippet(ctx, snippetPatch{Name: ptr("Other"), Body: ptr("x")})
	list, _ := s.ListSnippets(ctx)
	if len(list) != 2 || list[0].ID != sn.ID || list[1].ID != other.ID {
		t.Fatalf("list order %+v", list)
	}
	if err := s.DeleteSnippet(ctx, sn.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSnippet(ctx, sn.ID); err == nil {
		t.Fatal("deleted snippet still found")
	}
	if _, err := s.UseSnippet(ctx, sn.ID); err == nil {
		t.Fatal("use of deleted snippet succeeded")
	}

	bad := []snippetPatch{
		{Body: ptr("x")},
		{Name: ptr("n")},
		{Name: ptr("n"), Body: ptr("x"), Kind: ptr("macro")},
		{Name: ptr(strings.Repeat("n", maxName+1)), Body: ptr("x")},
		{Name: ptr("n"), Body: ptr(strings.Repeat("x", maxSnippet+1))},
		{Name: ptr("n"), Body: ptr("x"), Tags: ptr(make([]string, maxTags+1))},
	}
	for i, p := range bad {
		if _, err := s.CreateSnippet(ctx, p); err == nil {
			t.Errorf("bad snippet %d accepted", i)
		}
	}
}

func TestNoteCRUD(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	n, err := s.CreateNote(ctx, notePatch{Text: ptr("\n# Shopping list\n- milk")})
	if err != nil {
		t.Fatal(err)
	}
	if n.Title != "Shopping list" {
		t.Fatalf("derived title %q", n.Title)
	}
	// Derived titles follow the text…
	n, _ = s.UpdateNote(ctx, n.ID, notePatch{Text: ptr("Todo\n- ship")})
	if n.Title != "Todo" {
		t.Fatalf("title did not follow text: %q", n.Title)
	}
	// …explicit titles stick.
	n, _ = s.UpdateNote(ctx, n.ID, notePatch{Title: ptr("Pinned title")})
	n, _ = s.UpdateNote(ctx, n.ID, notePatch{Text: ptr("changed")})
	if n.Title != "Pinned title" {
		t.Fatalf("explicit title lost: %q", n.Title)
	}
	empty, err := s.CreateNote(ctx, notePatch{})
	if err != nil || empty.Title != "Untitled" {
		t.Fatalf("empty note %+v %v", empty, err)
	}
	list, _ := s.ListNotes(ctx)
	if len(list) != 2 || list[0].ID != empty.ID {
		t.Fatalf("notes order %+v", list)
	}
	if _, err := s.CreateNote(ctx, notePatch{Text: ptr(strings.Repeat("x", maxNoteText+1))}); err == nil {
		t.Fatal("huge note accepted")
	}
	if err := s.DeleteNote(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNote(ctx, n.ID); err == nil {
		t.Fatal("double delete")
	}
}

func TestSearchProviders(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mk := func(name, body string) *api.Snippet {
		sn, err := s.CreateSnippet(ctx, snippetPatch{Name: ptr(name), Body: ptr(body)})
		if err != nil {
			t.Fatal(err)
		}
		return sn
	}
	deploy := mk("Deploy staging", "make deploy ENV={{env}}")
	mk("Write tests", "Write table-driven tests for {{package}}")
	mk("Redeploy hotfix", "git push && deploy")
	mk("100% done", "literal percent")

	p := s.SnippetProvider()
	if p.Scope() != "snippets" {
		t.Fatal(p.Scope())
	}
	res := p.Search(ctx, "deploy", 8)
	if len(res) != 2 || res[0].ID != deploy.ID {
		t.Fatalf("deploy results %+v", res)
	}
	if res[0].Meta["variables"] != "env" || res[0].Score <= res[1].Score {
		t.Fatalf("meta/score %+v", res)
	}
	if res := p.Search(ctx, "table tests", 8); len(res) != 1 || res[0].Title != "Write tests" {
		t.Fatalf("multi-word %+v", res)
	}
	// LIKE wildcards are literal.
	if res := p.Search(ctx, "%", 8); len(res) != 1 || res[0].Title != "100% done" {
		t.Fatalf("wildcard %+v", res)
	}
	if res := p.Search(ctx, "", 2); len(res) != 2 {
		t.Fatalf("empty query %d", len(res))
	}
	if res := p.Search(ctx, "nothing-matches", 8); len(res) != 0 {
		t.Fatalf("no match %+v", res)
	}

	if _, err := s.CreateNote(ctx, notePatch{Title: ptr("Ideas"), Text: ptr("first line\nuse sqlite FTS for notes later")}); err != nil {
		t.Fatal(err)
	}
	nres := s.NoteProvider().Search(ctx, "sqlite", 8)
	if len(nres) != 1 || nres[0].Scope != "notes" || !strings.Contains(nres[0].Subtitle, "sqlite") {
		t.Fatalf("notes %+v", nres)
	}
}

func TestMatchScore(t *testing.T) {
	tests := []struct {
		q, title, body string
		want           float64
	}{
		{"deploy", "deploy", "", 1},
		{"dep", "Deploy staging", "", 0.9},
		{"stag", "Deploy staging", "", 0.75},
		{"ploy", "Deploy staging", "", 0.6},
		{"env make", "Deploy", "make ENV=x", 0.3},
		{"deploy make", "Deploy", "make x", 0.425},
		{"zzz", "Deploy", "make", 0},
	}
	for _, tc := range tests {
		if got := matchScore(tc.q, tc.title, tc.body); got != tc.want {
			t.Errorf("%q vs %q: got %v want %v", tc.q, tc.title, got, tc.want)
		}
	}
}

type auth struct{}

func (auth) Identify(*http.Request) *server.Principal {
	return &server.Principal{User: "owner", Method: "token"}
}

func TestHTTP(t *testing.T) {
	s := newTestService(t)
	rt := server.NewRouter(auth{}, func() []string { return nil })
	s.Routes(rt)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	rec := call("POST", "/api/v1/snippets", `{"name":"Hi","body":"Hello {{name}}"}`)
	var sn api.Snippet
	_ = json.Unmarshal(rec.Body.Bytes(), &sn)
	if rec.Code != 200 || sn.ID == "" {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	if rec := call("POST", "/api/v1/snippets", `{"name":""}`); rec.Code != 400 {
		t.Fatalf("invalid create %d", rec.Code)
	}
	if rec := call("PATCH", "/api/v1/snippets/"+sn.ID, `{"agent":"claude"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"agent":"claude"`) {
		t.Fatalf("patch %d %s", rec.Code, rec.Body)
	}
	if rec := call("POST", "/api/v1/snippets/"+sn.ID+"/use", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"uses":1`) {
		t.Fatalf("use %d %s", rec.Code, rec.Body)
	}
	if rec := call("GET", "/api/v1/snippets", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), sn.ID) {
		t.Fatalf("list %s", rec.Body)
	}
	if rec := call("DELETE", "/api/v1/snippets/"+sn.ID, ""); rec.Code != 204 {
		t.Fatalf("delete %d", rec.Code)
	}
	if rec := call("PATCH", "/api/v1/snippets/"+sn.ID, `{"name":"x"}`); rec.Code != 404 {
		t.Fatalf("patch missing %d", rec.Code)
	}

	rec = call("POST", "/api/v1/notes", `{"text":"scratch"}`)
	var n api.Note
	_ = json.Unmarshal(rec.Body.Bytes(), &n)
	if rec.Code != 200 || n.Title != "scratch" {
		t.Fatalf("note %d %s", rec.Code, rec.Body)
	}
	if rec := call("GET", "/api/v1/notes/"+n.ID, ""); rec.Code != 200 {
		t.Fatalf("get note %d", rec.Code)
	}
	if rec := call("PATCH", "/api/v1/notes/"+n.ID, `{"text":"more"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"text":"more"`) {
		t.Fatalf("patch note %s", rec.Body)
	}
	if rec := call("GET", "/api/v1/notes", ""); !strings.Contains(rec.Body.String(), n.ID) {
		t.Fatalf("list notes %s", rec.Body)
	}
	if rec := call("DELETE", "/api/v1/notes/"+n.ID, ""); rec.Code != 204 {
		t.Fatalf("delete note %d", rec.Code)
	}
	if rec := call("GET", "/api/v1/notes/"+n.ID, ""); rec.Code != 404 {
		t.Fatalf("get deleted note %d", rec.Code)
	}
}
