package api_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// Parse registrations rather than comments or handler names. Fail on an
// unresolved registration so a future routing convention cannot silently vanish.
func TestRegisteredRouteInventory(t *testing.T) {
	doc := read(t, "../../docs/dev/API.md")
	rows := map[string]string{}
	for _, line := range strings.Split(doc, "\n") {
		cells := strings.Split(line, "|")
		for i := 1; i+1 < len(cells); i++ {
			method := strings.Fields(strings.TrimSpace(cells[i]))
			if len(method) == 0 || !strings.Contains(" GET POST PUT PATCH DELETE ANY ", " "+method[0]+" ") {
				continue
			}
			path := strings.Trim(strings.TrimSpace(cells[i+1]), "`")
			if !strings.HasPrefix(path, "/") {
				continue
			}
			path = strings.SplitN(path, "?", 2)[0]
			key := method[0] + " " + path
			if _, duplicate := rows[key]; duplicate {
				t.Errorf("duplicate documented route %s", key)
			}
			rows[key] = line
		}
	}
	registered := map[string]bool{}
	counts := map[string]int{}
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		values := map[string]string{}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ValueSpec:
				for i, name := range n.Names {
					if i < len(n.Values) {
						if v, ok := literalString(n.Values[i], values); ok {
							values[name.Name] = v
						}
					}
				}
			case *ast.AssignStmt:
				for i, lhs := range n.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && i < len(n.Rhs) {
						if v, ok := literalString(n.Rhs[i], values); ok {
							values[id.Name] = v
						}
					}
				}
			case *ast.CallExpr:
				sel, ok := n.Fun.(*ast.SelectorExpr)
				if !ok || len(n.Args) == 0 {
					return true
				}
				// Feature Router registrations plus the private daemon ServeMux.
				kind := sel.Sel.Name
				isDaemon := filepath.ToSlash(path) == "../ptyd/server.go" && kind == "HandleFunc"
				if !isDaemon && kind != "Public" && kind != "Handle" && kind != "WS" && kind != "Raw" {
					return true
				}
				if filepath.ToSlash(path) == "../server/router.go" {
					return true
				}
				pattern, ok := literalString(n.Args[0], values)
				if !ok {
					t.Errorf("unresolved registration %s in %s", kind, path)
					return true
				}
				if !strings.Contains(pattern, " ") {
					pattern = "ANY " + pattern
				}
				line, exists := rows[pattern]
				if !exists {
					t.Errorf("undocumented %s (%s in %s)", pattern, kind, path)
				}
				if registered[pattern] {
					t.Errorf("duplicate registration %s", pattern)
				}
				registered[pattern] = true
				if isDaemon {
					kind = "ptyd"
				}
				counts[kind]++
				if kind == "Public" && !strings.Contains(line, "| P |") {
					t.Errorf("public auth drift: %s", line)
				}
				if kind == "Handle" && !regexp.MustCompile(`\| [ACT] \|`).MatchString(line) {
					t.Errorf("authenticated auth drift: %s", line)
				}
				if kind == "WS" && (!strings.Contains(line, "🔌") || !strings.Contains(line, "| A |")) {
					t.Errorf("socket auth drift: %s", line)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for key := range rows {
		if !registered[key] && key != "GET /_relay/preview-callback" {
			t.Errorf("documented route not registered: %s", key)
		}
	}
	if len(registered) < 140 || counts["ptyd"] != 16 || counts["Public"] != 6 {
		t.Fatalf("incomplete inventory: %d, %v", len(registered), counts)
	}
	t.Logf("%d registrations, %v; host callback documented separately", len(registered), counts)
}

func literalString(e ast.Expr, values map[string]string) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			v, err := strconv.Unquote(e.Value)
			return v, err == nil
		}
	case *ast.Ident:
		v, ok := values[e.Name]
		return v, ok
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			a, ok := literalString(e.X, values)
			b, ok2 := literalString(e.Y, values)
			return a + b, ok && ok2
		}
	}
	return "", false
}

type mirrorFields struct {
	fields map[string]bool
	embeds []string
}

func TestBrowserTypeInventory(t *testing.T) {
	goTypes := map[string]mirrorFields{}
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := spec.Type.(*ast.StructType)
			if !ok {
				return true
			}
			m := mirrorFields{fields: map[string]bool{}}
			for _, field := range st.Fields.List {
				if len(field.Names) == 0 {
					if id, ok := field.Type.(*ast.Ident); ok {
						m.embeds = append(m.embeds, id.Name)
					}
					continue
				}
				if field.Tag == nil {
					continue
				}
				tag, err := strconv.Unquote(field.Tag.Value)
				if err != nil {
					t.Fatal(err)
				}
				parts := strings.Split(reflect.StructTag(tag).Get("json"), ",")
				if parts[0] != "" && parts[0] != "-" {
					m.fields[parts[0]] = strings.Contains(","+strings.Join(parts[1:], ",")+",", ",omitempty,")
				}
			}
			goTypes[spec.Name.Name] = m
			return true
		})
	}
	tsTypes := map[string]mirrorFields{}
	paths, err = filepath.Glob("../../web/src/api/*.ts")
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?:export )?interface (\w+)(?:<[^>]+>)?(?: extends (\w+))?\s*\{`)
	property := regexp.MustCompile(`^([A-Za-z]\w*)(\?)?\s*:`)
	comments := regexp.MustCompile(`(?m)//[^\n]*|/\*(?s:.*?)\*/`)
	for _, path := range paths {
		if strings.HasSuffix(path, ".test.ts") {
			continue
		}
		src := comments.ReplaceAllString(read(t, path), "")
		for _, loc := range decl.FindAllStringSubmatchIndex(src, -1) {
			name := src[loc[2]:loc[3]]
			if _, exists := tsTypes[name]; exists {
				t.Errorf("duplicate browser interface %s", name)
			}
			m := mirrorFields{fields: map[string]bool{}}
			if loc[4] >= 0 {
				m.embeds = append(m.embeds, src[loc[4]:loc[5]])
			}
			depth := 1
			start := loc[1]
			for i := start; i < len(src) && depth > 0; i++ {
				switch src[i] {
				case '{':
					depth++
				case '}':
					depth--
				}
				if depth == 0 || (depth == 1 && (src[i] == ';' || src[i] == '\n')) {
					if p := property.FindStringSubmatch(strings.TrimSpace(src[start:i])); p != nil {
						m.fields[p[1]] = p[2] == "?"
					}
					start = i + 1
				}
			}
			tsTypes[name] = m
		}
		// Capability lifecycle uses an intersection with a discriminated union;
		// retain that consumer narrowing instead of replacing it with a duplicate
		// flat interface solely for the inventory checker.
		aliases := regexp.MustCompile(`(?s)export type (\w+) = (\w+) &(.*?)(?:\nexport|$)`)
		for _, alias := range aliases.FindAllStringSubmatch(src, -1) {
			m := mirrorFields{fields: map[string]bool{}, embeds: []string{alias[2]}}
			for _, p := range regexp.MustCompile(`([A-Za-z]\w*)(\?)?\s*:`).FindAllStringSubmatch(alias[3], -1) {
				m.fields[p[1]] = p[2] == "?"
			}
			tsTypes[alias[1]] = m
		}
	}
	var flatten func(map[string]mirrorFields, string) map[string]bool
	flatten = func(all map[string]mirrorFields, name string) map[string]bool {
		out := map[string]bool{}
		m, ok := all[name]
		if !ok {
			t.Fatalf("missing embedded type %s", name)
		}
		for _, base := range m.embeds {
			for k, v := range flatten(all, base) {
				out[k] = v
			}
		}
		for k, v := range m.fields {
			out[k] = v
		}
		return out
	}
	checked := 0
	for name := range goTypes {
		if name == "CwdUsage" {
			continue
		} // explicitly backend-only, API.md/agents.ts
		browserName := name
		if name == "Event" {
			browserName = "RelayEvent"
		}
		if _, ok := tsTypes[browserName]; !ok {
			t.Errorf("missing browser mirror %s", name)
			continue
		}
		a, b := flatten(goTypes, name), flatten(tsTypes, browserName)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s field/omitempty drift: Go %v; TS %v", name, a, b)
		}
		checked++
	}
	t.Logf("%d public structs checked; CwdUsage excluded as backend exchange", checked)
}

func TestPublicEventInventory(t *testing.T) {
	types := read(t, "../../web/src/api/types.ts")
	start := strings.Index(types, "export type EventType =")
	if start < 0 {
		t.Fatal("missing EventType declaration in web/src/api/types.ts")
	}
	end := strings.Index(types[start:], "export interface RelayEvent")
	if end < 0 {
		t.Fatal("missing RelayEvent declaration after EventType in web/src/api/types.ts")
	}
	known := map[string]bool{}
	for _, m := range regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(types[start:start+end], -1) {
		known[m[1]] = true
	}
	events := read(t, "../../web/src/api/events.ts")
	doc := read(t, "../../docs/dev/API.md")
	paths, _ := filepath.Glob("*.go")
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		for _, m := range regexp.MustCompile(`Ev\w+\s*=\s*"([^"]+)"`).FindAllStringSubmatch(read(t, path), -1) {
			if !known[m[1]] {
				t.Errorf("missing EventType %s", m[1])
			}
			if !strings.Contains(doc, "`"+m[1]+"`") {
				t.Errorf("undocumented event %s", m[1])
			}
			if !strings.Contains(events, "'"+m[1]+"':") && !strings.Contains(events, m[1]+":") {
				t.Errorf("missing EventMap %s", m[1])
			}
		}
	}
	for _, topic := range []string{"audit", "clip.capture", "auth.session.revoked"} {
		if known[topic] {
			t.Errorf("backend topic in public union: %s", topic)
		}
	}
	if !known["pong"] {
		t.Error("missing live application pong")
	}
}

func TestCanonicalJobFixtures(t *testing.T) {
	var fixtures []struct {
		Name  string          `json:"name"`
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal([]byte(read(t, "testdata/jobs.json")), &fixtures); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	zero := 0
	failed := 2
	jobs := []any{
		api.ToolboxJob{Tool: "fixture-tool", TerminalID: "fixture-terminal", State: api.ToolboxJobRunning},
		api.ToolboxJob{Tool: "fixture-tool", TerminalID: "fixture-terminal", State: api.ToolboxJobDone, ExitCode: &zero},
		api.ToolboxJob{Tool: "fixture-tool", TerminalID: "fixture-terminal", State: api.ToolboxJobFailed, ExitCode: &failed},
		api.ToolboxJob{Tool: "fixture-tool", TerminalID: "fixture-terminal", State: api.ToolboxJobFailed},
	}
	for _, state := range []string{"running", "done", "failed", "canceled"} {
		job := api.FileJob{ID: "fixture-copy", Op: "copy", State: state, From: []string{"/fixture/source"}, To: "/fixture/destination", StartedAt: at}
		if state != "running" {
			job.EndedAt = at.Add(time.Second)
			job.Files = 1
			job.TotalFiles = 2
			job.Bytes = 3
			job.TotalBytes = 6
			job.Skipped = 1
			job.Result = []string{"/fixture/destination/source"}
		}
		if state == "failed" {
			job.Error = "synthetic copy failure"
		}
		jobs = append(jobs, job)
	}
	if len(fixtures) != len(jobs) {
		t.Fatalf("fixtures=%d want %d", len(fixtures), len(jobs))
	}
	for i, job := range jobs {
		t.Run(fixtures[i].Name, func(t *testing.T) {
			topic := api.EvToolboxJob
			if i >= 4 {
				topic = api.EvFilesJob
			}
			actual, err := json.Marshal(api.Event{Type: topic, At: at, Data: job})
			if err != nil {
				t.Fatal(err)
			}
			var a, b any
			if err := json.Unmarshal(actual, &a); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(fixtures[i].Event, &b); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("canonical serialization drift: got %s want %s", actual, fixtures[i].Event)
			}
		})
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
