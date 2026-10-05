// Synthetic search fixtures; owned registrations and scenario controls.
import type { SearchScope } from '../../api/types'
import * as db from '../data'
import { body, q, qn } from '../helpers'
import { defineMockModule } from '../registry'
import { searchAll } from '../search-fixtures'
import { agentTerminal } from '../terminal-fixtures'
import { notFound, ok } from '../util'

export default defineMockModule('search', (owner) => {
  const route = owner.http
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
})
