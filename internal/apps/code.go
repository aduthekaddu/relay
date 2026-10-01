package apps

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ideFlavor is which VS Code server we found.
type ideFlavor int

const (
	flavorCodeServer ideFlavor = iota
	flavorOpenVSCode
)

// codeBinary locates code-server or openvscode-server: the configured
// binary exclusively when set; otherwise PATH, ~/.local/bin and standalone
// ~/.local/lib/code-server-* installs.
func codeBinary(configured, home string, lookPath func(string) (string, error)) (string, ideFlavor, bool) {
	bin, flavor, _, ok := discoverCode(configured, home, lookPath)
	return bin, flavor, ok
}

// discoverCode is shared by capability queries and the launch resolver.
// No positive or negative installation result is cached.
func discoverCode(configured, home string, lookPath func(string) (string, error)) (string, ideFlavor, string, bool) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		p := expandHome(configured, home)
		if !strings.ContainsRune(p, '/') {
			if lp, err := lookPath(p); err == nil && isExecutable(lp) {
				return lp, flavorOf(lp), "configured", true
			}
			p = filepath.Join(home, ".local", "bin", p)
		}
		p, err := filepath.Abs(p)
		if err != nil {
			return "", flavorCodeServer, "configured", false
		}
		return p, flavorOf(p), "configured", isExecutable(p)
	}
	for _, name := range []string{"code-server", "openvscode-server"} {
		if p, err := lookPath(name); err == nil && isExecutable(p) {
			return p, flavorOf(p), "path", true
		}
	}
	if home != "" {
		for _, name := range []string{"code-server", "openvscode-server"} {
			p := filepath.Join(home, ".local", "bin", name)
			if isExecutable(p) {
				return p, flavorOf(p), "local-bin", true
			}
		}
		matches, _ := filepath.Glob(filepath.Join(home, ".local", "lib", "code-server-*", "bin", "code-server"))
		sort.Slice(matches, func(i, j int) bool { return versionLess(matches[j], matches[i]) })
		for _, p := range matches {
			if isExecutable(p) {
				return p, flavorCodeServer, "standalone", true
			}
		}
	}
	return "", flavorCodeServer, "", false
}

func flavorOf(p string) ideFlavor {
	if strings.Contains(filepath.Base(p), "openvscode") {
		return flavorOpenVSCode
	}
	return flavorCodeServer
}

// versionLess orders "…/code-server-4.9.0/…" before "…/code-server-4.10.0/…".
func versionLess(a, b string) bool {
	va, vb := dirVersion(a), dirVersion(b)
	for i := 0; i < len(va) && i < len(vb); i++ {
		if va[i] != vb[i] {
			return va[i] < vb[i]
		}
	}
	return len(va) < len(vb)
}

func dirVersion(p string) []int {
	for _, part := range strings.Split(p, string(filepath.Separator)) {
		if v, ok := strings.CutPrefix(part, "code-server-"); ok {
			var out []int
			for _, n := range strings.Split(v, ".") {
				i, _ := strconv.Atoi(n)
				out = append(out, i)
			}
			return out
		}
	}
	return nil
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// codeArgs builds the argv for an isolated IDE instance listening on sock
// and served under base (e.g. "/apps/code"). The instance has its own
// config file, user data and extensions, so it never touches another
// code-server installation on the machine.
func codeArgs(bin string, flavor ideFlavor, sock, dataDir, base string) []string {
	user := filepath.Join(dataDir, "code", "user")
	ext := filepath.Join(dataDir, "code", "extensions")
	if flavor == flavorOpenVSCode {
		return []string{bin,
			"--socket-path", sock,
			"--without-connection-token",
			"--server-base-path", base,
			"--user-data-dir", user,
			"--extensions-dir", ext,
			"--telemetry-level", "off",
			"--accept-server-license-terms",
		}
	}
	return []string{bin,
		"--config", filepath.Join(dataDir, "code", "config.yaml"),
		"--socket", sock,
		"--socket-mode", "600",
		"--auth", "none",
		"--disable-telemetry",
		"--disable-update-check",
		"--disable-proxy", // Relay's previews own port proxying
		"--user-data-dir", user,
		"--extensions-dir", ext,
		"--abs-proxy-base-path", base,
	}
}

// codeConfigYAML is written to <data>/code/config.yaml so code-server
// never reads ~/.config/code-server/config.yaml (another instance's).
const codeConfigYAML = "# Managed by Relay. Flags on the command line take precedence.\nauth: none\ncert: false\n"

// childEnv returns Relay's environment minus variables that would change
// the child's authentication or leak Relay internals, plus extra.
func childEnv(base []string, extra map[string]string) []string {
	drop := map[string]bool{
		"PASSWORD": true, "HASHED_PASSWORD": true, "PORT": true,
		"CODE_SERVER_CONFIG": true, "VSCODE_PROXY_URI": true,
		"RELAY_SOCKET": true, "RELAY_SESSION": true,
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if drop[k] {
			continue
		}
		if _, override := extra[k]; override {
			continue
		}
		out = append(out, kv)
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+extra[k])
	}
	return out
}

var _ = exec.LookPath
