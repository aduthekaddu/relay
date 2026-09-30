package toolbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

func TestOrderedJSONRoundTrip(t *testing.T) {
	src := `{"zeta": 1, "alpha": {"b": [1, "two", true, null], "a": {}}, "mid": "x<y>&", "n": 1.50e3}`
	o, err := parseOrdered([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(o.keys, ",") != "zeta,alpha,mid,n" {
		t.Fatalf("key order lost: %v", o.keys)
	}
	o.set("new", "v")
	o.del("zeta")
	out, err := encodeOrdered(o)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "alpha": {
    "b": [
      1,
      "two",
      true,
      null
    ],
    "a": {}
  },
  "mid": "x<y>&",
  "n": 1.50e3,
  "new": "v"
}
`
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	for _, bad := range []string{`[1]`, `{"a":1} {}`, `{"a":`} {
		if _, err := parseOrdered([]byte(bad)); err == nil {
			t.Errorf("parseOrdered(%q) accepted invalid input", bad)
		}
	}
	if o, err := parseOrdered([]byte("  \n")); err != nil || len(o.keys) != 0 {
		t.Fatalf("empty input: %v %v", o, err)
	}
}

func TestStripJSONComments(t *testing.T) {
	tests := []struct {
		in, want string
		found    bool
	}{
		{`{"a": "//not a comment"}`, `{"a": "//not a comment"}`, false},
		{"{\n// line\n\"a\": 1 /* block */}", "{\n\n\"a\": 1 }", true},
		{`{"a": "esc \" // still string"}`, `{"a": "esc \" // still string"}`, false},
	}
	for _, tt := range tests {
		got, found := stripJSONComments([]byte(tt.in))
		if string(got) != tt.want || found != tt.found {
			t.Errorf("strip(%q) = %q,%v want %q,%v", tt.in, got, found, tt.want, tt.found)
		}
	}
}

// mcpHome builds a synthetic home directory with one config per agent.
func mcpHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	files := map[string]string{
		".claude.json": `{
  "numStartups": 3,
  "mcpServers": {
    "pw": {"type": "stdio", "command": "npx", "args": ["@playwright/mcp@latest"]}
  },
  "projects": {}
}`,
		".codex/config.toml": `# my codex config
model = "gpt-5"

[mcp_servers.docs]
command = "npx"
args = ["-y", "@upstash/context7-mcp"]

[mcp_servers.docs.env]
TOKEN = "synthetic"

[profiles.fast]
model = "mini"
`,
		".gemini/settings.json":          `{"theme": "Default"}`,
		".config/opencode/opencode.json": `{"$schema": "https://opencode.ai/config.json", "mcp": {"blender": {"type": "local", "command": ["uvx", "blender-mcp"], "enabled": true}}}`,
		".kiro/settings/mcp.json":        `{"mcpServers": {}}`,
		".cursor/.keep":                  ``,
	}
	for rel, body := range files {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func newTestMCP(home string) *MCP {
	m := NewMCP(home, Finder{Dirs: []string{filepath.Join(home, "nobin")}}, nil)
	m.Now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	return m
}

func row(t *testing.T, rows []api.MCPServer, id string) api.MCPServer {
	t.Helper()
	for _, r := range rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no row %s", id)
	return api.MCPServer{}
}

func TestMCPMatrix(t *testing.T) {
	home := mcpHome(t)
	m := newTestMCP(home)
	rows := m.Matrix(context.Background())
	if len(rows) != 5 {
		t.Fatalf("want 5 registry servers, got %d", len(rows))
	}
	tests := []struct {
		server, agent string
		want          bool
	}{
		{"playwright", "claude", true}, // matched by marker, registered as "pw"
		{"playwright", "codex", false},
		{"context7", "codex", true},
		{"blender", "opencode", true},
		{"blender", "kiro", false},
		{"filesystem", "cursor", false},
	}
	for _, tt := range tests {
		r := row(t, rows, tt.server)
		got, present := r.Agents[tt.agent]
		if !present {
			t.Errorf("%s: agent %s not detected", tt.server, tt.agent)
		}
		if got != tt.want {
			t.Errorf("%s × %s = %v, want %v", tt.server, tt.agent, got, tt.want)
		}
	}
	fs := row(t, rows, "filesystem")
	if fs.Command[len(fs.Command)-1] != home {
		t.Errorf("filesystem server not scoped to home: %v", fs.Command)
	}

	// An agent with no trace at all is omitted.
	empty := newTestMCP(t.TempDir())
	if len(row(t, empty.Matrix(context.Background()), "playwright").Agents) != 0 {
		t.Error("agents detected in an empty home")
	}
}

func TestMCPApplyFileMerge(t *testing.T) {
	home := mcpHome(t)
	m := newTestMCP(home)
	ctx := context.Background()

	r, err := m.Apply(ctx, api.MCPApplyRequest{Server: "chrome-devtools", Agents: []string{"claude", "codex", "gemini", "opencode", "kiro", "cursor"}})
	if err != nil {
		t.Fatal(err)
	}
	for agent, ok := range r.Agents {
		if !ok {
			t.Errorf("chrome-devtools not configured for %s after apply", agent)
		}
	}

	claude, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if !strings.HasPrefix(string(claude), "{\n  \"numStartups\": 3,\n  \"mcpServers\"") {
		t.Errorf("claude key order not preserved:\n%s", claude)
	}
	if !strings.Contains(string(claude), `"chrome-devtools": {
      "type": "stdio",
      "command": "npx",`) {
		t.Errorf("claude entry shape wrong:\n%s", claude)
	}
	codex, _ := os.ReadFile(filepath.Join(home, ".codex/config.toml"))
	if !strings.HasPrefix(string(codex), "# my codex config\n") || !strings.Contains(string(codex), "[mcp_servers.chrome-devtools]\ncommand = \"npx\"\nargs = [\"-y\", \"chrome-devtools-mcp@latest\"]\n") {
		t.Errorf("codex TOML edit wrong:\n%s", codex)
	}
	oc, _ := os.ReadFile(filepath.Join(home, ".config/opencode/opencode.json"))
	if !strings.Contains(string(oc), `"type": "local"`) || !strings.Contains(string(oc), `"enabled": true`) {
		t.Errorf("opencode entry wrong:\n%s", oc)
	}
	cursor, err := os.ReadFile(filepath.Join(home, ".cursor/mcp.json"))
	if err != nil || !strings.Contains(string(cursor), "chrome-devtools-mcp@latest") {
		t.Errorf("cursor config not created: %v\n%s", err, cursor)
	}
	if st, _ := os.Stat(filepath.Join(home, ".cursor/mcp.json")); st.Mode().Perm() != 0o600 {
		t.Errorf("new config mode %v, want 0600", st.Mode().Perm())
	}

	// Existing files were backed up, byte for byte.
	bak := filepath.Join(home, ".claude.json.relay-backup-20260102-030405")
	if b, err := os.ReadFile(bak); err != nil || !strings.Contains(string(b), `"pw"`) || strings.Contains(string(b), "chrome-devtools") {
		t.Errorf("claude backup missing or wrong: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor/mcp.json.relay-backup-20260102-030405")); err == nil {
		t.Error("a file that did not exist should not get a backup")
	}

	// Applying again is a no-op (no second backup).
	if _, err := m.Apply(ctx, api.MCPApplyRequest{Server: "chrome-devtools", Agents: []string{"claude"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bak + "-1"); err == nil {
		t.Error("idempotent apply created another backup")
	}

	// Remove: drops the entries and codex sub-tables, keeps the rest.
	r, err = m.Apply(ctx, api.MCPApplyRequest{Server: "context7", Agents: []string{"codex"}, Remove: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Agents["codex"] {
		t.Error("context7 still configured for codex")
	}
	codex, _ = os.ReadFile(filepath.Join(home, ".codex/config.toml"))
	for _, gone := range []string{"[mcp_servers.docs]", "TOKEN", "context7"} {
		if strings.Contains(string(codex), gone) {
			t.Errorf("codex still contains %q:\n%s", gone, codex)
		}
	}
	for _, kept := range []string{"# my codex config", "[profiles.fast]", "[mcp_servers.chrome-devtools]"} {
		if !strings.Contains(string(codex), kept) {
			t.Errorf("codex lost %q:\n%s", kept, codex)
		}
	}
	r, err = m.Apply(ctx, api.MCPApplyRequest{Server: "playwright", Agents: []string{"claude"}, Remove: true})
	if err != nil || r.Agents["claude"] {
		t.Fatalf("remove playwright (registered as pw): %v %+v", err, r)
	}
}

func TestMCPApplyErrors(t *testing.T) {
	home := mcpHome(t)
	if err := os.WriteFile(filepath.Join(home, ".gemini/settings.json"), []byte("{\n// keep me\n\"theme\": \"x\"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newTestMCP(home)
	tests := []struct {
		name   string
		req    api.MCPApplyRequest
		status int
	}{
		{"unknown server", api.MCPApplyRequest{Server: "evil", Agents: []string{"claude"}}, 400},
		{"no agents", api.MCPApplyRequest{Server: "playwright"}, 400},
		{"unknown agent", api.MCPApplyRequest{Server: "playwright", Agents: []string{"vim"}}, 400},
		{"jsonc with comments", api.MCPApplyRequest{Server: "playwright", Agents: []string{"gemini"}}, 409},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.Apply(context.Background(), tt.req)
			var he *httpx.Err
			if !errors.As(err, &he) || he.Status != tt.status {
				t.Fatalf("err = %v, want status %d", err, tt.status)
			}
		})
	}
	b, _ := os.ReadFile(filepath.Join(home, ".gemini/settings.json"))
	if !strings.Contains(string(b), "// keep me") {
		t.Fatal("a refused edit must leave the file alone")
	}
	if _, err := newTestMCP(t.TempDir()).Apply(context.Background(), api.MCPApplyRequest{Server: "playwright", Agents: []string{"claude"}}); err == nil {
		t.Fatal("applying to an agent that is not installed must fail")
	}
}

func TestMCPApplyViaCLI(t *testing.T) {
	home := mcpHome(t)
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(home, "argv.log")
	// A fake claude that records its argv and HOME, then edits the config
	// like the real CLI would (enough for the post-apply re-read).
	writeExe(t, bin, "claude", `printf '%s|' "$HOME" "$@" >> "`+log+`"; echo >> "`+log+`"
case "$2" in
  add) sed -i.tmp 's/"mcpServers": {/"mcpServers": {"context7": {"command": "npx", "args": ["@upstash\/context7-mcp@latest"]},/' "$HOME/.claude.json" ;;
esac`)
	writeExe(t, bin, "codex", `echo "codex failed: bad flag" >&2; exit 2`)
	m := NewMCP(home, Finder{Dirs: []string{bin, "/usr/bin", "/bin"}}, ExecRunner)
	ctx := context.Background()

	r, err := m.Apply(ctx, api.MCPApplyRequest{Server: "context7", Agents: []string{"claude"}})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Agents["claude"] {
		t.Fatal("claude not configured after CLI apply")
	}
	got, _ := os.ReadFile(log)
	want := home + "|mcp|add|-s|user|context7|--|npx|-y|@upstash/context7-mcp@latest|\n"
	if string(got) != want {
		t.Fatalf("claude argv:\n got %q\nwant %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json.relay-backup-20260102-030405")); err == nil {
		t.Fatal("CLI path must not write backups itself")
	}

	_, err = m.Apply(ctx, api.MCPApplyRequest{Server: "playwright", Agents: []string{"codex"}})
	var he *httpx.Err
	if !errors.As(err, &he) || he.Code != "agent_cli_failed" || !strings.Contains(he.Message, "bad flag") {
		t.Fatalf("codex CLI failure not surfaced: %v", err)
	}

	if got := cliRemove("/x/codex", m.agent("codex"), "pw"); strings.Join(got, " ") != "/x/codex mcp remove pw" {
		t.Fatalf("codex remove argv: %v", got)
	}
	def := &MCPDef{ID: "k", Command: []string{"run"}, Env: map[string]string{"A": "1"}}
	if got := cliAdd("/x/codex", m.agent("codex"), def); strings.Join(got, " ") != "/x/codex mcp add k --env A=1 -- run" {
		t.Fatalf("codex add argv: %v", got)
	}
	if got := cliAdd("/x/claude", m.agent("claude"), def); strings.Join(got, " ") != "/x/claude mcp add -s user -e A=1 k -- run" {
		t.Fatalf("claude add argv: %v", got)
	}
}

func TestCodexTableName(t *testing.T) {
	tests := []struct {
		in   string
		name string
		ok   bool
	}{
		{"[mcp_servers.docs]", "docs", true},
		{"[mcp_servers.docs.env]", "docs", true},
		{`[mcp_servers."my.server"]`, "my.server", true},
		{"[mcp_servers.x] # note", "x", true},
		{"[profiles.fast]", "", false},
		{"[mcp_servers]", "", false},
	}
	for _, tt := range tests {
		name, ok := codexTableName(tt.in)
		if name != tt.name || ok != tt.ok {
			t.Errorf("codexTableName(%q) = %q,%v want %q,%v", tt.in, name, ok, tt.name, tt.ok)
		}
	}
	if tomlString("a\"b\\c\n") != `"a\"b\\c\n"` {
		t.Error("tomlString escaping")
	}
}
