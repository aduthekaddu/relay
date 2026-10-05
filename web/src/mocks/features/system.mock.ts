// Synthetic system fixtures; owned registrations and scenario controls.
import * as db from '../data'
import { now, q, qn } from '../helpers'
import { defineMockModule } from '../registry'
import { FakeLogs } from '../sockets'
import { history, metricsAt, processes } from '../system'
import { fail, noContent, notFound, ok } from '../util'

export default defineMockModule('system', (owner) => {
  owner.socket('/system/logs', ({ url }) => new FakeLogs(url.href), {
    code: 1011,
    reason: 'log source ended',
  })
  const route = owner.http
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
})
