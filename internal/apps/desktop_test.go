package apps

import (
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/events"
)

// fakeLookPath resolves only the given names, to /usr/bin/<name>.
func fakeLookPath(names ...string) func(string) (string, error) {
	return func(n string) (string, error) {
		if filepath.IsAbs(n) {
			return n, nil
		}
		if slices.Contains(names, n) {
			return "/usr/bin/" + n, nil
		}
		return "", exec.ErrNotFound
	}
}

func TestDeskCatalog(t *testing.T) {
	cfg := []config.DesktopAppConfig{
		{ID: "gimp", Name: "GIMP", Command: []string{"gimp", "--no-splash"}},
		{ID: "files", Name: "My Files", Command: []string{"pcmanfm"}},
		{ID: "bad id!", Command: []string{"x"}},
		{ID: "ghost", Command: []string{"not-installed"}},
		{ID: "empty"},
	}
	got, errs := deskCatalog(cfg, "/data", "/home/u", fakeLookPath("chromium", "xterm", "gimp", "pcmanfm"))
	if len(errs) != 3 {
		t.Errorf("errors = %v, want 3", errs)
	}
	ids := make([]string, len(got))
	for i, a := range got {
		ids[i] = a.ID
	}
	if want := []string{"chrome", "terminal", "files", "gimp"}; !slices.Equal(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	chrome := got[0]
	if chrome.Argv[0] != "/usr/bin/chromium" || !slices.Contains(chrome.Argv, "--user-data-dir=/data/desktop/chrome") ||
		!slices.Contains(chrome.Argv, "--no-first-run") || !slices.Contains(chrome.Argv, "--no-default-browser-check") {
		t.Errorf("chrome argv = %v", chrome.Argv)
	}
	if got[2].Name != "My Files" || got[2].Argv[0] != "/usr/bin/pcmanfm" {
		t.Errorf("config must override the built-in files app: %+v", got[2])
	}
	if got[3].Icon == "" || !slices.Equal(got[3].Argv, []string{"/usr/bin/gimp", "--no-splash"}) {
		t.Errorf("gimp = %+v", got[3])
	}
}

func TestLauncherScriptQuoting(t *testing.T) {
	a := deskApp{ID: "x", Argv: []string{"/bin/echo", "it's", "$HOME", "a b"}}
	script := launcherScript(a)
	if !strings.Contains(script, `'it'\''s' '$HOME' 'a b' "$@"`) || !strings.Contains(script, "RELAY_DESKTOP_APP='x'") {
		t.Fatalf("script:\n%s", script)
	}
	out, err := exec.Command("/bin/sh", "-c", strings.TrimPrefix(script, "#!/bin/sh\n"), "sh", "tail").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "it's $HOME a b tail\n" {
		t.Fatalf("output = %q", out)
	}
}

func TestRenderSession(t *testing.T) {
	dir := filepath.Join(shortDir(t), "desktop")
	catalog := []deskApp{
		{ID: "terminal", Name: "Terminal", Icon: "utilities-terminal", Argv: []string{"/usr/bin/xterm"}},
		{ID: "evil", Name: "Evil\nExec=rm -rf ~ <&>", Icon: "x\ny", Argv: []string{"/bin/true"}},
	}
	if err := renderSession(dir, catalog); err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	rc := read("openbox/rc.xml")
	if strings.Contains(rc, "@BIN@") || !strings.Contains(rc, dir+"/bin/terminal") || !strings.Contains(rc, dir+"/openbox/menu.xml") {
		t.Error("rc.xml placeholders not rendered")
	}
	for _, key := range []string{"W-Left", "W-Right", "W-Up", "<name>Relay</name>"} {
		if !strings.Contains(rc, key) {
			t.Errorf("rc.xml lacks %s", key)
		}
	}
	if !strings.Contains(read("tint2rc"), "launcher_item_app = "+dir+"/applications/relay-terminal.desktop") {
		t.Error("tint2rc lacks the launcher item")
	}
	if !strings.Contains(read("share/themes/Relay/openbox-3/themerc"), "#ff5b1f") {
		t.Error("theme not written")
	}
	entry := read("applications/relay-evil.desktop")
	if strings.Contains(entry, "\nExec=rm") || strings.Contains(entry, "\ny\n") {
		t.Errorf("desktop entry injection:\n%s", entry)
	}
	menu := read("openbox/menu.xml")
	if strings.Contains(menu, "<&>") || !strings.Contains(menu, "&lt;&amp;&gt;") {
		t.Errorf("menu not escaped:\n%s", menu)
	}
	for _, rel := range []string{"xstartup", "bin/terminal"} {
		fi, err := os.Stat(filepath.Join(dir, rel))
		if err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %v (%v)", rel, fi.Mode(), err)
		}
	}
	// Re-rendering drops launchers that left the catalog.
	if err := renderSession(dir, catalog[:1]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "evil")); !errors.Is(err, os.ErrNotExist) {
		t.Error("stale launcher kept")
	}
	if err := renderSession("/tmp/with space", nil); err == nil {
		t.Error("unsafe directory accepted")
	}
}

func TestWriteXauthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Xauthority")
	if err := writeXauthority(path, 7); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	if binary.BigEndian.Uint16(b) != 0xffff {
		t.Fatal("family is not FamilyWild")
	}
	var fields []string
	rest := b[2:]
	for len(rest) >= 2 {
		n := int(binary.BigEndian.Uint16(rest))
		fields = append(fields, string(rest[2:2+n]))
		rest = rest[2+n:]
	}
	if len(fields) != 4 || fields[1] != "7" || fields[2] != "MIT-MAGIC-COOKIE-1" || len(fields[3]) != 16 {
		t.Fatalf("fields = %q", fields)
	}
	if xauth, err := exec.LookPath("xauth"); err == nil {
		out, err := exec.Command(xauth, "-f", path, "list").Output()
		if err != nil || !strings.Contains(string(out), "MIT-MAGIC-COOKIE-1") {
			t.Errorf("xauth list: %q %v", out, err)
		}
	}
}

func TestParseXrandr(t *testing.T) {
	out := "Screen 0: minimum 32 x 32, current 1600 x 1000, maximum 32768 x 32768\n" +
		"VNC-0 connected primary 1600x1000+0+0 0mm x 0mm\n" +
		"   1600x1000     60.00*+\n   1920x1080     60.00  \n" +
		"VNC-1 disconnected\n   800x600 60.00\n"
	output, modes := parseXrandr([]byte(out))
	if output != "VNC-0" || !modes["1600x1000"] || !modes["1920x1080"] || modes["800x600"] {
		t.Fatalf("output=%q modes=%v", output, modes)
	}
	if o, _ := parseXrandr(nil); o != "" {
		t.Fatal("empty input gave an output")
	}
}

func TestRunningScanner(t *testing.T) {
	proc := t.TempDir()
	write := func(pid, env string) {
		os.MkdirAll(filepath.Join(proc, pid), 0o755)
		os.WriteFile(filepath.Join(proc, pid, "environ"), []byte(env), 0o600)
	}
	write("10", "HOME=/h\x00DISPLAY=:7\x00RELAY_DESKTOP_APP=chrome\x00")
	write("11", "DISPLAY=:0\x00RELAY_DESKTOP_APP=blender\x00") // other display
	write("12", "DISPLAY=:7\x00")                              // not ours
	write("self", "DISPLAY=:7\x00RELAY_DESKTOP_APP=files\x00") // not a pid
	s := &runningScanner{procDir: proc, display: ":7", ttl: time.Minute}
	now := time.Now()
	got := s.Running(now)
	if !got["chrome"] || got["blender"] || got["files"] || len(got) != 1 {
		t.Fatalf("running = %v", got)
	}
	write("13", "DISPLAY=:7\x00RELAY_DESKTOP_APP=terminal\x00")
	if s.Running(now.Add(time.Second))["terminal"] {
		t.Fatal("cache ignored")
	}
	s.Reset()
	if !s.Running(now.Add(time.Second))["terminal"] {
		t.Fatal("reset ignored")
	}
}

func testDeps(t *testing.T, mut func(*config.Config)) *core.Deps {
	cfg := config.Defaults()
	if mut != nil {
		mut(cfg)
	}
	root := shortDir(t)
	return &core.Deps{
		Cfg: cfg, Bus: events.New(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Paths: config.Paths{Home: root, DataDir: filepath.Join(root, "data"), RuntimeDir: filepath.Join(root, "run")},
	}
}

func TestNewDesktopConfig(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name      string
		mut       func(*config.Config)
		bins      []string
		wantState string
		wantHint  bool
	}{
		{"ready", nil, []string{"Xvnc", "openbox"}, "stopped", false},
		{"missing tools", nil, []string{"openbox"}, "unavailable", true},
		{"disabled", func(c *config.Config) { c.Desktop.Enabled = false }, []string{"Xvnc", "openbox"}, "unavailable", false},
		{"bad display", func(c *config.Config) { c.Desktop.Display = "host:0" }, []string{"Xvnc", "openbox"}, "unavailable", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, err := newDesktop(testDeps(t, tt.mut), log, fakeLookPath(tt.bins...))
			if err != nil {
				t.Fatal(err)
			}
			st := k.State()
			if st.State != tt.wantState || (st.InstallHint != "") != tt.wantHint || st.Apps == nil {
				t.Fatalf("state = %+v", st)
			}
		})
	}
	k, _ := newDesktop(testDeps(t, func(c *config.Config) { c.Desktop.Geometry = "1280x720" }), log, fakeLookPath("Xvnc", "openbox"))
	args := strings.Join(k.xvncArgs("/usr/bin/Xvnc"), " ")
	for _, want := range []string{"-rfbport -1", "-rfbunixmode 0600", "-SecurityTypes None", "-localhost", "-nolisten tcp", "-geometry 1280x720", "-rfbunixpath " + k.sock} {
		if !strings.Contains(args, want) {
			t.Errorf("Xvnc args lack %q: %s", want, args)
		}
	}
	env := strings.Join(k.sessionEnv(), "\n")
	if !strings.Contains(env, "DISPLAY=:7\n") || !strings.Contains(env, "XAUTHORITY="+k.xauth) || strings.Contains(env, "WAYLAND_DISPLAY=") {
		t.Errorf("session env wrong")
	}
}
