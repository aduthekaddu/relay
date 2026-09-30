package api

// ToolboxJob is the payload of EvToolboxJob: the state of one visible
// install running in a toolbox terminal.
type ToolboxJob struct {
	Tool       string `json:"tool"`
	TerminalID string `json:"terminalId"`
	State      string `json:"state"` // running | done | failed
	ExitCode   *int   `json:"exitCode,omitempty"`
}

// Toolbox job states.
const (
	ToolboxJobRunning = "running"
	ToolboxJobDone    = "done"
	ToolboxJobFailed  = "failed"
)
