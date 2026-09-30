import { describe, expect, it } from 'vitest'
import { groupRanked, highlightRuns, pushRecent, rankItems } from './rank'
import type { PaletteItem } from './registry'

const item = (id: string, title: string, extra: Partial<PaletteItem> = {}): PaletteItem => ({
  id,
  title,
  ...extra,
})
const items = [
  item('nav.terminal', 'Terminal', { section: 'Go to', keywords: ['shell', 'tty'] }),
  item('nav.files', 'Files', { section: 'Go to' }),
  item('term.new', 'New terminal', { section: 'Terminal' }),
  item('theme.paper', 'Use Paper theme', { section: 'Appearance', keywords: ['light'] }),
  item('app.reload', 'Reload Relay', { section: 'App', subtitle: 'Fetch the latest app' }),
]

describe('rankItems', () => {
  it('keeps input order for an empty query', () => {
    expect(rankItems('  ', items).map((r) => r.item.id)).toEqual(items.map((i) => i.id))
  })
  it.each([
    ['term', 'nav.terminal'],
    ['new t', 'term.new'],
    ['shell', 'nav.terminal'],
    ['light', 'theme.paper'],
    ['fil', 'nav.files'],
  ])('%j ranks %s first', (q, id) => {
    expect(rankItems(q, items)[0]?.item.id).toBe(id)
  })
  it('drops non-matches', () => {
    expect(rankItems('zzqx', items)).toEqual([])
  })
  it('lets recent use break near-ties', () => {
    const plain = rankItems('te', items).map((r) => r.item.id)
    const recent = rankItems('te', items, { recent: ['term.new'] }).map((r) => r.item.id)
    expect(recent.indexOf('term.new')).toBeLessThanOrEqual(plain.indexOf('term.new'))
  })
  it('returns title match indexes for highlighting', () => {
    const r = rankItems('fil', items)[0]
    expect(r.indexes).toEqual([0, 1, 2])
  })
})

describe('groupRanked', () => {
  const r = (id: string, score: number, section?: string) => ({ item: item(id, id, { section }), score })
  it('groups by section in order of first appearance', () => {
    const s = groupRanked([r('a', 0.9, 'X'), r('b', 0.8, 'Y'), r('c', 0.7, 'X'), r('d', 0.6)])
    expect(s.map((x) => [x.title, x.items.map((i) => i.item.id)])).toEqual([
      ['X', ['a', 'c']],
      ['Y', ['b']],
      ['Results', ['d']],
    ])
  })
  it.each([
    [[r('a', 0.9, 'X'), r('b', 0.5, 'X')], true],
    [[r('a', 0.9, 'X'), r('b', 0.88, 'X')], false], // too close to call
    [[r('a', 0.6, 'X'), r('b', 0.2, 'X')], false], // not confident enough
    [[r('a', 0.99, 'X')], false], // single result needs no top hit
  ])('top hit %#', (ranked, want) => {
    expect(groupRanked(ranked, { topHit: true })[0].title === 'Top hit').toBe(want)
  })
})

describe('highlightRuns', () => {
  it.each([
    ['Files', undefined, [{ text: 'Files', hit: false }]],
    [
      'Files',
      [0, 1],
      [
        { text: 'Fi', hit: true },
        { text: 'les', hit: false },
      ],
    ],
    [
      'New terminal',
      [0, 4],
      [
        { text: 'N', hit: true },
        { text: 'ew ', hit: false },
        { text: 't', hit: true },
        { text: 'erminal', hit: false },
      ],
    ],
  ])('%s %j', (text, idx, want) => expect(highlightRuns(text, idx)).toEqual(want))
})

describe('pushRecent', () => {
  it.each([
    [[], 'a', 8, ['a']],
    [['b', 'a'], 'a', 8, ['a', 'b']],
    [['a', 'b', 'c'], 'd', 3, ['d', 'a', 'b']],
  ])('%j + %s', (list, id, max, want) => expect(pushRecent(list, id, max)).toEqual(want))
})
