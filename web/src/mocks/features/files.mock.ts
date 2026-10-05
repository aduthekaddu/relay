// Synthetic files fixtures; owned registrations and scenario controls.
import type { FileJob, OpenRequest } from '../../api/files'
import * as db from '../data'
import * as fs from '../fs'
import { body, now, q, qn } from '../helpers'
import { defineMockModule } from '../registry'
import { emit } from '../sockets'
import { accepted, fail, newId, noContent, notFound, ok } from '../util'

export default defineMockModule('files', (owner) => {
  const route = owner.http
  const notInRoot = () => fail(403, 'forbidden', 'That path is outside the files root.', { field: 'path' })
  const inRoot = (p: string | null) => {
    const path = fs.normalize(p)
    return path === db.HOME || path.startsWith(`${db.HOME}/`)
  }

  route('GET', '/files/list', (r) => {
    if (!inRoot(q(r, 'path'))) return notInRoot()
    const l = fs.list(q(r, 'path') ?? '~', {
      hidden: q(r, 'hidden') === '1' || q(r, 'hidden') === 'true',
      sort: q(r, 'sort') ?? 'name',
      desc: q(r, 'desc') === '1' || q(r, 'desc') === 'true',
      offset: Number(q(r, 'offset') ?? 0),
      limit: qn(r, 'limit', 500),
    })
    return l
      ? ok(owner.scenarios.state.empty ? { ...l, entries: [], total: 0, hidden: 0 } : l)
      : notFound('No such folder')
  })
  route('GET', '/files/stat', (r) => {
    const e = fs.stat(q(r, 'path') ?? '')
    return e ? ok(e) : notFound('No such file')
  })
  route('GET', '/files/raw', (r) => {
    const p = q(r, 'path') ?? ''
    const e = fs.stat(p)
    if (!e || e.type === 'dir') return notFound('No such file')
    const headers: Record<string, string> = {
      'Content-Security-Policy': 'sandbox',
      'X-Content-Type-Options': 'nosniff',
    }
    if (q(r, 'download') === '1') headers['Content-Disposition'] = `attachment; filename="${e.name}"`
    if (e.mime?.startsWith('image/'))
      return {
        text: fs.placeholderImage(e.path, 1024),
        headers: { ...headers, 'Content-Type': 'image/svg+xml' },
      }
    const t = fs.readText(p)
    return {
      text: t?.text ?? `(binary file: ${e.name})`,
      headers: { ...headers, 'Content-Type': e.mime ?? 'application/octet-stream' },
    }
  })
  route('GET', '/files/text', (r) => {
    const t = fs.readText(q(r, 'path') ?? '')
    return t ? ok(t) : fail(415, 'not_text', 'This file isn’t text.')
  })
  route('PUT', '/files/text', (r) => {
    const res = fs.writeText(
      q(r, 'path') ?? '',
      body<{ text: string }>(r).text ?? '',
      q(r, 'mtime') ?? undefined,
    )
    if (res === 'conflict') return fail(409, 'conflict', 'The file changed on disk since you opened it.')
    return res ? ok(res) : notFound()
  })
  route('GET', '/files/thumb', (r) => ({
    text: fs.placeholderImage(q(r, 'path') ?? '', qn(r, 'size', 256)),
    headers: { 'Content-Type': 'image/svg+xml' },
  }))
  route('POST', '/files/mkdir', (r) => {
    const e = fs.mkdir(body<{ path: string }>(r).path, 'dir')
    return e ? ok(e) : fail(409, 'exists', 'Something with that name already exists.')
  })
  route('POST', '/files/touch', (r) => {
    const e = fs.mkdir(body<{ path: string }>(r).path, 'file')
    return e ? ok(e) : fail(409, 'exists', 'Something with that name already exists.')
  })
  route('POST', '/files/rename', (r) => {
    const b = body<{ path: string; name: string }>(r)
    const e = fs.rename(b.path, b.name)
    return e ? ok(e) : fail(409, 'exists', 'Something with that name already exists.', { field: 'name' })
  })
  route('POST', '/files/move', (r) => {
    const b = body<{ from: string[]; to: string }>(r)
    return ok(fs.move(b.from ?? [], b.to))
  })
  // Synthetic admission/cancellation fixtures; no host filesystem copy occurs.
  const fileJobs = new Map<string, FileJob>()
  route('POST', '/files/copy', (r) => {
    const b = body<{ from?: string[]; to?: string }>(r)
    if (!b.from?.length || !b.to) return fail(400, 'bad_request', 'Source and destination required')
    const job: FileJob = {
      id: newId('copy'),
      op: 'copy',
      state: 'running',
      from: b.from,
      to: b.to,
      files: 0,
      totalFiles: 0,
      bytes: 0,
      totalBytes: 0,
      skipped: 0,
      startedAt: now(),
    }
    fileJobs.set(job.id, job)
    emit('files.job', job)
    return accepted({ ...job })
  })
  route('GET', '/files/jobs', () =>
    ok([...fileJobs.values()].sort((a, b) => b.startedAt.localeCompare(a.startedAt))),
  )
  route('DELETE', '/files/jobs/{id}', (r) => {
    const job = fileJobs.get(r.params.id)
    if (!job) return notFound('Job not found')
    if (job.state === 'running') {
      job.state = 'canceled'
      job.endedAt = now()
      emit('files.job', { ...job })
    }
    return noContent()
  })
  route('POST', '/open', (r) => {
    const b = body<OpenRequest>(r)
    if (!b.path) return fail(400, 'bad_request', 'Path required')
    const entry = fs.stat(b.path)
    if (!entry) return notFound()
    const data: OpenRequest = {
      path: entry.path,
      ...(entry.type !== 'dir' && (b.line ?? 0) > 0 ? { line: b.line } : {}),
    }
    emit('open', data)
    return ok(data)
  })
  route('POST', '/files/delete', (r) => {
    const b = body<{ paths: string[]; trash?: boolean }>(r)
    fs.remove(b.paths ?? [], b.trash !== false)
    return noContent()
  })
  route('GET', '/files/trash', () => ok(fs.trashList()))
  route('POST', '/files/trash/restore', (r) => {
    fs.restore(body<{ paths: string[] }>(r).paths ?? [])
    return noContent()
  })
  route('POST', '/files/trash/empty', () => {
    fs.emptyTrash()
    return noContent()
  })
  route('GET', '/files/zip', () => ({
    body: new Blob(['PK\x05\x06'.padEnd(22, '\0')], { type: 'application/zip' }),
    headers: { 'Content-Type': 'application/zip', 'Content-Disposition': 'attachment; filename="files.zip"' },
  }))
  route('GET', '/files/usage', (r) => ok(fs.usage(q(r, 'path') ?? '~')))
  route('GET', '/files/search', (r) => ({
    stream: fs.search(q(r, 'q') ?? '', q(r, 'path') ?? '~', q(r, 'content') === '1', qn(r, 'limit', 50)),
    streamDelay: 25,
  }))
  owner.onReset(() => {
    fileJobs.clear()
  })
})
