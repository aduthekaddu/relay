// Synthetic snippets fixtures; owned registrations and scenario controls.
import * as db from '../data'
import { body, now } from '../helpers'
import { defineMockModule } from '../registry'
import { newId, noContent, notFound, ok } from '../util'

export default defineMockModule('snippets', (owner) => {
  const route = owner.http
  route('GET', '/snippets', () => ok(owner.scenarios.state.empty ? [] : db.snippets))
  route('GET', '/snippets/{id}', (r) => {
    const snippet = db.snippets.find((x) => x.id === r.params.id)
    return snippet ? ok(snippet) : notFound()
  })
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
})
