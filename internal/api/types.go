// Package api holds the JSON contract between the Relay server and its
// clients (the web app, the CLI and third-party automation).
//
// Every type here is serialised with encoding/json and mirrored by hand in
// web/src/api/types.ts. When you change a struct, change the TypeScript
// twin in the same commit; `make check-types` diffs the field names.
//
// Conventions:
//   - JSON field names are camelCase.
//   - Timestamps are time.Time (RFC 3339 in JSON). Zero values are omitted.
//   - Optional fields use pointers or `omitempty`.
//   - IDs are opaque strings. Never parse them on the client.
package api

import "time"

// ---------------------------------------------------------------------------
// Errors

// ErrorBody is the envelope for every non-2xx JSON response:
//
//	{"error": {"code": "not_found", "message": "session not found"}}
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code    string `json:"code"`              // machine readable: bad_request, unauthorized, forbidden, not_found, conflict, rate_limited, internal, unavailable
	Message string `json:"message"`           // human readable, safe to show
	Field   string `json:"field,omitempty"`   // offending input field, if any
	RetryIn int    `json:"retryIn,omitempty"` // seconds, for rate_limited
}

// Page wraps a cursor-paginated list.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
	Total      int    `json:"total,omitempty"`
}

// ---------------------------------------------------------------------------
// Info / health

type Info struct {
	Version   string    `json:"version"`
	Commit    string    `json:"commit,omitempty"`
	Hostname  string    `json:"hostname"`
	OS        string    `json:"os"`
	Arch      string    `json:"arch"`
	User      string    `json:"user"`
	Home      string    `json:"home"`
	StartedAt time.Time `json:"startedAt"`
	// PublicURL is the canonical browser origin, e.g. https://dev.example.com
	PublicURL    string       `json:"publicUrl"`
	Features     Features     `json:"features"`
	Capabilities Capabilities `json:"capabilities"`
}

type Features struct {
	Passkeys     bool   `json:"passkeys"`     // WebAuthn usable at this origin
	Push         bool   `json:"push"`         // Web Push configured (VAPID keys)
	Desktop      bool   `json:"desktop"`      // remote desktop available
	Code         bool   `json:"code"`         // browser IDE available
	PreviewsMode string `json:"previewsMode"` // "subdomain" | "path" | "off"
	PreviewsHost string `json:"previewsHost"` // base host for subdomain previews, e.g. dev.example.com
	Recording    bool   `json:"recording"`    // terminal recording enabled
	Ripgrep      bool   `json:"ripgrep"`      // content search available
}

// ---------------------------------------------------------------------------
// Auth

type AuthState struct {
	Authenticated bool        `json:"authenticated"`
	User          string      `json:"user,omitempty"`
	SetupRequired bool        `json:"setupRequired"` // no password set yet (first run)
	Methods       AuthMethods `json:"methods"`
	SessionID     string      `json:"sessionId,omitempty"`
}

type AuthMethods struct {
	Password bool `json:"password"`
	Passkey  bool `json:"passkey"` // at least one passkey registered and WebAuthn usable
	TOTP     bool `json:"totp"`    // second factor required after password
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TOTP     string `json:"totp,omitempty"`
	Remember bool   `json:"remember"` // long-lived session (30d) vs 12h
}

type LoginResponse struct {
	OK           bool `json:"ok"`
	NeedTOTP     bool `json:"needTotp,omitempty"` // password ok, send again with totp
	SetupPasskey bool `json:"setupPasskey,omitempty"`
}

type ChangePasswordRequest struct {
	Current string `json:"current"`
	Next    string `json:"next"`
}

type Passkey struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt,omitempty"`
}

type TOTPSetup struct {
	Secret     string `json:"secret"`
	OtpauthURL string `json:"otpauthUrl"`
}

type DeviceSession struct {
	ID         string    `json:"id"`
	Current    bool      `json:"current"`
	Device     string    `json:"device"` // "iPhone", "Mac", "Windows PC"...
	Browser    string    `json:"browser"`
	OS         string    `json:"os"`
	IP         string    `json:"ip"`
	Method     string    `json:"method"` // "password" | "passkey"
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

type APIToken struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Prefix     string    `json:"prefix"` // first chars, e.g. "rly_3kf9"
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt,omitempty"`
}

type CreatedToken struct {
	APIToken
	Token string `json:"token"` // shown exactly once
}

type AuditEntry struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	Event  string    `json:"event"` // login.ok, login.fail, logout, passkey.add, token.create, session.revoke, file.delete, ...
	Actor  string    `json:"actor"`
	IP     string    `json:"ip,omitempty"`
	Device string    `json:"device,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

// ---------------------------------------------------------------------------
// Terminal sessions (owned by ptyd, surfaced by the terminal package)

type TerminalKind string

const (
	KindShell   TerminalKind = "shell"
	KindAgent   TerminalKind = "agent"
	KindTask    TerminalKind = "task"    // one-off command (script command, schedule run)
	KindTmux    TerminalKind = "tmux"    // attached existing tmux session
	KindToolbox TerminalKind = "toolbox" // install recipe
)

// Activity is the derived state of a live session.
type Activity string

const (
	ActivityWorking Activity = "working" // output streamed recently
	ActivityIdle    Activity = "idle"    // quiet, nothing asked of the user
	ActivityWaiting Activity = "waiting" // needs the user (hook, OSC 9/777, bell, prompt heuristic)
	ActivityExited  Activity = "exited"
)

type TerminalSession struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Title          string            `json:"title,omitempty"` // latest OSC 0/2 title
	Kind           TerminalKind      `json:"kind"`
	Agent          string            `json:"agent,omitempty"`          // adapter id when kind=agent
	AgentSessionID string            `json:"agentSessionId,omitempty"` // native agent session id when known
	Command        []string          `json:"command"`
	Cwd            string            `json:"cwd"`
	CurrentCwd     string            `json:"currentCwd,omitempty"` // from OSC 7
	Workspace      string            `json:"workspace,omitempty"`  // workspace root path
	Pid            int               `json:"pid,omitempty"`
	Cols           int               `json:"cols"`
	Rows           int               `json:"rows"`
	Clients        int               `json:"clients"`
	Activity       Activity          `json:"activity"`
	Attention      *Attention        `json:"attention,omitempty"`
	ExitCode       *int              `json:"exitCode,omitempty"`
	Recording      bool              `json:"recording"`
	Pinned         bool              `json:"pinned"`
	Meta           map[string]string `json:"meta,omitempty"`
	CreatedAt      time.Time         `json:"createdAt"`
	LastOutputAt   time.Time         `json:"lastOutputAt,omitempty"`
	LastInputAt    time.Time         `json:"lastInputAt,omitempty"`
	ExitedAt       time.Time         `json:"exitedAt,omitempty"`
	Preview        string            `json:"preview,omitempty"` // last non-empty screen lines, plain text
}

type Attention struct {
	Reason  string    `json:"reason"` // "hook" | "osc" | "bell" | "prompt" | "idle"
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
}

type CreateTerminalRequest struct {
	Name    string            `json:"name,omitempty"`
	Command []string          `json:"command,omitempty"` // default: user's login shell
	Cwd     string            `json:"cwd,omitempty"`     // default: home; "~" expanded
	Env     map[string]string `json:"env,omitempty"`
	Kind    TerminalKind      `json:"kind,omitempty"`
	Agent   string            `json:"agent,omitempty"`
	Cols    int               `json:"cols,omitempty"`
	Rows    int               `json:"rows,omitempty"`
	Record  *bool             `json:"record,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

type UpdateTerminalRequest struct {
	Name   *string `json:"name,omitempty"`
	Pinned *bool   `json:"pinned,omitempty"`
}

type TerminalInput struct {
	Data  string `json:"data"`            // UTF-8 text to write to the pty
	Paste bool   `json:"paste,omitempty"` // wrap in bracketed-paste markers when the app enabled them
}

type TerminalSnapshot struct {
	Text string `json:"text"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// Terminal WebSocket protocol (GET /api/v1/terminals/{id}/attach):
//
//	server -> client  binary frame : raw pty output bytes
//	server -> client  text frame   : TermServerMsg JSON
//	client -> server  binary frame : raw input bytes
//	client -> server  text frame   : TermClientMsg JSON
type TermServerMsg struct {
	T        string           `json:"t"` // hello | replay-begin | replay-end | exit | title | cwd | resize | notify | bell | attention | clients | error | pong
	Session  *TerminalSession `json:"session,omitempty"`
	Cols     int              `json:"cols,omitempty"`
	Rows     int              `json:"rows,omitempty"`
	Code     *int             `json:"code,omitempty"`
	Title    string           `json:"title,omitempty"`
	Cwd      string           `json:"cwd,omitempty"`
	Message  string           `json:"message,omitempty"`
	Clients  int              `json:"clients,omitempty"`
	ReadOnly bool             `json:"readOnly,omitempty"`
}

type TermClientMsg struct {
	T       string `json:"t"` // resize | focus | ping | ack
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Visible *bool  `json:"visible,omitempty"` // focus: tab visible/foreground
	Bytes   int64  `json:"bytes,omitempty"`   // ack: bytes processed (flow control)
}

// ---------------------------------------------------------------------------
// Uploads (chunked, resumable)

type StartUploadRequest struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Dir  string `json:"dir,omitempty"` // default: ~/.relay/uploads/YYYY-MM-DD (for terminal attachments)
	Mime string `json:"mime,omitempty"`
}

type Upload struct {
	ID        string `json:"id"`
	ChunkSize int    `json:"chunkSize"`
	Received  int64  `json:"received"`
	Size      int64  `json:"size"`
}

type UploadResult struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// ---------------------------------------------------------------------------
// Agents

type AgentInfo struct {
	ID           string            `json:"id"`   // "claude", "codex", "gemini", "opencode", "kiro", "cursor", "grok", "pi", "hermes", "amp", "copilot", "aider", "qwen", "crush"
	Name         string            `json:"name"` // "Claude Code"
	Vendor       string            `json:"vendor"`
	Installed    bool              `json:"installed"`
	Version      string            `json:"version,omitempty"`
	Binary       string            `json:"binary,omitempty"`
	Color        string            `json:"color"` // brand accent, hex
	Capabilities AgentCapabilities `json:"capabilities"`
	Hooks        *HookStatus       `json:"hooks,omitempty"`
	InstallHint  string            `json:"installHint,omitempty"` // toolbox recipe id
	Sessions     int               `json:"sessions"`              // indexed history count
}

type AgentCapabilities struct {
	Resume    bool `json:"resume"`
	Fork      bool `json:"fork"`
	Headless  bool `json:"headless"`  // one-shot prompt mode (claude -p, codex exec)
	Hooks     bool `json:"hooks"`     // relay can install attention hooks
	History   bool `json:"history"`   // transcripts readable from disk
	Usage     bool `json:"usage"`     // token usage in transcripts
	Quota     bool `json:"quota"`     // plan limits readable
	Worktrees bool `json:"worktrees"` // launch in fresh git worktree supported by relay
	Prompt    bool `json:"prompt"`    // initial prompt as argument
}

type HookStatus struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type AgentSessionStatus string

const (
	AgentLive    AgentSessionStatus = "live"    // paired with a running terminal
	AgentHistory AgentSessionStatus = "history" // transcript only
)

type AgentSession struct {
	ID         string             `json:"id"` // "<agent>:<nativeId>"
	Agent      string             `json:"agent"`
	NativeID   string             `json:"nativeId"`
	Title      string             `json:"title"`
	Summary    string             `json:"summary,omitempty"`
	Cwd        string             `json:"cwd"`
	Workspace  string             `json:"workspace,omitempty"`
	GitBranch  string             `json:"gitBranch,omitempty"`
	Model      string             `json:"model,omitempty"`
	StartedAt  time.Time          `json:"startedAt"`
	UpdatedAt  time.Time          `json:"updatedAt"`
	Messages   int                `json:"messages"`
	Tokens     *TokenUsage        `json:"tokens,omitempty"`
	CostUSD    *float64           `json:"costUsd,omitempty"`
	Status     AgentSessionStatus `json:"status"`
	TerminalID string             `json:"terminalId,omitempty"` // when live
	Activity   Activity           `json:"activity,omitempty"`   // when live
	Resumable  bool               `json:"resumable"`
	Pinned     bool               `json:"pinned"`
	Archived   bool               `json:"archived"`
}

type TokenUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Reasoning  int64 `json:"reasoning,omitempty"`
}

type AgentMessage struct {
	ID     string      `json:"id"`
	Role   string      `json:"role"` // user | assistant | tool | system
	At     time.Time   `json:"at,omitempty"`
	Model  string      `json:"model,omitempty"`
	Parts  []Part      `json:"parts"`
	Tokens *TokenUsage `json:"tokens,omitempty"`
}

// Part is one block of a message.
type Part struct {
	Type     string `json:"type"` // text | thinking | tool_call | tool_result | image | diff
	Text     string `json:"text,omitempty"`
	Tool     string `json:"tool,omitempty"`     // tool_call / tool_result: tool name
	Input    string `json:"input,omitempty"`    // tool_call: JSON or command text (truncated)
	Output   string `json:"output,omitempty"`   // tool_result: truncated output
	IsError  bool   `json:"isError,omitempty"`  // tool_result failed
	Path     string `json:"path,omitempty"`     // image / diff target
	MimeType string `json:"mimeType,omitempty"` // image
}

type Transcript struct {
	Session  AgentSession   `json:"session"`
	Messages []AgentMessage `json:"messages"`
	HasMore  bool           `json:"hasMore"` // older messages exist (use ?before=<id>)
}

type LaunchAgentRequest struct {
	Agent    string          `json:"agent"`
	Cwd      string          `json:"cwd"`
	Prompt   string          `json:"prompt,omitempty"`
	Model    string          `json:"model,omitempty"`
	Worktree *WorktreeOption `json:"worktree,omitempty"`
	Name     string          `json:"name,omitempty"`
	Cols     int             `json:"cols,omitempty"`
	Rows     int             `json:"rows,omitempty"`
}

type WorktreeOption struct {
	Branch string `json:"branch"`         // new branch name
	Base   string `json:"base,omitempty"` // default: current HEAD
}

type ResumeAgentRequest struct {
	Fork bool `json:"fork,omitempty"`
	Cols int  `json:"cols,omitempty"`
	Rows int  `json:"rows,omitempty"`
}

type UpdateAgentSessionRequest struct {
	Title    *string `json:"title,omitempty"`
	Pinned   *bool   `json:"pinned,omitempty"`
	Archived *bool   `json:"archived,omitempty"`
}

type SearchHit struct {
	SessionID string    `json:"sessionId"`
	Agent     string    `json:"agent"`
	Title     string    `json:"title"`
	Cwd       string    `json:"cwd,omitempty"`
	Role      string    `json:"role"`
	Snippet   string    `json:"snippet"` // match wrapped in « »
	At        time.Time `json:"at"`
	MessageID string    `json:"messageId,omitempty"`
}

type UsageSummary struct {
	Range   string       `json:"range"` // today | 7d | 30d | all
	Totals  UsageTotals  `json:"totals"`
	ByAgent []UsageSlice `json:"byAgent"`
	ByModel []UsageSlice `json:"byModel"`
	Daily   []UsageDay   `json:"daily"`
	Updated time.Time    `json:"updated"`
}

type UsageTotals struct {
	CostUSD  float64    `json:"costUsd"`
	Tokens   TokenUsage `json:"tokens"`
	Sessions int        `json:"sessions"`
	Messages int        `json:"messages"`
}

type UsageSlice struct {
	Key       string  `json:"key"` // agent id or model id
	Label     string  `json:"label"`
	CostUSD   float64 `json:"costUsd"`
	Tokens    int64   `json:"tokens"`
	Sessions  int     `json:"sessions"`
	Estimated bool    `json:"estimated,omitempty"` // price guessed (unknown model)
}

type UsageDay struct {
	Date    string             `json:"date"` // YYYY-MM-DD, local time
	CostUSD float64            `json:"costUsd"`
	Tokens  int64              `json:"tokens"`
	ByAgent map[string]float64 `json:"byAgent"`
}

type Quota struct {
	Agent     string        `json:"agent"`
	Plan      string        `json:"plan,omitempty"`
	Windows   []QuotaWindow `json:"windows"`
	UpdatedAt time.Time     `json:"updatedAt"`
	Source    string        `json:"source"` // "transcript" | "api" | "statusline"
	Stale     bool          `json:"stale"`
}

type QuotaWindow struct {
	Label    string    `json:"label"` // "5h", "Weekly", "Opus weekly"
	UsedPct  float64   `json:"usedPct"`
	ResetsAt time.Time `json:"resetsAt,omitempty"`
}

// ---------------------------------------------------------------------------
// Workspaces & git

type Workspace struct {
	Path       string    `json:"path"`
	Name       string    `json:"name"`
	Git        *GitBrief `json:"git,omitempty"`
	Pinned     bool      `json:"pinned"`
	LastUsedAt time.Time `json:"lastUsedAt,omitempty"`
	Terminals  int       `json:"terminals"` // live sessions whose cwd is inside
	Agents     int       `json:"agents"`    // indexed agent sessions for this path
	Languages  []string  `json:"languages,omitempty"`
}

type GitBrief struct {
	Branch string    `json:"branch"`
	Dirty  int       `json:"dirty"` // changed files
	Ahead  int       `json:"ahead"`
	Behind int       `json:"behind"`
	Last   *Commit   `json:"last,omitempty"`
	Remote string    `json:"remote,omitempty"` // origin url (credentials stripped)
	At     time.Time `json:"at"`
}

type Commit struct {
	Hash    string    `json:"hash"`
	Short   string    `json:"short"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	At      time.Time `json:"at"`
}

type GitStatus struct {
	Path      string     `json:"path"`
	Root      string     `json:"root"`
	Branch    string     `json:"branch"`
	Upstream  string     `json:"upstream,omitempty"`
	Ahead     int        `json:"ahead"`
	Behind    int        `json:"behind"`
	Files     []GitFile  `json:"files"`
	Last      *Commit    `json:"last,omitempty"`
	Worktree  bool       `json:"worktree"` // path is a linked worktree
	Stashes   int        `json:"stashes"`
	Remotes   []string   `json:"remotes,omitempty"`
	Worktrees []Worktree `json:"worktrees,omitempty"`
}

type GitFile struct {
	Path     string `json:"path"`
	OrigPath string `json:"origPath,omitempty"` // renames
	Index    string `json:"index"`              // porcelain X
	Work     string `json:"work"`               // porcelain Y
	Staged   bool   `json:"staged"`
	Added    int    `json:"added"`
	Removed  int    `json:"removed"`
	Binary   bool   `json:"binary,omitempty"`
}

type GitDiff struct {
	Path      string `json:"path"`
	File      string `json:"file,omitempty"`
	Staged    bool   `json:"staged"`
	Diff      string `json:"diff"` // unified diff text
	Truncated bool   `json:"truncated,omitempty"`
}

type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
	Main   bool   `json:"main"`
}

type GitActionRequest struct {
	Path    string   `json:"path"`
	Files   []string `json:"files,omitempty"`
	Message string   `json:"message,omitempty"` // commit
	Branch  string   `json:"branch,omitempty"`  // worktree create
	Base    string   `json:"base,omitempty"`
}

// ---------------------------------------------------------------------------
// Files

type FileEntry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"` // absolute
	Type     string    `json:"type"` // file | dir | symlink | other
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"modTime"`
	Mode     string    `json:"mode"` // "-rw-r--r--"
	Mime     string    `json:"mime,omitempty"`
	Hidden   bool      `json:"hidden"`
	Target   string    `json:"target,omitempty"` // symlink target
	Children int       `json:"children,omitempty"`
	Git      string    `json:"git,omitempty"` // porcelain status letter when inside a repo
}

type DirListing struct {
	Path    string      `json:"path"`
	Parent  string      `json:"parent,omitempty"`
	Entries []FileEntry `json:"entries"`
	Total   int         `json:"total"`
	Offset  int         `json:"offset"`
	Hidden  int         `json:"hidden"` // number of hidden entries not shown
	GitRoot string      `json:"gitRoot,omitempty"`
}

type FileOpRequest struct {
	Paths []string `json:"paths,omitempty"`
	From  []string `json:"from,omitempty"`
	To    string   `json:"to,omitempty"`
	Path  string   `json:"path,omitempty"`
	Name  string   `json:"name,omitempty"`
	Trash *bool    `json:"trash,omitempty"` // delete: move to trash (default true)
}

type DiskUsage struct {
	Path     string           `json:"path"`
	Size     int64            `json:"size"`
	Files    int64            `json:"files"`
	Dirs     int64            `json:"dirs"`
	Children map[string]int64 `json:"children"`
	Complete bool             `json:"complete"`
	Pending  bool             `json:"pending"`
}

type FileSearchHit struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Preview string `json:"preview,omitempty"`
	Score   int    `json:"score,omitempty"`
}

// ---------------------------------------------------------------------------
// System

type Metrics struct {
	At     time.Time   `json:"at"`
	CPU    CPUStats    `json:"cpu"`
	Memory MemStats    `json:"memory"`
	Disks  []DiskStats `json:"disks"`
	Net    NetStats    `json:"net"`
	GPU    []GPUStats  `json:"gpu,omitempty"`
	Uptime int64       `json:"uptimeSec"`
	Load   [3]float64  `json:"load"`
	Procs  int         `json:"procs"`
	Host   HostInfo    `json:"host"`
}

type CPUStats struct {
	Percent float64   `json:"percent"`
	PerCore []float64 `json:"perCore"`
	Cores   int       `json:"cores"`
	Model   string    `json:"model,omitempty"`
	TempC   float64   `json:"tempC,omitempty"`
}

type MemStats struct {
	Total     uint64 `json:"total"`
	Used      uint64 `json:"used"`
	Available uint64 `json:"available"`
	Cached    uint64 `json:"cached"`
	SwapTotal uint64 `json:"swapTotal"`
	SwapUsed  uint64 `json:"swapUsed"`
}

type DiskStats struct {
	Mount    string  `json:"mount"`
	FS       string  `json:"fs"`
	Total    uint64  `json:"total"`
	Used     uint64  `json:"used"`
	Free     uint64  `json:"free"`
	ReadBps  float64 `json:"readBps"`
	WriteBps float64 `json:"writeBps"`
}

type NetStats struct {
	RxBps   float64 `json:"rxBps"`
	TxBps   float64 `json:"txBps"`
	RxTotal uint64  `json:"rxTotal"`
	TxTotal uint64  `json:"txTotal"`
}

type GPUStats struct {
	Name     string  `json:"name"`
	Util     float64 `json:"util"`
	MemUsed  uint64  `json:"memUsed"`
	MemTotal uint64  `json:"memTotal"`
	TempC    float64 `json:"tempC,omitempty"`
}

type HostInfo struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Kernel   string `json:"kernel"`
	Arch     string `json:"arch"`
	Virt     string `json:"virt,omitempty"`
}

type Process struct {
	PID       int       `json:"pid"`
	PPID      int       `json:"ppid"`
	Name      string    `json:"name"`
	Cmd       string    `json:"cmd"` // truncated command line
	User      string    `json:"user"`
	CPU       float64   `json:"cpu"` // percent of one core
	RSS       uint64    `json:"rss"`
	MemPct    float64   `json:"memPct"`
	State     string    `json:"state"`
	Threads   int       `json:"threads"`
	StartedAt time.Time `json:"startedAt"`
	Terminal  string    `json:"terminal,omitempty"` // relay terminal session id if descendant
	Protected bool      `json:"protected"`          // relay's own processes, pid 1
}

type SignalRequest struct {
	Signal string `json:"signal"` // TERM | KILL | INT | HUP | STOP | CONT
}

type Service struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Active      string    `json:"active"` // active | inactive | failed | activating
	Sub         string    `json:"sub"`
	Since       time.Time `json:"since,omitempty"`
	Restarts    int       `json:"restarts"`
	User        bool      `json:"user"`    // systemd --user unit
	Managed     bool      `json:"managed"` // relay-owned unit
}

type LogLine struct {
	At   time.Time `json:"at"`
	Unit string    `json:"unit,omitempty"`
	Prio int       `json:"prio"`
	Text string    `json:"text"`
}

// ---------------------------------------------------------------------------
// Previews (listening ports)

type Preview struct {
	Port        int       `json:"port"`
	Address     string    `json:"address"` // 127.0.0.1, 0.0.0.0, ::
	PID         int       `json:"pid,omitempty"`
	Process     string    `json:"process,omitempty"`
	Cwd         string    `json:"cwd,omitempty"`
	Workspace   string    `json:"workspace,omitempty"`
	Label       string    `json:"label,omitempty"` // user label or detected framework ("Vite", "Next.js")
	URL         string    `json:"url"`             // preview URL to open in the browser
	HTTP        bool      `json:"http"`            // responded to an HTTP probe
	Title       string    `json:"title,omitempty"` // <title> of the page when HTTP
	FirstSeenAt time.Time `json:"firstSeenAt"`
	Pinned      bool      `json:"pinned"`
	Hidden      bool      `json:"hidden"`
}

type UpdatePreviewRequest struct {
	Label  *string `json:"label,omitempty"`
	Pinned *bool   `json:"pinned,omitempty"`
	Hidden *bool   `json:"hidden,omitempty"`
}

// ---------------------------------------------------------------------------
// Apps (browser IDE, desktop, user-defined services)

type App struct {
	ID          string         `json:"id"` // "code", "desktop", or user-defined
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Kind        string         `json:"kind"`  // "code" | "desktop" | "web"
	State       string         `json:"state"` // running | starting | stopped | unavailable | error
	Installed   bool           `json:"installed"`
	URL         string         `json:"url,omitempty"`
	Icon        string         `json:"icon,omitempty"`
	Since       time.Time      `json:"since,omitempty"`
	Error       string         `json:"error,omitempty"`
	InstallHint string         `json:"installHint,omitempty"`
	Capability  *AppCapability `json:"capability,omitempty"`
}

type DesktopApp struct {
	ID      string `json:"id"` // chrome, blender, files, terminal...
	Name    string `json:"name"`
	Running bool   `json:"running"`
	Icon    string `json:"icon,omitempty"`
}

type DesktopState struct {
	State       string        `json:"state"` // running | stopped | starting | unavailable
	Display     string        `json:"display,omitempty"`
	Width       int           `json:"width"`
	Height      int           `json:"height"`
	Apps        []DesktopApp  `json:"apps"`
	Viewers     int           `json:"viewers"`
	Error       string        `json:"error,omitempty"`
	InstallHint string        `json:"installHint,omitempty"`
	Capability  AppCapability `json:"capability"`
}

// ---------------------------------------------------------------------------
// Notifications

type Notification struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"` // attention | done | exited | preview | security | system | schedule | custom
	Title     string    `json:"title"`
	Body      string    `json:"body,omitempty"`
	At        time.Time `json:"at"`
	Read      bool      `json:"read"`
	Link      string    `json:"link,omitempty"` // in-app path, e.g. /terminal/t_abc
	SessionID string    `json:"sessionId,omitempty"`
	Agent     string    `json:"agent,omitempty"`
	Severity  string    `json:"severity,omitempty"` // info | success | warning | danger
}

type NotifyRequest struct {
	Kind      string `json:"kind,omitempty"`
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	Link      string `json:"link,omitempty"`
	SessionID string `json:"sessionId,omitempty"` // RELAY_SESSION of the caller
	Agent     string `json:"agent,omitempty"`
	Severity  string `json:"severity,omitempty"`
}

type PushSubscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	Device string `json:"device,omitempty"`
}

type NotifySettings struct {
	Rules      map[string]bool `json:"rules"`                // kind -> enabled
	QuietStart string          `json:"quietStart,omitempty"` // "22:00"
	QuietEnd   string          `json:"quietEnd,omitempty"`
	NtfyURL    string          `json:"ntfyUrl,omitempty"`
	WebhookURL string          `json:"webhookUrl,omitempty"`
	Devices    int             `json:"devices"` // push subscriptions
	VAPIDKey   string          `json:"vapidKey,omitempty"`
}

// ---------------------------------------------------------------------------
// Clipboard, snippets, notes

type Clip struct {
	ID     string    `json:"id"`
	Text   string    `json:"text"`
	Source string    `json:"source"` // "terminal", "cli", "web", "osc52", "desktop"
	At     time.Time `json:"at"`
	Size   int       `json:"size"`
}

type Snippet struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Body      string    `json:"body"` // may contain {{variables}}
	Kind      string    `json:"kind"` // prompt | command
	Tags      []string  `json:"tags,omitempty"`
	Agent     string    `json:"agent,omitempty"` // preferred agent for prompts
	UpdatedAt time.Time `json:"updatedAt"`
	Uses      int       `json:"uses"`
}

type Note struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Text      string    `json:"text"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ---------------------------------------------------------------------------
// Schedules ("night shift")

type Schedule struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Cron      string       `json:"cron"` // 5-field cron or @daily etc.
	Timezone  string       `json:"timezone,omitempty"`
	Cwd       string       `json:"cwd"`
	Agent     string       `json:"agent,omitempty"`   // headless agent run
	Prompt    string       `json:"prompt,omitempty"`  // for agent runs
	Command   []string     `json:"command,omitempty"` // or a plain command
	Mode      string       `json:"mode"`              // headless | interactive
	Enabled   bool         `json:"enabled"`
	Notify    bool         `json:"notify"`
	NextRun   time.Time    `json:"nextRun,omitempty"`
	LastRun   *ScheduleRun `json:"lastRun,omitempty"`
	CreatedAt time.Time    `json:"createdAt"`
}

type ScheduleRun struct {
	ID         string    `json:"id"`
	ScheduleID string    `json:"scheduleId"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	Status     string    `json:"status"` // running | ok | failed | skipped
	ExitCode   *int      `json:"exitCode,omitempty"`
	TerminalID string    `json:"terminalId,omitempty"`
	Output     string    `json:"output,omitempty"` // tail of output
}

// ---------------------------------------------------------------------------
// Command center

type SearchResult struct {
	Scope    string            `json:"scope"` // terminals | agents | history | files | workspaces | previews | processes | snippets | notes | scripts
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Subtitle string            `json:"subtitle,omitempty"`
	Icon     string            `json:"icon,omitempty"` // agent id, file type, etc.
	Link     string            `json:"link,omitempty"` // in-app path to navigate to
	Score    float64           `json:"score"`
	At       time.Time         `json:"at,omitempty"`
	Meta     map[string]string `json:"meta,omitempty"`
}

type SearchResponse struct {
	Query   string         `json:"query"`
	Results []SearchResult `json:"results"`
	TookMs  int64          `json:"tookMs"`
}

// ScriptCommand is a user script in ~/.config/relay/commands with
// Raycast-style metadata comments (# @relay.title, @relay.mode, ...).
type ScriptCommand struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	Icon        string      `json:"icon,omitempty"`
	Mode        string      `json:"mode"` // inline | terminal | silent
	Args        []ScriptArg `json:"args,omitempty"`
	Path        string      `json:"path"`
	Cwd         string      `json:"cwd,omitempty"`
}

type ScriptArg struct {
	Name        string `json:"name"`
	Placeholder string `json:"placeholder,omitempty"`
	Optional    bool   `json:"optional,omitempty"`
}

type RunScriptRequest struct {
	Args []string `json:"args,omitempty"`
}

type RunScriptResult struct {
	TerminalID string `json:"terminalId,omitempty"`
	Output     string `json:"output,omitempty"`
	ExitCode   int    `json:"exitCode"`
}

type AskRequest struct {
	Agent  string `json:"agent,omitempty"` // default: first installed headless agent
	Prompt string `json:"prompt"`
	Cwd    string `json:"cwd,omitempty"`
}

// AskChunk is one streamed chunk (NDJSON) of a Quick AI answer.
type AskChunk struct {
	T    string `json:"t"` // text | done | error
	Text string `json:"text,omitempty"`
}

// ---------------------------------------------------------------------------
// Toolbox

type Tool struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Category     string   `json:"category"` // agents | runtimes | browsers | desktop | creative | cli | editors | mcp
	Description  string   `json:"description"`
	Homepage     string   `json:"homepage,omitempty"`
	Installed    bool     `json:"installed"`
	Version      string   `json:"version,omitempty"`
	Installable  bool     `json:"installable"`
	RequiresSudo bool     `json:"requiresSudo"`
	Size         string   `json:"size,omitempty"` // approximate download
	Tags         []string `json:"tags,omitempty"`
}

type MCPServer struct {
	ID          string            `json:"id"` // playwright, chrome-devtools, blender, context7, filesystem
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Command     []string          `json:"command"`
	Env         map[string]string `json:"env,omitempty"`
	Agents      map[string]bool   `json:"agents"` // agent id -> configured
}

type MCPApplyRequest struct {
	Server string   `json:"server"`
	Agents []string `json:"agents"`
	Remove bool     `json:"remove,omitempty"`
}

// ---------------------------------------------------------------------------
// Live events (GET /api/v1/events, WebSocket, text frames of Event JSON)

type Event struct {
	Type string    `json:"type"`
	At   time.Time `json:"at"`
	Data any       `json:"data,omitempty"`
}

// Event types. Data payload in parentheses.
const (
	EvHello            = "hello"             // (Info) first message after connect
	EvTerminalCreated  = "terminal.created"  // (TerminalSession)
	EvTerminalUpdated  = "terminal.updated"  // (TerminalSession) title/cwd/activity/clients/attention changed
	EvTerminalExited   = "terminal.exited"   // (TerminalSession)
	EvTerminalRemoved  = "terminal.removed"  // ({id})
	EvAgentsIndexed    = "agents.indexed"    // ({agent, sessions})
	EvAgentSession     = "agents.session"    // (AgentSession) new or updated
	EvNotification     = "notification"      // (Notification)
	EvNotificationRead = "notification.read" // ({ids})
	EvMetrics          = "metrics"           // (Metrics) only when subscribed to "metrics"
	EvPreviewsChanged  = "previews.changed"  // ([]Preview)
	EvClip             = "clip"              // (Clip)
	EvOpen             = "open"              // ({path, line?}) `relay open` from a terminal
	EvScheduleRun      = "schedule.run"      // (ScheduleRun)
	EvAppState         = "app.state"         // (App)
	EvDesktopState     = "desktop.state"     // (DesktopState)
	EvToolboxJob       = "toolbox.job"       // ({tool, terminalId, state})
	EvWorkspaceChanged = "workspace.changed" // ({path})
)

// ClientEvent is sent by the browser on the events socket.
type ClientEvent struct {
	Type    string   `json:"type"`              // subscribe | unsubscribe | visibility | ping
	Topics  []string `json:"topics,omitempty"`  // "metrics"
	Visible *bool    `json:"visible,omitempty"` // page visible
	Path    string   `json:"path,omitempty"`    // current route (for smart notification suppression)
}

// ---------------------------------------------------------------------------
// Settings (server-side, user editable subset of relay.toml)

type Settings struct {
	WorkspaceRoots []string `json:"workspaceRoots"`
	DefaultShell   string   `json:"defaultShell"`
	DefaultCwd     string   `json:"defaultCwd"`
	RecordAgents   bool     `json:"recordAgents"`
	ClaudeQuota    bool     `json:"claudeQuota"` // allow reading Claude plan usage via its OAuth token
	IdleMinutes    int      `json:"idleMinutes"` // Code/Desktop idle stop; user apps keep their own timeout
}
