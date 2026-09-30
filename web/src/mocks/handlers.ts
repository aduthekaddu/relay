// Route table for the mock backend: every endpoint in docs/dev/API.md.
import type {
  AgentSession,
  CreateTerminalRequest,
  LaunchAgentRequest,
  LoginRequest,
  Notification,
  SearchResult,
  SearchScope,
  TerminalSession,
  UsageSummary,
} from '../api/types'
import * as db from './data'
import * as fs from './fs'
import { emit } from './sockets'
import { history, metricsAt, processes } from './system'
import {
  accepted,
  before,
  fail,
  HOUR,
  type MockResponse,
  modes,
  newId,
  noContent,
  notFound,
  ok,
  setModes,
} from './util'

export interface Req {
  method: string
  url: URL
  params: Record<string, string>
  body: unknown
}

type Handler = (req: Req) => MockResponse | Promise<MockResponse>

interface RouteEntry {
  method: string
  re: RegExp
  keys: string[]
  fn: Handler
}

const routes: RouteEntry[] = []

function route(method: string, pattern: string, fn: Handler): void {
  const keys: string[] = []
  const src = pattern.replace(/\{(\w+)\}/g, (_, k: string) => {
    keys.push(k)
    return '([^/]+)'
  })
  routes.push({ method, re: new RegExp(`^/api/v1${src}$`), keys, fn })
}

/** Find the handler for a request (null → 404). */
export function match(method: string, path: string): { fn: Handler; params: Record<string, string> } | null {
  for (const r of routes) {
    if (r.method !== method && !(r.method === 'GET' && method === 'HEAD')) continue
    const m = r.re.exec(path)
    if (!m) continue
    const params: Record<string, string> = {}
    r.keys.forEach((k, i) => {
      params[k] = decodeURIComponent(m[i + 1])
    })
    return { fn: r.fn, params }
  }
  return null
}

const body = <T>(req: Req) => (req.body ?? {}) as T
const q = (req: Req, k: string) => req.url.searchParams.get(k)
const qn = (req: Req, k: string, d: number) => {
  const v = Number(q(req, k))
  return Number.isFinite(v) && v > 0 ? v : d
}
const now = () => new Date().toISOString()

/** Signing in clears the signed-out scenarios so a reload stays signed in. */
function signedIn(user: string): void {
  db.auth.state = { ...db.auth.state, authenticated: true, setupRequired: false, user }
  setModes([...modes].filter((m) => m !== 'signed-out' && m !== 'setup' && m !== 'totp'))
}

function requireAuth(): MockResponse | null {
  return db.auth.state.authenticated ? null : fail(401, 'unauthorized', 'Sign in to continue.')
}

// ---------------------------------------------------------------- info & settings

route('GET', '/health', () => ok({ ok: true, version: db.info.version }))
route('GET', '/info', () => ok(db.info))
route('GET', '/settings', () => ok(db.settings))
route('PATCH', '/settings', (r) => ok(Object.assign(db.settings, body(r))))

// ---------------------------------------------------------------- auth

route('GET', '/auth/state', () => ok(db.auth.state))
route('POST', '/auth/setup', (r) => {
  const b = body<{ username?: string; password?: string }>(r)
  if (!db.auth.state.setupRequired) return fail(409, 'already_setup', 'An account already exists.')
  if (!b.username?.trim()) return fail(400, 'invalid', 'Choose a username.', { field: 'username' })
  if ((b.password ?? '').length < 10)
    return fail(400, 'weak_password', 'Use at least 10 characters.', { field: 'password' })
  signedIn(b.username.trim())
  return ok({ ok: true, setupPasskey: true })
})
route('POST', '/auth/login', (r) => {
  const b = body<LoginRequest>(r)
  if (Date.now() < db.auth.lockedUntil) {
    const retryIn = Math.ceil((db.auth.lockedUntil - Date.now()) / 1000)
    return fail(429, 'rate_limited', 'Too many attempts. Try again soon.', { retryIn })
  }
  // Any username works; the password "wrong" (or empty) fails.
  if (!b.password || b.password === 'wrong') {
    db.auth.failures++
    if (db.auth.failures >= 3) {
      db.auth.lockedUntil = Date.now() + 30_000
      db.auth.failures = 0
      return fail(429, 'rate_limited', 'Too many attempts. Try again soon.', { retryIn: 30 })
    }
    return fail(401, 'bad_credentials', 'That username and password don’t match.')
  }
  if (db.auth.state.methods.totp) {
    if (!b.totp) return ok({ ok: false, needTotp: true })
    if (b.totp !== '123456')
      return fail(401, 'bad_totp', 'That code didn’t work. Codes change every 30 seconds.', { field: 'totp' })
  }
  db.auth.failures = 0
  signedIn(b.username || 'dev')
  return ok({ ok: true })
})
route('POST', '/auth/logout', () => {
  db.auth.state = { ...db.auth.state, authenticated: false, user: undefined }
  setModes([...modes.add('signed-out')])
  return noContent()
})
route('POST', '/auth/passkey/begin', () =>
  ok({
    challenge: 'bW9jay1jaGFsbGVuZ2UtZm9yLWRldmVsb3BtZW50',
    rpId: location.hostname,
    timeout: 60000,
    userVerification: 'preferred',
    allowCredentials: [],
  }),
)
route('POST', '/auth/passkey/finish', () => {
  signedIn('dev')
  return ok({ ok: true })
})
route('GET', '/auth/passkeys', () => ok(db.passkeys))
route('POST', '/auth/passkeys/begin', (r) =>
  ok({
    challenge: 'bW9jay1jaGFsbGVuZ2U',
    rp: { id: location.hostname, name: 'Relay' },
    user: { id: 'ZGV2', name: 'dev', displayName: body<{ name?: string }>(r).name ?? 'dev' },
    pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
    authenticatorSelection: { residentKey: 'required', userVerification: 'preferred' },
    timeout: 60000,
  }),
)
route('POST', '/auth/passkeys/finish', () => {
  const pk = { id: newId('pk'), name: 'New passkey', createdAt: now() }
  db.passkeys.push(pk)
  return ok(pk)
})
route('PATCH', '/auth/passkeys/{id}', (r) => {
  const pk = db.passkeys.find((p) => p.id === r.params.id)
  if (!pk) return notFound('Passkey not found')
  pk.name = body<{ name: string }>(r).name ?? pk.name
  return ok(pk)
})
route('DELETE', '/auth/passkeys/{id}', (r) => {
  const i = db.passkeys.findIndex((p) => p.id === r.params.id)
  if (i >= 0) db.passkeys.splice(i, 1)
  return noContent()
})
route('POST', '/auth/password', (r) => {
  const b = body<{ current?: string; next?: string }>(r)
  if (b.current === 'wrong')
    return fail(401, 'bad_credentials', 'Your current password is incorrect.', { field: 'current' })
  if ((b.next ?? '').length < 10)
    return fail(400, 'weak_password', 'Use at least 10 characters.', { field: 'next' })
  return noContent()
})
let totpEnabled = db.auth.state.methods.totp
route('GET', '/auth/totp', () => ok({ enabled: totpEnabled }))
route('POST', '/auth/totp/setup', () =>
  ok({
    secret: 'JBSWY3DPEHPK3PXP',
    otpauthUrl: 'otpauth://totp/Relay:dev?secret=JBSWY3DPEHPK3PXP&issuer=Relay',
  }),
)
route('POST', '/auth/totp/enable', (r) => {
  if (body<{ code?: string }>(r).code !== '123456')
    return fail(400, 'bad_totp', 'That code didn’t work.', { field: 'code' })
  totpEnabled = true
  return noContent()
})
route('POST', '/auth/totp/disable', () => {
  totpEnabled = false
  return noContent()
})
route('GET', '/auth/sessions', () => ok(db.deviceSessions))
route('DELETE', '/auth/sessions/{id}', (r) => {
  const i = db.deviceSessions.findIndex((s) => s.id === r.params.id && !s.current)
  if (i >= 0) db.deviceSessions.splice(i, 1)
  return noContent()
})
route('POST', '/auth/sessions/revoke-others', () => {
  const n = db.deviceSessions.filter((s) => !s.current).length
  db.deviceSessions.splice(0, db.deviceSessions.length, ...db.deviceSessions.filter((s) => s.current))
  return ok({ revoked: n })
})
route('GET', '/auth/tokens', () => ok(db.tokens))
route('POST', '/auth/tokens', (r) => {
  const tk = {
    id: newId('tk'),
    name: body<{ name?: string }>(r).name || 'Untitled token',
    prefix: 'rly_9z1x',
    createdAt: now(),
  }
  db.tokens.push(tk)
  return ok({ ...tk, token: 'rly_9z1x_mock_token_value_shown_once_0000000000' })
})
route('DELETE', '/auth/tokens/{id}', (r) => {
  const i = db.tokens.findIndex((t) => t.id === r.params.id)
  if (i >= 0) db.tokens.splice(i, 1)
  return noContent()
})
route('GET', '/auth/activity', (r) => ok(db.audit.slice(0, qn(r, 'limit', 50))))

// ---------------------------------------------------------------- terminals & uploads

const termById = (id: string) => db.terminals.find((t) => t.id === id)

route('GET', '/terminals', () => ok(db.terminals))
route('POST', '/terminals', (r) => {
  const b = body<CreateTerminalRequest>(r)
  const t: TerminalSession = {
    id: newId('t'),
    name: b.name || (b.command?.join(' ') ?? 'zsh'),
    kind: b.kind ?? 'shell',
    agent: b.agent,
    command: b.command ?? ['/bin/zsh', '-l'],
    cwd: fs.normalize(b.cwd),
    cols: b.cols ?? 120,
    rows: b.rows ?? 34,
    clients: 0,
    activity: 'idle',
    recording: !!b.record,
    pinned: false,
    meta: b.meta,
    createdAt: now(),
    pid: 50000 + Math.floor(Math.random() * 9000),
  }
  db.terminals.unshift(t)
  emit('terminal.created', t)
  return ok(t)
})
route('GET', '/terminals/{id}', (r) => {
  const t = termById(r.params.id)
  return t ? ok(t) : notFound('Session not found')
})
route('PATCH', '/terminals/{id}', (r) => {
  const t = termById(r.params.id)
  if (!t) return notFound('Session not found')
  Object.assign(t, body(r))
  emit('terminal.updated', t)
  return ok(t)
})
route('DELETE', '/terminals/{id}', (r) => {
  const t = termById(r.params.id)
  if (!t) return notFound('Session not found')
  if (q(r, 'forget') === '1' || t.activity === 'exited') {
    db.terminals.splice(db.terminals.indexOf(t), 1)
    emit('terminal.removed', { id: t.id })
  } else {
    Object.assign(t, { activity: 'exited', exitCode: 143, exitedAt: now(), attention: undefined })
    emit('terminal.exited', t)
  }
  return noContent()
})
route('POST', '/terminals/{id}/input', (r) => (termById(r.params.id) ? noContent() : notFound()))
route('POST', '/terminals/{id}/resize', (r) => (termById(r.params.id) ? noContent() : notFound()))
route('GET', '/terminals/{id}/snapshot', (r) => {
  const t = termById(r.params.id)
  if (!t) return notFound()
  return ok({
    text: `dev@atlas ${t.cwd.replace(db.HOME, '~')} % ${t.command.join(' ')}\n${t.preview ?? ''}\n`,
    cols: t.cols,
    rows: t.rows,
  })
})
route('POST', '/terminals/{id}/attention/ack', (r) => {
  const t = termById(r.params.id)
  if (!t) return notFound()
  t.attention = undefined
  if (t.activity === 'waiting') t.activity = 'idle'
  emit('terminal.updated', t)
  return noContent()
})
route('GET', '/terminals/{id}/recording', (r) => {
  const t = termById(r.params.id)
  if (!t) return notFound()
  const header = JSON.stringify({
    version: 2,
    width: t.cols,
    height: t.rows,
    timestamp: Math.floor(Date.parse(t.createdAt) / 1000),
  })
  const lines = ['$ ', 'g', 'o', ' ', 't', 'e', 's', 't', '\r\n', 'ok  \trelay-demo\t0.4s\r\n'].map((s, i) =>
    JSON.stringify([i * 0.12, 'o', s]),
  )
  return { text: [header, ...lines].join('\n'), headers: { 'Content-Type': 'application/x-asciicast' } }
})

const uploads = new Map<string, { size: number; received: number; name: string; dir?: string }>()
route('POST', '/uploads', (r) => {
  const b = body<{ name: string; size: number; dir?: string }>(r)
  const id = newId('up')
  uploads.set(id, { size: b.size, received: 0, name: b.name, dir: b.dir })
  return ok({ id, chunkSize: 4 * 1024 * 1024, received: 0, size: b.size })
})
route('PUT', '/uploads/{id}', async (r) => {
  const u = uploads.get(r.params.id)
  if (!u) return notFound('Upload not found')
  const chunk = r.body instanceof Blob ? r.body.size : r.body instanceof ArrayBuffer ? r.body.byteLength : 0
  u.received = Math.min(u.size, Number(q(r, 'offset') ?? 0) + chunk)
  return ok({ id: r.params.id, chunkSize: 4 * 1024 * 1024, received: u.received, size: u.size })
})
route('POST', '/uploads/{id}/complete', (r) => {
  const u = uploads.get(r.params.id)
  if (!u) return notFound('Upload not found')
  uploads.delete(r.params.id)
  const dir = fs.normalize(u.dir ?? `${db.HOME}/Downloads`)
  fs.mkdir(`${dir}/${u.name}`, 'file')
  return ok({ path: `${dir}/${u.name}`, name: u.name, size: u.size })
})
route('DELETE', '/uploads/{id}', (r) => {
  uploads.delete(r.params.id)
  return noContent()
})

// ---------------------------------------------------------------- agents

const sessionById = (id: string) => db.agentSessions.find((s) => s.id === id)

route('GET', '/agents', () => ok(db.agents))
route('GET', '/agents/sessions', (r) => {
  const agent = q(r, 'agent')
  const text = (q(r, 'q') ?? '').toLowerCase()
  const cwd = q(r, 'cwd')
  const status = q(r, 'status') ?? 'all'
  const archived = q(r, 'archived') === '1'
  const pinned = q(r, 'pinned') === '1'
  let list = db.agentSessions.filter(
    (s) =>
      (!agent || s.agent === agent) &&
      (!cwd || s.cwd.startsWith(fs.normalize(cwd))) &&
      (status === 'all' || s.status === status) &&
      s.archived === archived &&
      (!pinned || s.pinned) &&
      (!text || s.title.toLowerCase().includes(text) || s.cwd.toLowerCase().includes(text)),
  )
  list = list.sort(
    (a, b) =>
      Number(b.status === 'live') - Number(a.status === 'live') || b.updatedAt.localeCompare(a.updatedAt),
  )
  const limit = qn(r, 'limit', 30)
  const start = Number(q(r, 'cursor') ?? 0)
  const items = list.slice(start, start + limit)
  return ok({
    items,
    total: list.length,
    nextCursor: start + limit < list.length ? String(start + limit) : undefined,
  })
})
route('GET', '/agents/sessions/{id}', (r) => {
  const s = sessionById(r.params.id)
  return s ? ok(s) : notFound('Session not found')
})
route('PATCH', '/agents/sessions/{id}', (r) => {
  const s = sessionById(r.params.id)
  if (!s) return notFound('Session not found')
  Object.assign(s, body(r))
  emit('agents.session', s)
  return ok(s)
})
route('GET', '/agents/sessions/{id}/transcript', (r) => {
  const s = sessionById(r.params.id)
  if (!s) return notFound('Session not found')
  return ok({ session: s, messages: db.transcriptFor(s), hasMore: false })
})
function agentTerminal(agent: string, cwd: string, name: string, sessionId?: string): TerminalSession {
  const t: TerminalSession = {
    id: newId('t'),
    name,
    kind: 'agent',
    agent,
    agentSessionId: sessionId,
    command: [agent],
    cwd,
    workspace: cwd,
    cols: 120,
    rows: 34,
    clients: 0,
    activity: 'working',
    recording: true,
    pinned: false,
    createdAt: now(),
    lastOutputAt: now(),
    preview: 'Starting…',
  }
  db.terminals.unshift(t)
  emit('terminal.created', t)
  return t
}
route('POST', '/agents/sessions/{id}/resume', (r) => {
  const s = sessionById(r.params.id)
  if (!s) return notFound('Session not found')
  if (!s.resumable) return fail(409, 'not_resumable', `${s.agent} can’t resume this session.`)
  const t = agentTerminal(s.agent, s.cwd, `${s.agent} · ${s.title}`, s.id)
  Object.assign(s, { status: 'live', terminalId: t.id, activity: 'working' } satisfies Partial<AgentSession>)
  return ok(t)
})
route('POST', '/agents/launch', (r) => {
  const b = body<LaunchAgentRequest>(r)
  if (!db.agents.find((a) => a.id === b.agent)?.installed)
    return fail(400, 'not_installed', 'That agent isn’t installed.', { field: 'agent' })
  return ok(
    agentTerminal(
      b.agent,
      fs.normalize(b.cwd),
      b.name || `${b.agent} · ${b.prompt?.slice(0, 32) || 'new session'}`,
    ),
  )
})
route('GET', '/agents/search', (r) => {
  const text = (q(r, 'q') ?? '').toLowerCase()
  const hits = db.agentSessions
    .filter((s) => !text || s.title.toLowerCase().includes(text))
    .slice(0, qn(r, 'limit', 20))
    .map((s) => ({
      sessionId: s.id,
      agent: s.agent,
      title: s.title,
      cwd: s.cwd,
      role: 'user',
      snippet: `…${s.title.toLowerCase()}. Keep the public API unchanged…`,
      at: s.updatedAt,
      messageId: 'm1',
    }))
  return ok(hits)
})
route('GET', '/agents/usage', (r) => ok(db.usageFor((q(r, 'range') as UsageSummary['range']) ?? '7d')))
route('GET', '/agents/quotas', () => ok(db.quotas))
route('POST', '/agents/{agent}/hooks', (r) => {
  const a = db.agents.find((x) => x.id === r.params.agent)
  if (!a) return notFound('Unknown agent')
  a.hooks = { installed: true, path: `~/.${a.id}/settings.json`, detail: 'Backup saved next to the file.' }
  return ok(a.hooks)
})
route('DELETE', '/agents/{agent}/hooks', (r) => {
  const a = db.agents.find((x) => x.id === r.params.agent)
  if (!a) return notFound('Unknown agent')
  a.hooks = { installed: false }
  return ok(a.hooks)
})
route('POST', '/agents/reindex', () => {
  setTimeout(() => emit('agents.indexed', { sessions: db.agentSessions.length }), 1200)
  return accepted()
})

// ---------------------------------------------------------------- workspaces & git

function gitStatus(path: string) {
  const w = db.workspaces.find((x) => path.startsWith(x.path)) ?? db.workspaces[0]
  const files =
    w.git && w.git.dirty > 0
      ? [
          { path: 'internal/share/link.go', index: ' ', work: 'M', staged: false, added: 12, removed: 3 },
          { path: 'internal/share/link_test.go', index: 'A', work: ' ', staged: true, added: 64, removed: 0 },
          { path: 'internal/share/sign.go', index: '?', work: '?', staged: false, added: 31, removed: 0 },
          { path: 'package.json', index: 'M', work: ' ', staged: true, added: 1, removed: 1 },
          {
            path: 'docs/diagram.png',
            index: ' ',
            work: 'M',
            staged: false,
            added: 0,
            removed: 0,
            binary: true,
          },
        ].slice(0, Math.max(1, Math.min(5, w.git.dirty)))
      : []
  return {
    path,
    root: w.path,
    branch: w.git?.branch ?? 'main',
    upstream: w.git?.remote ? `${w.git.remote}/${w.git.branch}` : undefined,
    ahead: w.git?.ahead ?? 0,
    behind: w.git?.behind ?? 0,
    files,
    last: w.git?.last,
    worktree: false,
    stashes: 1,
    remotes: ['origin'],
    worktrees: [{ path: w.path, branch: w.git?.branch ?? 'main', head: w.git?.last?.hash ?? '', main: true }],
  }
}
const DIFF = `diff --git a/internal/share/link.go b/internal/share/link.go
--- a/internal/share/link.go
+++ b/internal/share/link.go
@@ -8,6 +8,8 @@ import (
 // Link is a signed, expiring pointer to a session.
 type Link struct {
 	ID      string
 	Expires time.Time
+	// ReadOnly links can watch but never type.
+	ReadOnly bool
 }
@@ -20,7 +22,7 @@ func (l Link) Valid(t time.Time) bool {
-	return t.Before(l.Expires)
+	return !l.Expires.IsZero() && t.Before(l.Expires)
 }
`

route('GET', '/workspaces', () => ok(db.workspaces))
route('POST', '/workspaces/pin', (r) => {
  const b = body<{ path: string; pinned: boolean }>(r)
  const w = db.workspaces.find((x) => x.path === fs.normalize(b.path))
  if (!w) return notFound('Workspace not found')
  w.pinned = b.pinned
  emit('workspace.changed', w)
  return ok(w)
})
route('GET', '/workspaces/git/status', (r) => ok(gitStatus(fs.normalize(q(r, 'path')))))
route('GET', '/workspaces/git/diff', (r) =>
  ok({
    path: fs.normalize(q(r, 'path')),
    file: q(r, 'file') ?? undefined,
    staged: q(r, 'staged') === '1',
    diff: DIFF,
  }),
)
route('GET', '/workspaces/git/log', (r) =>
  ok(
    Array.from({ length: Math.min(30, qn(r, 'limit', 30)) }, (_, i) => ({
      hash: `${(0xabcdef12 + i * 7919).toString(16)}00`,
      short: (0xabcdef12 + i * 7919).toString(16).slice(0, 7),
      subject: [
        'Share a read-only session link',
        'Extract signing helpers',
        'Add link expiry tests',
        'Tidy imports',
        'Bump deps',
      ][i % 5],
      author: 'Dev',
      at: before(i * 5 * HOUR + 40 * 60),
    })),
  ),
)
for (const action of ['stage', 'unstage', 'discard', 'commit']) {
  route('POST', `/workspaces/git/${action}`, (r) =>
    ok(gitStatus(fs.normalize(body<{ path: string }>(r).path))),
  )
}
route('POST', '/workspaces/git/push', () => accepted())
route('POST', '/workspaces/git/pull', () => accepted())
route('POST', '/workspaces/git/worktrees', (r) => {
  const b = body<{ path: string; branch: string }>(r)
  return ok({
    path: `${fs.normalize(b.path)}-${b.branch.replace(/\W+/g, '-')}`,
    branch: b.branch,
    head: 'e4f1a9c2b7',
    main: false,
  })
})
route('DELETE', '/workspaces/git/worktrees', () => noContent())

// ---------------------------------------------------------------- files

const notInRoot = () => fail(403, 'outside_root', 'That path is outside the files root.')
const inRoot = (p: string | null) => fs.normalize(p).startsWith(db.HOME)

route('GET', '/files/list', (r) => {
  if (!inRoot(q(r, 'path'))) return notInRoot()
  const l = fs.list(q(r, 'path') ?? '~', {
    hidden: q(r, 'hidden') === '1' || q(r, 'hidden') === 'true',
    sort: q(r, 'sort') ?? 'name',
    desc: q(r, 'desc') === '1' || q(r, 'desc') === 'true',
    offset: Number(q(r, 'offset') ?? 0),
    limit: qn(r, 'limit', 500),
  })
  return l ? ok(l) : notFound('No such folder')
})
route('GET', '/files/stat', (r) => {
  const e = fs.stat(q(r, 'path') ?? '')
  return e ? ok(e) : notFound('No such file')
})
route('GET', '/files/raw', (r) => {
  const p = q(r, 'path') ?? ''
  const e = fs.stat(p)
  if (!e || e.type === 'dir') return notFound('No such file')
  const headers: Record<string, string> = {
    'Content-Security-Policy': 'sandbox',
    'X-Content-Type-Options': 'nosniff',
  }
  if (q(r, 'download') === '1') headers['Content-Disposition'] = `attachment; filename="${e.name}"`
  if (e.mime?.startsWith('image/'))
    return {
      text: fs.placeholderImage(e.path, 1024),
      headers: { ...headers, 'Content-Type': 'image/svg+xml' },
    }
  const t = fs.readText(p)
  return {
    text: t?.text ?? `(binary file: ${e.name})`,
    headers: { ...headers, 'Content-Type': e.mime ?? 'application/octet-stream' },
  }
})
route('GET', '/files/text', (r) => {
  const t = fs.readText(q(r, 'path') ?? '')
  return t ? ok(t) : fail(415, 'not_text', 'This file isn’t text.')
})
route('PUT', '/files/text', (r) => {
  const res = fs.writeText(
    q(r, 'path') ?? '',
    body<{ text: string }>(r).text ?? '',
    q(r, 'mtime') ?? undefined,
  )
  if (res === 'conflict') return fail(409, 'conflict', 'The file changed on disk since you opened it.')
  return res ? ok(res) : notFound()
})
route('GET', '/files/thumb', (r) => ({
  text: fs.placeholderImage(q(r, 'path') ?? '', qn(r, 'size', 256)),
  headers: { 'Content-Type': 'image/svg+xml' },
}))
route('POST', '/files/mkdir', (r) => {
  const e = fs.mkdir(body<{ path: string }>(r).path, 'dir')
  return e ? ok(e) : fail(409, 'exists', 'Something with that name already exists.')
})
route('POST', '/files/touch', (r) => {
  const e = fs.mkdir(body<{ path: string }>(r).path, 'file')
  return e ? ok(e) : fail(409, 'exists', 'Something with that name already exists.')
})
route('POST', '/files/rename', (r) => {
  const b = body<{ path: string; name: string }>(r)
  const e = fs.rename(b.path, b.name)
  return e ? ok(e) : fail(409, 'exists', 'Something with that name already exists.', { field: 'name' })
})
route('POST', '/files/move', (r) => {
  const b = body<{ from: string[]; to: string }>(r)
  return ok(fs.move(b.from ?? [], b.to))
})
route('POST', '/files/copy', () => accepted())
route('POST', '/files/delete', (r) => {
  const b = body<{ paths: string[]; trash?: boolean }>(r)
  fs.remove(b.paths ?? [], b.trash !== false)
  return noContent()
})
route('GET', '/files/trash', () => ok(fs.trashList()))
route('POST', '/files/trash/restore', (r) => {
  fs.restore(body<{ paths: string[] }>(r).paths ?? [])
  return noContent()
})
route('POST', '/files/trash/empty', () => {
  fs.emptyTrash()
  return noContent()
})
route('GET', '/files/zip', () => ({
  body: new Blob(['PK\x05\x06'.padEnd(22, '\0')], { type: 'application/zip' }),
  headers: { 'Content-Type': 'application/zip', 'Content-Disposition': 'attachment; filename="files.zip"' },
}))
route('GET', '/files/usage', (r) => ok(fs.usage(q(r, 'path') ?? '~')))
route('GET', '/files/search', (r) => ({
  stream: fs.search(q(r, 'q') ?? '', q(r, 'path') ?? '~', q(r, 'content') === '1', qn(r, 'limit', 50)),
  streamDelay: 25,
}))

// ---------------------------------------------------------------- system

route('GET', '/system/metrics', () => ok(metricsAt(Date.now())))
route('GET', '/system/metrics/history', (r) => ok(history(qn(r, 'minutes', 60))))
route('GET', '/system/processes', (r) => {
  const sort = q(r, 'sort') ?? 'cpu'
  const text = (q(r, 'q') ?? '').toLowerCase()
  const list = processes
    .filter((p) => !text || p.name.includes(text) || p.cmd.toLowerCase().includes(text))
    .map((p) => ({ ...p, cpu: Math.max(0, Math.round((p.cpu + (Math.random() - 0.5) * 2) * 10) / 10) }))
    .sort((a, b) => (sort === 'mem' ? b.rss - a.rss : b.cpu - a.cpu))
  return ok(list.slice(0, qn(r, 'limit', 100)))
})
route('POST', '/system/processes/{pid}/signal', (r) => {
  const p = processes.find((x) => x.pid === Number(r.params.pid))
  if (!p) return notFound('No such process')
  if (p.protected) return fail(403, 'protected', 'Relay won’t signal its own processes.')
  processes.splice(processes.indexOf(p), 1)
  return noContent()
})
route('GET', '/system/services', () => ok(db.services))
route('POST', '/system/services/{name}/{action}', (r) => {
  const s = db.services.find((x) => x.name === r.params.name)
  if (!s) return notFound('No such service')
  if (r.params.action === 'stop') Object.assign(s, { active: 'inactive', sub: 'dead', since: now() })
  else
    Object.assign(s, {
      active: 'active',
      sub: 'running',
      since: now(),
      restarts: s.restarts + (r.params.action === 'restart' ? 1 : 0),
    })
  return ok(s)
})

// ---------------------------------------------------------------- previews / apps / desktop

route('GET', '/previews', () => ok(db.previews))
route('PATCH', '/previews/{port}', (r) => {
  const p = db.previews.find((x) => x.port === Number(r.params.port))
  if (!p) return notFound('No such preview')
  Object.assign(p, body(r))
  emit('previews.changed', db.previews)
  return ok(p)
})
route('GET', '/apps', () => ok(db.apps))
for (const action of ['start', 'stop'] as const) {
  route('POST', `/apps/{id}/${action}`, (r) => {
    const a = db.apps.find((x) => x.id === r.params.id)
    if (!a) return notFound('No such app')
    if (!a.installed) return fail(409, 'not_installed', a.installHint ?? 'Not installed.')
    a.state = action === 'start' ? 'starting' : 'stopped'
    if (action === 'start')
      setTimeout(() => {
        a.state = 'running'
        a.since = now()
        emit('app.state', a)
      }, 1500)
    emit('app.state', a)
    return ok(a)
  })
}
route('GET', '/desktop', () => ok(db.desktop))
route('POST', '/desktop/start', () => {
  db.desktop.state = 'starting'
  setTimeout(() => {
    db.desktop.state = 'running'
    db.desktop.display = ':1'
    emit('desktop.state', db.desktop)
  }, 1800)
  return ok(db.desktop)
})
route('POST', '/desktop/stop', () => {
  Object.assign(db.desktop, { state: 'stopped', display: undefined, viewers: 0 })
  return ok(db.desktop)
})
route('POST', '/desktop/launch', (r) => {
  const app = db.desktop.apps.find((a) => a.id === body<{ app: string }>(r).app)
  if (app) app.running = true
  return ok(db.desktop)
})
let desktopClip = ''
route('POST', '/desktop/clipboard', (r) => {
  desktopClip = body<{ text: string }>(r).text ?? ''
  return noContent()
})
route('GET', '/desktop/clipboard', () => ok({ text: desktopClip }))
route('POST', '/desktop/resize', (r) => ok(Object.assign(db.desktop, body(r))))

// ---------------------------------------------------------------- notifications

route('GET', '/notifications', (r) => {
  const unread = q(r, 'unread') === '1' || q(r, 'unread') === 'true'
  return ok(db.notifications.filter((n) => !unread || !n.read).slice(0, qn(r, 'limit', 50)))
})
route('POST', '/notifications/read', (r) => {
  const b = body<{ ids?: string[]; all?: boolean }>(r)
  for (const n of db.notifications) if (b.all || b.ids?.includes(n.id)) n.read = true
  emit('notification.read', b)
  return noContent()
})
route('DELETE', '/notifications/{id}', (r) => {
  const i = db.notifications.findIndex((n) => n.id === r.params.id)
  if (i >= 0) db.notifications.splice(i, 1)
  return noContent()
})
route('POST', '/notify', (r) => {
  const b = body<{
    title: string
    body?: string
    kind?: string
    link?: string
    agent?: string
    severity?: string
  }>(r)
  const n = {
    id: newId('n'),
    kind: (b.kind ?? 'custom') as Notification['kind'],
    title: b.title,
    body: b.body,
    at: now(),
    read: false,
    link: b.link,
    agent: b.agent,
    severity: b.severity as Notification['severity'],
  }
  db.notifications.unshift(n)
  emit('notification', n)
  return ok(n)
})
route('GET', '/notify/settings', () => ok(db.notifySettings))
route('PATCH', '/notify/settings', (r) => ok(Object.assign(db.notifySettings, body(r))))
route('GET', '/push/key', () => ok({ publicKey: db.notifySettings.vapidKey }))
route('POST', '/push/subscribe', () => {
  db.notifySettings.devices++
  return noContent()
})
route('POST', '/push/unsubscribe', () => noContent())
route('POST', '/push/test', () => {
  const n = {
    id: newId('n'),
    kind: 'system' as const,
    title: 'Test notification',
    body: 'If you can read this, push works.',
    at: now(),
    read: false,
    severity: 'info' as const,
  }
  db.notifications.unshift(n)
  emit('notification', n)
  return noContent()
})

// ---------------------------------------------------------------- clip / snippets / notes

route('GET', '/clip', (r) => ok(db.clips.slice(0, qn(r, 'limit', 50))))
route('POST', '/clip', (r) => {
  const b = body<{ text: string; source?: string }>(r)
  const c = {
    id: newId('c'),
    text: b.text,
    source: (b.source ?? 'web') as 'web',
    at: now(),
    size: b.text.length,
  }
  db.clips.unshift(c)
  emit('clip', c)
  return ok(c)
})
route('DELETE', '/clip/{id}', (r) => {
  const i = db.clips.findIndex((c) => c.id === r.params.id)
  if (i >= 0) db.clips.splice(i, 1)
  return noContent()
})
route('DELETE', '/clip', () => {
  db.clips.length = 0
  return noContent()
})
route('GET', '/snippets', () => ok(db.snippets))
route('POST', '/snippets', (r) => {
  const s = {
    id: newId('sn'),
    name: 'Untitled',
    body: '',
    kind: 'prompt' as const,
    uses: 0,
    ...body<object>(r),
    updatedAt: now(),
  }
  db.snippets.unshift(s)
  return ok(s)
})
route('PATCH', '/snippets/{id}', (r) => {
  const s = db.snippets.find((x) => x.id === r.params.id)
  return s ? ok(Object.assign(s, body(r), { updatedAt: now() })) : notFound()
})
route('DELETE', '/snippets/{id}', (r) => {
  const i = db.snippets.findIndex((x) => x.id === r.params.id)
  if (i >= 0) db.snippets.splice(i, 1)
  return noContent()
})
route('POST', '/snippets/{id}/use', (r) => {
  const s = db.snippets.find((x) => x.id === r.params.id)
  if (!s) return notFound()
  s.uses++
  return ok(s)
})
route('GET', '/notes', () => ok(db.notes))
route('POST', '/notes', (r) => {
  const n = { id: newId('no'), title: 'Untitled', text: '', ...body<object>(r), updatedAt: now() }
  db.notes.unshift(n)
  return ok(n)
})
route('GET', '/notes/{id}', (r) => {
  const n = db.notes.find((x) => x.id === r.params.id)
  return n ? ok(n) : notFound()
})
route('PATCH', '/notes/{id}', (r) => {
  const n = db.notes.find((x) => x.id === r.params.id)
  return n ? ok(Object.assign(n, body(r), { updatedAt: now() })) : notFound()
})
route('DELETE', '/notes/{id}', (r) => {
  const i = db.notes.findIndex((x) => x.id === r.params.id)
  if (i >= 0) db.notes.splice(i, 1)
  return noContent()
})

// ---------------------------------------------------------------- schedules

route('GET', '/schedules', () => ok(db.schedules))
route('POST', '/schedules', (r) => {
  const s = {
    id: newId('sc'),
    name: 'New schedule',
    cron: '0 2 * * *',
    cwd: db.HOME,
    mode: 'headless' as const,
    enabled: true,
    notify: true,
    ...body<object>(r),
    createdAt: now(),
  }
  db.schedules.push(s)
  return ok(s)
})
route('PATCH', '/schedules/{id}', (r) => {
  const s = db.schedules.find((x) => x.id === r.params.id)
  return s ? ok(Object.assign(s, body(r))) : notFound()
})
route('DELETE', '/schedules/{id}', (r) => {
  const i = db.schedules.findIndex((x) => x.id === r.params.id)
  if (i >= 0) db.schedules.splice(i, 1)
  return noContent()
})
route('POST', '/schedules/{id}/run', (r) => {
  const s = db.schedules.find((x) => x.id === r.params.id)
  if (!s) return notFound()
  const run = { id: newId('sr'), scheduleId: s.id, startedAt: now(), status: 'running' as const }
  db.scheduleRuns.unshift(run)
  s.lastRun = run
  emit('schedule.run', run)
  return ok(run)
})
route('GET', '/schedules/{id}/runs', (r) =>
  ok(db.scheduleRuns.filter((x) => x.scheduleId === r.params.id).slice(0, qn(r, 'limit', 20))),
)

// ---------------------------------------------------------------- command center

function score(text: string, needle: string): number {
  const i = text.toLowerCase().indexOf(needle)
  if (i < 0) return 0
  return Math.max(0.3, 1 - i / 30 - (text.length - needle.length) / 200)
}

/** Naive federated search across the fixtures (pure over the store). */
export function searchAll(query: string, scopes: SearchScope[] | null, limit: number): SearchResult[] {
  const needle = query.trim().toLowerCase()
  if (!needle) return []
  const want = (s: SearchScope) => !scopes || scopes.includes(s)
  const out: SearchResult[] = []
  const push = (scope: SearchScope, list: SearchResult[]) => {
    if (want(scope))
      out.push(
        ...list
          .filter((x) => x.score > 0)
          .sort((a, b) => b.score - a.score)
          .slice(0, limit),
      )
  }
  push(
    'terminals',
    db.terminals
      .filter((t) => t.meta?.importable !== '1')
      .map((t) => ({
        scope: 'terminals',
        id: t.id,
        title: t.name,
        subtitle: t.preview,
        link: `/terminal/${t.id}`,
        score: score(`${t.name} ${t.command.join(' ')}`, needle),
        at: t.lastOutputAt,
        meta: { activity: t.attention ? 'waiting' : t.activity, ...(t.agent ? { agent: t.agent } : {}) },
      })),
  )
  push(
    'agents',
    db.agentSessions.map((s) => ({
      scope: 'agents',
      id: s.id,
      title: s.title,
      subtitle: `${s.agent} · ${s.cwd.replace(db.HOME, '~')}`,
      link: `/agents/s/${encodeURIComponent(s.id)}`,
      score: score(s.title, needle),
      at: s.updatedAt,
      meta: { agent: s.agent, ...(s.activity ? { activity: s.activity } : {}) },
    })),
  )
  push(
    'workspaces',
    db.workspaces.map((w) => ({
      scope: 'workspaces',
      id: w.path,
      title: w.name,
      subtitle: w.path.replace(db.HOME, '~'),
      link: `/workspace?path=${encodeURIComponent(w.path)}`,
      score: score(w.name, needle),
      meta: { path: w.path },
    })),
  )
  push(
    'files',
    fs
      .allPaths()
      .map((p) => ({
        scope: 'files',
        id: p,
        title: p.slice(p.lastIndexOf('/') + 1),
        subtitle: p.replace(db.HOME, '~'),
        link: `/files${p.replace(db.HOME, '')}`,
        score: score(p.slice(p.lastIndexOf('/') + 1), needle) * 0.9,
        meta: { path: p },
      })),
  )
  push(
    'previews',
    db.previews
      .filter((p) => !p.hidden)
      .map((p) => ({
        scope: 'previews',
        id: String(p.port),
        title: p.label ?? p.title ?? `:${p.port}`,
        subtitle: `:${p.port} · ${p.process ?? ''}`,
        link: '/previews',
        score: score(`${p.label ?? ''} ${p.title ?? ''} ${p.port} ${p.process ?? ''}`, needle),
      })),
  )
  push(
    'processes',
    processes.map((p) => ({
      scope: 'processes',
      id: String(p.pid),
      title: p.name,
      subtitle: `pid ${p.pid} · ${p.cmd}`,
      link: `/system/processes?pid=${p.pid}`,
      score: score(p.name, needle) * 0.7,
    })),
  )
  push(
    'snippets',
    db.snippets.map((s) => ({
      scope: 'snippets',
      id: s.id,
      title: s.name,
      subtitle: s.body,
      score: score(`${s.name} ${s.body}`, needle) * 0.85,
    })),
  )
  push(
    'notes',
    db.notes.map((n) => ({
      scope: 'notes',
      id: n.id,
      title: n.title,
      subtitle: n.text.split('\n')[0],
      score: score(`${n.title} ${n.text}`, needle) * 0.8,
    })),
  )
  push(
    'scripts',
    db.scripts.map((s) => ({
      scope: 'scripts',
      id: s.id,
      title: s.title,
      subtitle: s.description,
      icon: s.icon,
      score: score(s.title, needle),
    })),
  )
  return out
}

route('GET', '/search', (r) => {
  const started = performance.now()
  const scopes = q(r, 'scopes')?.split(',').filter(Boolean) as SearchScope[] | undefined
  const results = searchAll(q(r, 'q') ?? '', scopes ?? null, qn(r, 'limit', 8))
  return ok({ query: q(r, 'q') ?? '', results, tookMs: Math.round(performance.now() - started) })
})
route('GET', '/scripts', () => ok(db.scripts))
route('POST', '/scripts/{id}/run', (r) => {
  const s = db.scripts.find((x) => x.id === r.params.id)
  if (!s) return notFound('No such script')
  if (s.mode === 'inline') return ok({ output: '203.0.113.24', exitCode: 0 })
  if (s.mode === 'terminal') {
    const t = agentTerminal('shell', s.cwd ?? db.HOME, s.title)
    t.kind = 'task'
    t.agent = undefined
    return ok({ terminalId: t.id, exitCode: 0 })
  }
  return ok({ exitCode: 0 })
})
route('POST', '/ask', (r) => {
  const prompt = body<{ prompt?: string }>(r).prompt ?? ''
  const answer = `Here’s a short answer about “${prompt.slice(0, 60)}”. In this mock, every question gets the same careful shrug — but the stream is real NDJSON, so the UI can render it token by token.`
  const words = answer.split(/(?<=\s)/)
  return { stream: [...words.map((w) => ({ t: 'text', text: w })), { t: 'done' }], streamDelay: 35 }
})

// ---------------------------------------------------------------- toolbox

route('GET', '/toolbox', () => ok(db.tools))
route('POST', '/toolbox/{id}/install', (r) => {
  const tool = db.tools.find((x) => x.id === r.params.id)
  if (!tool) return notFound('Unknown tool')
  const t: TerminalSession = {
    id: newId('t'),
    name: `install ${tool.name}`,
    kind: 'toolbox',
    command: ['sh', '-c', `install ${tool.id}`],
    cwd: db.HOME,
    cols: 120,
    rows: 34,
    clients: 0,
    activity: 'working',
    recording: false,
    pinned: false,
    createdAt: now(),
    lastOutputAt: now(),
  }
  db.terminals.unshift(t)
  emit('terminal.created', t)
  setTimeout(() => {
    tool.installed = true
    Object.assign(t, { activity: 'exited', exitCode: 0, exitedAt: now() })
    emit('terminal.exited', t)
    emit('toolbox.job', { tool: tool.id, status: 'done' })
  }, 4000)
  return ok(t)
})
route('GET', '/toolbox/mcp', () => ok(db.mcpServers))
route('POST', '/toolbox/mcp/apply', (r) => {
  const b = body<{ server: string; agents: string[]; remove?: boolean }>(r)
  const s = db.mcpServers.find((x) => x.id === b.server)
  if (!s) return notFound('Unknown MCP server')
  for (const a of b.agents ?? []) s.agents[a] = !b.remove
  return ok(s)
})

/** Paths that answer without a session (mirrors the server's public list). */
const PUBLIC = new Set([
  '/api/v1/health',
  '/api/v1/auth/state',
  '/api/v1/auth/setup',
  '/api/v1/auth/login',
  '/api/v1/auth/passkey/begin',
  '/api/v1/auth/passkey/finish',
])

/** Dispatch one request. */
export async function handle(method: string, url: URL, reqBody: unknown): Promise<MockResponse> {
  if (!PUBLIC.has(url.pathname)) {
    const denied = requireAuth()
    if (denied) return denied
  }
  const m = match(method, url.pathname)
  if (!m) {
    console.warn(`[mock] no handler for ${method} ${url.pathname}`)
    return fail(404, 'not_found', `Mock has no handler for ${method} ${url.pathname}`)
  }
  return m.fn({ method, url, params: m.params, body: reqBody })
}
