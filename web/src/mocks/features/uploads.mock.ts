// Synthetic uploads fixtures; owned registrations and scenario controls.
import * as db from '../data'
import * as fs from '../fs'
import { body, q } from '../helpers'
import { defineMockModule } from '../registry'
import { newId, noContent, notFound, ok } from '../util'

export default defineMockModule('uploads', (owner) => {
  const route = owner.http
  const uploads = new Map<string, { size: number; received: number; name: string; dir?: string }>()
  route('GET', '/uploads/{id}', (r) => {
    const u = uploads.get(r.params.id)
    return u
      ? ok({ id: r.params.id, chunkSize: 4 * 1024 * 1024, received: u.received, size: u.size })
      : notFound('Upload not found')
  })
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
  owner.onReset(() => {
    uploads.clear()
  })
})
