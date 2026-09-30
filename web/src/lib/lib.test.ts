import { describe, expect, it } from 'vitest'
import { ago, bytes, compact, duration, pct, tildify } from './format'
import { keyLabels, matchChord, parseChord } from './keys'
import { passwordStrength } from './strength'
import { loginUrl, safeNext } from './url'
import { fromB64u, toB64u } from './webauthn'

describe('safeNext', () => {
  it.each([
    [null, '/'],
    ['', '/'],
    ['/terminal/t1', '/terminal/t1'],
    ['/files?path=%2Ftmp#x', '/files?path=/tmp#x'],
    ['%2Fagents', '/agents'],
    ['https://evil.example/', '/'],
    ['//evil.example/', '/'],
    ['/\\evil.example', '/'],
    ['javascript:alert(1)', '/'],
    ['/login?next=/x', '/'],
    ['/login/totp', '/'],
    ['/a\u0000b', '/'],
    ['%E0%A4%A', '/'],
  ])('%j → %j', (input, want) => {
    expect(safeNext(input)).toBe(want)
  })
  it('builds login URLs', () => {
    expect(loginUrl('/')).toBe('/login')
    expect(loginUrl('/files?path=/a b')).toBe(`/login?next=${encodeURIComponent('/files?path=/a%20b')}`)
  })
})

describe('format', () => {
  it.each([
    [0, '0 B'],
    [1536, '1.5 KB'],
    [10 * 1024 * 1024, '10 MB'],
    [-1, '—'],
    [Number.NaN, '—'],
  ])('bytes(%d) = %s', (n, want) => expect(bytes(n)).toBe(want))
  it.each([
    [42, '42s'],
    [180, '3m'],
    [3600, '1h'],
    [4320, '1h 12m'],
    [86400 * 2 + 3600 * 4, '2d 4h'],
    [-5, '—'],
  ])('duration(%d) = %s', (n, want) => expect(duration(n)).toBe(want))
  const now = Date.parse('2026-03-12T12:00:00Z')
  it.each([
    [undefined, '—'],
    ['nope', '—'],
    ['2026-03-12T11:59:30Z', 'now'],
    ['2026-03-12T11:55:00Z', '5m ago'],
    ['2026-03-12T09:00:00Z', '3h ago'],
  ])('ago(%s)', (iso, want) => expect(ago(iso, now)).toBe(want))
  it('pct, compact, tildify', () => {
    expect(pct(0.4567)).toBe('46%')
    expect(pct(42, true)).toBe('42%')
    expect(compact(999)).toBe('999')
    expect(compact(1234)).toBe('1.2k')
    expect(compact(5_600_000)).toBe('5.6M')
    expect(tildify('/home/u/code', '/home/u')).toBe('~/code')
    expect(tildify('/home/u', '/home/u')).toBe('~')
    expect(tildify('/home/user2/x', '/home/u')).toBe('/home/user2/x')
  })
})

describe('keys', () => {
  it.each([
    [['mod', 'K'], true, ['⌘', 'K']],
    [['mod', 'K'], false, ['Ctrl', 'K']],
    [['shift', 'enter'], false, ['Shift', 'Enter']],
    [['g', 't'], true, ['G', 'T']],
  ])('keyLabels(%j, mac=%s)', (keys, mac, want) => expect(keyLabels(keys, mac)).toEqual(want))
  const ev = (init: KeyboardEventInit) => new KeyboardEvent('keydown', init)
  it.each([
    ['mod+k', ev({ key: 'k', ctrlKey: true }), false, true],
    ['mod+k', ev({ key: 'k', metaKey: true }), true, true],
    ['mod+k', ev({ key: 'k', metaKey: true }), false, false],
    ['mod+k', ev({ key: 'k' }), false, false],
    ['?', ev({ key: '?', shiftKey: true }), false, true],
    ['mod+k', ev({ key: 'л', code: 'KeyK', ctrlKey: true }), false, true],
    ['escape', ev({ key: 'Escape', shiftKey: true }), false, false],
  ])('%s matches %#', (spec, e, mac, want) => expect(matchChord(e, parseChord(spec), mac)).toBe(want))
})

describe('passwordStrength', () => {
  it.each([
    ['short', 0],
    ['password123', 1],
    ['aaaaaaaaaaaa', 1],
  ])('%s scores ≤ %i', (pw, max) => expect(passwordStrength(pw).score).toBeLessThanOrEqual(max))
  it('rewards long mixed passphrases', () => {
    expect(passwordStrength('tidal-Harbor-47-ember-quiet').score).toBeGreaterThanOrEqual(3)
  })
  it('penalises the username', () => {
    const a = passwordStrength('marguerite-2026!', [])
    const b = passwordStrength('marguerite-2026!', ['marguerite'])
    expect(b.bits).toBeLessThan(a.bits)
    expect(b.hint).not.toBe('')
  })
})

describe('base64url', () => {
  it.each([[[]], [[0]], [[255, 254]], [[1, 2, 3]], [[251, 255, 191, 0, 62, 63]]])('round-trips %j', (arr) => {
    const u8 = new Uint8Array(arr)
    const s = toB64u(u8)
    expect(s).not.toMatch(/[+/=]/)
    expect([...new Uint8Array(fromB64u(s))]).toEqual(arr)
  })
  it('accepts padded input and views with offsets', () => {
    expect([...new Uint8Array(fromB64u('AQI='))]).toEqual([1, 2])
    const view = new Uint8Array([9, 1, 2, 9]).subarray(1, 3)
    expect(toB64u(view)).toBe('AQI')
  })
})
