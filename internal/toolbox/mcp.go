package toolbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

// MCPDef is one server in Relay's registry of recommended MCP servers.
type MCPDef struct {
	ID          string
	Name        string
	Description string
	Command     []string
	Env         map[string]string
	// Markers identify the server inside an existing agent config even
	// when the user registered it under a different name.
	Markers []string
}

// MCPRegistry returns the recommended MCP servers. home scopes the
// filesystem server to the user's home directory.
func MCPRegistry(home string) []MCPDef {
	return []MCPDef{
		{
			ID: "playwright", Name: "Playwright",
			Description: "Drive a real browser: navigate, click, fill forms, take snapshots.",
			Command:     []string{"npx", "-y", "@playwright/mcp@latest"},
			Markers:     []string{"@playwright/mcp"},
		},
		{
			ID: "chrome-devtools", Name: "Chrome DevTools",
			Description: "Inspect pages with DevTools: console, network, performance traces.",
			Command:     []string{"npx", "-y", "chrome-devtools-mcp@latest"},
			Markers:     []string{"chrome-devtools-mcp"},
		},
		{
			ID: "blender", Name: "Blender",
			Description: "Let agents build and render scenes in a running Blender.",
			Command:     []string{"uvx", "blender-mcp"},
			Markers:     []string{"blender-mcp"},
		},
		{
			ID: "context7", Name: "Context7",
			Description: "Up-to-date library documentation and code examples for prompts.",
			Command:     []string{"npx", "-y", "@upstash/context7-mcp@latest"},
			Markers:     []string{"@upstash/context7-mcp", "mcp.context7.com"},
		},
		{
			ID: "filesystem", Name: "Filesystem",
			Description: "Read and write files under your home directory.",
			Command:     []string{"npx", "-y", "@modelcontextprotocol/server-filesystem", home},
			Markers:     []string{"@modelcontextprotocol/server-filesystem"},
		},
	}
}

type configFormat int

const (
	formatJSON     configFormat = iota // {"<key>": {"name": {"command", "args", "env"}}}
	formatOpenCode                     // {"mcp": {"name": {"type": "local", "command": [...]}}}
	formatCodex                        // TOML [mcp_servers.name] command/args/env
)

// agentMCP describes where one agent keeps its user-level MCP servers.
type agentMCP struct {
	ID      string
	Name    string
	Binary  string   // CLI used for detection and, when cli is set, applying
	Files   []string // candidate config files relative to home; first existing wins, else the first
	Markers []string // paths relative to home whose existence means the agent is set up
	Format  configFormat
	Key     string
	// CLI applies changes through the agent's own command when present.
	CLI bool
	// TypeField adds "type": "stdio" to JSON entries (Claude's schema).
	TypeField bool
}

// MCPAgents lists the agents whose MCP configuration Relay understands.
var MCPAgents = []agentMCP{
	{ID: "claude", Name: "Claude Code", Binary: "claude", Files: []string{".claude.json"}, Markers: []string{".claude", ".claude.json"}, Format: formatJSON, Key: "mcpServers", CLI: true, TypeField: true},
	{ID: "codex", Name: "Codex", Binary: "codex", Files: []string{".codex/config.toml"}, Markers: []string{".codex"}, Format: formatCodex, CLI: true},
	{ID: "gemini", Name: "Gemini CLI", Binary: "gemini", Files: []string{".gemini/settings.json"}, Markers: []string{".gemini"}, Format: formatJSON, Key: "mcpServers"},
	{ID: "opencode", Name: "OpenCode", Binary: "opencode", Files: []string{".config/opencode/opencode.json", ".config/opencode/opencode.jsonc"}, Markers: []string{".config/opencode"}, Format: formatOpenCode, Key: "mcp"},
	{ID: "kiro", Name: "Kiro", Binary: "kiro-cli", Files: []string{".kiro/settings/mcp.json"}, Markers: []string{".kiro"}, Format: formatJSON, Key: "mcpServers"},
	{ID: "cursor", Name: "Cursor", Binary: "cursor-agent", Files: []string{".cursor/mcp.json"}, Markers: []string{".cursor"}, Format: formatJSON, Key: "mcpServers"},
}

// mcpEntry is one configured server in an agent's config.
type mcpEntry struct {
	Name    string
	Command []string // command + args, or [url] for remote servers
}

// MCP reads and edits agents' MCP configuration under one home directory.
type MCP struct {
	Home   string
	Finder Finder
	Run    Runner // runs agent CLIs; HOME is forced to Home
	Now    func() time.Time
	// UseCLI allows applying through agent CLIs (claude/codex mcp add).
	UseCLI bool

	mu sync.Mutex // serialises applies
}

// NewMCP returns an MCP manager for home using the given finder.
func NewMCP(home string, finder Finder, run Runner) *MCP {
	return &MCP{Home: home, Finder: finder, Run: run, Now: time.Now, UseCLI: true}
}

func (m *MCP) agent(id string) *agentMCP {
	for i := range MCPAgents {
		if MCPAgents[i].ID == id {
			return &MCPAgents[i]
		}
	}
	return nil
}

func (m *MCP) def(id string) *MCPDef {
	for _, d := range MCPRegistry(m.Home) {
		if d.ID == id {
			return &d
		}
	}
	return nil
}

// detected reports whether an agent appears to be installed.
func (m *MCP) detected(a *agentMCP) bool {
	if _, ok := m.Finder.Find(a.Binary); ok {
		return true
	}
	for _, p := range append(append([]string{}, a.Markers...), a.Files...) {
		if _, err := os.Stat(filepath.Join(m.Home, p)); err == nil {
			return true
		}
	}
	return false
}

// configPath returns the config file used for a.
func (m *MCP) configPath(a *agentMCP) string {
	for _, f := range a.Files {
		p := filepath.Join(m.Home, f)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(m.Home, a.Files[0])
}

// read returns the servers configured for a (nil when the file is missing).
func (m *MCP) read(a *agentMCP) ([]mcpEntry, error) {
	b, err := os.ReadFile(m.configPath(a))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s config: %w", a.Name, err)
	}
	if a.Format == formatCodex {
		return readCodex(b)
	}
	return readJSON(b, a.Key, a.Format)
}

func readJSON(b []byte, key string, format configFormat) ([]mcpEntry, error) {
	b, _ = stripJSONComments(b)
	root, err := parseOrdered(b)
	if err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	v, ok := root.get(key)
	if !ok || v == nil {
		return nil, nil
	}
	servers, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("%q is not an object", key)
	}
	var out []mcpEntry
	for _, name := range servers.keys {
		srv, _ := servers.vals[name].(*object)
		e := mcpEntry{Name: name}
		if srv != nil {
			if format == formatOpenCode {
				e.Command = stringList(srv.vals["command"])
			} else if c, ok := srv.vals["command"].(string); ok {
				e.Command = append([]string{c}, stringList(srv.vals["args"])...)
			}
			if len(e.Command) == 0 {
				for _, k := range []string{"url", "httpUrl", "serverUrl"} {
					if u, ok := srv.vals[k].(string); ok {
						e.Command = []string{u}
						break
					}
				}
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func readCodex(b []byte) ([]mcpEntry, error) {
	var doc struct {
		Servers map[string]struct {
			Command string   `toml:"command"`
			Args    []string `toml:"args"`
			URL     string   `toml:"url"`
		} `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse TOML: %w", err)
	}
	var out []mcpEntry
	for _, name := range sortedKeys(doc.Servers) {
		s := doc.Servers[name]
		e := mcpEntry{Name: name}
		switch {
		case s.Command != "":
			e.Command = append([]string{s.Command}, s.Args...)
		case s.URL != "":
			e.Command = []string{s.URL}
		}
		out = append(out, e)
	}
	return out, nil
}

// matches returns the names of entries that are def.
func matches(def *MCPDef, entries []mcpEntry) []string {
	var names []string
	for _, e := range entries {
		joined := strings.Join(e.Command, " ")
		hit := e.Name == def.ID
		for _, mk := range def.Markers {
			if strings.Contains(joined, mk) {
				hit = true
			}
		}
		if hit {
			names = append(names, e.Name)
		}
	}
	return names
}

// Matrix returns every registry server with, for each detected agent,
// whether it is configured there.
func (m *MCP) Matrix(ctx context.Context) []api.MCPServer {
	type agentState struct {
		id      string
		entries []mcpEntry
	}
	var agents []agentState
	for i := range MCPAgents {
		a := &MCPAgents[i]
		if !m.detected(a) {
			continue
		}
		entries, err := m.read(a)
		if err != nil {
			entries = nil // unreadable config counts as "not configured"
		}
		agents = append(agents, agentState{id: a.ID, entries: entries})
	}
	defs := MCPRegistry(m.Home)
	out := make([]api.MCPServer, 0, len(defs))
	for i := range defs {
		d := &defs[i]
		row := api.MCPServer{ID: d.ID, Name: d.Name, Description: d.Description, Command: d.Command, Env: d.Env, Agents: map[string]bool{}}
		for _, a := range agents {
			row.Agents[a.id] = len(matches(d, a.entries)) > 0
		}
		out = append(out, row)
	}
	return out
}

// Apply adds (or removes) a registry server for the requested agents and
// returns the updated matrix row.
func (m *MCP) Apply(ctx context.Context, req api.MCPApplyRequest) (*api.MCPServer, error) {
	def := m.def(req.Server)
	if def == nil {
		return nil, httpx.BadRequest("unknown MCP server " + strconv.Quote(req.Server))
	}
	if len(req.Agents) == 0 {
		return nil, httpx.BadRequest("choose at least one agent")
	}
	var targets []*agentMCP
	for _, id := range req.Agents {
		a := m.agent(id)
		if a == nil {
			return nil, httpx.BadRequest("unknown agent " + strconv.Quote(id))
		}
		if !m.detected(a) {
			return nil, httpx.BadRequest(a.Name + " is not installed")
		}
		targets = append(targets, a)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range targets {
		var err error
		if req.Remove {
			err = m.remove(ctx, a, def)
		} else {
			err = m.add(ctx, a, def)
		}
		if err != nil {
			return nil, err
		}
	}
	for _, row := range m.Matrix(ctx) {
		if row.ID == def.ID {
			return &row, nil
		}
	}
	return nil, httpx.NotFound("server vanished from the registry")
}

func (m *MCP) cli(a *agentMCP) (string, bool) {
	if !m.UseCLI || !a.CLI || m.Run == nil {
		return "", false
	}
	return m.Finder.Find(a.Binary)
}

func (m *MCP) add(ctx context.Context, a *agentMCP, def *MCPDef) error {
	entries, err := m.read(a)
	if err != nil {
		return &httpx.Err{Status: 409, Code: "conflict", Message: err.Error()}
	}
	if len(matches(def, entries)) > 0 {
		return nil // already configured
	}
	if bin, ok := m.cli(a); ok {
		return m.runCLI(ctx, a, cliAdd(bin, a, def))
	}
	return m.editFile(a, func(b []byte) ([]byte, error) { return addToConfig(b, a, def) })
}

func (m *MCP) remove(ctx context.Context, a *agentMCP, def *MCPDef) error {
	entries, err := m.read(a)
	if err != nil {
		return &httpx.Err{Status: 409, Code: "conflict", Message: err.Error()}
	}
	names := matches(def, entries)
	if len(names) == 0 {
		return nil
	}
	if bin, ok := m.cli(a); ok {
		for _, n := range names {
			if err := m.runCLI(ctx, a, cliRemove(bin, a, n)); err != nil {
				return err
			}
		}
		return nil
	}
	return m.editFile(a, func(b []byte) ([]byte, error) { return removeFromConfig(b, a, names) })
}

func cliAdd(bin string, a *agentMCP, def *MCPDef) []string {
	argv := []string{bin, "mcp", "add"}
	if a.ID == "claude" {
		argv = append(argv, "-s", "user")
	}
	env := sortedKeys(def.Env)
	if a.ID == "claude" {
		for _, k := range env {
			argv = append(argv, "-e", k+"="+def.Env[k])
		}
		argv = append(argv, def.ID)
	} else {
		argv = append(argv, def.ID)
		for _, k := range env {
			argv = append(argv, "--env", k+"="+def.Env[k])
		}
	}
	argv = append(argv, "--")
	return append(argv, def.Command...)
}

func cliRemove(bin string, a *agentMCP, name string) []string {
	if a.ID == "claude" {
		return []string{bin, "mcp", "remove", "-s", "user", name}
	}
	return []string{bin, "mcp", "remove", name}
}

func (m *MCP) runCLI(ctx context.Context, a *agentMCP, argv []string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	env := append(os.Environ(), "HOME="+m.Home, m.Finder.Env(), "NO_COLOR=1", "CI=1")
	out, err := m.Run(ctx, env, argv)
	if err != nil {
		msg := strings.TrimSpace(out)
		if len(msg) > 400 {
			msg = msg[:400] + "…"
		}
		if msg == "" {
			msg = err.Error()
		}
		return &httpx.Err{Status: 502, Code: "agent_cli_failed", Message: fmt.Sprintf("%s mcp: %s", a.Binary, msg)}
	}
	return nil
}

// editFile backs up the agent config, applies fn and writes the result
// atomically, preserving the file mode (0600 for new files).
func (m *MCP) editFile(a *agentMCP, fn func([]byte) ([]byte, error)) error {
	path := m.configPath(a)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	orig, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	next, err := fn(orig)
	if err != nil {
		return &httpx.Err{Status: 409, Code: "conflict", Message: fmt.Sprintf("%s: %v", path, err)}
	}
	if exists {
		if _, err := m.backup(path, orig, mode); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	return writeAtomic(path, next, mode)
}

// backup copies data next to path as <path>.relay-backup-<timestamp>.
func (m *MCP) backup(path string, data []byte, mode fs.FileMode) (string, error) {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	base := path + ".relay-backup-" + now().UTC().Format("20060102-150405")
	dst := base
	for i := 1; ; i++ {
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, fs.ErrExist) {
			dst = fmt.Sprintf("%s-%d", base, i)
			continue
		}
		if err != nil {
			return "", fmt.Errorf("back up %s: %w", path, err)
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			return "", fmt.Errorf("back up %s: %w", path, errors.Join(werr, cerr))
		}
		return dst, nil
	}
}

// writeAtomic writes data to a temp file beside path and renames it over.
func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".relay-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// addToConfig returns config b with def added for agent a.
func addToConfig(b []byte, a *agentMCP, def *MCPDef) ([]byte, error) {
	if a.Format == formatCodex {
		return addCodex(b, def)
	}
	if _, hasComments := stripJSONComments(b); hasComments {
		return nil, errors.New("the file has comments that an automatic edit would lose; add the server by hand")
	}
	root, err := parseOrdered(b)
	if err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	servers, err := root.child(a.Key)
	if err != nil {
		return nil, err
	}
	e := newObject()
	if a.Format == formatOpenCode {
		e.set("type", "local")
		e.set("command", toAny(def.Command))
		e.set("enabled", true)
		if len(def.Env) > 0 {
			e.set("environment", def.Env)
		}
	} else {
		if a.TypeField {
			e.set("type", "stdio")
		}
		e.set("command", def.Command[0])
		e.set("args", toAny(def.Command[1:]))
		if len(def.Env) > 0 {
			e.set("env", def.Env)
		}
	}
	servers.set(def.ID, e)
	return encodeOrdered(root)
}

// removeFromConfig returns config b without the named servers.
func removeFromConfig(b []byte, a *agentMCP, names []string) ([]byte, error) {
	if a.Format == formatCodex {
		return removeCodex(b, names)
	}
	if _, hasComments := stripJSONComments(b); hasComments {
		return nil, errors.New("the file has comments that an automatic edit would lose; remove the server by hand")
	}
	root, err := parseOrdered(b)
	if err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	servers, err := root.child(a.Key)
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		servers.del(n)
	}
	return encodeOrdered(root)
}

// addCodex appends a [mcp_servers.<id>] table, leaving the rest of the
// file (comments included) untouched.
func addCodex(b []byte, def *MCPDef) ([]byte, error) {
	if _, err := readCodex(b); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Write(b)
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		out.WriteByte('\n')
	}
	if len(b) > 0 {
		out.WriteByte('\n')
	}
	fmt.Fprintf(&out, "[mcp_servers.%s]\n", tomlKey(def.ID))
	fmt.Fprintf(&out, "command = %s\n", tomlString(def.Command[0]))
	args := make([]string, 0, len(def.Command)-1)
	for _, a := range def.Command[1:] {
		args = append(args, tomlString(a))
	}
	fmt.Fprintf(&out, "args = [%s]\n", strings.Join(args, ", "))
	if len(def.Env) > 0 {
		var kv []string
		for _, k := range sortedKeys(def.Env) {
			kv = append(kv, tomlKey(k)+" = "+tomlString(def.Env[k]))
		}
		fmt.Fprintf(&out, "env = { %s }\n", strings.Join(kv, ", "))
	}
	res := out.Bytes()
	if _, err := readCodex(res); err != nil {
		return nil, fmt.Errorf("edit produced invalid TOML: %w", err)
	}
	return res, nil
}

// removeCodex deletes the [mcp_servers.<name>] tables (and their
// sub-tables) for names, line by line.
func removeCodex(b []byte, names []string) ([]byte, error) {
	drop := map[string]bool{}
	for _, n := range names {
		drop[n] = true
	}
	lines := strings.SplitAfter(string(b), "\n")
	var out strings.Builder
	skipping := false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "[") {
			skipping = false
			if name, ok := codexTableName(t); ok && drop[name] {
				skipping = true
			}
		}
		if !skipping {
			out.WriteString(ln)
		}
	}
	res := []byte(strings.TrimRight(out.String(), "\n") + "\n")
	if strings.TrimSpace(string(res)) == "" {
		res = nil
	}
	entries, err := readCodex(res)
	if err != nil {
		return nil, fmt.Errorf("edit produced invalid TOML: %w", err)
	}
	for _, e := range entries {
		if drop[e.Name] {
			return nil, fmt.Errorf("server %q is defined inline; remove it by hand", e.Name)
		}
	}
	return res, nil
}

// codexTableName parses "[mcp_servers.name]" / "[mcp_servers.name.env]" /
// `[mcp_servers."name"]` and returns name.
func codexTableName(header string) (string, bool) {
	h := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(header, "["), "]"))
	if i := strings.Index(h, "#"); i >= 0 {
		h = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(h[:i]), "]"))
	}
	const p = "mcp_servers."
	if !strings.HasPrefix(h, p) {
		return "", false
	}
	rest := strings.TrimSpace(h[len(p):])
	if strings.HasPrefix(rest, `"`) {
		end := strings.Index(rest[1:], `"`)
		if end < 0 {
			return "", false
		}
		return rest[1 : 1+end], true
	}
	if i := strings.Index(rest, "."); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest), rest != ""
}

func tomlKey(k string) string {
	for _, r := range k {
		if !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return tomlString(k)
		}
	}
	return k
}

func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func stringList(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
