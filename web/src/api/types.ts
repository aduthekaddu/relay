// Mirror of internal/api/types.go — keep in sync (same field names).
// Timestamps are ISO-8601 strings. Optional fields may be absent.

export interface ErrorBody { error: ErrorDetail }
export interface ErrorDetail { code: string; message: string; field?: string; retryIn?: number }
export interface Page<T> { items: T[]; nextCursor?: string; total?: number }

import type { AppCapability, Capabilities } from "./capabilities"

// ---------------------------------------------------------------- info
export interface Features {
  passkeys: boolean; push: boolean; desktop: boolean; code: boolean
  previewsMode: 'subdomain' | 'path' | 'off'; previewsHost: string
  recording: boolean; ripgrep: boolean
}
export interface Info {
  version: string; commit?: string; hostname: string; os: string; arch: string
  user: string; home: string; startedAt: string; publicUrl: string; features: Features; capabilities: Capabilities
}

// ---------------------------------------------------------------- auth
export interface AuthMethods { password: boolean; passkey: boolean; totp: boolean }
export interface AuthState {
  authenticated: boolean; user?: string; setupRequired: boolean
  methods: AuthMethods; sessionId?: string
}
export interface LoginRequest { username: string; password: string; totp?: string; remember: boolean }
export interface LoginResponse { ok: boolean; needTotp?: boolean; setupPasskey?: boolean }
export interface ChangePasswordRequest { current: string; next: string }
export interface Passkey { id: string; name: string; createdAt: string; lastUsedAt?: string }
export interface TOTPSetup { secret: string; otpauthUrl: string }
export interface DeviceSession {
  id: string; current: boolean; device: string; browser: string; os: string; ip: string
  method: 'password' | 'passkey'; createdAt: string; lastSeenAt: string; expiresAt: string
}
export interface APIToken { id: string; name: string; prefix: string; createdAt: string; lastUsedAt?: string }
export interface CreatedToken extends APIToken { token: string }
export interface AuditEntry { id: number; at: string; event: string; actor: string; ip?: string; device?: string; detail?: string }

// ---------------------------------------------------------------- terminals
export type TerminalKind = 'shell' | 'agent' | 'task' | 'tmux' | 'toolbox'
export type Activity = 'working' | 'idle' | 'waiting' | 'exited'
export interface Attention { reason: 'hook' | 'osc' | 'bell' | 'prompt' | 'idle'; message?: string; at: string }
export interface TerminalSession {
  id: string; name: string; title?: string; kind: TerminalKind
  agent?: string; agentSessionId?: string; command: string[]; cwd: string; currentCwd?: string
  workspace?: string; pid?: number; cols: number; rows: number; clients: number
  activity: Activity; attention?: Attention; exitCode?: number; recording: boolean; pinned: boolean
  meta?: Record<string, string>; createdAt: string; lastOutputAt?: string; lastInputAt?: string
  exitedAt?: string; preview?: string
}
export interface CreateTerminalRequest {
  name?: string; command?: string[]; cwd?: string; env?: Record<string, string>
  kind?: TerminalKind; agent?: string; cols?: number; rows?: number; record?: boolean
  meta?: Record<string, string>
}
export interface UpdateTerminalRequest { name?: string; pinned?: boolean }
export interface TerminalInput { data: string; paste?: boolean }
export interface TerminalSnapshot { text: string; cols: number; rows: number }
export interface TermServerMsg {
  t: 'hello' | 'replay-begin' | 'replay-end' | 'exit' | 'title' | 'cwd' | 'resize' | 'notify'
    | 'bell' | 'attention' | 'clients' | 'error' | 'pong'
  session?: TerminalSession; cols?: number; rows?: number; code?: number; title?: string
  cwd?: string; message?: string; clients?: number; readOnly?: boolean
}
export interface TermClientMsg { t: 'resize' | 'focus' | 'ping' | 'ack'; cols?: number; rows?: number; visible?: boolean; bytes?: number }

// ---------------------------------------------------------------- uploads
export interface StartUploadRequest { name: string; size: number; dir?: string; mime?: string }
export interface Upload { id: string; chunkSize: number; received: number; size: number }
export interface UploadResult { path: string; name: string; size: number }

// ---------------------------------------------------------------- agents
export interface AgentCapabilities {
  resume: boolean; fork: boolean; headless: boolean; hooks: boolean; history: boolean
  usage: boolean; quota: boolean; worktrees: boolean; prompt: boolean
}
export interface HookStatus { installed: boolean; path?: string; detail?: string }
export interface AgentInfo {
  id: string; name: string; vendor: string; installed: boolean; version?: string; binary?: string
  color: string; capabilities: AgentCapabilities; hooks?: HookStatus; installHint?: string; sessions: number
}
export interface TokenUsage { input: number; output: number; cacheRead: number; cacheWrite: number; reasoning?: number }
export interface AgentSession {
  id: string; agent: string; nativeId: string; title: string; summary?: string; cwd: string
  workspace?: string; gitBranch?: string; model?: string; startedAt: string; updatedAt: string
  messages: number; tokens?: TokenUsage; costUsd?: number; status: 'live' | 'history'
  terminalId?: string; activity?: Activity; resumable: boolean; pinned: boolean; archived: boolean
}
export interface Part {
  type: 'text' | 'thinking' | 'tool_call' | 'tool_result' | 'image' | 'diff'
  text?: string; tool?: string; input?: string; output?: string; isError?: boolean; path?: string; mimeType?: string
}
export interface AgentMessage { id: string; role: 'user' | 'assistant' | 'tool' | 'system'; at?: string; model?: string; parts: Part[]; tokens?: TokenUsage }
export interface Transcript { session: AgentSession; messages: AgentMessage[]; hasMore: boolean }
export interface WorktreeOption { branch: string; base?: string }
export interface LaunchAgentRequest { agent: string; cwd: string; prompt?: string; model?: string; worktree?: WorktreeOption; name?: string; cols?: number; rows?: number }
export interface ResumeAgentRequest { fork?: boolean; cols?: number; rows?: number }
export interface UpdateAgentSessionRequest { title?: string; pinned?: boolean; archived?: boolean }
export interface SearchHit { sessionId: string; agent: string; title: string; cwd?: string; role: string; snippet: string; at: string; messageId?: string }
export interface UsageTotals { costUsd: number; tokens: TokenUsage; sessions: number; messages: number }
export interface UsageSlice { key: string; label: string; costUsd: number; tokens: number; sessions: number; estimated?: boolean }
export interface UsageDay { date: string; costUsd: number; tokens: number; byAgent: Record<string, number> }
export interface UsageSummary { range: 'today' | '7d' | '30d' | 'all'; totals: UsageTotals; byAgent: UsageSlice[]; byModel: UsageSlice[]; daily: UsageDay[]; updated: string }
export interface QuotaWindow { label: string; usedPct: number; resetsAt?: string }
export interface Quota { agent: string; plan?: string; windows: QuotaWindow[]; updatedAt: string; source: string; stale: boolean }

// ---------------------------------------------------------------- workspaces & git
export interface Commit { hash: string; short: string; subject: string; author: string; at: string }
export interface GitBrief { branch: string; dirty: number; ahead: number; behind: number; last?: Commit; remote?: string; at: string }
export interface Workspace {
  path: string; name: string; git?: GitBrief; pinned: boolean; lastUsedAt?: string
  terminals: number; agents: number; languages?: string[]
}
export interface GitFile { path: string; origPath?: string; index: string; work: string; staged: boolean; added: number; removed: number; binary?: boolean }
export interface Worktree { path: string; branch: string; head: string; main: boolean }
export interface GitStatus {
  path: string; root: string; branch: string; upstream?: string; ahead: number; behind: number
  files: GitFile[]; last?: Commit; worktree: boolean; stashes: number; remotes?: string[]; worktrees?: Worktree[]
}
export interface GitDiff { path: string; file?: string; staged: boolean; diff: string; truncated?: boolean }
export interface GitActionRequest { path: string; files?: string[]; message?: string; branch?: string; base?: string }

// ---------------------------------------------------------------- files
export interface FileEntry {
  name: string; path: string; type: 'file' | 'dir' | 'symlink' | 'other'; size: number; modTime: string
  mode: string; mime?: string; hidden: boolean; target?: string; children?: number; git?: string
}
export interface DirListing { path: string; parent?: string; entries: FileEntry[]; total: number; offset: number; hidden: number; gitRoot?: string }
export interface FileOpRequest { paths?: string[]; from?: string[]; to?: string; path?: string; name?: string; trash?: boolean }
export interface DiskUsage { path: string; size: number; files: number; dirs: number; children: Record<string, number>; complete: boolean; pending: boolean }
export interface FileSearchHit { path: string; line?: number; column?: number; preview?: string; score?: number }

// ---------------------------------------------------------------- system
export interface CPUStats { percent: number; perCore: number[]; cores: number; model?: string; tempC?: number }
export interface MemStats { total: number; used: number; available: number; cached: number; swapTotal: number; swapUsed: number }
export interface DiskStats { mount: string; fs: string; total: number; used: number; free: number; readBps: number; writeBps: number }
export interface NetStats { rxBps: number; txBps: number; rxTotal: number; txTotal: number }
export interface GPUStats { name: string; util: number; memUsed: number; memTotal: number; tempC?: number }
export interface HostInfo { hostname: string; os: string; kernel: string; arch: string; virt?: string }
export interface Metrics {
  at: string; cpu: CPUStats; memory: MemStats; disks: DiskStats[]; net: NetStats; gpu?: GPUStats[]
  uptimeSec: number; load: [number, number, number]; procs: number; host: HostInfo
}
export interface Process {
  pid: number; ppid: number; name: string; cmd: string; user: string; cpu: number; rss: number
  memPct: number; state: string; threads: number; startedAt: string; terminal?: string; protected: boolean
}
export interface SignalRequest { signal: 'TERM' | 'KILL' | 'INT' | 'HUP' | 'STOP' | 'CONT' }
export interface Service { name: string; description: string; active: string; sub: string; since?: string; restarts: number; user: boolean; managed: boolean }
export interface LogLine { at: string; unit?: string; prio: number; text: string }

// ---------------------------------------------------------------- previews
export interface Preview {
  port: number; address: string; pid?: number; process?: string; cwd?: string; workspace?: string
  label?: string; url: string; http: boolean; title?: string; firstSeenAt: string; pinned: boolean; hidden: boolean
}
export interface UpdatePreviewRequest { label?: string; pinned?: boolean; hidden?: boolean }

// ---------------------------------------------------------------- apps & desktop
export interface App {
  id: string; name: string; description: string; kind: 'code' | 'desktop' | 'web'
  state: 'running' | 'starting' | 'stopped' | 'unavailable' | 'error'
  installed: boolean; url?: string; icon?: string; since?: string; error?: string; installHint?: string; capability?: AppCapability
}
export interface DesktopApp { id: string; name: string; running: boolean; icon?: string }
export interface DesktopState {
  state: 'running' | 'stopped' | 'starting' | 'unavailable'; display?: string; width: number; height: number
  apps: DesktopApp[]; viewers: number; error?: string; installHint?: string; capability: AppCapability
}

// ---------------------------------------------------------------- notifications
export type NotificationKind = 'attention' | 'done' | 'exited' | 'preview' | 'security' | 'system' | 'schedule' | 'custom'
export interface Notification {
  id: string; kind: NotificationKind; title: string; body?: string; at: string; read: boolean
  link?: string; sessionId?: string; agent?: string; severity?: 'info' | 'success' | 'warning' | 'danger'
}
export interface NotifyRequest { kind?: string; title: string; body?: string; link?: string; sessionId?: string; agent?: string; severity?: string }
export interface PushSubscriptionJSON { endpoint: string; keys: { p256dh: string; auth: string }; device?: string }
export interface NotifySettings {
  rules: Record<string, boolean>; quietStart?: string; quietEnd?: string; ntfyUrl?: string; webhookUrl?: string
  devices: number; vapidKey?: string
}

// ---------------------------------------------------------------- clipboard, snippets, notes
export interface Clip { id: string; text: string; source: 'terminal' | 'cli' | 'web' | 'osc52' | 'desktop'; at: string; size: number }
export interface Snippet { id: string; name: string; body: string; kind: 'prompt' | 'command'; tags?: string[]; agent?: string; updatedAt: string; uses: number }
export interface Note { id: string; title: string; text: string; updatedAt: string }

// ---------------------------------------------------------------- schedules
export interface ScheduleRun { id: string; scheduleId: string; startedAt: string; finishedAt?: string; status: 'running' | 'ok' | 'failed' | 'skipped'; exitCode?: number; terminalId?: string; output?: string }
export interface Schedule {
  id: string; name: string; cron: string; timezone?: string; cwd: string; agent?: string; prompt?: string
  command?: string[]; mode: 'headless' | 'interactive'; enabled: boolean; notify: boolean
  nextRun?: string; lastRun?: ScheduleRun; createdAt: string
}

// ---------------------------------------------------------------- command center
export type SearchScope = 'terminals' | 'agents' | 'history' | 'files' | 'workspaces' | 'previews' | 'processes' | 'snippets' | 'notes' | 'scripts'
export interface SearchResult { scope: SearchScope; id: string; title: string; subtitle?: string; icon?: string; link?: string; score: number; at?: string; meta?: Record<string, string> }
export interface SearchResponse { query: string; results: SearchResult[]; tookMs: number }
export interface ScriptArg { name: string; placeholder?: string; optional?: boolean }
export interface ScriptCommand { id: string; title: string; description?: string; icon?: string; mode: 'inline' | 'terminal' | 'silent'; args?: ScriptArg[]; path: string; cwd?: string }
export interface RunScriptRequest { args?: string[] }
export interface RunScriptResult { terminalId?: string; output?: string; exitCode: number }
export interface AskRequest { agent?: string; prompt: string; cwd?: string }
export interface AskChunk { t: 'text' | 'done' | 'error'; text?: string }

// ---------------------------------------------------------------- toolbox
export interface Tool {
  id: string; name: string; category: 'agents' | 'runtimes' | 'browsers' | 'desktop' | 'creative' | 'cli' | 'editors' | 'mcp'
  description: string; homepage?: string; installed: boolean; version?: string; installable: boolean
  requiresSudo: boolean; size?: string; tags?: string[]
}
export interface MCPServer { id: string; name: string; description: string; command: string[]; env?: Record<string, string>; agents: Record<string, boolean> }
export interface MCPApplyRequest { server: string; agents: string[]; remove?: boolean }

// ---------------------------------------------------------------- live events
export type EventType =
  | 'hello' | 'terminal.created' | 'terminal.updated' | 'terminal.exited' | 'terminal.removed'
  | 'agents.indexed' | 'agents.session' | 'notification' | 'notification.read' | 'metrics'
  | 'previews.changed' | 'clip' | 'open' | 'schedule.run' | 'app.state' | 'desktop.state'
  | 'toolbox.job' | 'workspace.changed' | 'capabilities.changed'
export interface RelayEvent<T = unknown> { type: EventType; at: string; data?: T }
export interface ClientEvent { type: 'subscribe' | 'unsubscribe' | 'visibility' | 'ping'; topics?: string[]; visible?: boolean; path?: string }

// ---------------------------------------------------------------- settings
export interface Settings {
  workspaceRoots: string[]; defaultShell: string; defaultCwd: string; recordAgents: boolean
  claudeQuota: boolean; idleMinutes: number
}
