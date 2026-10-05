// Synthetic fixtures for the mock backend. Every name, path and host here
// is invented. The store is mutable so actions (rename, kill, mark read…)
// behave like a real server for the rest of the session.
import type { SettingsState } from '../api/runtime-settings'
import type {
  AgentInfo,
  AgentMessage,
  AgentSession,
  APIToken,
  App,
  AuditEntry,
  AuthState,
  Clip,
  DesktopState,
  DeviceSession,
  Info,
  MCPServer,
  Note,
  Notification,
  NotifySettings,
  Passkey,
  Preview,
  Quota,
  Schedule,
  ScheduleRun,
  ScriptCommand,
  Service,
  Snippet,
  TerminalSession,
  Tool,
  UsageSummary,
  Workspace,
} from '../api/types'
import { AGENTS } from '../lib/agents'
import { before, DAY, HOUR, has, MIN, rng } from './util'

export const HOME = '/home/dev'
export const HOST = 'atlas'

export const info: Info = {
  version: '0.1.0-dev',
  commit: 'a1b2c3d',
  hostname: HOST,
  os: 'linux',
  arch: 'amd64',
  user: 'dev',
  home: HOME,
  startedAt: before(3 * DAY + 4 * HOUR),
  publicUrl: 'https://atlas.example.test',
  capabilities: {
    previews: {
      configuredMode: 'auto',
      effectiveMode: 'subdomain',
      host: 'atlas.example.test',
      detection: 'verified',
      checkedAt: before(30),
    },
    code: {
      enabled: true,
      available: true,
      state: 'running',
      missing: [],
      implementation: 'code-server',
      source: 'standalone',
    },
    desktop: {
      enabled: true,
      available: true,
      state: 'stopped',
      missing: [],
      implementation: 'Xtigervnc',
      source: 'path',
    },
  },
  features: {
    passkeys: true,
    push: true,
    desktop: true,
    code: true,
    previewsMode: 'subdomain',
    previewsHost: 'atlas.example.test',
    recording: true,
    ripgrep: true,
  },
}

export const settings: SettingsState = {
  workspaceRoots: [`${HOME}/code`],
  defaultShell: '/bin/zsh',
  defaultCwd: HOME,
  recordAgents: true,
  claudeQuota: true,
  idleMinutes: 10,
  recordingMode: 'agents',
  effective: {
    workspaceRoots: [`${HOME}/code`],
    claudeQuota: true,
    codeIdleStop: '10m0s',
    desktopIdleStop: '10m0s',
    terminal: { defaultShell: '/bin/zsh', defaultCwd: HOME, recordingMode: 'agents', source: 'file' },
    terminalStatus: 'next-session',
  },
  apply: {
    workspaceRoots: 'next-request',
    defaultShell: 'next-session',
    defaultCwd: 'next-session',
    recordAgents: 'next-session',
    claudeQuota: 'next-request',
    idleMinutes: 'next-idle-check',
  },
}

// ---------------------------------------------------------------- auth

export const auth = {
  state: {
    authenticated: !has('signed-out') && !has('setup') && !has('totp'),
    user: 'dev',
    setupRequired: has('setup'),
    methods: { password: true, passkey: true, totp: has('totp') },
    sessionId: 'ds_current',
  } as AuthState,
  failures: 0,
  lockedUntil: 0,
}

export const passkeys: Passkey[] = [
  { id: 'pk_1', name: 'MacBook Touch ID', createdAt: before(40 * DAY), lastUsedAt: before(2 * HOUR) },
  { id: 'pk_2', name: 'Pixel phone', createdAt: before(12 * DAY), lastUsedAt: before(1 * DAY) },
]

export const deviceSessions: DeviceSession[] = [
  {
    id: 'ds_current',
    current: true,
    device: 'Mac',
    browser: 'Chrome 140',
    os: 'macOS 15',
    ip: '203.0.113.24',
    method: 'passkey',
    createdAt: before(3 * DAY),
    lastSeenAt: before(10),
    expiresAt: before(-27 * DAY),
  },
  {
    id: 'ds_phone',
    current: false,
    device: 'iPhone',
    browser: 'Safari 18',
    os: 'iOS 18',
    ip: '198.51.100.7',
    method: 'passkey',
    createdAt: before(9 * DAY),
    lastSeenAt: before(35 * MIN),
    expiresAt: before(-21 * DAY),
  },
  {
    id: 'ds_tablet',
    current: false,
    device: 'iPad',
    browser: 'Safari 18',
    os: 'iPadOS 18',
    ip: '198.51.100.19',
    method: 'password',
    createdAt: before(20 * DAY),
    lastSeenAt: before(4 * DAY),
    expiresAt: before(-10 * DAY),
  },
]

export const tokens: APIToken[] = [
  {
    id: 'tk_1',
    name: 'CI deploy hook',
    prefix: 'rly_8f2a',
    createdAt: before(30 * DAY),
    lastUsedAt: before(6 * HOUR),
  },
  { id: 'tk_2', name: 'Shortcuts on phone', prefix: 'rly_03cd', createdAt: before(5 * DAY) },
]

export const audit: AuditEntry[] = [
  {
    id: 9,
    at: before(10 * MIN),
    event: 'login.passkey',
    actor: 'dev',
    ip: '203.0.113.24',
    device: 'Chrome on macOS',
  },
  {
    id: 8,
    at: before(2 * HOUR),
    event: 'file.delete',
    actor: 'dev',
    detail: '~/Downloads/old-build.zip (trash)',
  },
  { id: 7, at: before(5 * HOUR), event: 'process.signal', actor: 'dev', detail: 'TERM → node (pid 48121)' },
  {
    id: 6,
    at: before(9 * HOUR),
    event: 'login.failed',
    actor: '—',
    ip: '192.0.2.200',
    detail: 'bad password',
  },
  { id: 5, at: before(1 * DAY), event: 'token.used', actor: 'CI deploy hook', ip: '192.0.2.10' },
  { id: 4, at: before(2 * DAY), event: 'hooks.install', actor: 'dev', detail: 'claude (backup saved)' },
  { id: 3, at: before(3 * DAY), event: 'session.revoke', actor: 'dev', detail: 'Firefox on Linux' },
]

// ---------------------------------------------------------------- workspaces

const W = (name: string) => `${HOME}/code/${name}`

export const workspaces: Workspace[] = [
  {
    path: W('relay-demo'),
    name: 'relay-demo',
    pinned: true,
    lastUsedAt: before(3 * MIN),
    terminals: 3,
    agents: 2,
    languages: ['Go', 'TypeScript'],
    git: {
      branch: 'feat/session-sharing',
      dirty: 7,
      ahead: 2,
      behind: 0,
      remote: 'origin',
      at: before(2 * MIN),
      last: {
        hash: 'e4f1a9c2b7',
        short: 'e4f1a9c',
        subject: 'Share a read-only session link',
        author: 'Dev',
        at: before(40 * MIN),
      },
    },
  },
  {
    path: W('orbit-api'),
    name: 'orbit-api',
    pinned: true,
    lastUsedAt: before(50 * MIN),
    terminals: 1,
    agents: 1,
    languages: ['Python'],
    git: {
      branch: 'main',
      dirty: 0,
      ahead: 0,
      behind: 3,
      remote: 'origin',
      at: before(8 * MIN),
      last: {
        hash: '9ad07e11c3',
        short: '9ad07e1',
        subject: 'Bump pydantic to 2.9',
        author: 'Dev',
        at: before(1 * DAY),
      },
    },
  },
  {
    path: W('field-notes'),
    name: 'field-notes',
    pinned: false,
    lastUsedAt: before(5 * HOUR),
    terminals: 0,
    agents: 1,
    languages: ['Markdown', 'Astro'],
    git: {
      branch: 'draft/winter',
      dirty: 2,
      ahead: 0,
      behind: 0,
      at: before(1 * HOUR),
      last: {
        hash: '1c2d3e4f5a',
        short: '1c2d3e4',
        subject: 'Winter issue outline',
        author: 'Dev',
        at: before(5 * HOUR),
      },
    },
  },
  {
    path: W('pixel-garden'),
    name: 'pixel-garden',
    pinned: false,
    lastUsedAt: before(2 * DAY),
    terminals: 0,
    agents: 0,
    languages: ['Rust'],
    git: {
      branch: 'main',
      dirty: 0,
      ahead: 0,
      behind: 0,
      at: before(2 * DAY),
      last: {
        hash: '77aa88bb99',
        short: '77aa88b',
        subject: 'Palette cycling',
        author: 'Dev',
        at: before(2 * DAY),
      },
    },
  },
  {
    path: W('scratch'),
    name: 'scratch',
    pinned: false,
    lastUsedAt: before(6 * DAY),
    terminals: 0,
    agents: 0,
  },
]

// ---------------------------------------------------------------- terminals

const t = (
  s: Partial<TerminalSession> & Pick<TerminalSession, 'id' | 'name' | 'kind' | 'activity'>,
): TerminalSession => ({
  command: ['/bin/zsh', '-l'],
  cwd: W('relay-demo'),
  cols: 120,
  rows: 34,
  clients: 0,
  recording: false,
  pinned: false,
  createdAt: before(2 * HOUR),
  ...s,
})

export const terminals: TerminalSession[] = [
  t({
    id: 't_claude1',
    name: 'claude · session sharing',
    title: 'Refactor share links',
    kind: 'agent',
    agent: 'claude',
    agentSessionId: 'claude:7c1e0b52',
    command: ['claude'],
    workspace: W('relay-demo'),
    activity: 'waiting',
    attention: {
      reason: 'hook',
      message: 'Allow running `go test ./internal/share/...`?',
      at: before(95),
    },
    pid: 48210,
    clients: 1,
    recording: true,
    pinned: true,
    lastOutputAt: before(95),
    preview: 'Do you want to run go test ./internal/share/...? (y/n)',
    createdAt: before(52 * MIN),
  }),
  t({
    id: 't_codex1',
    name: 'codex · rate limiter',
    kind: 'agent',
    agent: 'codex',
    agentSessionId: 'codex:0f3b91aa',
    command: ['codex'],
    workspace: W('orbit-api'),
    cwd: W('orbit-api'),
    activity: 'working',
    pid: 48877,
    recording: true,
    lastOutputAt: before(2),
    preview: 'Editing app/limits.py — adding a sliding window per API key',
    createdAt: before(18 * MIN),
  }),
  t({
    id: 't_dev',
    name: 'dev server',
    kind: 'task',
    command: ['pnpm', 'dev'],
    activity: 'working',
    pid: 47001,
    lastOutputAt: before(4),
    preview: 'VITE ready in 412 ms  ➜  Local: http://127.0.0.1:5173/',
    createdAt: before(3 * HOUR),
    pinned: true,
  }),
  t({
    id: 't_gemini1',
    name: 'gemini · docs pass',
    kind: 'agent',
    agent: 'gemini',
    agentSessionId: 'gemini:b8d2e4f0',
    command: ['gemini'],
    workspace: W('field-notes'),
    cwd: W('field-notes'),
    activity: 'idle',
    pid: 49102,
    lastOutputAt: before(14 * MIN),
    preview: 'Done. Updated 4 files. Anything else?',
    createdAt: before(70 * MIN),
  }),
  t({
    id: 't_shell',
    name: 'zsh',
    kind: 'shell',
    activity: 'idle',
    pid: 46010,
    cwd: HOME,
    lastOutputAt: before(6 * MIN),
    preview: 'dev@atlas ~ %',
    createdAt: before(5 * HOUR),
  }),
  t({
    id: 't_test',
    name: 'go test ./...',
    kind: 'task',
    command: ['go', 'test', './...'],
    activity: 'exited',
    exitCode: 0,
    exitedAt: before(25 * MIN),
    preview: 'ok  	relay-demo/internal/share	0.412s',
    createdAt: before(27 * MIN),
  }),
  t({
    id: 't_build',
    name: 'release build',
    kind: 'task',
    command: ['make', 'release'],
    activity: 'exited',
    exitCode: 2,
    exitedAt: before(3 * HOUR),
    preview: 'make: *** [release] Error 2',
    createdAt: before(3 * HOUR + 4 * MIN),
  }),
  t({
    id: 't_tmux',
    name: 'tmux: monitoring',
    kind: 'tmux',
    activity: 'idle',
    meta: { importable: '1', tmux: 'monitoring' },
    cwd: HOME,
    createdAt: before(6 * DAY),
  }),
]

// ---------------------------------------------------------------- agents

const caps = (o: Partial<AgentInfo['capabilities']> = {}): AgentInfo['capabilities'] => ({
  resume: true,
  fork: true,
  headless: true,
  hooks: true,
  history: true,
  usage: true,
  quota: false,
  worktrees: true,
  prompt: true,
  ...o,
})

const installed = new Set(['claude', 'codex', 'gemini', 'opencode', 'kiro', 'cursor', 'pi'])

export const agents: AgentInfo[] = AGENTS.slice(0, 10).map((a) => ({
  id: a.id,
  name: a.name,
  vendor: a.vendor,
  installed: installed.has(a.id),
  version: installed.has(a.id)
    ? `${1 + (a.mono.charCodeAt(0) % 3)}.${a.mono.charCodeAt(1) % 10}.${a.id.length}`
    : undefined,
  binary: installed.has(a.id) ? `${HOME}/.local/bin/${a.id}` : undefined,
  color: a.color,
  capabilities: caps({ quota: a.id === 'claude' || a.id === 'codex', hooks: a.id !== 'cursor' }),
  hooks: a.id === 'claude' ? { installed: true, path: '~/.claude/settings.json' } : { installed: false },
  installHint: installed.has(a.id) ? undefined : `Install ${a.name} from the Toolbox.`,
  sessions: 0,
}))

const TITLES = [
  'Refactor share links',
  'Add a sliding-window rate limiter',
  'Docs pass on the onboarding guide',
  'Fix flaky websocket reconnect test',
  'Migrate settings to TOML',
  'Explain the ptyd ring buffer',
  'Dark mode contrast audit',
  'Write a release checklist',
  'Speed up cold start',
  'Port the CSV importer to streams',
  'Investigate memory spike in indexer',
  'Draft winter newsletter',
  'Add keyboard shortcuts sheet',
  'Clean up unused feature flags',
  'Tune SQLite WAL checkpoints',
  'Sketch the pricing page',
  'Summarise yesterday’s incident',
  'Make the tab bar safe-area aware',
  'Add OpenGraph images',
  'Profile the diff renderer',
  'Rename config keys',
  'Try a new palette ranking',
  'Batch notifications while quiet hours',
  'Upgrade to Go 1.25',
]
const MODELS: Record<string, string> = {
  claude: 'claude-sonnet-4.5',
  codex: 'gpt-5-codex',
  gemini: 'gemini-2.5-pro',
  opencode: 'kimi-k2',
  kiro: 'claude-sonnet-4.5',
  cursor: 'auto',
  pi: 'pi-2',
}

function buildSessions(): AgentSession[] {
  const r = rng(7)
  const pool = ['claude', 'codex', 'gemini', 'opencode', 'kiro', 'cursor', 'pi']
  const ws = workspaces.map((w) => w)
  const out: AgentSession[] = []
  const live: Record<string, TerminalSession> = {}
  for (const term of terminals) if (term.agentSessionId) live[term.agentSessionId] = term
  // Live sessions first (linked to terminals).
  for (const [id, term] of Object.entries(live)) {
    const [agent, nativeId] = id.split(':')
    out.push({
      id,
      agent,
      nativeId,
      title: term.title ?? term.name.split('·')[1]?.trim() ?? term.name,
      summary: term.preview,
      cwd: term.cwd,
      workspace: term.workspace,
      gitBranch: workspaces.find((w) => w.path === term.workspace)?.git?.branch,
      model: MODELS[agent],
      startedAt: term.createdAt,
      updatedAt: term.lastOutputAt ?? term.createdAt,
      messages: 12 + Math.floor(r() * 60),
      tokens: {
        input: 180_000 + Math.floor(r() * 900_000),
        output: 20_000 + Math.floor(r() * 90_000),
        cacheRead: 1_200_000,
        cacheWrite: 90_000,
      },
      costUsd: Math.round((0.4 + r() * 6) * 100) / 100,
      status: 'live',
      terminalId: term.id,
      activity: term.activity,
      resumable: true,
      pinned: term.pinned,
      archived: false,
    })
  }
  for (let i = 0; i < TITLES.length; i++) {
    const agent = pool[Math.floor(r() * pool.length)]
    const w = ws[Math.floor(r() * ws.length)]
    const age = (i + 1) * (3 * HOUR) + Math.floor(r() * 2 * HOUR)
    const nativeId = Math.floor(r() * 0xffffffff)
      .toString(16)
      .padStart(8, '0')
    const id = `${agent}:${nativeId}`
    if (out.some((s) => s.id === id)) continue
    out.push({
      id,
      agent,
      nativeId,
      title: TITLES[i],
      summary:
        i % 3 === 0 ? `Worked through ${TITLES[i].toLowerCase()} and left notes in the PR.` : undefined,
      cwd: w.path,
      workspace: w.path,
      gitBranch: w.git?.branch,
      model: MODELS[agent],
      startedAt: before(age + 40 * MIN),
      updatedAt: before(age),
      messages: 4 + Math.floor(r() * 140),
      tokens: {
        input: Math.floor(r() * 2_000_000),
        output: Math.floor(r() * 120_000),
        cacheRead: Math.floor(r() * 4_000_000),
        cacheWrite: Math.floor(r() * 200_000),
      },
      costUsd: agent === 'cursor' ? undefined : Math.round(r() * 900) / 100,
      status: 'history',
      resumable: agent !== 'cursor',
      pinned: i === 2,
      archived: i === TITLES.length - 1,
    })
  }
  return out
}

export const agentSessions: AgentSession[] = buildSessions()
for (const a of agents) a.sessions = agentSessions.filter((s) => s.agent === a.id).length

/** A plausible transcript for any session id (deterministic). */
export function transcriptFor(s: AgentSession): AgentMessage[] {
  const r = rng(s.nativeId.split('').reduce((n, c) => n + c.charCodeAt(0), 0))
  const at = (i: number) => new Date(Date.parse(s.startedAt) + i * 47_000).toISOString()
  const file = pick([
    'internal/share/link.go',
    'app/limits.py',
    'docs/onboarding.md',
    'web/src/app/Shell.tsx',
  ])
  function pick<T>(xs: T[]): T {
    return xs[Math.floor(r() * xs.length)]
  }
  const msgs: AgentMessage[] = [
    {
      id: 'm1',
      role: 'user',
      at: at(0),
      parts: [{ type: 'text', text: `${s.title}. Keep the public API unchanged and add tests.` }],
    },
    {
      id: 'm2',
      role: 'assistant',
      at: at(1),
      model: s.model,
      parts: [
        {
          type: 'thinking',
          text: 'Start by reading the current implementation and its tests, then plan the smallest change.',
        },
        { type: 'text', text: `I'll look at \`${file}\` first.` },
        { type: 'tool_call', tool: 'read', input: JSON.stringify({ path: file }) },
      ],
    },
    {
      id: 'm3',
      role: 'tool',
      at: at(2),
      parts: [
        {
          type: 'tool_result',
          tool: 'read',
          output:
            '// 214 lines\npackage share\n\n// Link is a signed, expiring pointer to a session.\ntype Link struct {\n\tID      string\n\tExpires time.Time\n}\n',
        },
      ],
    },
    {
      id: 'm4',
      role: 'assistant',
      at: at(3),
      model: s.model,
      parts: [
        {
          type: 'text',
          text: 'Here is the plan:\n\n1. Extract token signing into `sign.go`\n2. Add `ReadOnly` to `Link`\n3. Cover expiry and tampering with table tests',
        },
        {
          type: 'diff',
          path: file,
          text: `--- a/${file}\n+++ b/${file}\n@@ -4,6 +4,8 @@\n // Link is a signed, expiring pointer to a session.\n type Link struct {\n \tID      string\n \tExpires time.Time\n+\t// ReadOnly links can watch but never type.\n+\tReadOnly bool\n }\n`,
        },
      ],
      tokens: { input: 18_400, output: 1_120, cacheRead: 64_000, cacheWrite: 2_300 },
    },
    {
      id: 'm5',
      role: 'assistant',
      at: at(4),
      model: s.model,
      parts: [
        {
          type: 'tool_call',
          tool: 'bash',
          input: JSON.stringify({ command: 'go test ./internal/share/...' }),
        },
      ],
    },
    {
      id: 'm6',
      role: 'tool',
      at: at(5),
      parts: [
        {
          type: 'tool_result',
          tool: 'bash',
          output: 'ok  \trelay-demo/internal/share\t0.412s',
          isError: false,
        },
      ],
    },
    {
      id: 'm7',
      role: 'assistant',
      at: at(6),
      model: s.model,
      parts: [
        {
          type: 'text',
          text: 'Tests pass. The link format is unchanged, so existing links keep working. Want me to open a PR?',
        },
      ],
    },
  ]
  return msgs
}

export function usageFor(range: UsageSummary['range']): UsageSummary {
  const days = range === 'today' ? 1 : range === '7d' ? 7 : range === '30d' ? 30 : 90
  const r = rng(days)
  const daily = Array.from({ length: days }, (_, i) => {
    const byAgent: Record<string, number> = {
      claude: Math.round((2 + r() * 14) * 100) / 100,
      codex: Math.round((1 + r() * 8) * 100) / 100,
      gemini: Math.round(r() * 3 * 100) / 100,
    }
    const cost = Object.values(byAgent).reduce((a, b) => a + b, 0)
    return {
      date: before((days - 1 - i) * DAY).slice(0, 10),
      costUsd: Math.round(cost * 100) / 100,
      tokens: Math.floor(cost * 180_000),
      byAgent,
    }
  })
  const total = daily.reduce((a, d) => a + d.costUsd, 0)
  const sum = (k: string) => daily.reduce((a, d) => a + (d.byAgent[k] ?? 0), 0)
  return {
    range,
    totals: {
      costUsd: Math.round(total * 100) / 100,
      tokens: {
        input: Math.floor(total * 150_000),
        output: Math.floor(total * 12_000),
        cacheRead: Math.floor(total * 400_000),
        cacheWrite: Math.floor(total * 20_000),
      },
      sessions: 6 * days,
      messages: 180 * days,
    },
    byAgent: [
      {
        key: 'claude',
        label: 'Claude Code',
        costUsd: Math.round(sum('claude') * 100) / 100,
        tokens: Math.floor(sum('claude') * 180_000),
        sessions: 3 * days,
      },
      {
        key: 'codex',
        label: 'Codex',
        costUsd: Math.round(sum('codex') * 100) / 100,
        tokens: Math.floor(sum('codex') * 180_000),
        sessions: 2 * days,
      },
      {
        key: 'gemini',
        label: 'Gemini CLI',
        costUsd: Math.round(sum('gemini') * 100) / 100,
        tokens: Math.floor(sum('gemini') * 180_000),
        sessions: days,
        estimated: true,
      },
    ],
    byModel: [
      {
        key: 'claude-sonnet-4.5',
        label: 'claude-sonnet-4.5',
        costUsd: Math.round(sum('claude') * 70) / 100,
        tokens: 0,
        sessions: 2 * days,
      },
      {
        key: 'claude-opus-4.1',
        label: 'claude-opus-4.1',
        costUsd: Math.round(sum('claude') * 30) / 100,
        tokens: 0,
        sessions: days,
      },
      {
        key: 'gpt-5-codex',
        label: 'gpt-5-codex',
        costUsd: Math.round(sum('codex') * 100) / 100,
        tokens: 0,
        sessions: 2 * days,
      },
      {
        key: 'gemini-2.5-pro',
        label: 'gemini-2.5-pro',
        costUsd: Math.round(sum('gemini') * 100) / 100,
        tokens: 0,
        sessions: days,
        estimated: true,
      },
    ],
    daily,
    updated: before(40),
  }
}

export const quotas: Quota[] = [
  {
    agent: 'claude',
    plan: 'Max 5×',
    windows: [
      { label: '5-hour', usedPct: 62, resetsAt: before(-(1 * HOUR + 48 * MIN)) },
      { label: 'Weekly', usedPct: 38, resetsAt: before(-(3 * DAY + 2 * HOUR)) },
    ],
    updatedAt: before(2 * MIN),
    source: 'statusline',
    stale: false,
  },
  {
    agent: 'codex',
    plan: 'Pro',
    windows: [
      { label: '5-hour', usedPct: 18, resetsAt: before(-(3 * HOUR + 10 * MIN)) },
      { label: 'Weekly', usedPct: 71, resetsAt: before(-(1 * DAY + 6 * HOUR)) },
    ],
    updatedAt: before(4 * MIN),
    source: 'session files',
    stale: false,
  },
  {
    agent: 'gemini',
    windows: [{ label: 'Daily', usedPct: 9, resetsAt: before(-(9 * HOUR)) }],
    updatedAt: before(3 * HOUR),
    source: 'api',
    stale: true,
  },
]

// ---------------------------------------------------------------- previews / apps

export const previews: Preview[] = [
  {
    port: 5173,
    address: '127.0.0.1',
    pid: 47012,
    process: 'node',
    cwd: W('relay-demo'),
    workspace: W('relay-demo'),
    label: 'relay-demo web',
    url: 'https://5173.atlas.example.test/',
    http: true,
    title: 'Relay demo',
    firstSeenAt: before(3 * HOUR),
    pinned: true,
    hidden: false,
  },
  {
    port: 8000,
    address: '127.0.0.1',
    pid: 48900,
    process: 'uvicorn',
    cwd: W('orbit-api'),
    workspace: W('orbit-api'),
    url: 'https://8000.atlas.example.test/',
    http: true,
    title: 'Orbit API — Swagger UI',
    firstSeenAt: before(40 * MIN),
    pinned: false,
    hidden: false,
  },
  {
    port: 4321,
    address: '127.0.0.1',
    pid: 49300,
    process: 'astro',
    cwd: W('field-notes'),
    workspace: W('field-notes'),
    url: 'https://4321.atlas.example.test/',
    http: true,
    title: 'Field notes',
    firstSeenAt: before(2 * MIN),
    pinned: false,
    hidden: false,
  },
  {
    port: 6379,
    address: '127.0.0.1',
    pid: 1201,
    process: 'redis-server',
    url: 'https://6379.atlas.example.test/',
    http: false,
    firstSeenAt: before(3 * DAY),
    pinned: false,
    hidden: true,
  },
]

export const apps: App[] = [
  {
    id: 'code',
    name: 'VS Code',
    description: 'code-server in the browser, on this machine.',
    kind: 'code',
    capability: info.capabilities.code,
    state: 'running',
    installed: true,
    url: '/apps/code/',
    since: before(2 * HOUR),
  },
  {
    id: 'desktop',
    name: 'Desktop',
    description: 'A real Linux desktop over noVNC.',
    kind: 'desktop',
    capability: info.capabilities.desktop,
    state: 'stopped',
    installed: true,
  },
  {
    id: 'jupyter',
    name: 'JupyterLab',
    description: 'Notebooks next to your code.',
    kind: 'web',
    state: 'unavailable',
    installed: false,
    installHint: 'pipx install jupyterlab',
  },
]

export const desktop: DesktopState = {
  capability: info.capabilities.desktop,
  display: ':7',
  state: 'stopped',
  width: 1600,
  height: 1000,
  apps: [
    { id: 'chrome', name: 'Chrome', running: false },
    { id: 'blender', name: 'Blender', running: false },
    { id: 'files', name: 'Files', running: false },
    { id: 'terminal', name: 'Terminal', running: false },
  ],
  viewers: 0,
}

// ---------------------------------------------------------------- notifications

export const notifications: Notification[] = [
  {
    id: 'n_1',
    kind: 'attention',
    title: 'Claude Code needs you',
    body: 'Allow running `go test ./internal/share/...`?',
    at: before(95),
    read: false,
    link: '/terminal/t_claude1',
    sessionId: 't_claude1',
    agent: 'claude',
  },
  {
    id: 'n_2',
    kind: 'done',
    title: 'Tests passed',
    body: 'go test ./... finished in 38s',
    at: before(25 * MIN),
    read: false,
    link: '/terminal/t_test',
    sessionId: 't_test',
    severity: 'success',
  },
  {
    id: 'n_3',
    kind: 'preview',
    title: 'New preview on :4321',
    body: 'astro dev in field-notes',
    at: before(2 * MIN),
    read: false,
    link: '/previews',
    severity: 'info',
  },
  {
    id: 'n_4',
    kind: 'exited',
    title: 'release build failed',
    body: 'make release exited with code 2',
    at: before(3 * HOUR),
    read: true,
    link: '/terminal/t_build',
    sessionId: 't_build',
    severity: 'danger',
  },
  {
    id: 'n_5',
    kind: 'security',
    title: 'New sign-in from iPhone',
    body: 'Safari on iOS · passkey',
    at: before(9 * HOUR),
    read: true,
    link: '/settings/security',
    severity: 'warning',
  },
  {
    id: 'n_6',
    kind: 'schedule',
    title: 'Nightly dependency review finished',
    body: '3 updates proposed in orbit-api',
    at: before(20 * HOUR),
    read: true,
    link: '/settings/schedules',
    agent: 'codex',
  },
  {
    id: 'n_7',
    kind: 'done',
    title: 'Gemini CLI finished',
    body: 'Docs pass on the onboarding guide',
    at: before(2 * DAY),
    read: true,
    link: '/agents',
    agent: 'gemini',
  },
]

export const notifySettings: NotifySettings = {
  rules: {
    attention: true,
    done: true,
    exited: true,
    preview: false,
    security: true,
    system: true,
    schedule: true,
    custom: true,
  },
  quietStart: '23:00',
  quietEnd: '07:30',
  devices: 2,
  vapidKey: 'BMockVapidPublicKeyForDevelopmentOnly000000000000000000000000000000000000000000000',
}

// ---------------------------------------------------------------- clip / snippets / notes

export const clips: Clip[] = [
  {
    id: 'c_1',
    text: 'go test ./internal/share/... -run TestLink -v',
    source: 'terminal',
    at: before(4 * MIN),
    size: 44,
  },
  {
    id: 'c_2',
    text: 'https://5173.atlas.example.test/settings',
    source: 'web',
    at: before(30 * MIN),
    size: 40,
  },
  { id: 'c_3', text: 'export RELAY_HOME=~/.relay-dev', source: 'cli', at: before(2 * HOUR), size: 30 },
  {
    id: 'c_4',
    text: 'SELECT id, title FROM sessions ORDER BY updated_at DESC LIMIT 20;',
    source: 'desktop',
    at: before(1 * DAY),
    size: 66,
  },
]

export const snippets: Snippet[] = [
  {
    id: 'sn_1',
    name: 'Review this diff',
    body: 'Review the staged diff in {{path}}. Point out bugs first, then style. Be brief.',
    kind: 'prompt',
    tags: ['review'],
    updatedAt: before(3 * DAY),
    uses: 41,
  },
  {
    id: 'sn_2',
    name: 'Write table tests',
    body: 'Add table-driven tests for {{function}} covering edge cases and errors.',
    kind: 'prompt',
    tags: ['tests'],
    agent: 'claude',
    updatedAt: before(6 * DAY),
    uses: 18,
  },
  {
    id: 'sn_3',
    name: 'Tail relay logs',
    body: 'journalctl --user -fu relay -n 200',
    kind: 'command',
    tags: ['ops'],
    updatedAt: before(12 * DAY),
    uses: 9,
  },
]

export const notes: Note[] = [
  {
    id: 'no_1',
    title: 'Release checklist',
    text: '- bump version\n- changelog\n- tag + push\n- verify install script',
    updatedAt: before(1 * DAY),
  },
  {
    id: 'no_2',
    title: 'Ideas',
    text: 'Share a read-only terminal link with a QR code.\nNight shift: summarise overnight runs at 8am.',
    updatedAt: before(4 * DAY),
  },
]

// ---------------------------------------------------------------- schedules / scripts / toolbox

const run = (
  id: string,
  scheduleId: string,
  ago: number,
  status: ScheduleRun['status'],
  exitCode?: number,
): ScheduleRun => ({
  id,
  scheduleId,
  startedAt: before(ago),
  finishedAt: status === 'running' ? undefined : before(ago - 4 * MIN),
  status,
  exitCode,
  output:
    status === 'ok'
      ? 'Done. 3 updates proposed.'
      : status === 'failed'
        ? 'npm ERR! network timeout'
        : undefined,
})

export const schedules: Schedule[] = [
  {
    id: 'sc_1',
    name: 'Nightly dependency review',
    cron: '0 2 * * 1-5',
    timezone: 'Europe/Berlin',
    cwd: W('orbit-api'),
    agent: 'codex',
    prompt: 'Check for outdated dependencies, propose safe upgrades, run tests.',
    mode: 'headless',
    enabled: true,
    notify: true,
    nextRun: before(-(14 * HOUR)),
    lastRun: run('sr_1', 'sc_1', 20 * HOUR, 'ok', 0),
    createdAt: before(30 * DAY),
  },
  {
    id: 'sc_2',
    name: 'Backup notes',
    cron: '30 23 * * *',
    cwd: W('field-notes'),
    command: ['make', 'backup'],
    mode: 'headless',
    enabled: true,
    notify: false,
    nextRun: before(-(8 * HOUR)),
    lastRun: run('sr_2', 'sc_2', 16 * HOUR, 'failed', 1),
    createdAt: before(60 * DAY),
  },
  {
    id: 'sc_3',
    name: 'Weekly summary',
    cron: '0 8 * * 1',
    cwd: HOME,
    agent: 'claude',
    prompt: 'Summarise last week’s commits across my workspaces.',
    mode: 'interactive',
    enabled: false,
    notify: true,
    createdAt: before(10 * DAY),
  },
]

export const scheduleRuns: ScheduleRun[] = [
  run('sr_1', 'sc_1', 20 * HOUR, 'ok', 0),
  run('sr_0', 'sc_1', 44 * HOUR, 'ok', 0),
  run('sr_2', 'sc_2', 16 * HOUR, 'failed', 1),
]

export const scripts: ScriptCommand[] = [
  {
    id: 'deploy-preview',
    title: 'Deploy preview',
    description: 'Build and upload the preview site',
    icon: 'upload',
    mode: 'terminal',
    path: `${HOME}/.config/relay/commands/deploy-preview.sh`,
    cwd: W('relay-demo'),
  },
  {
    id: 'ip',
    title: 'Public IP',
    description: 'Show this machine’s public address',
    mode: 'inline',
    path: `${HOME}/.config/relay/commands/ip.sh`,
  },
  {
    id: 'open-pr',
    title: 'Open pull request',
    mode: 'silent',
    args: [
      { name: 'title', placeholder: 'Title' },
      { name: 'base', placeholder: 'Base', optional: true },
    ],
    path: `${HOME}/.config/relay/commands/open-pr.sh`,
  },
]

export const tools: Tool[] = [
  {
    id: 'claude',
    name: 'Claude Code',
    category: 'agents',
    description: 'Anthropic’s coding agent for the terminal.',
    installed: true,
    version: '2.0.14',
    installable: true,
    requiresSudo: false,
    size: '48 MB',
  },
  {
    id: 'codex',
    name: 'Codex',
    category: 'agents',
    description: 'OpenAI’s coding agent CLI.',
    installed: true,
    version: '0.44.0',
    installable: true,
    requiresSudo: false,
    size: '31 MB',
  },
  {
    id: 'aider',
    name: 'Aider',
    category: 'agents',
    description: 'Pair programming in your terminal.',
    installed: false,
    installable: true,
    requiresSudo: false,
    size: '120 MB',
  },
  {
    id: 'node',
    name: 'Node.js 22',
    category: 'runtimes',
    description: 'JavaScript runtime (via fnm).',
    installed: true,
    version: '22.11.0',
    installable: true,
    requiresSudo: false,
  },
  {
    id: 'go',
    name: 'Go',
    category: 'runtimes',
    description: 'The Go toolchain.',
    installed: true,
    version: '1.25.1',
    installable: true,
    requiresSudo: false,
  },
  {
    id: 'chrome',
    name: 'Google Chrome',
    category: 'browsers',
    description: 'For the desktop and headless testing.',
    installed: false,
    installable: true,
    requiresSudo: true,
    size: '310 MB',
  },
  {
    id: 'blender',
    name: 'Blender',
    category: 'creative',
    description: '3D creation suite for the desktop.',
    installed: false,
    installable: true,
    requiresSudo: false,
    size: '380 MB',
  },
  {
    id: 'ripgrep',
    name: 'ripgrep',
    category: 'cli',
    description: 'Fast content search (used by Files).',
    installed: true,
    version: '14.1.1',
    installable: true,
    requiresSudo: false,
  },
]

export const mcpServers: MCPServer[] = [
  {
    id: 'playwright',
    name: 'Playwright',
    description: 'Browser automation for agents.',
    command: ['npx', '@playwright/mcp@latest'],
    agents: { claude: true, codex: false, gemini: false },
  },
  {
    id: 'context7',
    name: 'Context7',
    description: 'Up-to-date library docs.',
    command: ['npx', '-y', '@upstash/context7-mcp'],
    agents: { claude: true, codex: true, gemini: false },
  },
]

export const services: Service[] = [
  {
    name: 'relay.service',
    description: 'Relay web server',
    active: 'active',
    sub: 'running',
    since: before(3 * DAY + 4 * HOUR),
    restarts: 0,
    user: true,
    managed: true,
  },
  {
    name: 'relay-ptyd.service',
    description: 'Relay session daemon',
    active: 'active',
    sub: 'running',
    since: before(12 * DAY),
    restarts: 0,
    user: true,
    managed: true,
  },
  {
    name: 'code-server.service',
    description: 'VS Code in the browser',
    active: 'active',
    sub: 'running',
    since: before(2 * HOUR),
    restarts: 1,
    user: true,
    managed: true,
  },
  {
    name: 'relay-desktop.service',
    description: 'Xvnc desktop session',
    active: 'inactive',
    sub: 'dead',
    restarts: 0,
    user: true,
    managed: true,
  },
  {
    name: 'syncthing.service',
    description: 'File synchronisation',
    active: 'active',
    sub: 'running',
    since: before(12 * DAY),
    restarts: 0,
    user: true,
    managed: false,
  },
  {
    name: 'backup.timer',
    description: 'Nightly restic backup',
    active: 'active',
    sub: 'waiting',
    since: before(12 * DAY),
    restarts: 0,
    user: true,
    managed: false,
  },
  {
    name: 'ollama.service',
    description: 'Local models',
    active: 'failed',
    sub: 'failed',
    since: before(5 * HOUR),
    restarts: 3,
    user: true,
    managed: false,
  },
]

// Snapshots are detached from mutable objects; exports keep their identity for consumers.
const fixtureStore: Record<string, object> = {
  info,
  settings,
  auth,
  passkeys,
  deviceSessions,
  tokens,
  audit,
  workspaces,
  terminals,
  agents,
  agentSessions,
  quotas,
  previews,
  apps,
  desktop,
  notifications,
  notifySettings,
  clips,
  snippets,
  notes,
  schedules,
  scheduleRuns,
  scripts,
  tools,
  mcpServers,
  services,
}
const baseline = structuredClone(fixtureStore)
export function resetData(): void {
  for (const key of Object.keys(fixtureStore)) {
    const target = fixtureStore[key]
    const fresh = structuredClone(baseline[key])
    if (Array.isArray(target) && Array.isArray(fresh)) target.splice(0, target.length, ...fresh)
    else {
      for (const property of Object.keys(target)) Reflect.deleteProperty(target, property)
      Object.assign(target, fresh)
    }
  }
  auth.state.authenticated = !has('signed-out') && !has('setup') && !has('totp')
  auth.state.setupRequired = has('setup')
  auth.state.methods.totp = has('totp')
  if (has('empty')) for (const list of [terminals, agentSessions, previews, notifications]) list.length = 0
}
resetData()
