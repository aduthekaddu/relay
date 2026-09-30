// Palette ranking: fuzzy match (fuzzysort) over title, keywords and
// subtitle with small bonuses for priority and recent use, then grouping
// into sections. Pure functions; tested in rank.test.ts.
import fuzzysort from 'fuzzysort'
import type { PaletteItem } from './registry'

export interface Ranked {
  item: PaletteItem
  score: number
  /** Matched character indexes in the title (for highlighting). */
  indexes?: readonly number[]
}

export interface Section {
  title: string
  items: Ranked[]
}

interface Target {
  item: PaletteItem
  title: string
  keywords: string
  subtitle: string
}

const WEIGHTS = [1, 0.88, 0.72] as const

/** Rank `items` for `query`. Empty query keeps the input order. */
export function rankItems(
  query: string,
  items: PaletteItem[],
  opts: { recent?: readonly string[]; limit?: number } = {},
): Ranked[] {
  const q = query.trim()
  const recent = opts.recent ?? []
  if (!q) return items.map((item) => ({ item, score: 0 }))
  const targets: Target[] = items.map((item) => ({
    item,
    title: item.title,
    keywords: item.keywords?.join(' ') ?? '',
    subtitle: item.subtitle ?? '',
  }))
  const results = fuzzysort.go(q, targets, {
    keys: ['title', 'keywords', 'subtitle'],
    threshold: 0.2,
    limit: opts.limit ?? 60,
    scoreFn: (r) => Math.max(...[0, 1, 2].map((i) => (r[i] ? r[i].score * WEIGHTS[i] : 0))),
  })
  const out: Ranked[] = results.map((r) => {
    const ri = recent.indexOf(r.obj.item.id)
    const bonus = (r.obj.item.priority ?? 0) * 0.01 + (ri >= 0 ? 0.06 - ri * 0.005 : 0)
    return { item: r.obj.item, score: r.score + bonus, indexes: r[0]?.indexes }
  })
  // A prefix match on the title beats everything else of similar quality.
  const lq = q.toLowerCase()
  for (const r of out) if (r.item.title.toLowerCase().startsWith(lq)) r.score += 0.08
  return out.sort((a, b) => b.score - a.score)
}

/**
 * Group ranked items into sections in order of each section's best score.
 * With `topHit`, a clearly best first result (≥ 0.75 and ahead of the
 * runner-up) is lifted into its own "Top hit" section.
 */
export function groupRanked(ranked: Ranked[], opts: { topHit?: boolean; fallback?: string } = {}): Section[] {
  const fallback = opts.fallback ?? 'Results'
  const sections: Section[] = []
  let rest = ranked
  if (opts.topHit && ranked.length > 1 && ranked[0].score >= 0.75 && ranked[0].score - ranked[1].score > 0.04) {
    sections.push({ title: 'Top hit', items: [ranked[0]] })
    rest = ranked.slice(1)
  }
  const bySection = new Map<string, Section>()
  for (const r of rest) {
    const title = r.item.section ?? fallback
    let s = bySection.get(title)
    if (!s) {
      s = { title, items: [] }
      bySection.set(title, s)
      sections.push(s)
    }
    s.items.push(r)
  }
  return sections
}

/** Split a title into plain/highlighted runs for the given indexes. */
export function highlightRuns(text: string, indexes?: readonly number[]): Array<{ text: string; hit: boolean }> {
  if (!indexes?.length) return [{ text, hit: false }]
  const set = new Set(indexes)
  const runs: Array<{ text: string; hit: boolean }> = []
  for (let i = 0; i < text.length; i++) {
    const hit = set.has(i)
    const last = runs[runs.length - 1]
    if (last && last.hit === hit) last.text += text[i]
    else runs.push({ text: text[i], hit })
  }
  return runs
}

/** Keep the most recent ids first, unique, at most `max`. Pure. */
export function pushRecent(list: readonly string[], id: string, max = 8): string[] {
  return [id, ...list.filter((x) => x !== id)].slice(0, max)
}
