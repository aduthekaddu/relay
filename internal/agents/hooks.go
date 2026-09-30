package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

// hookInstaller installs Relay's attention hooks into one agent's own
// configuration. Every change is idempotent and preceded by a timestamped
// backup of the file being modified.
type hookInstaller interface {
	// path is the config file the installer edits (for status/UI).
	path(e *env) string
	status(e *env) api.HookStatus
	install(e *env, relay string) (api.HookStatus, error)
	remove(e *env) (api.HookStatus, error)
}

// hookEvent is the adapter's interpretation of a hook invocation.
type hookEvent struct {
	Action   string // "attention" (needs the user), "done" (turn finished) or "" (ignore)
	Message  string
	NativeID string // agent session id, when the payload carries one
	Cwd      string
}

const (
	hookAttention = "attention"
	hookDone      = "done"
)

// hookCommand renders the shell command an agent runs for a hook. relay
// is the path of the relay executable (quoted), never user input.
func hookCommand(relay, agent, event string) string {
	return shellQuote(relay) + " hook " + agent + " " + event
}

// isRelayHook reports whether a hook command string was installed by Relay
// for agent.
func isRelayHook(cmd, agent string) bool {
	return strings.Contains(cmd, "relay") && strings.Contains(cmd, " hook "+agent+" ")
}

// shellQuote single-quotes s when it contains anything but safe characters.
func shellQuote(s string) string {
	safe := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+=:@", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// backupFile copies path to path.relay-backup-<UTC timestamp> (0600).
// A missing file needs no backup.
func backupFile(path string, now time.Time) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	dst := path + ".relay-backup-" + now.UTC().Format("20060102T150405Z")
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return "", fmt.Errorf("backup %s: %w", path, err)
	}
	return dst, nil
}

// writeFileAtomic replaces path with data via a temp file + rename,
// keeping the existing permission bits (0600 for new files).
func writeFileAtomic(path string, data []byte) error {
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".relay-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ---------------------------------------------------------------------------
// Order-preserving JSON objects, so editing a user's settings file only
// touches the hooks subtree and keeps their key order.

type jsonObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func newJSONObject() *jsonObject { return &jsonObject{vals: map[string]json.RawMessage{}} }

func parseJSONObject(b []byte) (*jsonObject, error) {
	o := newJSONObject()
	if len(bytes.TrimSpace(b)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("expected a JSON object")
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.set(k, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *jsonObject) get(k string) (json.RawMessage, bool) {
	v, ok := o.vals[k]
	return v, ok
}

func (o *jsonObject) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *jsonObject) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, kk := range o.keys {
		if kk == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *jsonObject) len() int { return len(o.keys) }

// MarshalJSON writes the object with keys in their original order.
func (o *jsonObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(o.vals[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// pretty renders v as indented JSON with a trailing newline.
func pretty(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// ---------------------------------------------------------------------------
// jsonHooks edits the Claude-Code-style hooks block shared by Claude Code
// and Gemini CLI:
//
//	{"hooks": {"Notification": [{"matcher": "", "hooks": [{"type": "command", "command": "…"}]}]}}
//
// With flat=true it edits the Cursor layout instead:
//
//	{"version": 1, "hooks": {"stop": [{"command": "…"}]}}
type jsonHooks struct {
	agent  string
	rel    []string          // config path relative to $HOME
	events map[string]string // agent hook event -> relay hook event name
	order  []string          // events in install order
	flat   bool
	now    func() time.Time
}

func (h *jsonHooks) path(e *env) string { return e.path(h.rel...) }

func (h *jsonHooks) load(e *env) (*jsonObject, *jsonObject, error) {
	b, err := os.ReadFile(h.path(e))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	root, err := parseJSONObject(b)
	if err != nil {
		return nil, nil, fmt.Errorf("%s is not valid JSON: %w", filepath.Base(h.path(e)), err)
	}
	hooks := newJSONObject()
	if raw, ok := root.get("hooks"); ok {
		if hooks, err = parseJSONObject(raw); err != nil {
			return nil, nil, fmt.Errorf("hooks: %w", err)
		}
	}
	return root, hooks, nil
}

// entries decodes one event's array as generic objects.
func entries(raw json.RawMessage) []map[string]json.RawMessage {
	var arr []map[string]json.RawMessage
	_ = json.Unmarshal(raw, &arr)
	return arr
}

func commandOf(m map[string]json.RawMessage) string {
	var s string
	_ = json.Unmarshal(m["command"], &s)
	return s
}

// installedEvents reports which of h.events already carry a Relay command.
func (h *jsonHooks) installedEvents(hooks *jsonObject) map[string]bool {
	out := map[string]bool{}
	for ev := range h.events {
		raw, ok := hooks.get(ev)
		if !ok {
			continue
		}
		for _, ent := range entries(raw) {
			if h.flat {
				if isRelayHook(commandOf(ent), h.agent) {
					out[ev] = true
				}
				continue
			}
			for _, hk := range entries(ent["hooks"]) {
				if isRelayHook(commandOf(hk), h.agent) {
					out[ev] = true
				}
			}
		}
	}
	return out
}

func (h *jsonHooks) status(e *env) api.HookStatus {
	st := api.HookStatus{Path: h.path(e)}
	_, hooks, err := h.load(e)
	if err != nil {
		st.Detail = err.Error()
		return st
	}
	got := h.installedEvents(hooks)
	st.Installed = len(got) == len(h.events)
	switch {
	case st.Installed:
		st.Detail = "Relay hooks: " + strings.Join(h.order, ", ")
	case len(got) > 0:
		st.Detail = "partially installed"
	}
	return st
}

func (h *jsonHooks) install(e *env, relay string) (api.HookStatus, error) {
	root, hooks, err := h.load(e)
	if err != nil {
		return h.status(e), err
	}
	have := h.installedEvents(hooks)
	if len(have) == len(h.events) {
		return h.status(e), nil
	}
	for _, ev := range h.order {
		if have[ev] {
			continue
		}
		cmd := hookCommand(relay, h.agent, h.events[ev])
		var arr []json.RawMessage
		if raw, ok := hooks.get(ev); ok {
			if err := json.Unmarshal(raw, &arr); err != nil {
				return h.status(e), fmt.Errorf("hooks.%s is not an array", ev)
			}
		}
		var entry any
		if h.flat {
			entry = map[string]any{"command": cmd}
		} else {
			entry = map[string]any{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": cmd, "timeout": 5}}}
		}
		eb, _ := json.Marshal(entry)
		arr = append(arr, eb)
		ab, _ := json.Marshal(arr)
		hooks.set(ev, ab)
	}
	if h.flat {
		if _, ok := root.get("version"); !ok {
			root.set("version", json.RawMessage("1"))
		}
	}
	hb, _ := hooks.MarshalJSON()
	root.set("hooks", hb)
	if err := h.write(e, root); err != nil {
		return h.status(e), err
	}
	return h.status(e), nil
}

func (h *jsonHooks) remove(e *env) (api.HookStatus, error) {
	root, hooks, err := h.load(e)
	if err != nil {
		return h.status(e), err
	}
	if len(h.installedEvents(hooks)) == 0 {
		return h.status(e), nil
	}
	for ev := range h.events {
		raw, ok := hooks.get(ev)
		if !ok {
			continue
		}
		var kept []any
		for _, ent := range entries(raw) {
			if h.flat {
				if !isRelayHook(commandOf(ent), h.agent) {
					kept = append(kept, ent)
				}
				continue
			}
			var inner []map[string]json.RawMessage
			for _, hk := range entries(ent["hooks"]) {
				if !isRelayHook(commandOf(hk), h.agent) {
					inner = append(inner, hk)
				}
			}
			if len(inner) == 0 {
				continue
			}
			ib, _ := json.Marshal(inner)
			ent["hooks"] = ib
			kept = append(kept, ent)
		}
		if len(kept) == 0 {
			hooks.del(ev)
			continue
		}
		kb, _ := json.Marshal(kept)
		hooks.set(ev, kb)
	}
	if hooks.len() == 0 {
		root.del("hooks")
	} else {
		hb, _ := hooks.MarshalJSON()
		root.set("hooks", hb)
	}
	if err := h.write(e, root); err != nil {
		return h.status(e), err
	}
	return h.status(e), nil
}

func (h *jsonHooks) write(e *env, root *jsonObject) error {
	now := time.Now
	if h.now != nil {
		now = h.now
	}
	if _, err := backupFile(h.path(e), now()); err != nil {
		return err
	}
	out, err := pretty(root)
	if err != nil {
		return err
	}
	return writeFileAtomic(h.path(e), out)
}

// payloadString extracts a top-level string field from a JSON payload.
func payloadString(m map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		var s string
		if raw, ok := m[k]; ok && json.Unmarshal(raw, &s) == nil && s != "" {
			return s
		}
	}
	return ""
}

// decodePayload parses a hook payload object; invalid JSON yields an empty map.
func decodePayload(b []byte) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	_ = json.Unmarshal(b, &m)
	return m
}
