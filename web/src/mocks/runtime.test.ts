import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api, onUnauthorized } from '../api/client'
import { events } from '../api/events'
import type { RelayEvent } from '../api/types'
import * as db from './data'
import * as fs from './fs'
import { handle, registry, resetMocks } from './handlers'
import { installMocks } from './index'
import { emit } from './sockets'
import { processes } from './system'
import { FakeSocket } from './util'

const call = (method: string, path: string, body?: unknown) =>
  handle(method, new URL(`/api/v1/${path}`, location.href), body)
const wsURL = (path: string) =>
  `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/api/v1/${path}`
const externalSockets: { url: string | URL; protocols?: string | string[] }[] = []
class RealSocket {
  constructor(url: string | URL, protocols?: string | string[]) {
    externalSockets.push({ url, protocols })
  }
}
let cleanup: (() => void) | undefined
let realFetch: ReturnType<typeof vi.fn>
beforeEach(() => {
  vi.useFakeTimers()
  vi.spyOn(Math, 'random').mockReturnValue(0.5)
  vi.spyOn(console, 'info').mockImplementation(() => {})
  vi.spyOn(console, 'error').mockImplementation(() => {})
  resetMocks()
  externalSockets.length = 0
  realFetch = vi.fn(async () => new Response('external'))
  vi.stubGlobal('fetch', realFetch)
  vi.stubGlobal('WebSocket', RealSocket)
  cleanup = installMocks()
})
afterEach(() => {
  cleanup?.()
  cleanup = undefined
  resetMocks()
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('integrated owner controls and HTTP contracts', () => {
  it('automatically discovers independent terminal/notification owners with distinct controls', async () => {
    expect(registry.inventory().filter((r) => r.path === '/api/v1/terminals')[0].owner).toBe('terminal')
    expect(registry.inventory().filter((r) => r.path === '/api/v1/notifications')[0].owner).toBe('notify')
    registry.feature('terminal').set({ empty: true })
    expect((await call('GET', 'terminals')).json).toEqual([])
    expect((await call('GET', 'notifications')).json).toEqual(db.notifications)
    registry.feature('files').set({ empty: true })
    expect((await call('GET', 'files/list')).json).toMatchObject({ entries: [], total: 0 })
    registry.feature('notify').set({ empty: true })
    expect((await call('GET', 'notifications')).json).toEqual([])
  })

  it('keeps the public/auth distinction and rejects unauthenticated fake socket admission', async () => {
    resetMocks(['signed-out'])
    expect((await call('GET', 'health')).status).toBe(200)
    expect((await call('GET', 'auth/state')).status).toBe(200)
    expect((await call('GET', 'info')).status).toBe(401)
    expect((await call('POST', 'auth/login', { username: 'fixture', password: 'correct' })).status).toBe(200)
    expect((await call('GET', 'info')).status).toBe(200)
    resetMocks(['signed-out'])
    const socket = new WebSocket(wsURL('events'))
    const open = vi.fn(),
      error = vi.fn()
    socket.onopen = open
    socket.onerror = error
    await vi.advanceTimersByTimeAsync(40)
    expect(open).not.toHaveBeenCalled()
    expect(error).toHaveBeenCalledOnce()
    expect(socket.readyState).toBe(3)
  })

  it('serializes contract errors through ApiError and preserves retry/field/401 callbacks', async () => {
    const unauthorized = vi.fn()
    onUnauthorized(unauthorized)
    registry.feature('terminal').set({
      latency: 10,
      error: { status: 401, detail: { code: 'unauthorized', message: 'Synthetic expired session' } },
    })
    const rejected = expect(api.get('terminals')).rejects.toMatchObject({ status: 401, code: 'unauthorized' })
    await vi.advanceTimersByTimeAsync(10)
    await rejected
    expect(unauthorized).toHaveBeenCalledOnce()
    registry.feature('auth').set({
      latency: 10,
      error: {
        status: 429,
        detail: { code: 'rate_limited', message: 'Synthetic throttle', retryIn: 5, field: 'password' },
      },
    })
    const response = fetch('/api/v1/auth/login', { method: 'POST' })
    await vi.advanceTimersByTimeAsync(10)
    const res = await response
    expect(res.headers.get('Retry-After')).toBe('5')
    expect(await res.json()).toEqual({
      error: { code: 'rate_limited', message: 'Synthetic throttle', retryIn: 5, field: 'password' },
    })
    const error = expect(api.post('auth/login')).rejects.toBeInstanceOf(ApiError)
    await vi.advanceTimersByTimeAsync(10)
    await error
  })

  it('intercepts only same-origin API HTTP and socket URLs, preserving external protocols', async () => {
    expect(await (await fetch('https://other.invalid/api/v1/terminals')).text()).toBe('external')
    expect(await (await fetch('/assets/fixture')).text()).toBe('external')
    expect(realFetch).toHaveBeenCalledTimes(2)
    expect(new WebSocket('wss://other.invalid/api/v1/events', ['fixture'])).toBeInstanceOf(RealSocket)
    expect(new WebSocket(`wss://${location.host}/api/v1/events`, 'fixture')).toBeInstanceOf(RealSocket)
    expect(externalSockets).toHaveLength(2)
    expect(externalSockets[0].protocols).toEqual(['fixture'])
    expect(new WebSocket(wsURL('events'))).toBeInstanceOf(FakeSocket)
  })

  it('handles Request objects, method/body overrides, HEAD, and immediate aborts', async () => {
    registry.feature('terminal').set({ latency: 10 })
    const request = new Request(new URL('/api/v1/terminals', location.href), {
      method: 'POST',
      body: JSON.stringify({ name: 'Synthetic request' }),
      headers: { 'Content-Type': 'application/json' },
    })
    const pending = fetch(request)
    await vi.advanceTimersByTimeAsync(10)
    expect((await pending).status).toBe(201)
    const head = fetch('/api/v1/terminals', { method: 'HEAD' })
    await vi.advanceTimersByTimeAsync(10)
    expect(await (await head).text()).toBe('')
    const controller = new AbortController()
    controller.abort()
    await expect(fetch('/api/v1/terminals', { signal: controller.signal })).rejects.toMatchObject({
      name: 'AbortError',
    })
  })

  it('aborts Request signals, delayed responses and body decoding before mutation', async () => {
    registry.feature('terminal').set({ latency: 100 })
    const length = db.terminals.length
    const controller = new AbortController()
    const request = new Request(new URL('/api/v1/terminals', location.href), {
      method: 'POST',
      body: JSON.stringify({ name: 'must not appear' }),
      signal: controller.signal,
    })
    const rejected = expect(fetch(request)).rejects.toMatchObject({ name: 'AbortError' })
    await vi.advanceTimersByTimeAsync(1)
    controller.abort()
    await rejected
    expect(db.terminals).toHaveLength(length)
    expect(vi.getTimerCount()).toBe(0)
    const body = new Blob(['{}'], { type: 'application/json' })
    let finish: (text: string) => void = () => {}
    vi.spyOn(body, 'text').mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve
        }),
    )
    const reading = new AbortController()
    const failed = expect(
      fetch('/api/v1/terminals', { method: 'POST', body, signal: reading.signal }),
    ).rejects.toMatchObject({ name: 'AbortError' })
    reading.abort()
    await failed
    finish('{}')
    await vi.advanceTimersByTimeAsync(200)
    expect(db.terminals).toHaveLength(length)
  })

  it('fails visibly for unknown registered surfaces and reinstalls/restores globals exactly', async () => {
    await expect(fetch('/api/v1/missing')).rejects.toThrow('Missing HTTP handler')
    expect(() => new WebSocket(wsURL('missing'))).toThrow('Missing socket handler')
    expect(installMocks()).toBe(cleanup)
    cleanup?.()
    expect(window.fetch).toBe(realFetch)
    expect(window.WebSocket).toBe(RealSocket)
    expect(Object.hasOwn(window, '__relayMock')).toBe(false)
    cleanup = installMocks()
    const request = fetch('/api/v1/health')
    await vi.advanceTimersByTimeAsync(200)
    expect((await request).headers.get('X-Relay-Mock')).toBe('synthetic')
  })
})

describe('fake stream and client lifecycles', () => {
  it('counts each terminal attachment once through close, disconnect, and repeated teardown', async () => {
    const term = db.terminals[0]
    const baseline = term.clients
    const first = new WebSocket(wsURL(`terminals/${term.id}/attach`)) as unknown as FakeSocket
    const second = new WebSocket(wsURL(`terminals/${term.id}/attach`)) as unknown as FakeSocket
    await vi.advanceTimersByTimeAsync(40)
    expect(term.clients).toBe(baseline + 2)
    first.close()
    await vi.advanceTimersByTimeAsync(10)
    expect(term.clients).toBe(baseline + 1)
    first.end()
    first.dispose()
    expect(term.clients).toBe(baseline + 1)
    registry.disconnect('terminal')
    expect(term.clients).toBe(baseline)
    second.end()
    expect(term.clients).toBe(baseline)
    const unopened = new WebSocket(wsURL(`terminals/${term.id}/attach`))
    unopened.close()
    await vi.advanceTimersByTimeAsync(100)
    expect(term.clients).toBe(baseline)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('invokes property callbacks and native listeners once each, then releases them', async () => {
    const socket = new WebSocket(wsURL('events'))
    const property = vi.fn(),
      listener = vi.fn()
    socket.onopen = property
    socket.addEventListener('open', listener)
    await vi.advanceTimersByTimeAsync(40)
    expect(property).toHaveBeenCalledOnce()
    expect(listener).toHaveBeenCalledOnce()
    socket.close()
    await vi.advanceTimersByTimeAsync(10)
    socket.dispatchEvent(new Event('open'))
    expect(property).toHaveBeenCalledOnce()
    expect(listener).toHaveBeenCalledOnce()
    expect(socket.onopen).toBeNull()
  })

  it('refuses offline owner admission and uses canonical terminal/log disconnect reasons', async () => {
    registry.feature('events').set({ offline: true })
    const refused = new WebSocket(wsURL('events'))
    const open = vi.fn()
    refused.onopen = open
    await vi.advanceTimersByTimeAsync(40)
    expect(open).not.toHaveBeenCalled()
    expect(refused.readyState).toBe(3)
    registry.feature('events').set({ offline: false })
    const terminal = new WebSocket(wsURL(`terminals/${db.terminals[0].id}/attach`))
    const logs = new WebSocket(wsURL('system/logs?unit=relay.service'))
    const terminalClose = vi.fn(),
      logClose = vi.fn()
    terminal.onclose = terminalClose
    logs.onclose = logClose
    await vi.advanceTimersByTimeAsync(40)
    registry.disconnect('terminal')
    expect(terminalClose).toHaveBeenCalledWith(
      expect.objectContaining({ code: 1013, reason: 'terminal daemon disconnected' }),
    )
    expect(logs.readyState).toBe(1)
    registry.disconnect('system')
    expect(logClose).toHaveBeenCalledWith(expect.objectContaining({ code: 1011, reason: 'log source ended' }))
    expect(vi.getTimerCount()).toBe(0)
  })

  it('ignores asynchronous binary sends resolved after socket disposal', async () => {
    const binary = vi.fn()
    class BinarySocket extends FakeSocket {
      protected override onClientBinary(data: Uint8Array): void {
        binary(data)
      }
    }
    const socket = new BinarySocket(wsURL('fixture'))
    expect(() => socket.send('before open')).toThrow('Socket is connecting')
    await vi.advanceTimersByTimeAsync(40)
    let finish: (buffer: ArrayBuffer) => void = () => {}
    const blob = new Blob(['synthetic'])
    vi.spyOn(blob, 'arrayBuffer').mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve
        }),
    )
    socket.send(blob)
    socket.dispose()
    finish(new ArrayBuffer(8))
    await vi.advanceTimersByTimeAsync(0)
    expect(binary).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('closes while connecting without reopening or leaking timers/listeners', async () => {
    const socket = new WebSocket(wsURL('events'))
    const opened = vi.fn(),
      closed = vi.fn()
    socket.onopen = opened
    socket.addEventListener('open', opened)
    socket.onclose = closed
    socket.close()
    await vi.advanceTimersByTimeAsync(100)
    expect(socket.readyState).toBe(3)
    expect(opened).not.toHaveBeenCalled()
    expect(closed).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('disconnects/reconnects the real events client abstraction and resends subscriptions', async () => {
    const metric = vi.fn()
    events.on('metrics', metric)
    const off = events.subscribe('metrics')
    events.connect()
    await vi.advanceTimersByTimeAsync(1040)
    expect(metric).toHaveBeenCalledOnce()
    registry.disconnect('events')
    expect(events.getStatus().state).toBe('closed')
    await vi.advanceTimersByTimeAsync(1540)
    expect(events.getStatus().state).toBe('open')
    expect(metric).toHaveBeenCalledTimes(2)
    off()
    await vi.advanceTimersByTimeAsync(1000)
    expect(metric).toHaveBeenCalledTimes(2)
  })

  it('retains unknown-event compatibility and omits backend-only broadcasts', async () => {
    const messages: RelayEvent[] = []
    const socket = new WebSocket(wsURL('events'))
    socket.onmessage = (event) => messages.push(JSON.parse(event.data))
    await vi.advanceTimersByTimeAsync(40)
    messages.length = 0
    socket.send(JSON.stringify({ type: 'ping' }))
    emit('fixture.future', { extra: ['synthetic'] })
    emit('audit', { synthetic: true })
    emit('auth.session.revoked', { SessionIDs: ['fixture'] })
    expect(messages.map((event) => event.type)).toEqual(['pong', 'fixture.future'])
    expect(messages[0]).not.toHaveProperty('data')
  })

  it('keeps terminal/log bytes labeled synthetic and DeadSocket unavailable', async () => {
    const terminal = new WebSocket(wsURL(`terminals/${db.terminals[0].id}/attach`))
    const data: unknown[] = []
    terminal.binaryType = 'arraybuffer'
    terminal.onmessage = (event) => data.push(event.data)
    const logs: string[] = []
    const log = new WebSocket(wsURL('system/logs?unit=relay.service'))
    log.onmessage = (event) => logs.push(JSON.parse(event.data).text)
    const desktop = new WebSocket(wsURL('desktop/ws'))
    const open = vi.fn(),
      frame = vi.fn()
    desktop.onopen = open
    desktop.onmessage = frame
    await vi.advanceTimersByTimeAsync(40)
    expect(
      data
        .filter((value) => value instanceof ArrayBuffer)
        .some((value) => new TextDecoder().decode(value as ArrayBuffer).includes('[synthetic mock]')),
    ).toBe(true)
    expect(logs.every((text) => text.startsWith('[synthetic]'))).toBe(true)
    expect(desktop.readyState).toBe(3)
    expect(open).not.toHaveBeenCalled()
    expect(frame).not.toHaveBeenCalled()
  })

  it.each(['abort', 'cancel', 'reset'] as const)('cleans delayed NDJSON on %s', async (operation) => {
    registry.feature('search').set({ latency: 1 })
    const controller = new AbortController()
    const pending = fetch('/api/v1/ask', {
      method: 'POST',
      body: JSON.stringify({ prompt: 'synthetic' }),
      signal: controller.signal,
    })
    await vi.advanceTimersByTimeAsync(1)
    const response = await pending
    const reader = response.body!.getReader()
    const read = reader.read()
    if (operation === 'cancel') {
      await reader.cancel()
      expect(await read).toMatchObject({ done: true })
    } else {
      const rejected = expect(read).rejects.toMatchObject({ name: 'AbortError' })
      if (operation === 'abort') controller.abort()
      else resetMocks()
      await rejected
    }
    expect(vi.getTimerCount()).toBe(0)
  })

  it('resets all fixtures, private maps, jobs, IDs, client listeners/subscriptions and timers', async () => {
    const terminals = structuredClone(db.terminals)
    const originalArray = db.terminals
    const processCount = processes.length
    const first = await call('POST', 'uploads', { name: 'synthetic.bin', size: 10 })
    await call('POST', 'files/copy', { from: ['/fixture/source'], to: '/fixture/to' })
    await call('POST', `toolbox/${db.tools[0].id}/install`)
    await call('POST', 'agents/reindex')
    await call('POST', 'desktop/start')
    await call('POST', 'desktop/clipboard', { text: 'synthetic' })
    await call('POST', 'auth/totp/enable', { code: '123456' })
    processes.splice(2, 1)
    fs.remove([`${db.HOME}/todo.md`], true)
    db.notifications.length = 0
    db.settings.idleMinutes = 99
    const oldEvent = vi.fn(),
      old401 = vi.fn()
    events.on('*', oldEvent)
    events.subscribe('metrics')
    events.connect()
    onUnauthorized(old401)
    const socket = new WebSocket(wsURL('events'))
    const oldMessage = vi.fn()
    socket.onmessage = oldMessage
    await vi.advanceTimersByTimeAsync(40)
    oldEvent.mockClear()
    oldMessage.mockClear()
    resetMocks()
    expect(events.getStatus().state).toBe('idle')
    expect(vi.getTimerCount()).toBe(0)
    expect(socket.readyState).toBe(3)
    expect(db.terminals).toBe(originalArray)
    expect(db.terminals).toEqual(terminals)
    expect(processes).toHaveLength(processCount)
    expect(fs.stat(`${db.HOME}/todo.md`)).not.toBeNull()
    expect(fs.trashList()).toEqual([])
    expect(db.notifications.length).toBeGreaterThan(0)
    expect(db.settings.idleMinutes).not.toBe(99)
    expect((await call('GET', 'files/jobs')).json).toEqual([])
    expect((await call('GET', 'desktop/clipboard')).json).toEqual({ text: '' })
    expect((await call('GET', 'auth/totp')).json).toEqual({ enabled: false })
    expect((await call('GET', `uploads/${(first.json as { id: string }).id}`)).status).toBe(404)
    expect((await call('POST', 'uploads', { name: 'synthetic.bin', size: 10 })).json).toEqual(first.json)
    events.dispatch({ type: 'fixture.future', at: '2026-01-01T00:00:00Z' })
    expect(oldEvent).not.toHaveBeenCalled()
    expect(oldMessage).not.toHaveBeenCalled()
    resetMocks(['signed-out'])
    const rejected = expect(api.get('info')).rejects.toMatchObject({ status: 401 })
    await vi.advanceTimersByTimeAsync(200)
    await rejected
    expect(old401).not.toHaveBeenCalled()
  })
})
