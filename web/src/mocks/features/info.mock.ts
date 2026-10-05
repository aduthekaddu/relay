// Synthetic info fixtures; owned registrations and scenario controls.
import * as db from '../data'
import { body } from '../helpers'
import { defineMockModule, type MockOwner } from '../registry'
import { patchRuntimeSettings } from '../runtime-settings'
import { fail, ok } from '../util'

export default defineMockModule('info', (owner) => {
  const publicPaths = new Set(['/health'])
  const route: MockOwner['http'] = (method, path, handler, options) =>
    owner.http(method, path, handler, {
      auth: publicPaths.has(path) ? 'public' : 'authenticated',
      ...options,
    })
  route('GET', '/health', () => ok({ ok: true, version: db.info.version }))
  route('GET', '/info', () => ok(db.info))
  route('GET', '/settings', () => ok(db.settings))
  route('PATCH', '/settings', (r) => {
    const result = patchRuntimeSettings(db.settings, body(r))
    return result.ok ? ok(Object.assign(db.settings, result.value)) : fail(400, 'bad_request', result.message)
  })
})
