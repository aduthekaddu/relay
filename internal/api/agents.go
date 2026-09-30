package api

import (
	"encoding/json"
	"time"
)

// AgentHookRequest is sent by `relay hook <agent> <event>` (via the local
// control socket) to POST /api/v1/agents/hook. Payload is the agent's own
// hook JSON, passed through verbatim (≤ 1 MiB).
type AgentHookRequest struct {
	Agent     string          `json:"agent"`
	Event     string          `json:"event"`               // adapter specific: notification | stop | notify | idle ...
	SessionID string          `json:"sessionId,omitempty"` // RELAY_SESSION of the calling terminal
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// AgentHookResult is the response to an AgentHookRequest.
type AgentHookResult struct {
	Action     string `json:"action"`               // attention | done | ignored
	TerminalID string `json:"terminalId,omitempty"` // terminal whose state changed
}

// ReindexResponse acknowledges POST /api/v1/agents/reindex (202).
type ReindexResponse struct {
	Started bool `json:"started"`
}

// PinWorkspaceRequest is the body of POST /api/v1/workspaces/pin.
type PinWorkspaceRequest struct {
	Path   string `json:"path"`
	Pinned bool   `json:"pinned"`
}

// GitTaskResponse is returned (202) by push/pull: the task terminal that
// runs the command visibly.
type GitTaskResponse struct {
	Terminal *TerminalSession `json:"terminal,omitempty"`
}

// CwdUsage summarises indexed agent sessions per working directory. It is
// exchanged between internal/agents and internal/workspaces (workspace
// discovery and the Workspace.Agents count); it is not part of the HTTP
// contract.
type CwdUsage struct {
	Path       string    `json:"path"`
	Sessions   int       `json:"sessions"`
	LastUsedAt time.Time `json:"lastUsedAt"`
}
