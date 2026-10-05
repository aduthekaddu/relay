import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import doc from '../../../docs/dev/API.md?raw'
import { handle, match, registry, resetMocks } from './handlers'

// Every endpoint documented in docs/dev/API.md must have a mock handler, so
// the next wave can build any screen with `pnpm dev:mock`.
const ROW = /^\| (GET|POST|PUT|PATCH|DELETE)([^|]*)\| `([^`]+)`/gm

function concrete(path: string): string {
  return path
    .split('?')[0]
    .replace(/\{(\w+)(\\\|[\w\\|]+)?\}/g, (_, name: string, alts?: string) =>
      alts ? name : name === 'pid' || name === 'port' ? '4321' : name === 'agent' ? 'claude' : 'x1',
    )
}

const rows = [...doc.matchAll(ROW)].map((m) => ({
  method: m[1],
  ws: m[2].includes('🔌'),
  path: concrete(m[3]),
}))
// 🔌 rows are WebSocket upgrades served by the fake sockets (mocks/sockets.ts).
const endpoints = rows.filter((r) => !r.ws).map((r) => [r.method, r.path] as const)

describe('mock coverage of docs/dev/API.md', () => {
  it('finds the documented endpoints', () => {
    expect(endpoints.length).toBeGreaterThan(80)
    expect(rows.filter((r) => r.ws).length).toBeGreaterThan(2)
  })
  it.each(endpoints)('%s %s has a handler', (method, path) => {
    expect(match(method, path)).not.toBeNull()
  })
})

describe('mock responses', () => {
  it.each([
    ['GET', '/api/v1/health', 200],
    ['GET', '/api/v1/info', 200],
    ['GET', '/api/v1/terminals', 200],
    ['GET', '/api/v1/notifications', 200],
  ])('%s %s → %i', async (method, path, status) => {
    const res = await handle(method, new URL(path, 'http://mock.invalid'), undefined)
    expect(res.status).toBe(status)
  })
})

beforeEach(() => resetMocks())
afterEach(() => {
  resetMocks()
  vi.restoreAllMocks()
})
it('fails visibly for a missing HTTP registration instead of disguising it as a server 404', async () => {
  const diagnostic = vi.spyOn(console, 'error').mockImplementation(() => {})
  await expect(
    handle('GET', new URL('/api/v1/no-such-endpoint', 'http://mock.invalid'), undefined),
  ).rejects.toThrow('Missing HTTP handler')
  expect(diagnostic).toHaveBeenCalledWith(expect.stringContaining('Missing HTTP handler'))
})
it.each(rows.filter((r) => r.ws))(
  '$method $path has an explicit synthetic socket registration',
  ({ path }) => {
    expect(
      registry
        .inventory()
        .filter((r) => r.transport === 'socket')
        .some((r) => {
          const expression = r.path.replace(/\{\w+\}/g, '[^/]+')
          return new RegExp(`^${expression}$`).test(path)
        }),
    ).toBe(true)
  },
)
