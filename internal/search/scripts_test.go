package search

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

func TestParseScript(t *testing.T) {
	tests := []struct {
		name string
		src  string
		ok   bool
		want api.ScriptCommand
	}{
		{
			name: "raycast bash",
			src: `#!/bin/bash
# Required parameters:
# @raycast.schemaVersion 1
# @raycast.title Say Hello
# @raycast.mode fullOutput
# @raycast.icon 👋
# @raycast.description Greets someone
# @raycast.argument1 { "type": "text", "placeholder": "Name" }
# @raycast.argument2 { "type": "text", "placeholder": "Greeting", "optional": true }
# @raycast.currentDirectoryPath ~/work
echo hi`,
			ok: true,
			want: api.ScriptCommand{
				Title: "Say Hello", Description: "Greets someone", Icon: "👋", Mode: ModeTerminal,
				Cwd: "/home/u/work",
				Args: []api.ScriptArg{
					{Name: "argument1", Placeholder: "Name"},
					{Name: "argument2", Placeholder: "Greeting", Optional: true},
				},
			},
		},
		{
			name: "relay overrides raycast, js comments",
			src: `#!/usr/bin/env node
// @raycast.title Old
// @relay.title New
// @raycast.mode silent
console.log(1)`,
			ok:   true,
			want: api.ScriptCommand{Title: "New", Mode: ModeSilent},
		},
		{
			name: "compact and non-json argument, gap stops args",
			src: `# @raycast.title T
# @raycast.mode compact
# @raycast.argument1 query
# @raycast.argument3 {"placeholder":"skipped"}
# @raycast.icon ./icon.png`,
			ok:   true,
			want: api.ScriptCommand{Title: "T", Mode: ModeInline, Args: []api.ScriptArg{{Name: "argument1", Placeholder: "query"}}},
		},
		{name: "no title", src: "#!/bin/sh\n# @raycast.mode inline\n", ok: false},
		{name: "not a comment", src: "@raycast.title Nope\n", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseScript(strings.NewReader(tt.src), "/home/u")
			if ok != tt.ok {
				t.Fatalf("ok = %v", ok)
			}
			if !ok {
				return
			}
			if got.Title != tt.want.Title || got.Description != tt.want.Description || got.Icon != tt.want.Icon ||
				got.Mode != tt.want.Mode || got.Cwd != tt.want.Cwd || len(got.Args) != len(tt.want.Args) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for i := range got.Args {
				if got.Args[i] != tt.want.Args[i] {
					t.Fatalf("arg %d = %+v, want %+v", i, got.Args[i], tt.want.Args[i])
				}
			}
		})
	}
}

// writeScript creates an executable script in dir.
func writeScript(t *testing.T, dir, name, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

type fakePty struct{ got ptyclient.CreateSpec }

func (f *fakePty) Create(_ context.Context, spec ptyclient.CreateSpec) (*api.TerminalSession, error) {
	f.got = spec
	return &api.TerminalSession{ID: "t1"}, nil
}

func scriptService(t *testing.T) (*Service, string, *fakePty) {
	t.Helper()
	dir := t.TempDir()
	writeScript(t, dir, "echo.sh", `#!/bin/sh
# @raycast.title Echo Args
# @raycast.mode inline
# @raycast.argument1 {"type":"text","placeholder":"first"}
# @raycast.argument2 {"type":"text","placeholder":"second","optional":true}
printf '%s|%s|%s' "$1" "$2" "$(pwd)"
`, 0o755)
	writeScript(t, dir, "quiet.sh", "#!/bin/sh\n# @relay.title Quiet\n# @relay.mode silent\necho one; echo two; echo\n", 0o755)
	writeScript(t, dir, "fail.sh", "#!/bin/sh\n# @relay.title Fail\necho oops >&2; exit 3\n", 0o755)
	writeScript(t, dir, "slow.sh", "#!/bin/sh\n# @relay.title Slow\nsleep 30 & wait\n", 0o755)
	writeScript(t, dir, "term.sh", "#!/bin/sh\n# @raycast.title Watch\n# @raycast.mode fullOutput\n", 0o755)
	writeScript(t, dir, "noexec.sh", "#!/bin/sh\n# @raycast.title Hidden\n", 0o644)
	writeScript(t, dir, ".hidden.sh", "#!/bin/sh\n# @raycast.title Hidden\n", 0o755)
	writeScript(t, dir, "plain.sh", "#!/bin/sh\necho no metadata\n", 0o755)
	writeScript(t, dir, "bad name.sh", "#!/bin/sh\n# @raycast.title Spaces\n", 0o755)
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	fp := &fakePty{}
	s, err := New(&core.Deps{Search: core.NewSearchRegistry()},
		WithScriptDirs(dir, filepath.Join(dir, "missing")), WithPty(fp), WithScriptTimeout(300*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return s, dir, fp
}

func TestScanScripts(t *testing.T) {
	s, dir, _ := scriptService(t)
	got := s.Scripts()
	var ids []string
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "echo.sh,fail.sh,quiet.sh,slow.sh,term.sh" {
		t.Fatalf("ids = %v", ids)
	}
	if got[0].Cwd != dir || got[0].Path != filepath.Join(dir, "echo.sh") {
		t.Fatalf("echo = %+v", got[0])
	}
	// The cache notices new files (directory mtime changes).
	time.Sleep(10 * time.Millisecond)
	writeScript(t, dir, "new.sh", "#!/bin/sh\n# @raycast.title Brand New\n", 0o755)
	if n := len(s.Scripts()); n != 6 {
		t.Fatalf("after add: %d scripts", n)
	}
}

func TestRunScriptModes(t *testing.T) {
	s, dir, fp := scriptService(t)
	ctx := context.Background()

	r, err := s.RunScript(ctx, "echo.sh", []string{"a b", "; rm -rf /"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Output != "a b|; rm -rf /|"+dir || r.ExitCode != 0 {
		t.Fatalf("inline = %+v", r)
	}
	if r, err = s.RunScript(ctx, "echo.sh", []string{"only"}); err != nil || !strings.HasPrefix(r.Output, "only||") {
		t.Fatalf("optional padding = %+v, %v", r, err)
	}
	if r, err = s.RunScript(ctx, "quiet.sh", nil); err != nil || r.Output != "two" {
		t.Fatalf("silent = %+v, %v", r, err)
	}
	if r, err = s.RunScript(ctx, "fail.sh", nil); err != nil || r.ExitCode != 3 || strings.TrimSpace(r.Output) != "oops" {
		t.Fatalf("fail = %+v, %v", r, err)
	}
	if r, err = s.RunScript(ctx, "term.sh", nil); err != nil || r.TerminalID != "t1" {
		t.Fatalf("terminal = %+v, %v", r, err)
	}
	if fp.got.Kind != "task" || fp.got.Command[0] != filepath.Join(dir, "term.sh") || fp.got.Cwd != dir {
		t.Fatalf("pty spec = %+v", fp.got.CreateTerminalRequest)
	}

	start := time.Now()
	_, err = s.RunScript(ctx, "slow.sh", nil)
	var he *httpx.Err
	if !errors.As(err, &he) || he.Status != http.StatusGatewayTimeout {
		t.Fatalf("slow err = %v", err)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("timeout took %v (process group not killed?)", el)
	}
}

func TestRunScriptErrors(t *testing.T) {
	s, _, _ := scriptService(t)
	tests := []struct {
		id   string
		args []string
		code int
	}{
		{"missing.sh", nil, 404},
		{"noexec.sh", nil, 404},
		{"../etc/passwd", nil, 404},
		{"echo.sh", nil, 400},                     // required arg missing
		{"echo.sh", []string{"a", "b", "c"}, 400}, // too many
		{"echo.sh", []string{"a\x00b"}, 400},      // NUL
		{"echo.sh", []string{strings.Repeat("x", 5000)}, 400},
	}
	for _, tt := range tests {
		_, err := s.RunScript(context.Background(), tt.id, tt.args)
		var he *httpx.Err
		if !errors.As(err, &he) || he.Status != tt.code {
			t.Errorf("RunScript(%q, %d args) = %v, want %d", tt.id, len(tt.args), err, tt.code)
		}
	}
}

func TestScriptProvider(t *testing.T) {
	s, _, _ := scriptService(t)
	p := s.ScriptProvider()
	if p.Scope() != "scripts" {
		t.Fatal(p.Scope())
	}
	if got := p.Search(context.Background(), "", 50); len(got) != 5 {
		t.Fatalf("empty query: %d", len(got))
	}
	got := p.Search(context.Background(), "echo", 50)
	if len(got) != 1 || got[0].ID != "echo.sh" || got[0].Meta["args"] != "2" || got[0].Score != 0.6 {
		t.Fatalf("echo: %+v", got)
	}
	if got := p.Search(context.Background(), "echo zzz", 50); len(got) != 0 {
		t.Fatalf("all words required: %+v", got)
	}
}

func TestCapBuffer(t *testing.T) {
	b := &capBuffer{max: 5}
	n, _ := b.Write([]byte("héllo world"))
	if n != 12 || !b.truncated {
		t.Fatalf("n=%d truncated=%v", n, b.truncated)
	}
	if got := b.String(); got != "héll" {
		t.Fatalf("got %q", got)
	}
}

func TestDefaultScriptDirs(t *testing.T) {
	got := defaultScriptDirs("/h", "/h/.config/relay")
	if len(got) != 1 || got[0] != "/h/.config/relay/commands" {
		t.Fatalf("same dir twice: %v", got)
	}
	got = defaultScriptDirs("/h", "/r/config")
	if len(got) != 2 || got[1] != "/r/config/commands" {
		t.Fatalf("RELAY_HOME: %v", got)
	}
}
