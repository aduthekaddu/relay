package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

// codexHooks manages the top-level `notify` program in ~/.codex/config.toml:
//
//	notify = ["/path/to/relay", "hook", "codex", "notify"]
//
// Codex appends the event JSON as the last argument. The file is edited
// textually (only the notify statement is touched) so comments and
// formatting survive.
type codexHooks struct {
	now func() time.Time
}

var notifyKeyRe = regexp.MustCompile(`^\s*notify\s*=`)

func (h *codexHooks) path(e *env) string { return e.path(".codex", "config.toml") }

// findNotify locates the top-level notify statement: the line range
// [start, end) and its text. start is -1 when absent.
func findNotify(lines []string) (start, end int, stmt string) {
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			break // first table: top-level keys are over
		}
		if !notifyKeyRe.MatchString(l) {
			continue
		}
		depth := 0
		j := i
		for ; j < len(lines); j++ {
			depth += bracketDelta(lines[j])
			if depth <= 0 {
				break
			}
		}
		if j >= len(lines) {
			j = len(lines) - 1
		}
		return i, j + 1, strings.Join(lines[i:j+1], "\n")
	}
	return -1, -1, ""
}

// bracketDelta counts [ minus ] outside quoted strings and comments.
func bracketDelta(line string) int {
	d := 0
	var quote rune
	esc := false
	for _, r := range line {
		switch {
		case esc:
			esc = false
		case quote != 0:
			if r == '\\' && quote == '"' {
				esc = true
			} else if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '#':
			return d
		case r == '[':
			d++
		case r == ']':
			d--
		}
	}
	return d
}

func isRelayNotify(stmt string) bool {
	return strings.Contains(stmt, "relay") && strings.Contains(stmt, `"hook"`) && strings.Contains(stmt, `"codex"`)
}

func (h *codexHooks) read(e *env) ([]string, error) {
	b, err := os.ReadFile(h.path(e))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read codex config: %w", err)
	}
	return strings.Split(string(b), "\n"), nil
}

func (h *codexHooks) status(e *env) api.HookStatus {
	st := api.HookStatus{Path: h.path(e)}
	lines, err := h.read(e)
	if err != nil {
		st.Detail = err.Error()
		return st
	}
	_, _, stmt := findNotify(lines)
	switch {
	case stmt == "":
	case isRelayNotify(stmt):
		st.Installed = true
		st.Detail = "Relay notify program"
	default:
		st.Detail = "another notify program is configured"
	}
	return st
}

func (h *codexHooks) install(e *env, relay string) (api.HookStatus, error) {
	lines, err := h.read(e)
	if err != nil {
		return h.status(e), err
	}
	start, _, stmt := findNotify(lines)
	if start >= 0 {
		if isRelayNotify(stmt) {
			return h.status(e), nil
		}
		return h.status(e), httpx.Conflict("Codex already runs another notify program; remove it from ~/.codex/config.toml first")
	}
	argv, _ := json.Marshal([]string{relay, "hook", "codex", "notify"})
	stmtLine := "notify = " + strings.ReplaceAll(string(argv), ",", ", ") + " # added by Relay (relay hook)"
	// Insert after any leading comment block, before the first key/table.
	at := 0
	for at < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[at]), "#") {
		at++
	}
	out := append([]string{}, lines[:at]...)
	out = append(out, stmtLine)
	out = append(out, lines[at:]...)
	if err := h.write(e, out); err != nil {
		return h.status(e), err
	}
	return h.status(e), nil
}

func (h *codexHooks) remove(e *env) (api.HookStatus, error) {
	lines, err := h.read(e)
	if err != nil {
		return h.status(e), err
	}
	start, end, stmt := findNotify(lines)
	if start < 0 || !isRelayNotify(stmt) {
		return h.status(e), nil
	}
	out := append(append([]string{}, lines[:start]...), lines[end:]...)
	if err := h.write(e, out); err != nil {
		return h.status(e), err
	}
	return h.status(e), nil
}

func (h *codexHooks) write(e *env, lines []string) error {
	now := time.Now
	if h.now != nil {
		now = h.now
	}
	if _, err := backupFile(h.path(e), now()); err != nil {
		return err
	}
	text := strings.Join(lines, "\n")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return writeFileAtomic(h.path(e), []byte(text))
}
