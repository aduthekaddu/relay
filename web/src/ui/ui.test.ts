import { describe, expect, it } from 'vitest'
import { DOT_CHARS, dotLayout, dotRows } from './dotfont'
import { GLYPHS, type GlyphName, glyphDots, isGlyph } from './glyphs'
import { place } from './overlay'
import { sparkPath } from './Sparkline'
import { statusLabel, statusOf } from './StatusDot'
import { visibleRange } from './VirtualList'

describe('dot font', () => {
  it('covers A–Z, 0–9 and punctuation with 7 rows of 5', () => {
    for (const ch of 'ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.:-/!?') {
      expect(DOT_CHARS).toContain(ch)
      const rows = dotRows(ch)
      expect(rows).toHaveLength(7)
      for (const r of rows) expect(r).toMatch(/^[#.]{5}$/)
    }
  })
  it('maps lowercase and unknown characters', () => {
    expect(dotRows('a')).toEqual(dotRows('A'))
    expect(dotRows('\u2603').join('')).not.toContain('#')
  })
  it.each([
    ['', 0],
    ['I', 5],
    ['RELAY', 29],
  ])('dotLayout(%j) is %i columns wide', (text, cols) => {
    const l = dotLayout(text)
    expect(l.cols).toBe(cols)
    expect(l.on.length + l.off.length).toBe([...text].length * 35)
  })
})

describe('glyphs', () => {
  const names = Object.keys(GLYPHS) as GlyphName[]
  it.each(names)('%s is a 7×7 bitmap with lit dots', (name) => {
    const rows = GLYPHS[name]
    expect(rows).toHaveLength(7)
    for (const r of rows) expect(r).toMatch(/^[#.]{7}$/)
    const d = glyphDots(name)
    expect(d.on.length).toBeGreaterThan(0)
    expect(d.on.length + d.off.length).toBe(49)
    expect(glyphDots(name)).toBe(d) // cached
  })
  it('has a glyph for every area', () => {
    for (const a of [
      'home',
      'terminal',
      'agents',
      'files',
      'code',
      'desktop',
      'previews',
      'system',
      'settings',
    ])
      expect(isGlyph(a)).toBe(true)
    expect(isGlyph('toString')).toBe(false)
  })
})

describe('statusOf', () => {
  it.each([
    [undefined, {}, 'idle'],
    ['working', {}, 'working'],
    ['working', { attention: true }, 'needs-you'],
    ['waiting', {}, 'needs-you'],
    ['exited', { exitCode: 0 }, 'exited'],
    ['exited', { exitCode: 2 }, 'failed'],
    ['idle', {}, 'idle'],
  ] as const)('%s %j → %s', (activity, opts, want) => {
    expect(statusOf(activity, opts)).toBe(want)
  })
  it('labels in plain words', () => expect(statusLabel('needs-you')).toBe('Needs you'))
})

describe('place', () => {
  const anchor = { left: 100, top: 100, width: 40, height: 20 }
  it.each([
    ['bottom-start', 1000, 800, { x: 100, y: 126, side: 'bottom' }],
    ['bottom-end', 1000, 800, { x: 40, y: 126, side: 'bottom' }],
    ['top', 1000, 800, { x: 70, y: 126, side: 'bottom' }], // no room above → flips below
    ['right', 1000, 800, { x: 146, y: 60, side: 'right' }],
    ['bottom-start', 150, 800, { x: 42, y: 126, side: 'bottom' }], // clamped into the viewport
  ] as const)('%s in %i×%i', (placement, vw, vh, want) => {
    const r = place(anchor, 100, 100, placement, vw, vh)
    expect(r).toEqual(want)
  })
  it('flips to the top when there is no room below', () => {
    expect(place({ left: 10, top: 700, width: 10, height: 10 }, 50, 80, 'bottom', 400, 760).side).toBe('top')
  })
})

describe('visibleRange', () => {
  it.each([
    [0, 400, 40, 0, 6, [0, 0]],
    [0, 400, 40, 1000, 6, [0, 16]],
    [4000, 400, 40, 1000, 6, [94, 116]],
    [39_800, 400, 40, 1000, 6, [989, 1000]],
    [0, 400, 0, 10, 6, [0, 0]],
  ])('top=%i vh=%i h=%i n=%i', (top, vh, h, n, over, want) => {
    expect(visibleRange(top, vh, h, n, over)).toEqual(want)
  })
})

describe('sparkPath', () => {
  it('draws nothing without values', () => {
    expect(sparkPath([], 100, 20)).toBe('')
  })
  it('spans the width and stays inside the height', () => {
    const d = sparkPath([0, 5, 10], 100, 20, 0, 10)
    const nums = [...d.matchAll(/(-?\d+(?:\.\d+)?)[ ,](-?\d+(?:\.\d+)?)/g)].map((m) => [
      Number(m[1]),
      Number(m[2]),
    ])
    expect(nums.length).toBe(3)
    for (const [x, y] of nums) {
      expect(x).toBeGreaterThanOrEqual(0)
      expect(x).toBeLessThanOrEqual(100)
      expect(y).toBeGreaterThanOrEqual(0)
      expect(y).toBeLessThanOrEqual(20)
    }
    expect(nums[0][1]).toBeGreaterThan(nums[2][1]) // higher value → smaller y
  })
})
