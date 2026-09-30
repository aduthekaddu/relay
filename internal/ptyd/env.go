package ptyd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// newID returns "t_" followed by 10 random base32 characters (50 bits).
func newID() string {
	var b [7]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err)) // never happens on supported systems
	}
	return "t_" + idEncoding.EncodeToString(b[:])[:10]
}

// validID reports whether s looks like a session id (cheap input check).
func validID(s string) bool {
	if len(s) != 12 || !strings.HasPrefix(s, "t_") {
		return false
	}
	for _, c := range s[2:] {
		if !(c >= 'a' && c <= 'z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}

// envDrop lists variables never passed from the daemon to sessions: they
// describe the daemon's own process (systemd, an enclosing tmux/screen)
// and would confuse programs in the session.
var envDrop = map[string]bool{
	"INVOCATION_ID": true, "JOURNAL_STREAM": true, "NOTIFY_SOCKET": true,
	"LISTEN_PID": true, "LISTEN_FDS": true, "LISTEN_FDNAMES": true, "WATCHDOG_PID": true,
	"WATCHDOG_USEC": true, "MANAGERPID": true, "SYSTEMD_EXEC_PID": true,
	"TMUX": true, "TMUX_PANE": true, "STY": true, "WINDOW": true,
	"RELAY_SESSION": true, "RELAY_NO_PTYD": true, "RELAY_LISTEN": true,
	"TERM_SESSION_ID": true, "ITERM_SESSION_ID": true, "KITTY_WINDOW_ID": true,
	"WEZTERM_PANE": true, "ALACRITTY_WINDOW_ID": true, "VTE_VERSION": true,
	"COLUMNS": true, "LINES": true, "SHLVL": true, "OLDPWD": true, "PWD": true, "_": true,
}

// envMap turns KEY=VALUE pairs into a map (later entries win).
func envMap(pairs []string) map[string]string {
	m := make(map[string]string, len(pairs))
	for _, kv := range pairs {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k != "" {
			m[k] = v
		}
	}
	return m
}

// envList renders a map as sorted KEY=VALUE pairs.
func envList(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// validEnvKey rejects names that cannot be exported safely.
func validEnvKey(k string) bool {
	if k == "" || len(k) > 128 {
		return false
	}
	for i, c := range k {
		switch {
		case c == '_', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// loginEnv asks the user's login shell for its environment, so sessions
// started by a systemd unit (with its minimal environment) see the same
// PATH and variables as a terminal would. The command is fixed (no user
// input) and bounded by a timeout.
func loginEnv(ctx context.Context, shell string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-l", "-c", "env -0")
	cmd.Stdin = nil
	cmd.Stderr = nil
	cmd.Dir = os.Getenv("HOME")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("login environment from %s: %w", shell, err)
	}
	if len(out) > 1<<20 {
		return nil, errors.New("login environment too large")
	}
	m := map[string]string{}
	for _, kv := range bytes.Split(out, []byte{0}) {
		k, v, ok := strings.Cut(string(kv), "=")
		if ok && validEnvKey(k) {
			m[k] = v
		}
	}
	if m["PATH"] == "" {
		return nil, errors.New("login environment has no PATH")
	}
	return m, nil
}

// lookPath resolves argv0 against the session PATH (not the daemon's).
func lookPath(argv0, pathEnv string) (string, error) {
	if strings.Contains(argv0, "/") {
		if isExecutable(argv0) {
			return argv0, nil
		}
		return "", fmt.Errorf("%s: not an executable file", argv0)
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		p := filepath.Join(dir, argv0)
		if isExecutable(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s: command not found", argv0)
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// displaySocket returns the X11 socket path for a display like ":7".
func displaySocket(display string) string {
	d := strings.TrimPrefix(display, ":")
	if i := strings.IndexByte(d, '.'); i >= 0 {
		d = d[:i]
	}
	if d == "" || strings.Trim(d, "0123456789") != "" {
		return ""
	}
	return "/tmp/.X11-unix/X" + d
}

// defaultShell picks the configured shell, then $SHELL, then bash, then sh.
func defaultShell(configured string, env map[string]string) string {
	for _, s := range []string{configured, env["SHELL"], os.Getenv("SHELL"), "/bin/bash", "/bin/sh"} {
		if s != "" && isExecutable(s) {
			return s
		}
	}
	return "/bin/sh"
}
