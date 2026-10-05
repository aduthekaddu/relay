// Synthetic terminal fixtures; owned registrations and scenario controls.
import type { CreateTerminalRequest, TerminalSession } from '../../api/types'
import * as db from '../data'
import * as fs from '../fs'
import { body, now, q } from '../helpers'
import { defineMockModule } from '../registry'
import { emit, FakeTerminal } from '../sockets'
import { fail, newId, noContent, notFound, ok } from '../util'

export default defineMockModule('terminal', (owner) => {
  owner.socket('/terminals/{id}/attach', ({ url }) => new FakeTerminal(url.href), {
    code: 1013,
    reason: 'terminal daemon disconnected',
  })
  const route = owner.http
  const termById = (id: string) => db.terminals.find((t) => t.id === id)

  route<unknown, TerminalSession[]>('GET', '/terminals', () =>
    ok(owner.scenarios.state.empty ? [] : db.terminals),
  )
  route<CreateTerminalRequest, TerminalSession>('POST', '/terminals', (r) => {
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
    return { status: 201, json: t }
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
  route('POST', '/terminals/{id}/restore', (r) => {
    const old = termById(r.params.id)
    if (!old) return notFound()
    if (old.activity !== 'exited') return fail(409, 'conflict', 'Session is still running')
    const t: TerminalSession = {
      ...old,
      id: newId('t'),
      activity: 'idle',
      exitCode: undefined,
      exitedAt: undefined,
      createdAt: now(),
      meta: { ...old.meta, restoredFrom: old.id },
    }
    db.terminals.unshift(t)
    emit('terminal.created', t)
    return { status: 201, json: t }
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
    const lines = ['$ ', 'g', 'o', ' ', 't', 'e', 's', 't', '\r\n', 'ok  \trelay-demo\t0.4s\r\n'].map(
      (s, i) => JSON.stringify([i * 0.12, 'o', s]),
    )
    return { text: [header, ...lines].join('\n'), headers: { 'Content-Type': 'application/x-asciicast' } }
  })
})
