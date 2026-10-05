// Synthetic notes fixtures; owned registrations and scenario controls.
import * as db from '../data'
import { body, now } from '../helpers'
import { defineMockModule } from '../registry'
import { newId, noContent, notFound, ok } from '../util'

export default defineMockModule('notes', (owner) => {
  const route = owner.http
  route('GET', '/notes', () => ok(owner.scenarios.state.empty ? [] : db.notes))
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
})
