// Synthetic previews fixtures; owned registrations and scenario controls.
import type { PreviewLink } from '../../api/previews'
import * as db from '../data'
import { body } from '../helpers'
import { defineMockModule } from '../registry'
import { emit } from '../sockets'
import { fail, notFound, ok } from '../util'

export default defineMockModule('previews', (owner) => {
  const route = owner.http
  route('GET', '/previews', () => ok(owner.scenarios.state.empty ? [] : db.previews))
  route('GET', '/previews/{port}/link', (r) => {
    const port = Number(r.params.port)
    if (!Number.isInteger(port) || port < 1 || port > 65535) return fail(400, 'bad_request', 'Invalid port')
    const capability = db.info.capabilities.previews
    if (capability.effectiveMode === 'off') return fail(503, 'unavailable', 'Previews are off')
    const preview = db.previews.find((x) => x.port === port)
    const link: PreviewLink = {
      port,
      mode: capability.effectiveMode,
      listening: !!preview,
      preview,
      url:
        preview?.url ??
        (capability.effectiveMode === 'path'
          ? `${location.origin}/p/${port}/`
          : `https://${port}.${capability.host}/`),
    }
    return ok(link)
  })
  route('PATCH', '/previews/{port}', (r) => {
    const p = db.previews.find((x) => x.port === Number(r.params.port))
    if (!p) return notFound('No such preview')
    Object.assign(p, body(r))
    emit('previews.changed', db.previews)
    return ok(p)
  })
})
