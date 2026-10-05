import type { ErrorBody } from '../api/types'
import { abortable, abortError, linkSignals, sleep, TimerScope } from './lifecycle'
import { ScenarioControls } from './scenarios'
import { FakeSocket, fail, type MockResponse } from './util'

export type HttpMethod = 'GET' | 'HEAD' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
export type MockAuth = 'public' | 'authenticated'
export interface MockRequest<B = unknown> {
  method: string
  url: URL
  params: Record<string, string>
  body: B
  signal?: AbortSignal
}
export type HttpHandler<B = unknown, J = unknown> = (
  request: MockRequest<B>,
) => MockResponse<NoInfer<J> | ErrorBody> | Promise<MockResponse<NoInfer<J> | ErrorBody>>
export interface SocketRequest {
  url: URL
  params: Record<string, string>
  protocols?: string | string[]
}

export interface MockOwner {
  scenarios: ScenarioControls
  http: <B = unknown, J = unknown>(
    method: HttpMethod,
    path: string,
    handler: HttpHandler<B, J>,
    options?: { auth?: MockAuth },
  ) => void
  socket: (
    path: string,
    create: (request: SocketRequest) => FakeSocket,
    disconnect?: { code: number; reason: string },
  ) => void
  later: (callback: () => void, ms: number) => void
  onReset: (callback: () => void) => void
}
export interface MockModule {
  id: string
  register: (owner: MockOwner) => void
}

export function defineMockModule(id: string, register: MockModule['register']): MockModule {
  return { id, register }
}

interface RegisteredOwner {
  id: string
  scenarios: ScenarioControls
  scope: TimerScope
  resets: (() => void)[]
}

interface Pattern {
  path: string
  shape: string
  parts: string[]
  regex: RegExp
  keys: string[]
}
interface HttpEntry extends Pattern {
  owner: RegisteredOwner
  method: string
  auth: MockAuth
  fn: HttpHandler
}
interface SocketEntry extends Pattern {
  owner: RegisteredOwner
  create: (request: SocketRequest) => FakeSocket
  disconnect: { code: number; reason: string }
}

export class MockRegistrationError extends Error {
  constructor(message: string) {
    super(`[relay mock] ${message}`)
    this.name = 'MockRegistrationError'
    console.error(this.message)
  }
}

function pattern(path: string): Pattern {
  if (!path.startsWith('/') || path.includes('?') || path.includes('#'))
    throw new MockRegistrationError(`Invalid API pattern: ${path}`)
  const full = path.startsWith('/api/v1/') ? path : `/api/v1${path}`
  const keys: string[] = []
  const parts = full.split('/')
  const source = parts
    .map((part) => {
      const key = /^\{(\w+)\}$/.exec(part)
      if (key) {
        if (keys.includes(key[1])) throw new MockRegistrationError(`Repeated parameter in ${path}`)
        keys.push(key[1])
        return '([^/]+)'
      }
      if (/[{}]/.test(part)) throw new MockRegistrationError(`Invalid parameter in ${path}`)
      return part.replace(/[.*+?^$()|[\]\\]/g, '\\$&')
    })
    .join('/')
  return { path: full, parts, shape: full.replace(/\{\w+\}/g, '{}'), keys, regex: new RegExp(`^${source}$`) }
}

/** Static segments win over parameters; equal specificity sorts by path. */
function order(a: Pattern, b: Pattern): number {
  for (let i = 0; i < Math.min(a.parts.length, b.parts.length); i++) {
    const difference = Number(a.parts[i].startsWith('{')) - Number(b.parts[i].startsWith('{'))
    if (difference) return difference
  }
  return a.path < b.path ? -1 : a.path > b.path ? 1 : 0
}

function params(entry: Pattern, path: string): Record<string, string> | null {
  const hit = entry.regex.exec(path)
  if (!hit) return null
  try {
    return Object.fromEntries(entry.keys.map((key, i) => [key, decodeURIComponent(hit[i + 1])]))
  } catch {
    throw new MockRegistrationError(`Invalid encoded parameter for ${entry.path}`)
  }
}

/** Independent registries; registration is transactional and reset is explicit. */
export class MockRegistry {
  private http = [] as HttpEntry[]
  private sockets = [] as SocketEntry[]
  private owners = new Map<string, RegisteredOwner>()
  private activeSockets = new Map<FakeSocket, SocketEntry>()
  private epoch = new AbortController()

  constructor(private authenticated: () => boolean = () => true) {}

  get signal(): AbortSignal {
    return this.epoch.signal
  }

  register(module: MockModule): void {
    if (!module.id || this.owners.has(module.id))
      throw new MockRegistrationError(`Duplicate owner: ${module.id}`)
    const http: HttpEntry[] = []
    const sockets: SocketEntry[] = []
    const scope = new TimerScope()
    const resets: (() => void)[] = []
    const registered = { id: module.id, scenarios: new ScenarioControls(), scope, resets }
    const duplicate = (kind: string, key: string, owner: string) => {
      throw new MockRegistrationError(`Duplicate ${kind} ${key}: ${owner} and ${module.id}`)
    }
    const owner: MockOwner = {
      scenarios: registered.scenarios,
      http: (method, path, fn, options) => {
        const entry = {
          ...pattern(path),
          method,
          fn: fn as HttpHandler,
          auth: options?.auth ?? 'authenticated',
          owner: registered,
        }
        const prior = [...this.http, ...http].find((r) => r.method === method && r.shape === entry.shape)
        if (prior) duplicate('HTTP', `${method} ${entry.shape}`, prior.owner.id)
        http.push(entry)
      },
      socket: (path, create, disconnect = { code: 1001, reason: 'synthetic disconnect' }) => {
        const entry = { ...pattern(path), create, disconnect, owner: registered }
        const prior = [...this.sockets, ...sockets].find((r) => r.shape === entry.shape)
        if (prior) duplicate('socket', entry.shape, prior.owner.id)
        sockets.push(entry)
      },
      later: (callback, ms) => scope.later(callback, ms),
      onReset: (callback) => resets.push(callback),
    }
    try {
      module.register(owner)
    } catch (error) {
      scope.reset()
      throw error
    }
    this.http.push(...http)
    this.sockets.push(...sockets)
    this.http.sort(order)
    this.sockets.sort(order)
    this.owners.set(module.id, registered)
  }

  feature(id: string): ScenarioControls {
    const owner = this.owners.get(id)
    if (!owner) throw new MockRegistrationError(`Missing owner: ${id}`)
    return owner.scenarios
  }

  inventory(): { owner: string; transport: string; method: string; path: string; auth: MockAuth }[] {
    return [
      ...this.http.map((r) => ({
        owner: r.owner.id,
        transport: 'http',
        method: r.method,
        path: r.path,
        auth: r.auth,
      })),
      ...this.sockets.map((r) => ({
        owner: r.owner.id,
        transport: 'socket',
        method: 'GET',
        path: r.path,
        auth: 'authenticated' as const,
      })),
    ]
  }

  match(
    method: string,
    path: string,
  ): { entry: HttpEntry; fn: HttpHandler; params: Record<string, string> } | null {
    method = method.toUpperCase()
    for (const r of this.http) {
      if (r.method !== method) continue
      const hit = params(r, path)
      if (hit) return { entry: r, fn: r.fn, params: hit }
    }
    return method === 'HEAD' ? this.match('GET', path) : null
  }

  async dispatch(
    method: string,
    url: URL,
    body: unknown,
    signal?: AbortSignal,
    delay = 0,
  ): Promise<MockResponse> {
    const hit = this.match(method, url.pathname)
    if (!hit) throw new MockRegistrationError(`Missing HTTP handler: ${method.toUpperCase()} ${url.pathname}`)
    const linked = linkSignals(this.signal, signal)
    try {
      if (linked.signal.aborted) throw abortError()
      if (hit.entry.auth !== 'public' && !this.authenticated())
        return fail(401, 'unauthorized', 'Sign in to continue.')
      const scenario = hit.entry.owner.scenarios
      const latency = scenario.takeLatency(delay)
      await scenario.wait(linked.signal)
      if (latency > 0) await sleep(latency, linked.signal)
      if (linked.signal.aborted) throw abortError()
      const error = scenario.state.error
      if (error) return fail(error.status, error.detail.code, error.detail.message, { ...error.detail })
      const pending = hit.fn({
        method: method.toUpperCase(),
        url,
        params: hit.params,
        body,
        signal: linked.signal,
      })
      const response = await abortable(Promise.resolve(pending), linked.signal)
      if (linked.signal.aborted) throw abortError()
      return response
    } finally {
      linked.dispose()
    }
  }

  socket(url: URL, protocols?: string | string[]): FakeSocket {
    const entry = this.sockets.find((r) => r.regex.test(url.pathname))
    if (!entry) throw new MockRegistrationError(`Missing socket handler: ${url.pathname}`)
    const socket =
      !this.authenticated() || entry.owner.scenarios.state.offline
        ? new FakeSocket(url.href, { fail: true })
        : entry.create({ url, params: params(entry, url.pathname) ?? {}, protocols })
    this.activeSockets.set(socket, entry)
    socket.addEventListener('close', () => this.activeSockets.delete(socket), { once: true })
    return socket
  }

  disconnect(owner?: string): void {
    for (const [socket, entry] of [...this.activeSockets]) {
      if (!owner || entry.owner.id === owner) socket.end(entry.disconnect.code, entry.disconnect.reason)
    }
  }

  reset(): void {
    this.epoch.abort()
    this.epoch = new AbortController()
    for (const socket of [...this.activeSockets.keys()]) socket.dispose()
    this.activeSockets.clear()
    for (const { scenarios, scope, resets } of this.owners.values()) {
      scope.reset()
      scenarios.reset()
      for (const reset of resets) reset()
    }
  }

  clear(): void {
    this.reset()
    this.http.length = 0
    this.sockets.length = 0
    this.owners.clear()
  }
}
