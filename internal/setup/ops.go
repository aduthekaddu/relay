package setup

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aduthekaddu/relay/internal/config"
)

// LogsCommand returns the argv that shows Relay's logs on goos.
func LogsCommand(goos, logDir, service string, lines int, follow bool) ([]string, error) {
	if lines <= 0 || lines > 1_000_000 {
		return nil, fmt.Errorf("-n must be between 1 and 1000000")
	}
	var units, files []string
	switch service {
	case "all", "":
		units = []string{"relay-ptyd.service", "relay.service"}
		files = []string{"relay-ptyd.log", "relay.log"}
	case "serve", "relay":
		units, files = []string{"relay.service"}, []string{"relay.log"}
	case "ptyd":
		units, files = []string{"relay-ptyd.service"}, []string{"relay-ptyd.log"}
	default:
		return nil, fmt.Errorf("unknown service %q (use all, serve or ptyd)", service)
	}
	n := strconv.Itoa(lines)
	if goos == "darwin" {
		argv := []string{"tail", "-n", n}
		if follow {
			argv = append(argv, "-F")
		}
		for _, f := range files {
			argv = append(argv, filepath.Join(logDir, f))
		}
		return argv, nil
	}
	argv := []string{"journalctl", "--user", "-n", n, "-o", "short-iso"}
	for _, u := range units {
		argv = append(argv, "-u", u)
	}
	if follow {
		argv = append(argv, "-f")
	} else {
		argv = append(argv, "--no-pager")
	}
	return argv, nil
}

// PurgeTargets lists what --purge deletes. With RELAY_HOME everything
// lives under one root; otherwise the XDG directories are removed one by
// one. Paths outside the user's home (other than the runtime dir) are
// refused as a safety net.
func PurgeTargets(p config.Paths, relayHome string) ([]string, error) {
	var out []string
	if relayHome != "" {
		out = []string{config.Expand(relayHome)}
	} else {
		out = []string{p.ConfigDir, p.DataDir, p.CacheDir, p.RuntimeDir}
	}
	if cf := p.ConfigFile; !withinAny(cf, out) {
		out = append(out, cf)
	}
	for _, t := range out {
		clean := filepath.Clean(t)
		if !filepath.IsAbs(clean) || clean == "/" || clean == filepath.Clean(p.Home) {
			return nil, fmt.Errorf("refusing to delete %q", t)
		}
		if !strings.HasPrefix(clean, filepath.Clean(p.Home)+string(filepath.Separator)) && clean != filepath.Clean(p.RuntimeDir) {
			return nil, fmt.Errorf("refusing to delete %q: it is outside your home directory", t)
		}
	}
	return out, nil
}

func withinAny(path string, dirs []string) bool {
	for _, d := range dirs {
		if rel, err := filepath.Rel(d, path); err == nil && !strings.HasPrefix(rel, "..") {
			return true
		}
	}
	return false
}
