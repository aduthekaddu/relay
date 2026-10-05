// Synthetic agents fixtures; owned registrations and scenario controls.
import type { AgentHookResult } from '../../api/agents'
import type { AgentSession, LaunchAgentRequest, UsageSummary } from '../../api/types'
import * as db from '../data'
import * as fs from '../fs'
import { body, q, qn } from '../helpers'
import { defineMockModule } from '../registry'
import { emit } from '../sockets'
import { agentTerminal } from '../terminal-fixtures'
import { accepted, fail, notFound, ok } from '../util'

export default defineMockModule('agents', (owner) => {
  const route = owner.http
  route('POST', '/agents/hook', () => ok({ action: 'ignored' } satisfies AgentHookResult))

  const sessionById = (id: string) => db.agentSessions.find((s) => s.id === id)

  route('GET', '/agents', () => ok(db.agents))
  route('GET', '/agents/sessions', (r) => {
    const agent = q(r, 'agent')
    const text = (q(r, 'q') ?? '').toLowerCase()
    const cwd = q(r, 'cwd')
    const status = q(r, 'status') ?? 'all'
    const archived = q(r, 'archived') === '1'
    const pinned = q(r, 'pinned') === '1'
    let list = owner.scenarios.state.empty
      ? []
      : db.agentSessions.filter(
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
  route('POST', '/agents/sessions/{id}/resume', (r) => {
    const s = sessionById(r.params.id)
    if (!s) return notFound('Session not found')
    if (!s.resumable) return fail(409, 'not_resumable', `${s.agent} can’t resume this session.`)
    const t = agentTerminal(s.agent, s.cwd, `${s.agent} · ${s.title}`, s.id)
    Object.assign(s, {
      status: 'live',
      terminalId: t.id,
      activity: 'working',
    } satisfies Partial<AgentSession>)
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
    owner.later(
      () =>
        emit('agents.indexed', {
          agent: 'claude',
          sessions: db.agentSessions.filter((s) => s.agent === 'claude').length,
        }),
      1200,
    )
    return accepted()
  })
})
