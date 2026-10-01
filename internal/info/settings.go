package info

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/server"
)

// Settings limits.
const (
	maxWorkspaceRoots = 32
	maxPathLen        = 4096
	maxIdleMinutes    = 7 * 24 * 60 // one week
	maxSettingsBody   = 64 << 10
)

// settingsPatch is Partial<api.Settings>: nil fields are left unchanged.
type settingsPatch struct {
	WorkspaceRoots *[]string `json:"workspaceRoots"`
	DefaultShell   *string   `json:"defaultShell"`
	DefaultCwd     *string   `json:"defaultCwd"`
	RecordAgents   *bool     `json:"recordAgents"`
	ClaudeQuota    *bool     `json:"claudeQuota"`
	IdleMinutes    *int      `json:"idleMinutes"`
}

func (s *Service) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	var raw, runtime *config.Config
	err := s.d.Settings.Read(func(c *config.Config) error {
		var err error
		raw, err = loadSaved(s.d.Paths, false)
		runtime = c
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "config_read", "Could not read relay.toml.")
		return
	}
	httpx.OK(w, s.settingsState(r.Context(), raw, runtime))
}

func (s *Service) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	var p settingsPatch
	if err := httpx.DecodeLimit(r, &p, maxSettingsBody); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.validatePatch(&p); err != nil {
		httpx.Fail(w, httpx.BadRequest(err.Error()))
		return
	}
	var raw, runtime *config.Config
	err := s.d.Settings.Update(func(next *config.Config) error {
		var err error
		raw, err = loadSaved(s.d.Paths, true)
		if err != nil {
			var strict *toml.StrictMissingError
			if errors.As(err, &strict) {
				return httpx.Conflict("relay.toml contains unknown keys. Preserve or remove them before saving settings.")
			}
			return &httpx.Err{Status: 500, Code: "config_read", Message: "Could not read relay.toml."}
		}
		// Keep file values unexpanded and independent of process environment.
		applyPatch(raw, &p, false, s.d.Paths.Home)
		applyPatch(next, &p, true, s.d.Paths.Home)
		if err := config.Save(s.d.Paths, raw); err != nil {
			return &httpx.Err{Status: 500, Code: "config_save", Message: "Could not save relay.toml."}
		}
		runtime = config.Clone(next)
		return nil
	})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	s.audit(r, &p)
	httpx.OK(w, s.settingsState(r.Context(), raw, runtime))
}

// audit publishes a settings change on the backend bus (recorded by auth).
func (s *Service) audit(r *http.Request, p *settingsPatch) {
	if s.d.Bus == nil {
		return
	}
	var fields []string
	add := func(set bool, name string) {
		if set {
			fields = append(fields, name)
		}
	}
	add(p.WorkspaceRoots != nil, "workspaceRoots")
	add(p.DefaultShell != nil, "defaultShell")
	add(p.DefaultCwd != nil, "defaultCwd")
	add(p.RecordAgents != nil, "recordAgents")
	add(p.ClaudeQuota != nil, "claudeQuota")
	add(p.IdleMinutes != nil, "idleMinutes")
	if len(fields) == 0 {
		return
	}
	actor := "unknown"
	if pr := server.PrincipalFrom(r.Context()); pr != nil {
		actor = pr.User
	}
	s.d.Bus.Publish(core.BusAudit, core.AuditEvent{
		Event:  "settings.update",
		Actor:  actor,
		IP:     server.ClientIP(r),
		Detail: strings.Join(fields, ", "),
	})
}

// settingsFrom maps relay.toml fields to the API view.
func settingsFrom(c *config.Config) api.Settings {
	roots := append([]string{}, c.Agents.WorkspaceRoots...)
	rec := strings.ToLower(c.Terminal.Record)
	return api.Settings{
		WorkspaceRoots: roots,
		DefaultShell:   c.Terminal.Shell,
		DefaultCwd:     c.Terminal.DefaultCwd,
		RecordAgents:   rec == "agents" || rec == "all",
		ClaudeQuota:    c.Usage.ClaudeQuota,
		IdleMinutes:    int(c.Desktop.IdleStop.Duration / time.Minute),
	}
}

// applyPatch writes p into c. expand controls ~ expansion (the live config
// is normalised; the on-disk one is kept as the user wrote it).
func applyPatch(c *config.Config, p *settingsPatch, expand bool, home string) {
	path := func(v string) string {
		if expand {
			expanded := expandHome(v, home)
			if real, err := filepath.EvalSymlinks(expanded); err == nil {
				return real
			}
			return expanded
		}
		return v
	}
	if p.WorkspaceRoots != nil {
		roots := make([]string, 0, len(*p.WorkspaceRoots))
		for _, r := range *p.WorkspaceRoots {
			roots = append(roots, path(r))
		}
		c.Agents.WorkspaceRoots = roots
	}
	if p.DefaultShell != nil {
		c.Terminal.Shell = *p.DefaultShell
	}
	if p.DefaultCwd != nil {
		c.Terminal.DefaultCwd = path(*p.DefaultCwd)
	}
	if p.RecordAgents != nil {
		switch {
		case !*p.RecordAgents:
			c.Terminal.Record = "off"
		case strings.ToLower(c.Terminal.Record) != "all":
			c.Terminal.Record = "agents"
		}
	}
	if p.ClaudeQuota != nil {
		c.Usage.ClaudeQuota = *p.ClaudeQuota
	}
	if p.IdleMinutes != nil {
		d := config.Duration{Duration: time.Duration(*p.IdleMinutes) * time.Minute}
		c.Desktop.IdleStop = d
		c.Code.IdleStop = d
	}
}

// validatePatch normalises and checks every field that is present.
func (s *Service) validatePatch(p *settingsPatch) error {
	home := s.d.Paths.Home
	if p.WorkspaceRoots != nil {
		in := *p.WorkspaceRoots
		if len(in) > maxWorkspaceRoots {
			return fmt.Errorf("at most %d workspace roots", maxWorkspaceRoots)
		}
		seen := map[string]bool{}
		out := make([]string, 0, len(in))
		for _, r := range in {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			r = cleanPath(r)
			if err := checkDirPath(r, home); err != nil {
				return fmt.Errorf("workspace root %q: %w", r, err)
			}
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
		p.WorkspaceRoots = &out
	}
	if p.DefaultShell != nil {
		v := strings.TrimSpace(*p.DefaultShell)
		if v != "" {
			if !filepath.IsAbs(v) || len(v) > maxPathLen || strings.ContainsAny(v, "\x00\n") {
				return errors.New("default shell must be an absolute path")
			}
			if !isExecutable(v) {
				return fmt.Errorf("default shell %q is not an executable file", v)
			}
		}
		p.DefaultShell = &v
	}
	if p.DefaultCwd != nil {
		v := strings.TrimSpace(*p.DefaultCwd)
		if v == "" {
			v = "~"
		}
		v = cleanPath(v)
		if err := checkDirPath(v, home); err != nil {
			return fmt.Errorf("default folder: %w", err)
		}
		p.DefaultCwd = &v
	}
	if p.IdleMinutes != nil && (*p.IdleMinutes < 0 || *p.IdleMinutes > maxIdleMinutes) {
		return fmt.Errorf("idle minutes must be between 0 and %d", maxIdleMinutes)
	}
	return nil
}

// checkDirPath accepts absolute or ~-relative existing directories.
func checkDirPath(p, home string) error {
	if len(p) > maxPathLen || strings.ContainsAny(p, "\x00\n") {
		return errors.New("invalid path")
	}
	if p != "~" && !strings.HasPrefix(p, "~/") && !filepath.IsAbs(p) {
		return errors.New("use an absolute path or one starting with ~/")
	}
	st, err := os.Stat(expandHome(p, home))
	if err != nil || !st.IsDir() {
		return errors.New("folder does not exist")
	}
	return nil
}

// cleanPath tidies a path while keeping a leading "~/".
func cleanPath(p string) string {
	if p == "~" {
		return p
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		c := filepath.Clean("/" + rest)
		if c == "/" {
			return "~"
		}
		return "~" + c
	}
	return filepath.Clean(p)
}

// loadSaved reads relay.toml over the defaults without env overrides or
// normalisation.
func loadSaved(p config.Paths, strict bool) (*config.Config, error) {
	cfg := config.Defaults()
	b, err := os.ReadFile(p.ConfigFile)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p.ConfigFile, err)
	}
	decoder := toml.NewDecoder(bytes.NewReader(b))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p.ConfigFile, err)
	}
	return cfg, nil
}
