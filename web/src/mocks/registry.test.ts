import { afterEach, beforeEach, describe, expect, expectTypeOf, it, vi } from 'vitest'
import { defineMockModule, MockRegistry, type MockRequest } from './registry'
import { FakeSocket, ok, resetFakeSockets } from './util'

const url = (path: string) => new URL(`/api/v1${path}`, 'http://mock.invalid')
let registry: MockRegistry
beforeEach(() => {
  vi.useFakeTimers()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  registry = new MockRegistry()
})
afterEach(() => {
  registry.clear()
  resetFakeSockets()
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('typed owner registration', () => {
  it('supports independent typed owners and literal precedence in either registration order', async () => {
    for (const reverse of [false, true]) {
      const r = new MockRegistry()
      const dynamic = defineMockModule('dynamic', (owner) => {
        owner.http<{ name: string }, { id: string }>('POST', '/items', (req) => {
          expectTypeOf(req).toEqualTypeOf<MockRequest<{ name: string }>>()
          return ok({ id: req.body.name })
        })
        owner.http('GET', '/items/{id}', ({ params }) => ok({ id: params.id }))
        owner.socket('/items/{id}/attach', ({ url }) => new FakeSocket(url.href))
      })
      const fixed = defineMockModule('fixed', (owner) => {
        owner.http('GET', '/items/special', () => ok({ fixed: true }))
        owner.http('GET', '/literal/a.b', () => ok({ literal: true }))
      })
      for (const owner of reverse ? [fixed, dynamic] : [dynamic, fixed]) r.register(owner)
      expect((await r.dispatch('POST', url('/items'), { name: 'fixture' })).json).toEqual({ id: 'fixture' })
      expect((await r.dispatch('GET', url('/items/a%3Ab'), undefined)).json).toEqual({ id: 'a:b' })
      expect((await r.dispatch('HEAD', url('/items/special'), undefined)).json).toEqual({ fixed: true })
      expect(r.match('GET', '/api/v1/literal/aXb')).toBeNull()
      r.clear()
    }
  })

  it.each(['HTTP', 'socket'] as const)('rejects duplicate %s parameter shapes across owners', (transport) => {
    const create = (id: string, parameter: string) =>
      defineMockModule(id, (owner) => {
        if (transport === 'HTTP') owner.http('GET', `/items/{${parameter}}`, () => ok([]))
        else owner.socket(`/items/{${parameter}}`, ({ url }) => new FakeSocket(url.href))
      })
    registry.register(create('first', 'id'))
    expect(() => registry.register(create('second', 'name'))).toThrow(`Duplicate ${transport}`)
    expect(console.error).toHaveBeenCalledWith(expect.stringContaining('first and second'))
    expect(registry.inventory()).toHaveLength(1)
  })

  it.each(['HTTP', 'socket'] as const)(
    'rejects same-owner %s duplicates transactionally and permits registration after clear',
    (transport) => {
      expect(() =>
        registry.register(
          defineMockModule('broken', (owner) => {
            owner.later(() => {}, 100)
            for (let i = 0; i < 2; i++) {
              if (transport === 'HTTP') owner.http('GET', '/one', () => ok([]))
              else owner.socket('/one', ({ url }) => new FakeSocket(url.href))
            }
          }),
        ),
      ).toThrow(`Duplicate ${transport}`)
      expect(registry.inventory()).toEqual([])
      expect(vi.getTimerCount()).toBe(0)
      const module = defineMockModule('working', (owner) => owner.http('GET', '/one', () => ok([])))
      registry.register(module)
      expect(() => registry.register(module)).toThrow('Duplicate owner')
      registry.clear()
      expect(() => registry.feature('working')).toThrow('Missing owner')
      registry.register(module)
      expect(registry.inventory()).toHaveLength(1)
    },
  )

  it('reports missing HTTP/socket handlers visibly, with no URL query/body contents', async () => {
    await expect(registry.dispatch('GET', url('/missing?private=fixture'), undefined)).rejects.toThrow(
      'Missing HTTP handler: GET /api/v1/missing',
    )
    expect(() => registry.socket(url('/missing?private=fixture'))).toThrow(
      'Missing socket handler: /api/v1/missing',
    )
    expect(console.error).toHaveBeenCalledTimes(2)
    expect(JSON.stringify(vi.mocked(console.error).mock.calls)).not.toContain('private=')
  })

  it('keeps HTTP and socket registrations distinct and validates pattern syntax', () => {
    registry.register(
      defineMockModule('both', (owner) => {
        owner.http('GET', '/one', () => ok({ ok: true }))
        owner.socket('/one', ({ url }) => new FakeSocket(url.href))
      }),
    )
    expect(registry.inventory()).toHaveLength(2)
    expect(() =>
      registry.register(defineMockModule('bad', (owner) => owner.http('GET', '/{id}/{id}', () => ok([])))),
    ).toThrow('Repeated parameter')
    expect(() =>
      registry.register(defineMockModule('query', (owner) => owner.http('GET', '/one?q=', () => ok([])))),
    ).toThrow('Invalid API pattern')
  })

  it('gives explicit HEAD registrations precedence over the GET fallback', async () => {
    registry.register(
      defineMockModule('head', (owner) => {
        owner.http('GET', '/one', () => ok({ fallback: true }))
        owner.http('HEAD', '/one', () => ({ status: 204, headers: { 'X-Fixture': 'head' } }))
      }),
    )
    expect(await registry.dispatch('HEAD', url('/one'), undefined)).toEqual({
      status: 204,
      headers: { 'X-Fixture': 'head' },
    })
  })

  it('isolates owner-local mutable state and scenarios in separate registries', async () => {
    const module = defineMockModule('independent', (owner) => {
      let count = 0
      owner.http('GET', '/one', () => ok(owner.scenarios.state.empty ? [] : [++count]))
      owner.onReset(() => {
        count = 0
      })
    })
    const other = new MockRegistry()
    registry.register(module)
    other.register(module)
    registry.feature('independent').set({ empty: true })
    expect((await registry.dispatch('GET', url('/one'), undefined)).json).toEqual([])
    expect((await other.dispatch('GET', url('/one'), undefined)).json).toEqual([1])
    registry.reset()
    expect((await registry.dispatch('GET', url('/one'), undefined)).json).toEqual([1])
    expect((await other.dispatch('GET', url('/one'), undefined)).json).toEqual([2])
    other.clear()
  })
})

describe('request and owner lifetimes', () => {
  function requests() {
    const handler = vi.fn(() => ok({ ok: true }))
    registry.register(
      defineMockModule('feature', (owner) => {
        owner.http('GET', '/one', handler)
        owner.socket('/one', ({ url }) => new FakeSocket(url.href))
      }),
    )
    return handler
  }
  it('delivers deterministic out-of-order responses without cross-owner delay changes', async () => {
    requests()
    const finished: number[] = []
    registry.feature('feature').set({ latency: [100, 10] })
    const first = registry.dispatch('GET', url('/one'), undefined).then(() => finished.push(1))
    const second = registry.dispatch('GET', url('/one'), undefined).then(() => finished.push(2))
    await vi.advanceTimersByTimeAsync(10)
    expect(finished).toEqual([2])
    await vi.advanceTimersByTimeAsync(90)
    await Promise.all([first, second])
    expect(finished).toEqual([2, 1])
  })
  it('aborts delayed requests promptly before a mutation and removes the timer', async () => {
    const handler = requests()
    registry.feature('feature').set({ latency: 100 })
    const controller = new AbortController()
    const pending = registry.dispatch('GET', url('/one'), undefined, controller.signal)
    const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    await vi.advanceTimersByTimeAsync(1)
    controller.abort()
    await rejected
    expect(handler).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })
  it('supports held loading, release, and abort while loading', async () => {
    const handler = requests()
    const controls = registry.feature('feature')
    controls.set({ loading: true })
    const first = registry.dispatch('GET', url('/one'), undefined)
    await vi.advanceTimersByTimeAsync(2000)
    expect(handler).not.toHaveBeenCalled()
    controls.set({ loading: false })
    expect((await first).status).toBe(200)
    controls.set({ loading: true })
    const controller = new AbortController()
    const pending = registry.dispatch('GET', url('/one'), undefined, controller.signal)
    const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    controller.abort()
    await rejected
  })
  it('rejects promptly when an extension is awaiting I/O and accepts an already aborted signal', async () => {
    let finish: (response: ReturnType<typeof ok>) => void = () => {}
    registry.register(
      defineMockModule('waiting', (owner) =>
        owner.http<unknown, unknown>(
          'GET',
          '/one',
          () =>
            new Promise<ReturnType<typeof ok>>((resolve) => {
              finish = resolve
            }),
        ),
      ),
    )
    const controller = new AbortController()
    const pending = registry.dispatch('GET', url('/one'), undefined, controller.signal)
    const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    await vi.advanceTimersByTimeAsync(0)
    controller.abort()
    await rejected
    finish(ok({ ignored: true }))
    await expect(registry.dispatch('GET', url('/one'), undefined, controller.signal)).rejects.toMatchObject({
      name: 'AbortError',
    })
  })
  it('resets in-flight requests, owner jobs, sockets and scenarios while retaining definitions', async () => {
    const job = vi.fn()
    const reset = vi.fn()
    registry.register(
      defineMockModule('feature', (owner) => {
        owner.http('GET', '/one', () => {
          owner.later(job, 100)
          return ok([])
        })
        owner.socket('/one', ({ url }) => new FakeSocket(url.href))
        owner.onReset(reset)
      }),
    )
    await registry.dispatch('GET', url('/one'), undefined)
    registry.feature('feature').set({ loading: true, empty: true })
    const pending = registry.dispatch('GET', url('/one'), undefined)
    const rejected = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    const socket = registry.socket(url('/one'))
    const open = vi.fn(),
      close = vi.fn()
    socket.addEventListener('open', open)
    socket.onclose = close
    registry.reset()
    await rejected
    await vi.advanceTimersByTimeAsync(1000)
    expect(open).not.toHaveBeenCalled()
    expect(close).not.toHaveBeenCalled()
    expect(job).not.toHaveBeenCalled()
    expect(reset).toHaveBeenCalledOnce()
    expect(socket.readyState).toBe(3)
    expect(registry.feature('feature').state).toEqual({})
    expect(registry.inventory()).toHaveLength(2)
    expect(vi.getTimerCount()).toBe(0)
  })
})
