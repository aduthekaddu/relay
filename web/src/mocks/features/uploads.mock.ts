// Synthetic uploads fixtures; owned registrations and scenario controls.
import type { StartUploadRequest } from '../../api/types'
import * as db from '../data'
import * as fs from '../fs'
import { body, q } from '../helpers'
import { defineMockModule } from '../registry'
import { fail, newId, noContent, notFound, ok } from '../util'

const utf8 = new TextEncoder()
// Mirror internal/terminal/uploads.go's basename, invisible-character and byte-budget rules.
function sanitizeName(raw = ''): string {
  let name = [...(raw.replaceAll('\\', '/').split('/').at(-1) ?? '')]
    .map((char) => {
      const code = char.codePointAt(0)!
      if (code < 32 || code === 127 || code === 0xfffd || (code >= 0xd800 && code <= 0xdfff)) return '_'
      if (/[\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069\u200b\ufeff]/u.test(char)) return ''
      return char
    })
    .join('')
    .trim()
    .replace(/^[.-]+/, '')
    .replace(/[. ]+$/, '')
  if (!name) return 'upload'
  if (utf8.encode(name).length <= 200) return name
  let ext = /\.[^.]*$/.exec(name)?.[0] ?? ''
  if (utf8.encode(ext).length > 20) ext = ''
  const budget = 200 - utf8.encode(ext).length
  let stem = ''
  let bytes = 0
  for (const char of name.slice(0, name.length - ext.length)) {
    bytes += utf8.encode(char).length
    if (bytes > budget) break
    stem += char
  }
  name = stem + ext
  return name
}

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
    const b = body<StartUploadRequest>(r)
    const size = b.size ?? 0
    if (!Number.isSafeInteger(size) || size < 0) return fail(400, 'bad_request', 'Invalid upload size')
    if (b.name !== undefined && (typeof b.name !== 'string' || utf8.encode(b.name).length > 4096))
      return fail(400, 'bad_request', 'Invalid upload name')
    const id = newId('up')
    uploads.set(id, { size, received: 0, name: sanitizeName(b.name), dir: b.dir })
    return { status: 201, json: { id, chunkSize: 4 * 1024 * 1024, received: 0, size } }
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
    if (u.received !== u.size) return fail(409, 'incomplete', 'Upload is not fully received')
    const dir = fs.normalize(u.dir ?? `${db.HOME}/Downloads`)
    if (!dir.startsWith('/') || dir.includes('\0') || dir.split('/').includes('..'))
      return fail(400, 'bad_request', 'Invalid upload directory')
    if (dir !== db.HOME && !dir.startsWith(`${db.HOME}/`))
      return fail(403, 'forbidden', 'Upload directory is outside the files root')
    const folder = fs.stat(dir)
    if (!folder) return notFound('Upload directory not found')
    if (folder.type !== 'dir') return fail(400, 'bad_request', 'Upload target is not a directory')
    const ext = /\.[^.]*$/.exec(u.name)?.[0] ?? ''
    const stem = u.name.slice(0, u.name.length - ext.length)
    for (let i = 0; i < 1000; i++) {
      const name = i === 0 ? u.name : `${stem} (${i})${ext}`
      const path = `${dir}/${name}`
      if (fs.stat(path)) continue
      const entry = fs.mkdir(path, 'file')
      if (!entry) return fail(500, 'internal', 'Synthetic upload file creation failed')
      uploads.delete(r.params.id)
      return ok({ path: entry.path, name: entry.name, size: u.size })
    }
    return fail(409, 'conflict', 'No free upload filename')
  })
  route('DELETE', '/uploads/{id}', (r) => {
    uploads.delete(r.params.id)
    return noContent()
  })
  owner.onReset(() => {
    uploads.clear()
  })
})
