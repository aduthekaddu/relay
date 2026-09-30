package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const relayBin = "/opt/relay/bin/relay"

func backups(t *testing.T, path string) []string {
	t.Helper()
	m, _ := filepath.Glob(path + ".relay-backup-*")
	return m
}

func TestClaudeHooksInstallRemove(t *testing.T) {
	e := newTestEnv(t)
	h := newClaude().Hooks.(*jsonHooks)
	tick := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	h.now = func() time.Time { tick = tick.Add(time.Second); return tick }
	path := h.path(e)
	// Existing user settings with their own hook and key order.
	orig := `{
  "model": "opus",
  "hooks": {
    "Stop": [{"matcher": "", "hooks": [{"type": "command", "command": "say done"}]}]
  },
  "zeta": 1
}
`
	writeFile(t, path, orig)
	if st := h.status(e); st.Installed {
		t.Fatal("not installed yet")
	}
	st, err := h.install(e, relayBin)
	if err != nil || !st.Installed {
		t.Fatalf("install: %v %+v", err, st)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{relayBin + " hook claude notification", relayBin + " hook claude stop", "say done"} {
		if !strings.Contains(s, want) {
			t.Errorf("settings lack %q:\n%s", want, s)
		}
	}
	if strings.Index(s, `"model"`) > strings.Index(s, `"zeta"`) {
		t.Error("key order not preserved")
	}
	if bk := backups(t, path); len(bk) != 1 {
		t.Fatalf("backups = %v", bk)
	} else if got, _ := os.ReadFile(bk[0]); string(got) != orig {
		t.Error("backup differs from the original")
	}
	// Idempotent: a second install changes nothing and makes no backup.
	if _, err := h.install(e, relayBin); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(path)
	if string(b2) != s || len(backups(t, path)) != 1 {
		t.Error("second install modified the file")
	}
	// Remove restores the user's hooks only.
	st, err = h.remove(e)
	if err != nil || st.Installed {
		t.Fatalf("remove: %v %+v", err, st)
	}
	var got map[string]any
	b3, _ := os.ReadFile(path)
	if err := json.Unmarshal(b3, &got); err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	json.Unmarshal([]byte(orig), &want)
	if gb, _ := json.Marshal(got); string(gb) != mustJSON(want) {
		t.Errorf("after remove:\n%s\nwant\n%s", gb, mustJSON(want))
	}
	if strings.Contains(string(b3), "relay") {
		t.Error("relay hook left behind")
	}
	// Remove again: no-op.
	if _, err := h.remove(e); err != nil || len(backups(t, path)) != 2 {
		t.Errorf("second remove: %v backups=%d", err, len(backups(t, path)))
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestJSONHooksEdgeCases(t *testing.T) {
	e := newTestEnv(t)
	h := newClaude().Hooks.(*jsonHooks)
	// Missing file: created with 0600 and no backup.
	if _, err := h.install(e, "/path with space/relay"); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(h.path(e))
	if st.Mode().Perm() != 0o600 || len(backups(t, h.path(e))) != 0 {
		t.Errorf("mode %v backups %v", st.Mode().Perm(), backups(t, h.path(e)))
	}
	b, _ := os.ReadFile(h.path(e))
	if !strings.Contains(string(b), `'/path with space/relay' hook claude stop`) {
		t.Errorf("relay path must be shell-quoted: %s", b)
	}
	// Invalid JSON is refused, untouched.
	writeFile(t, h.path(e), "{not json")
	if _, err := h.install(e, relayBin); err == nil {
		t.Error("invalid settings must not be overwritten")
	}
	if b, _ := os.ReadFile(h.path(e)); string(b) != "{not json" {
		t.Error("invalid settings modified")
	}
	// Cursor's flat layout.
	c := newCursor().Hooks.(*jsonHooks)
	if st, err := c.install(e, relayBin); err != nil || !st.Installed {
		t.Fatalf("cursor install: %v %+v", err, st)
	}
	var doc struct {
		Version int `json:"version"`
		Hooks   map[string][]struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	b, _ = os.ReadFile(c.path(e))
	if json.Unmarshal(b, &doc) != nil || doc.Version != 1 || doc.Hooks["stop"][0].Command != relayBin+" hook cursor stop" {
		t.Errorf("cursor hooks.json = %s", b)
	}
	if st, _ := c.remove(e); st.Installed {
		t.Error("cursor remove")
	}
	// Gemini installs Notification + AfterAgent.
	g := newGemini().Hooks.(*jsonHooks)
	if st, err := g.install(e, relayBin); err != nil || !st.Installed {
		t.Fatalf("gemini: %v %+v", err, st)
	}
}

func TestCodexHooks(t *testing.T) {
	e := newTestEnv(t)
	h := &codexHooks{now: func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }}
	path := h.path(e)
	orig := "# my codex config\nmodel = \"gpt-5\"\n\n[profiles.fast]\nmodel = \"o4-mini\"\nnotify = [\"not\", \"top-level\"]\n"
	writeFile(t, path, orig)
	st, err := h.install(e, relayBin)
	if err != nil || !st.Installed {
		t.Fatalf("install: %v %+v", err, st)
	}
	b, _ := os.ReadFile(path)
	lines := strings.Split(string(b), "\n")
	if lines[0] != "# my codex config" || !strings.HasPrefix(lines[1], `notify = ["`+relayBin+`", "hook", "codex", "notify"]`) {
		t.Fatalf("config:\n%s", b)
	}
	if !strings.Contains(string(b), `notify = ["not", "top-level"]`) {
		t.Error("table-level notify must be left alone")
	}
	if len(backups(t, path)) != 1 {
		t.Error("no backup")
	}
	if _, err := h.install(e, relayBin); err != nil {
		t.Fatal(err)
	}
	if b2, _ := os.ReadFile(path); string(b2) != string(b) {
		t.Error("install is not idempotent")
	}
	if st, err := h.remove(e); err != nil || st.Installed {
		t.Fatalf("remove: %v %+v", err, st)
	}
	if b3, _ := os.ReadFile(path); string(b3) != orig {
		t.Errorf("remove did not restore:\n%q\nwant\n%q", b3, orig)
	}
	// Another program owns notify: refuse (409) rather than clobber it.
	writeFile(t, path, "notify = [\n  \"terminal-notifier\",\n  \"-title\", \"x\"\n]\n")
	if _, err := h.install(e, relayBin); err == nil {
		t.Error("foreign notify must not be replaced")
	}
	if st := h.status(e); st.Installed || st.Detail == "" {
		t.Errorf("status = %+v", st)
	}
}

func TestOpenCodePluginHooks(t *testing.T) {
	e := newTestEnv(t)
	h := opencodePlugin{}
	st, err := h.install(e, relayBin)
	if err != nil || !st.Installed || !strings.Contains(st.Path, filepath.Join(".config", "opencode", "plugins")) {
		t.Fatalf("install: %v %+v", err, st)
	}
	b, _ := os.ReadFile(h.path(e))
	if !strings.Contains(string(b), `"`+relayBin+`"`) || !strings.Contains(string(b), "session.idle") {
		t.Errorf("plugin:\n%s", b)
	}
	if st, _ := h.remove(e); st.Installed {
		t.Error("remove")
	}
	// A user file with the same name is backed up, never silently lost.
	writeFile(t, h.path(e), "// mine\n")
	if _, err := h.install(e, relayBin); err != nil {
		t.Fatal(err)
	}
	if len(backups(t, h.path(e))) != 1 {
		t.Error("user plugin not backed up")
	}
}

func TestParseHooks(t *testing.T) {
	cases := []struct {
		agent, event, payload string
		action, native, msg   string
	}{
		{"claude", "notification", `{"session_id":"s1","cwd":"/w","hook_event_name":"Notification","message":"Claude is waiting for your input"}`, hookAttention, "s1", "Claude is waiting for your input"},
		{"claude", "stop", `{"session_id":"s1","hook_event_name":"Stop"}`, hookDone, "s1", "Finished its turn"},
		{"claude", "stop", `{"session_id":"s1","hook_event_name":"SubagentStop"}`, "", "", ""},
		{"claude", "notification", `not json`, hookAttention, "", "Needs your input"},
		{"gemini", "stop", `{"session_id":"g","hook_event_name":"AfterAgent"}`, hookDone, "g", "Finished its turn"},
		{"codex", "notify", `{"type":"agent-turn-complete","thread-id":"th","cwd":"/w","last-assistant-message":"All   tests pass"}`, hookDone, "th", "All tests pass"},
		{"codex", "notify", `{"type":"approval-requested","thread-id":"th"}`, hookAttention, "th", "Needs your approval"},
		{"codex", "notify", `{"type":"something-else"}`, "", "", ""},
		{"opencode", "idle", `{"type":"session.idle","properties":{"sessionID":"ses_1"}}`, hookDone, "ses_1", "Finished its turn"},
		{"opencode", "permission", `{"type":"permission.asked","properties":{"sessionID":"ses_1"}}`, hookAttention, "ses_1", "Needs your permission"},
		{"cursor", "stop", `{"conversation_id":"c1","workspace_roots":["/w"]}`, hookDone, "c1", "Finished its turn"},
	}
	adapters := map[string]*Adapter{}
	for _, a := range builtinAdapters() {
		adapters[a.ID] = a
	}
	for _, c := range cases {
		ev := adapters[c.agent].ParseHook(c.event, []byte(c.payload))
		if ev.Action != c.action || ev.NativeID != c.native || ev.Message != c.msg {
			t.Errorf("%s %s %s: got %+v", c.agent, c.event, c.payload, ev)
		}
	}
}

func TestIsRelayHookAndQuote(t *testing.T) {
	if !isRelayHook("/usr/local/bin/relay hook claude stop", "claude") || isRelayHook("say hook claude", "claude") {
		t.Error("isRelayHook")
	}
	for in, want := range map[string]string{"/a/b": "/a/b", "/a b": "'/a b'", "it's": `'it'\''s'`, "": "''"} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
