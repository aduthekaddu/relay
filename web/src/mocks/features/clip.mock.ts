// Synthetic clip fixtures; owned registrations and scenario controls.
import * as db from '../data'
import { body, now, qn } from '../helpers'
import { defineMockModule } from '../registry'
import { emit } from '../sockets'
import { newId, noContent, ok } from '../util'

export default defineMockModule('clip', (owner) => {
  const route = owner.http
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
})
