import { afterEach, beforeEach, describe, expect, expectTypeOf, it, vi } from 'vitest'
import fixtures from '../../../internal/api/testdata/jobs.json'
import { type EventMap, events } from './events'
import type { FileJob } from './files'
import type { ToolboxJob } from './toolbox'
import type { EventType, RelayEvent } from './types'

describe('canonical job contracts and generic decoder compatibility', () => {
  let socket: WebSocket
  const cleanups: (() => void)[] = []

  beforeEach(() => {
    socket = {
      readyState: 1,
      send: vi.fn(),
      close: vi.fn(),
      onopen: null,
      onmessage: null,
      onclose: null,
      onerror: null,
    } as unknown as WebSocket
    events.setFactory(() => socket)
    events.connect()
    socket.onopen?.(new Event('open'))
  })

  afterEach(() => {
    for (const off of cleanups.splice(0)) off()
    events.disconnect()
    events.setFactory((url) => new WebSocket(url))
    vi.restoreAllMocks()
  })

  const receive = (data: unknown) => socket.onmessage?.(new MessageEvent('message', { data }))

  it('binds both job topics to the canonical feature types', () => {
    expectTypeOf<EventMap['files.job']>().toEqualTypeOf<FileJob>()
    expectTypeOf<EventMap['toolbox.job']>().toEqualTypeOf<ToolboxJob>()
    expectTypeOf<'files.job' | 'toolbox.job'>().toExtend<EventType>()
    cleanups.push(events.on('files.job', (data) => expectTypeOf(data).toEqualTypeOf<FileJob>()))
    cleanups.push(events.on('toolbox.job', (data) => expectTypeOf(data).toEqualTypeOf<ToolboxJob>()))
  })

  it.each(fixtures)('$name reaches its typed consumer and wildcard unchanged', ({ event }) => {
    const typed = vi.fn()
    const wildcard = vi.fn()
    cleanups.push(events.on(event.type as 'files.job' | 'toolbox.job', typed))
    cleanups.push(events.on('*', wildcard))
    receive(JSON.stringify(event))
    expect(typed).toHaveBeenCalledExactlyOnceWith(event.data, event)
    expect(wildcard).toHaveBeenCalledExactlyOnceWith(event.data, event)
    if (event.type === 'toolbox.job') expect(event.data).not.toHaveProperty('status')
  })

  it('forwards a future topic, arbitrary data and absent data without an allowlist', () => {
    const handler = vi.fn()
    cleanups.push(events.on('*', handler))
    const future: RelayEvent = { type: 'fixture.future', at: '2026-01-02T03:04:05Z', data: { extra: [1] } }
    receive(JSON.stringify(future))
    const noData = { type: 'fixture.future', at: future.at }
    receive(JSON.stringify(noData))
    expect(handler).toHaveBeenNthCalledWith(1, future.data, future)
    expect(handler).toHaveBeenNthCalledWith(2, undefined, noData)
  })

  it('preserves application pong and existing permissive known-event decoding', () => {
    const pong = vi.fn()
    const job = vi.fn()
    cleanups.push(events.on('pong', pong), events.on('files.job', job))
    const frame = { type: 'pong', at: '2026-01-02T03:04:05Z' }
    receive(JSON.stringify(frame))
    receive(JSON.stringify({ type: 'files.job', at: frame.at }))
    expect(pong).toHaveBeenCalledExactlyOnceWith(undefined, frame)
    expect(job).toHaveBeenCalledExactlyOnceWith(undefined, { type: 'files.job', at: frame.at })
  })

  it('ignores binary, malformed JSON, null and non-string topics', () => {
    const handler = vi.fn()
    cleanups.push(events.on('*', handler))
    for (const frame of [new Uint8Array([1]), '{', 'null', '{}', '{"type":7}', '[]']) receive(frame)
    expect(handler).not.toHaveBeenCalled()
  })

  it('isolates handler errors and honors unsubscribe', () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    const next = vi.fn()
    cleanups.push(
      events.on('pong', () => {
        throw new Error('synthetic handler failure')
      }),
    )
    const off = events.on('pong', next)
    cleanups.push(off)
    receive('{"type":"pong"}')
    expect(next).toHaveBeenCalledOnce()
    off()
    receive('{"type":"pong"}')
    expect(next).toHaveBeenCalledOnce()
    expect(error).toHaveBeenCalledTimes(2)
  })
})
