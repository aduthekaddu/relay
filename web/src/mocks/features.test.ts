// Synthetic regression fixtures for evaluated CodeRabbit PR #5 findings.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AgentSession, AuthState, Page, Upload, UploadResult } from '../api/types'
import * as db from './data'
import * as fs from './fs'
import { handle, resetMocks } from './handlers'

const call = (method: string, path: string, body?: unknown) =>
  handle(method, new URL(`/api/v1/${path}`, 'http://mock.invalid'), body)
const query = (path: string, params: Record<string, string>) => `${path}?${new URLSearchParams(params)}`

beforeEach(() => resetMocks())
afterEach(() => {
  resetMocks()
  vi.restoreAllMocks()
})

describe('synthetic agent filters and pagination', () => {
  beforeEach(() => {
    const template = db.agentSessions[0]
    db.agentSessions.splice(
      0,
      db.agentSessions.length,
      ...['project', 'project/src', 'project-other', 'project2'].map((path, i) => ({
        ...template,
        id: `fixture:${i}`,
        cwd: `${db.HOME}/${path}`,
        status: 'history' as const,
        archived: false,
        updatedAt: `2026-01-0${4 - i}T00:00:00Z`,
      })),
    )
  })

  it.each([`${db.HOME}/project`, `${db.HOME}/project/`, '~/project'])(
    'matches exact and descendant cwd paths without sibling prefixes for %s',
    async (cwd) => {
      const result = await call('GET', query('agents/sessions', { cwd }))
      expect(result.status).toBe(200)
      expect((result.json as Page<AgentSession>).items.map((item) => item.id)).toEqual([
        'fixture:0',
        'fixture:1',
      ])
    },
  )

  it('keeps the root cwd filter and empty scenario valid', async () => {
    expect((await call('GET', query('agents/sessions', { cwd: '/' }))).json).toMatchObject({ total: 4 })
    resetMocks(['empty'])
    expect((await call('GET', 'agents/sessions')).json).toEqual({
      items: [],
      total: 0,
      nextCursor: undefined,
    })
  })

  it.each(['invalid', '-1', '1.5', 'NaN', 'Infinity', '1e1', '0x1', '9007199254740992', ' '])(
    'returns a contract error for invalid synthetic cursor %s',
    async (cursor) => {
      expect(await call('GET', query('agents/sessions', { cursor, limit: '2' }))).toMatchObject({
        status: 400,
        json: { error: { code: 'bad_request', message: expect.any(String) } },
      })
    },
  )

  it('round-trips valid page cursors and treats absent/empty cursors as the first page', async () => {
    const first = (await call('GET', 'agents/sessions?limit=2')).json as Page<AgentSession>
    expect(first.items.map((item) => item.id)).toEqual(['fixture:0', 'fixture:1'])
    expect(first.nextCursor).toBe('2')
    const next = (await call('GET', query('agents/sessions', { limit: '2', cursor: first.nextCursor! })))
      .json as Page<AgentSession>
    expect(next.items.map((item) => item.id)).toEqual(['fixture:2', 'fixture:3'])
    expect(next.nextCursor).toBeUndefined()
    expect((await call('GET', 'agents/sessions?limit=2&cursor=')).json).toEqual(first)
    expect((await call('GET', 'agents/sessions?limit=2&cursor=0')).json).toEqual(first)
    expect((await call('GET', 'agents/sessions?limit=2&cursor=999')).json).toMatchObject({ items: [] })
  })
})

describe('synthetic TOTP settings and sign-in', () => {
  const login = { username: 'fixture', password: 'correct', remember: false }

  it('uses the enabled setting for status, public auth state and subsequent login', async () => {
    const methods = { ...db.auth.state.methods }
    expect((await call('POST', 'auth/totp/enable', { code: '123456' })).status).toBe(204)
    expect((await call('GET', 'auth/totp')).json).toEqual({ enabled: true })
    expect(((await call('GET', 'auth/state')).json as AuthState).methods).toEqual({ ...methods, totp: true })
    await call('POST', 'auth/logout')
    expect((await call('POST', 'auth/login', login)).json).toEqual({ ok: false, needTotp: true })
    expect(db.auth.state.authenticated).toBe(false)
    expect((await call('POST', 'auth/login', { ...login, totp: 'wrong' })).status).toBe(401)
    expect((await call('POST', 'auth/login', { ...login, totp: '123456' })).json).toEqual({ ok: true })
  })

  it('stops requiring TOTP after disabling it and restores the selected mode on reset', async () => {
    resetMocks(['totp'])
    expect((await call('POST', 'auth/login', { ...login, totp: '123456' })).json).toEqual({ ok: true })
    expect((await call('POST', 'auth/totp/disable', { code: '123456' })).status).toBe(204)
    expect((await call('GET', 'auth/totp')).json).toEqual({ enabled: false })
    await call('POST', 'auth/logout')
    expect((await call('POST', 'auth/login', login)).json).toEqual({ ok: true })
    resetMocks(['totp'])
    expect((await call('POST', 'auth/login', login)).json).toEqual({ ok: false, needTotp: true })
    resetMocks()
    expect((await call('GET', 'auth/totp')).json).toEqual({ enabled: false })
    expect(db.auth.state.methods.totp).toBe(false)
  })

  it('leaves both settings unchanged when the enable code is invalid', async () => {
    expect((await call('POST', 'auth/totp/enable', { code: 'wrong' })).status).toBe(400)
    expect((await call('GET', 'auth/totp')).json).toEqual({ enabled: false })
    expect(db.auth.state.methods.totp).toBe(false)
  })
})

describe('synthetic files root boundary', () => {
  it.each([db.HOME, `${db.HOME}/`, '~', `${db.HOME}/Downloads`])('lists allowed folder %s', async (path) => {
    expect((await call('GET', query('files/list', { path }))).status).toBe(200)
  })

  it.each([`${db.HOME}-other`, `${db.HOME}2`, '/'])(
    'refuses outside folder %s before listing',
    async (path) => {
      expect(await call('GET', query('files/list', { path }))).toMatchObject({
        status: 403,
        json: { error: { code: 'forbidden', field: 'path' } },
      })
    },
  )
})

describe('synthetic upload completion', () => {
  const start = async (name?: string, dir?: string, size = 0) => {
    const response = await call('POST', 'uploads', { name, dir, size })
    expect(response.status).toBe(201)
    return response.json as Upload
  }

  it.each([
    ['../../etc/passwd', 'passwd'],
    ['../shot.png', 'shot.png'],
    ['C:\\Users\\fixture\\shot.jpg', 'shot.jpg'],
    ['.bashrc', 'bashrc'],
    ['-rf', 'rf'],
    ['a\0b\nc.txt', 'a_b_c.txt'],
    ['evil\u202egnp.exe', 'evilgnp.exe'],
    ['  spaced  ', 'spaced'],
    ['trailing...', 'trailing'],
    ['..', 'upload'],
    ['/', 'upload'],
    ['', 'upload'],
    [undefined, 'upload'],
  ])('sanitizes %s to a basename within the destination', async (input, name) => {
    const upload = await start(input)
    const result = await call('POST', `uploads/${upload.id}/complete`)
    expect(result.status).toBe(200)
    expect(result.json).toEqual({ path: `${db.HOME}/Downloads/${name}`, name, size: 0 })
    expect(fs.stat((result.json as UploadResult).path)?.type).toBe('file')
    expect((await call('GET', `uploads/${upload.id}`)).status).toBe(404)
  })

  it('bounds Unicode filenames in bytes while preserving their extension', async () => {
    const upload = await start(`${'é'.repeat(300)}.tar.gz`)
    const result = (await call('POST', `uploads/${upload.id}/complete`)).json as UploadResult
    expect(new TextEncoder().encode(result.name).length).toBeLessThanOrEqual(200)
    expect(result.name.endsWith('.gz')).toBe(true)
    expect(result.name).not.toContain('\ufffd')
    expect(fs.stat(result.path)).not.toBeNull()
  })

  it('chooses distinct collision filenames without overwriting the existing file', async () => {
    const path = `${db.HOME}/Downloads/collision.txt`
    fs.writeText(path, 'synthetic original')
    for (let i = 1; i <= 2; i++) {
      const upload = await start('collision.txt')
      expect((await call('POST', `uploads/${upload.id}/complete`)).json).toMatchObject({
        path: `${db.HOME}/Downloads/collision (${i}).txt`,
        name: `collision (${i}).txt`,
      })
    }
    expect(fs.readText(path)?.text).toBe('synthetic original')
  })

  it('keeps a failed upload available when file creation fails, then allows retry', async () => {
    const upload = await start('retry.txt')
    vi.spyOn(fs, 'mkdir').mockReturnValueOnce(null)
    expect(await call('POST', `uploads/${upload.id}/complete`)).toMatchObject({
      status: 500,
      json: { error: { code: 'internal' } },
    })
    expect(fs.stat(`${db.HOME}/Downloads/retry.txt`)).toBeNull()
    expect((await call('GET', `uploads/${upload.id}`)).json).toEqual(upload)
    expect((await call('POST', `uploads/${upload.id}/complete`)).status).toBe(200)
    expect((await call('GET', `uploads/${upload.id}`)).status).toBe(404)
  })

  it('rechecks a missing destination and retains the upload until a successful retry', async () => {
    const dir = `${db.HOME}/Downloads/temporary`
    fs.mkdir(dir)
    const upload = await start('retry.txt', dir)
    fs.remove([dir], false)
    expect((await call('POST', `uploads/${upload.id}/complete`)).status).toBe(404)
    expect((await call('GET', `uploads/${upload.id}`)).status).toBe(200)
    fs.mkdir(dir)
    expect((await call('POST', `uploads/${upload.id}/complete`)).json).toMatchObject({
      path: `${dir}/retry.txt`,
    })
  })

  it.each([`${db.HOME}-other`, '/etc', `${db.HOME}/../other`])(
    'does not consume an upload or create a file outside its root for %s',
    async (dir) => {
      const upload = await start('fixture.txt', dir)
      expect([400, 403]).toContain((await call('POST', `uploads/${upload.id}/complete`)).status)
      expect((await call('GET', `uploads/${upload.id}`)).status).toBe(200)
      expect(fs.stat(`${dir}/fixture.txt`)).toBeNull()
    },
  )

  it('refuses incomplete completion without consuming the ID, then completes received bytes', async () => {
    const upload = await start('chunk.bin', undefined, 3)
    expect(await call('POST', `uploads/${upload.id}/complete`)).toMatchObject({
      status: 409,
      json: { error: { code: 'incomplete' } },
    })
    expect((await call('GET', `uploads/${upload.id}`)).json).toEqual(upload)
    await call('PUT', `uploads/${upload.id}?offset=0`, new Blob(['abc']))
    expect((await call('POST', `uploads/${upload.id}/complete`)).json).toMatchObject({ size: 3 })
    resetMocks()
    expect((await call('GET', `uploads/${upload.id}`)).status).toBe(404)
    expect(fs.stat(`${db.HOME}/Downloads/chunk.bin`)).toBeNull()
  })
})

describe('synthetic worktree requests', () => {
  it.each([undefined, {}, { branch: '' }, { branch: ' ' }, { branch: null }, { branch: 42 }])(
    'returns a field error instead of throwing for invalid branch body %j',
    async (body) => {
      expect(await call('POST', 'workspaces/git/worktrees', body)).toMatchObject({
        status: 400,
        json: { error: { code: 'bad_request', field: 'branch' } },
      })
    },
  )

  it('preserves valid branch behavior and trims the branch like the server', async () => {
    expect(
      await call('POST', 'workspaces/git/worktrees', { path: '~', branch: ' fix/fixture ' }),
    ).toMatchObject({
      status: 200,
      json: { path: `${db.HOME}-fix-fixture`, branch: 'fix/fixture', main: false },
    })
  })
})
