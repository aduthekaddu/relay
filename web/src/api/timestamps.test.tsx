import { cleanup, render } from '@testing-library/preact'
import { afterEach, describe, expect, it, vi } from 'vitest'
import timestampFixtures from '../../../internal/api/testdata/timestamps.json'
import { Inbox } from '../app/Notifications'
import { resultItem } from '../command/search'
import { ago, normalizeTimestamp } from '../lib/format'
import { notifications, notificationsLoaded } from '../state/notifications'
import type { Notification, SearchResult } from './types'

const fixtures = timestampFixtures as {
  name: string
  field: string
  absent: Record<string, unknown>
  defined: Record<string, unknown>
}[]
const now = Date.parse('2026-03-12T12:00:00Z')

afterEach(() => {
  cleanup()
  notifications.value = []
  notificationsLoaded.value = false
  vi.useRealTimers()
  vi.unstubAllEnvs()
})

describe('serialized optional timestamps', () => {
  it.each(fixtures)('$name has absent and defined display states', (f) => {
    expect(f.absent).not.toHaveProperty(f.field)
    expect(ago(f.absent[f.field] as undefined, now)).toBe('—')
    const defined = f.defined[f.field] as string
    expect(normalizeTimestamp(defined)).toBe(defined)
    expect(defined).toBe('2026-03-12T17:25:00.123456789+05:30')
    expect(ago(defined, now)).toBe('5m ago')
  })
})

describe('timestamp formatting boundary', () => {
  it.each([
    undefined,
    null,
    '',
    '0001-01-01T00:00:00Z',
    '0001-01-01T00:00:00.000000000Z',
    '0001-01-01T01:00:00+01:00',
    '0000-12-31T19:00:00-05:00',
    'invalid',
    '2026-02-30T00:00:00Z',
    '2025-02-29T00:00:00Z',
    '2026-13-01T00:00:00Z',
    '2026-01-00T00:00:00Z',
    '2026-03-12T24:00:00Z',
    '2026-03-12T12:60:00Z',
    '2026-03-12T12:00:60Z',
    '2026-03-12T12:00:00+24:00',
    '2026-03-12T12:00:00+00:60',
    '2026-03-12',
    '2026-03-12T12:00:00',
    ' 2026-03-12T12:00:00Z',
    '2026-03-12T12:00:00.1234567890Z',
  ])('treats %j as unavailable', (value) => {
    expect(normalizeTimestamp(value)).toBeUndefined()
    expect(ago(value, now)).toBe('—')
  })

  it.each([
    '2026-03-12T12:00:00.001Z',
    '2026-03-12T12:00:00.000000001Z',
    '2026-03-13T12:00:00Z',
    '2026-03-12T08:00:01-04:00',
  ])('retains future time %s without inventing an age', (value) => {
    expect(normalizeTimestamp(value)).toBe(value)
    expect(ago(value, now)).toBe('—')
  })

  it.each(['2026-03-12T11:55:00Z', '2026-03-12T17:25:00+05:30', '2026-03-12T07:55:00-04:00'])(
    'formats equivalent UTC offsets equally: %s',
    (value) => {
      expect(normalizeTimestamp(value)).toBe(value)
      expect(ago(value, now)).toBe('5m ago')
    },
  )

  it.each([
    '2024-02-29T00:00:00Z',
    '2000-02-29T00:00:00Z',
    '0001-01-02T00:00:00Z',
    '0001-01-01T00:00:00.000000001Z',
  ])('preserves defined time %s including nonzero year-one instants', (value) => {
    expect(normalizeTimestamp(value)).toBe(value)
  })

  it.each([Number.NaN, Number.POSITIVE_INFINITY, 9e15])(
    'does not invent an age for invalid now %s',
    (clock) => {
      expect(ago('2026-03-12T11:55:00Z', clock)).toBe('—')
    },
  )

  it('preserves sub-minute, hour, calendar and older-date formatting', () => {
    vi.stubEnv('TZ', 'UTC')
    expect(ago('2026-03-12T11:59:30Z', now)).toBe('now')
    expect(ago('2026-03-12T09:00:00Z', now)).toBe('3h ago')
    expect(ago('2026-03-11T09:00:00Z', now)).toBe('Yesterday')
    expect(ago('2026-03-10T12:00:00Z', now)).toBe(
      new Date('2026-03-10T12:00:00Z').toLocaleDateString(undefined, { weekday: 'short' }),
    )
    expect(ago('2025-03-01T12:00:00Z', now)).toBe(
      new Date('2025-03-01T12:00:00Z').toLocaleDateString(undefined, {
        day: 'numeric',
        month: 'short',
        year: 'numeric',
      }),
    )
  })

  it('retains elapsed and local-calendar semantics through both DST transitions', () => {
    vi.stubEnv('TZ', 'America/New_York')
    expect(new Date('2026-03-08T12:00:00-04:00').getHours()).toBe(12)
    expect(ago('2026-03-07T12:00:00-05:00', Date.parse('2026-03-08T12:00:00-04:00'))).toBe('23h ago')
    expect(ago('2026-10-31T12:00:00-04:00', Date.parse('2026-11-01T12:00:00-05:00'))).toBe('Yesterday')
    expect(ago('2026-11-01T01:30:00-04:00', Date.parse('2026-11-01T01:30:00-05:00'))).toBe('1h ago')
    expect(ago('2026-11-01T01:30:00-05:00', Date.parse('2026-11-01T01:30:00-04:00'))).toBe('—')
  })
})

describe('existing browser consumers', () => {
  it.each([
    undefined,
    null,
    '0001-01-01T00:00:00Z',
    'invalid',
    '2026-03-12T12:00:01Z',
    '2026-03-12T11:55:00Z',
  ])('search accessory and rendered inbox handle %j', (value) => {
    vi.useFakeTimers()
    vi.setSystemTime(now)
    // Null is an older-server fixture, not an expansion of the current TS wire contract.
    const search = {
      scope: 'workspaces',
      id: 'fixture',
      title: 'Unused',
      score: 1,
      at: value,
    } as unknown as SearchResult
    const item = resultItem(search)
    expect(item.accessory).toBe(value ? ago(value, now) : undefined)
    const n = {
      id: 'fixture',
      at: value,
      kind: 'system',
      severity: 'info',
      title: 'Fixture',
      body: '',
      read: true,
    } as unknown as Notification
    notifications.value = [n]
    notificationsLoaded.value = true
    const view = render(<Inbox />)
    expect(view.container.querySelector('time')?.textContent).toBe(ago(value, now))
    if (normalizeTimestamp(value))
      expect(view.container.querySelector('time')?.getAttribute('datetime')).toBe(value)
  })
})
