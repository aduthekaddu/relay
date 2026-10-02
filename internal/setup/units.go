package setup

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/aduthekaddu/relay/deploy/launchd"
	"github.com/aduthekaddu/relay/deploy/systemd"
)

// Logical service names.
const (
	SvcPtyd  = "ptyd"
	SvcServe = "serve"
)

// UnitMarker appears in every unit Relay renders; uninstall only removes
// files that carry it.
const UnitMarker = "Rendered by `relay setup`"

// UnitData feeds the unit templates.
type UnitData struct {
	Binary string
	Env    []string // KEY=VALUE
	LogDir string   // launchd only
}

type envPair struct{ Key, Value string }

// EnvPairs splits Env for the launchd template.
func (d UnitData) EnvPairs() []envPair {
	out := make([]envPair, 0, len(d.Env))
	for _, e := range d.Env {
		k, v, _ := strings.Cut(e, "=")
		out = append(out, envPair{k, v})
	}
	return out
}

// UnitEnv returns the environment baked into units: the Relay location
// overrides that are set, plus the caller's PATH so the server finds the
// same tools the user has in their shell.
func UnitEnv(getenv func(string) string) []string {
	var env []string
	for _, k := range []string{"RELAY_HOME", "RELAY_CONFIG", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"} {
		if v := getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	var dirs []string
	seen := map[string]bool{}
	for _, d := range filepath.SplitList(getenv("PATH")) {
		if filepath.IsAbs(d) && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	if len(dirs) > 0 {
		env = append(env, "PATH="+strings.Join(dirs, ":"))
	}
	return env
}

// systemdQuote quotes one word for ExecStart=/Environment= lines.
func systemdQuote(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	s = strings.ReplaceAll(s, "$", "$$")
	if s != "" && !strings.ContainsAny(s, " \t\"'\\;") {
		return s
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

var tmplFuncs = template.FuncMap{
	"exec": func(words ...string) string {
		q := make([]string, len(words))
		for i, w := range words {
			q[i] = systemdQuote(w)
		}
		return strings.Join(q, " ")
	},
	"envq": systemdQuote,
	"xml": func(s string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	},
}

// render validates unit data and expands the named template, leaving daemon
// ownership settings to the template without modifying the caller's environment.
func render(fsys fs.FS, name string, d UnitData) (string, error) {
	src, err := fs.ReadFile(fsys, name)
	if err != nil {
		return "", fmt.Errorf("unit template %s: %w", name, err)
	}
	for _, e := range d.Env {
		if strings.ContainsAny(e, "\n\r\x00") || !strings.Contains(e, "=") {
			return "", fmt.Errorf("invalid environment entry %q", e)
		}
	}
	// The templates own the service policy. Do not inherit a caller override
	// or emit duplicate launchd dictionary keys. Keep the caller slice intact.
	env := make([]string, 0, len(d.Env))
	for _, entry := range d.Env {
		key, _, _ := strings.Cut(entry, "=")
		if key != "RELAY_NO_PTYD" {
			env = append(env, entry)
		}
	}
	d.Env = env
	if strings.ContainsAny(d.Binary, "\n\r\x00") || !filepath.IsAbs(d.Binary) {
		return "", fmt.Errorf("binary path must be absolute: %q", d.Binary)
	}
	t, err := template.New(name).Funcs(tmplFuncs).Option("missingkey=error").Parse(string(src))
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", name, err)
	}
	var b bytes.Buffer
	if err := t.Execute(&b, d); err != nil {
		return "", fmt.Errorf("render %s: %w", name, err)
	}
	return b.String(), nil
}

// UnitFiles returns file name → rendered content for goos.
func UnitFiles(goos string, d UnitData) (map[string]string, []string, error) {
	var fsys fs.FS
	var names []string
	switch goos {
	case "linux":
		fsys, names = systemd.Units, systemd.Names
	case "darwin":
		fsys, names = launchd.Agents, launchd.Names
	default:
		return nil, nil, fmt.Errorf("no service templates for %s", goos)
	}
	out := map[string]string{}
	for _, n := range names {
		s, err := render(fsys, n, d)
		if err != nil {
			return nil, nil, err
		}
		out[n] = s
	}
	return out, names, nil
}

// DefaultUnitsDir is where the service manager looks for user units.
func DefaultUnitsDir(goos, home string, getenv func(string) string) string {
	if goos == "darwin" {
		return filepath.Join(home, "Library", "LaunchAgents")
	}
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user")
}

// WriteUnits renders and writes the units into dir (0644 files; units
// hold no secrets) and returns the written paths.
func WriteUnits(goos, dir string, d UnitData) ([]string, error) {
	files, names, err := UnitFiles(goos, d)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	var written []string
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p+".tmp", []byte(files[n]), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", p, err)
		}
		if err := os.Rename(p+".tmp", p); err != nil {
			return nil, fmt.Errorf("write %s: %w", p, err)
		}
		written = append(written, p)
	}
	return written, nil
}

// Manager controls the Relay services through systemd --user or launchd.
type Manager struct {
	Sys      System
	UnitsDir string
}

func (m Manager) unit(svc string) string {
	if m.Sys.GOOS() == "darwin" {
		if svc == SvcPtyd {
			return "dev.relay.ptyd"
		}
		return "dev.relay.serve"
	}
	if svc == SvcPtyd {
		return "relay-ptyd.service"
	}
	return "relay.service"
}

func (m Manager) domain() string { return "gui/" + strconv.Itoa(m.Sys.Getuid()) }

// Available reports whether the user service manager can be used.
func (m Manager) Available(ctx context.Context) bool {
	if m.Sys.GOOS() == "darwin" {
		_, err := m.Sys.LookPath("launchctl")
		return err == nil
	}
	if _, err := m.Sys.LookPath("systemctl"); err != nil {
		return false
	}
	_, err := m.Sys.Output(ctx, "systemctl", "--user", "show-environment")
	return err == nil
}

// Installed reports whether the unit file for svc exists in UnitsDir.
func (m Manager) Installed(svc string) bool {
	name := m.unit(svc)
	if m.Sys.GOOS() == "darwin" {
		name += ".plist"
	}
	_, err := os.Stat(filepath.Join(m.UnitsDir, name))
	return err == nil
}

// EnableNow (re)loads the units and starts ptyd, then the server.
func (m Manager) EnableNow(ctx context.Context) error {
	if m.Sys.GOOS() == "darwin" {
		for _, svc := range []string{SvcPtyd, SvcServe} {
			label := m.unit(svc)
			if svc == SvcPtyd {
				// A loaded daemon keeps its PTYs and running binary. Updated
				// plist settings take effect on its next explicit reload.
				if _, err := m.Sys.Output(ctx, "launchctl", "print", m.domain()+"/"+label); err == nil {
					// Without -k, kickstart starts an idle loaded job without
					// killing an instance that is already running.
					if out, err := m.Sys.Output(ctx, "launchctl", "kickstart", m.domain()+"/"+label); err != nil {
						return fmt.Errorf("launchctl kickstart %s: %v: %s", label, err, out)
					}
					continue
				}
			} else {
				// Reload serve's plist so regenerated ownership policy applies.
				_, _ = m.Sys.Output(ctx, "launchctl", "bootout", m.domain()+"/"+label)
			}
			if out, err := m.Sys.Output(ctx, "launchctl", "bootstrap", m.domain(), filepath.Join(m.UnitsDir, label+".plist")); err != nil {
				return fmt.Errorf("launchctl bootstrap %s: %v: %s", label, err, out)
			}
		}
		return nil
	}
	if out, err := m.Sys.Output(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %v: %s", err, out)
	}
	if out, err := m.Sys.Output(ctx, "systemctl", "--user", "enable", "--now", m.unit(SvcPtyd), m.unit(SvcServe)); err != nil {
		return fmt.Errorf("systemctl enable --now: %v: %s", err, out)
	}
	// enable --now does not restart a unit that was already running with
	// an older binary or config.
	if out, err := m.Sys.Output(ctx, "systemctl", "--user", "restart", m.unit(SvcServe)); err != nil {
		return fmt.Errorf("systemctl restart relay: %v: %s", err, out)
	}
	return nil
}

// Restart restarts one service.
func (m Manager) Restart(ctx context.Context, svc string) error {
	var out string
	var err error
	if m.Sys.GOOS() == "darwin" {
		out, err = m.Sys.Output(ctx, "launchctl", "kickstart", "-k", m.domain()+"/"+m.unit(svc))
	} else {
		out, err = m.Sys.Output(ctx, "systemctl", "--user", "restart", m.unit(svc))
	}
	if err != nil {
		return fmt.Errorf("restart %s: %v: %s", m.unit(svc), err, out)
	}
	return nil
}

// State returns a short state for svc: active, inactive, failed, … or
// "not installed".
func (m Manager) State(ctx context.Context, svc string) string {
	if !m.Installed(svc) {
		return "not installed"
	}
	if m.Sys.GOOS() == "darwin" {
		out, err := m.Sys.Output(ctx, "launchctl", "print", m.domain()+"/"+m.unit(svc))
		if err != nil {
			return "not loaded"
		}
		for _, ln := range strings.Split(out, "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(ln), "="); ok && strings.TrimSpace(k) == "state" {
				if s := strings.TrimSpace(v); s == "running" {
					return "active"
				} else {
					return s
				}
			}
		}
		return "loaded"
	}
	out, _ := m.Sys.Output(ctx, "systemctl", "--user", "is-active", m.unit(svc))
	if out == "" {
		return "unknown"
	}
	return strings.Fields(out)[0]
}

// Enabled reports whether svc starts at login/boot.
func (m Manager) Enabled(ctx context.Context, svc string) bool {
	if m.Sys.GOOS() == "darwin" {
		return m.Installed(svc)
	}
	out, err := m.Sys.Output(ctx, "systemctl", "--user", "is-enabled", m.unit(svc))
	return err == nil && strings.HasPrefix(out, "enabled")
}

// Remove stops, disables and deletes the units Relay rendered. Files
// without UnitMarker are left alone and reported.
func (m Manager) Remove(ctx context.Context) (removed []string, err error) {
	darwin := m.Sys.GOOS() == "darwin"
	var errs []error
	for _, svc := range []string{SvcServe, SvcPtyd} {
		if darwin {
			_, _ = m.Sys.Output(ctx, "launchctl", "bootout", m.domain()+"/"+m.unit(svc))
		} else {
			_, _ = m.Sys.Output(ctx, "systemctl", "--user", "disable", "--now", m.unit(svc))
		}
		name := m.unit(svc)
		if darwin {
			name += ".plist"
		}
		p := filepath.Join(m.UnitsDir, name)
		b, rerr := os.ReadFile(p)
		if errors.Is(rerr, fs.ErrNotExist) {
			continue
		}
		if rerr != nil {
			errs = append(errs, rerr)
			continue
		}
		if !bytes.Contains(b, []byte(UnitMarker)) {
			errs = append(errs, fmt.Errorf("%s was not written by relay setup; left in place", p))
			continue
		}
		if err := os.Remove(p); err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, p)
	}
	if !darwin {
		_, _ = m.Sys.Output(ctx, "systemctl", "--user", "daemon-reload")
		_, _ = m.Sys.Output(ctx, "systemctl", "--user", "reset-failed", m.unit(SvcServe), m.unit(SvcPtyd))
	}
	return removed, errors.Join(errs...)
}

// Linger reports whether the user's services keep running after logout.
func Linger(ctx context.Context, sys System) (bool, error) {
	out, err := sys.Output(ctx, "loginctl", "show-user", sys.Username(), "--property=Linger")
	if err != nil {
		return false, fmt.Errorf("loginctl: %v: %s", err, out)
	}
	return strings.TrimSpace(out) == "Linger=yes", nil
}
