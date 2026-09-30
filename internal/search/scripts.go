package search

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
)

// Script metadata limits.
const (
	// scriptHeaderMax is how much of a script is read looking for metadata.
	scriptHeaderMax = 16 << 10
	// scriptMaxArgs mirrors Raycast (argument1..argument3).
	scriptMaxArgs = 3
	// scriptMaxFiles bounds a directory scan.
	scriptMaxFiles = 500
	// scriptCacheTTL is how long a scan is reused when the directories'
	// modification times are unchanged.
	scriptCacheTTL = 30 * time.Second
)

// Script run modes.
const (
	ModeInline   = "inline"
	ModeTerminal = "terminal"
	ModeSilent   = "silent"
)

// metaLine matches "# @raycast.key value" and "// @relay.key value" style
// comments (#, //, --, ;, REM prefixes).
var metaLine = regexp.MustCompile(`^\s*(?:#|//|--|;|REM\s)\s*@(raycast|relay)\.([A-Za-z][A-Za-z0-9]*)\s*(.*?)\s*$`)

// scriptArgSpec is the JSON value of @raycast.argumentN.
type scriptArgSpec struct {
	Type        string `json:"type"`
	Placeholder string `json:"placeholder"`
	Optional    bool   `json:"optional"`
}

// parseScript reads Raycast/Relay metadata from the head of r. It returns
// ok=false when the file carries no title (it is then not a command).
// @relay.* keys win over @raycast.* keys.
func parseScript(r io.Reader, home string) (api.ScriptCommand, bool) {
	raycast := map[string]string{}
	relay := map[string]string{}
	sc := bufio.NewScanner(io.LimitReader(r, scriptHeaderMax))
	sc.Buffer(make([]byte, 0, 4096), scriptHeaderMax)
	for sc.Scan() {
		m := metaLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		dst := raycast
		if m[1] == "relay" {
			dst = relay
		}
		dst[m[2]] = m[3]
	}
	get := func(k string) string {
		if v, ok := relay[k]; ok {
			return v
		}
		return raycast[k]
	}
	cmd := api.ScriptCommand{
		Title:       get("title"),
		Description: get("description"),
		Icon:        scriptIcon(get("icon")),
		Mode:        scriptMode(get("mode")),
		Cwd:         expandHome(get("currentDirectoryPath"), home),
	}
	if cmd.Title == "" {
		return cmd, false
	}
	for i := 1; i <= scriptMaxArgs; i++ {
		raw := get("argument" + strconv.Itoa(i))
		if raw == "" {
			break // Raycast requires arguments to be contiguous
		}
		var spec scriptArgSpec
		if err := json.Unmarshal([]byte(raw), &spec); err != nil {
			spec = scriptArgSpec{Placeholder: raw}
		}
		cmd.Args = append(cmd.Args, api.ScriptArg{
			Name:        "argument" + strconv.Itoa(i),
			Placeholder: spec.Placeholder,
			Optional:    spec.Optional,
		})
	}
	return cmd, true
}

// scriptMode maps Raycast modes onto Relay's three: fullOutput streams in
// a task terminal, compact/inline return captured output, silent returns
// only the last line.
func scriptMode(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "fulloutput", "terminal":
		return ModeTerminal
	case "silent":
		return ModeSilent
	default:
		return ModeInline
	}
}

// scriptIcon keeps emoji and short names; file paths and URLs are dropped
// (the browser cannot load a local file and remote URLs would leak).
func scriptIcon(v string) string {
	if v == "" || len(v) > 64 || strings.ContainsAny(v, "/\\:") {
		return ""
	}
	return v
}

func expandHome(p, home string) string {
	switch {
	case p == "":
		return ""
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	}
	return p
}

// scriptCache remembers the last scan, keyed by the directories' mtimes.
type scriptCache struct {
	mu      sync.Mutex
	at      time.Time
	stamp   string
	scripts []api.ScriptCommand
}

// Scripts returns every script command in the configured directories.
// Earlier directories win on id collisions.
func (s *Service) Scripts() []api.ScriptCommand {
	stamp := dirStamp(s.scriptDirs)
	c := &s.scripts
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stamp == stamp && s.now().Sub(c.at) < scriptCacheTTL && c.scripts != nil {
		return c.scripts
	}
	c.scripts = scanScripts(s.scriptDirs, s.homeDir())
	c.stamp = stamp
	c.at = s.now()
	return c.scripts
}

// script returns the command with id, if present.
func (s *Service) script(id string) (api.ScriptCommand, bool) {
	for _, sc := range s.Scripts() {
		if sc.ID == id {
			return sc, true
		}
	}
	return api.ScriptCommand{}, false
}

// dirStamp summarises the directories' mtimes so edits (which rename or
// create entries) invalidate the cache immediately.
func dirStamp(dirs []string) string {
	var b strings.Builder
	for _, d := range dirs {
		if fi, err := os.Stat(d); err == nil {
			b.WriteString(strconv.FormatInt(fi.ModTime().UnixNano(), 36))
		}
		b.WriteByte('|')
	}
	return b.String()
}

// scanScripts lists executable regular files with metadata. Hidden files,
// directories, sockets and non-executables are ignored; symlinks are
// followed but must resolve to a regular file.
func scanScripts(dirs []string, home string) []api.ScriptCommand {
	out := []api.ScriptCommand{}
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for i, e := range entries {
			if i >= scriptMaxFiles {
				break
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") || seen[name] || !validScriptID(name) {
				continue
			}
			cmd, ok := loadScript(filepath.Join(dir, name), home)
			if !ok {
				continue
			}
			cmd.ID = name
			seen[name] = true
			out = append(out, cmd)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	return out
}

// loadScript stats and parses one candidate.
func loadScript(path, home string) (api.ScriptCommand, bool) {
	fi, err := os.Stat(path) // follows symlinks
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return api.ScriptCommand{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return api.ScriptCommand{}, false
	}
	defer f.Close()
	cmd, ok := parseScript(f, home)
	if !ok {
		return cmd, false
	}
	cmd.Path = path
	if cmd.Cwd == "" {
		cmd.Cwd = filepath.Dir(path)
	} else if !filepath.IsAbs(cmd.Cwd) {
		cmd.Cwd = filepath.Join(filepath.Dir(path), cmd.Cwd)
	}
	cmd.Cwd = filepath.Clean(cmd.Cwd)
	return cmd, true
}

// validScriptID accepts file names that are safe as a URL path segment.
func validScriptID(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_', r == '+', r == '@':
		default:
			return false
		}
	}
	return true
}

// validateArgs checks run arguments against the declared ones and pads
// missing optional arguments with "" (as Raycast does).
func validateArgs(cmd api.ScriptCommand, args []string) ([]string, string) {
	if len(args) > len(cmd.Args) {
		return nil, "too many arguments"
	}
	out := make([]string, len(cmd.Args))
	for i, a := range cmd.Args {
		v := ""
		if i < len(args) {
			v = args[i]
		}
		if len(v) > 4096 || strings.ContainsRune(v, 0) {
			return nil, "argument " + strconv.Itoa(i+1) + " is invalid"
		}
		if v == "" && !a.Optional {
			return nil, "argument " + strconv.Itoa(i+1) + " is required"
		}
		out[i] = v
	}
	return out, ""
}

// scriptProvider exposes script commands to the federated search.
type scriptProvider struct{ s *Service }

// ScriptProvider returns the "scripts" search provider.
func (s *Service) ScriptProvider() core.SearchProvider { return scriptProvider{s} }

// Scope implements core.SearchProvider.
func (scriptProvider) Scope() string { return "scripts" }

// Search implements core.SearchProvider. An empty query lists every
// script; otherwise every query word must appear in the title or
// description.
func (p scriptProvider) Search(ctx context.Context, query string, limit int) []api.SearchResult {
	q := strings.ToLower(query)
	words := strings.Fields(q)
	var out []api.SearchResult
	for _, sc := range p.s.Scripts() {
		if ctx.Err() != nil || len(out) >= limit {
			break
		}
		title := strings.ToLower(sc.Title)
		hay := title + " " + strings.ToLower(sc.Description)
		score := 0.3
		if q != "" {
			if !containsAll(hay, words) {
				continue
			}
			score = 0.4
			if strings.Contains(title, q) {
				score = 0.6
			}
		}
		out = append(out, api.SearchResult{
			Scope:    "scripts",
			ID:       sc.ID,
			Title:    sc.Title,
			Subtitle: sc.Description,
			Icon:     sc.Icon,
			Score:    score,
			Meta:     map[string]string{"mode": sc.Mode, "args": strconv.Itoa(len(sc.Args))},
		})
	}
	return out
}

func containsAll(hay string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}
