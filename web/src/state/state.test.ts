import { afterEach, describe, expect, it, vi } from 'vitest'
import { backoffDelay, events } from '../api/events'
import type { Notification, TerminalSession } from '../api/types'
import { areaForPath } from '../app/areas'
import { shouldTransition } from '../app/nav'
import { deriveConnection } from './connection'
import { mergeNotifications } from './notifications'
import { sortTerminals } from './terminals'
import { resolveTheme } from './theme'

describe('backoffDelay', () => {
  it.each([
    [0, 0.5, 500],
    [1, 0.5, 1000],
    [3, 0.5, 4000],
    [5, 0.5, 10_000],
    [50, 0.5, 10_000],
    [0, 0, 400],
    [0, 1, 600],
  ])('attempt %i rand %d → %i ms', (n, rand, want) => expect(backoffDelay(n, rand)).toBe(want))
})

describe('deriveConnection', () => {
  it.each([
    [{ state: 'open', failures: 0 }, false, true, 'offline'],
    [{ state: 'open', failures: 0 }, true, true, 'online'],
    [{ state: 'connecting', failures: 0 }, true, false, 'connecting'],
    [{ state: 'closed', failures: 3 }, true, false, 'reconnecting'],
    [{ state: 'closed', failures: 0 }, true, true, 'reconnecting'],
  ] as const)('%j online=%s ever=%s → %s', (s, online, ever, want) => {
    expect(deriveConnection(s, online, ever)).toBe(want)
  })
})

describe('resolveTheme', () => {
  it.each([
    ['carbon', true, 'carbon'],
    ['paper', false, 'paper'],
    ['auto', true, 'paper'],
    ['auto', false, 'carbon'],
  ] as const)('%s (os light %s) → %s', (pref, light, want) => expect(resolveTheme(pref, light)).toBe(want))
})

describe('mergeNotifications', () => {
  const n = (id: string, at: string, read = false) => ({ id, at, read, title: id }) as unknown as Notification
  it('dedupes by id (newest copy wins) and sorts newest first', () => {
    const out = mergeNotifications(
      [n('a', '2026-01-01T00:00:00Z'), n('b', '2026-01-02T00:00:00Z')],
      [n('a', '2026-01-01T00:00:00Z', true), n('c', '2026-01-03T00:00:00Z')],
    )
    expect(out.map((x) => x.id)).toEqual(['c', 'b', 'a'])
    expect(out.find((x) => x.id === 'a')?.read).toBe(true)
  })
  it('caps the list', () => {
    const many = Array.from({ length: 150 }, (_, i) => n(`n${i}`, new Date(i * 1000).toISOString()))
    expect(mergeNotifications([], many)).toHaveLength(100)
  })
})

describe('sortTerminals', () => {
  const t = (id: string, activity: string, extra: Partial<TerminalSession> = {}) =>
    ({
      id,
      activity,
      createdAt: '2026-01-01T00:00:00Z',
      pinned: false,
      ...extra,
    }) as unknown as TerminalSession
  it('puts sessions that need you first, then working, then the rest by recency', () => {
    const out = sortTerminals([
      t('idle-old', 'idle', { lastOutputAt: '2026-01-01T01:00:00Z' }),
      t('working', 'working'),
      t('waiting', 'waiting'),
      t('idle-new', 'idle', { lastOutputAt: '2026-01-01T05:00:00Z' }),
    ])
    expect(out.map((x) => x.id).slice(0, 2)).toEqual(['waiting', 'working'])
    expect(out.map((x) => x.id).indexOf('idle-new')).toBeLessThan(out.map((x) => x.id).indexOf('idle-old'))
  })
})

describe('areas and transitions', () => {
  it.each([
    ['/', 'home'],
    ['/terminal', 'terminal'],
    ['/terminal/t1', 'terminal'],
    ['/files/some/dir', 'files'],
    ['/settings/security', 'settings'],
    ['/nowhere', 'home'],
  ])('%s → %s', (path, area) => expect(areaForPath(path).id).toBe(area))
  it.each([
    ['/terminal', '/terminal/t1', false],
    ['/terminal', '/files', true],
    ['/files?a=1', '/files?a=2', false],
  ])('%s → %s animates: %s', (a, b, want) => expect(shouldTransition(a, b)).toBe(want))
})

class FakeSocket {
  static last: FakeSocket | null = null
  readyState = 0
  sent: unknown[] = []
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((m: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  constructor(public url: string) {
    FakeSocket.last = this
  }
  send(s: string) {
    this.sent.push(JSON.parse(s))
  }
  close() {
    this.readyState = 3
    this.onclose?.()
  }
  open() {
    this.readyState = 1
    this.onopen?.()
  }
}

describe('live events client', () => {
  afterEach(() => {
    events.disconnect()
    vi.useRealTimers()
  })
  it('subscribes, dispatches, ref-counts topics and reconnects with backoff', () => {
    vi.useFakeTimers()
    events.setFactory((url) => new FakeSocket(url) as unknown as WebSocket)
    const unsubA = events.subscribe('metrics')
    const unsubB = events.subscribe('metrics')
    events.connect()
    const ws = FakeSocket.last!
    expect(ws.url).toMatch(/\/api\/v1\/events$/)
    ws.open()
    expect(ws.sent).toContainEqual({ type: 'subscribe', topics: ['metrics'] })
    expect(ws.sent.some((m) => (m as { type: string }).type === 'visibility')).toBe(true)

    const seen: unknown[] = []
    const off = events.on('notification', (d) => seen.push(d))
    ws.onmessage?.({ data: JSON.stringify({ type: 'notification', data: { id: 'n1' } }) })
    ws.onmessage?.({ data: 'not json' })
    expect(seen).toEqual([{ id: 'n1' }])
    off()

    unsubA()
    expect(ws.sent).not.toContainEqual({ type: 'unsubscribe', topics: ['metrics'] })
    unsubB()
    unsubB() // idempotent
    expect(ws.sent.filter((m) => (m as { type: string }).type === 'unsubscribe')).toHaveLength(1)

    ws.close()
    expect(events.getStatus().state).toBe('closed')
    vi.advanceTimersByTime(700)
    expect(FakeSocket.last).not.toBe(ws)
  })
})
