package info

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

func (s *Service) terminalDefaults(ctx context.Context) (*api.TerminalDefaults, string) {
	if s.d.Pty == nil {
		return nil, "unavailable"
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	defaults, err := s.d.Pty.Defaults(ctx)
	if errors.Is(err, ptyclient.ErrNotFound) {
		return nil, "restart-required"
	}
	if err != nil {
		return nil, "unavailable"
	}
	if defaults.Source != "file" {
		return &defaults.TerminalDefaults, "restart-required"
	}
	sourceID := config.SettingsSourceID(s.d.Paths)
	if sourceID == "" || defaults.ConfigID != sourceID {
		return &defaults.TerminalDefaults, "different-config"
	}
	return &defaults.TerminalDefaults, "next-session"
}

func (s *Service) settingsState(ctx context.Context, raw, runtime *config.Config) api.SettingsState {
	terminal, status := s.terminalDefaults(ctx)
	saved := config.Normalized(raw, s.d.Paths.Home)
	policy := config.PinnedWorkspacePolicy(runtime, s.d.Paths.Home)
	apply := map[string]string{
		"workspaceRoots": "next-request", "defaultShell": status,
		"defaultCwd": status, "recordAgents": status,
		"claudeQuota": "next-request", "idleMinutes": "next-idle-check",
	}
	// Manual file edits do not hot-reload serve. Report the remaining restart
	// rather than implying that a subsequent consumer request will adopt them.
	savedPolicy := config.WorkspacePolicy(saved, s.d.Paths.Home)
	if !slices.Equal(savedPolicy.Roots, policy.Roots) {
		apply["workspaceRoots"] = "restart-required"
	}
	if saved.Usage.ClaudeQuota != runtime.Usage.ClaudeQuota {
		apply["claudeQuota"] = "restart-required"
	}
	if saved.Code.IdleStop != runtime.Code.IdleStop || saved.Desktop.IdleStop != runtime.Desktop.IdleStop {
		apply["idleMinutes"] = "restart-required"
	}
	return api.SettingsState{
		Settings: settingsFrom(saved), RecordingMode: raw.Terminal.Record,
		Effective: api.SettingsEffective{
			WorkspaceRoots: policy.Roots, ClaudeQuota: runtime.Usage.ClaudeQuota,
			CodeIdleStop:    runtime.Code.IdleStop.Duration.String(),
			DesktopIdleStop: runtime.Desktop.IdleStop.Duration.String(),
			Terminal:        terminal, TerminalStatus: status,
		}, Apply: apply,
	}
}
