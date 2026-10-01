package api

// SettingsState keeps the six editable saved fields flat for existing clients.
// Effective and Apply describe consumers, rather than promising adoption on save.
type SettingsState struct {
	Settings
	RecordingMode string            `json:"recordingMode"`
	Effective     SettingsEffective `json:"effective"`
	Apply         map[string]string `json:"apply"`
}

type SettingsEffective struct {
	WorkspaceRoots  []string          `json:"workspaceRoots"`
	ClaudeQuota     bool              `json:"claudeQuota"`
	CodeIdleStop    string            `json:"codeIdleStop"`
	DesktopIdleStop string            `json:"desktopIdleStop"`
	Terminal        *TerminalDefaults `json:"terminal"`
	// next-session | restart-required | different-config | unavailable
	TerminalStatus string `json:"terminalStatus"`
}

// TerminalDefaults describes the defaults for future ptyd sessions. It does
// not describe already-running sessions, whose state is in TerminalSession.
type TerminalDefaults struct {
	DefaultShell  string `json:"defaultShell"`
	DefaultCwd    string `json:"defaultCwd"`
	RecordingMode string `json:"recordingMode"`
	// file = reload before each creation; startup = construction snapshot
	Source string `json:"source"`
}
