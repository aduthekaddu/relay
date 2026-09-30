// Compose the palette's root list from commands, provider results and
// recents. Pure (no DOM, no signals) so it is unit-tested.
import { groupRanked, rankItems, type Ranked, type Section } from './rank'
import type { PaletteItem, PaletteProvider } from './registry'
import type { RecentEntry } from './state'

export interface ProviderResult {
  items: PaletteItem[]
  loading: boolean
}

export interface ComposeInput {
  query: string
  commands: PaletteItem[]
  providers: PaletteProvider[]
  results: Record<string, ProviderResult | undefined>
  recents: RecentEntry[]
}

export interface Composed {
  sections: Section[]
  /** Label for shimmer rows, when a provider is still searching. */
  loading: string | false
}

const plain = (item: PaletteItem): Ranked => ({ item, score: 0 })

function group(items: PaletteItem[], fallback: string): Section[] {
  return groupRanked(items.map(plain), { fallback })
}

/** Resolve a stored recent entry to a live item (commands by id, others by link). */
export function resolveRecent(e: RecentEntry, commands: Map<string, PaletteItem>): PaletteItem | null {
  const cmd = commands.get(e.id)
  if (cmd) return { ...cmd, section: 'Recent' }
  if (e.id.startsWith('cmd:') || !e.link) return null
  return { id: e.id, title: e.title, subtitle: e.subtitle, icon: e.icon, link: e.link, section: 'Recent', runLabel: 'Open' }
}

/** Build the root view's sections. */
export function composeRoot(input: ComposeInput): Composed {
  const q = input.query.trim()
  const seen = new Set<string>()
  const uniq = (items: PaletteItem[]) =>
    items.filter((it) => {
      if (seen.has(it.id)) return false
      seen.add(it.id)
      return true
    })
  const byId = new Map(input.commands.map((c) => [c.id, c]))
  const sections: Section[] = []
  let loading: string | false = false

  const provItems = (p: PaletteProvider) => input.results[p.id]?.items ?? []
  const top = input.providers.filter((p) => p.top)
  const rest = input.providers.filter((p) => !p.top)

  for (const p of top) sections.push(...group(uniq(provItems(p)), p.section))

  if (!q) {
    const recent = uniq(
      input.recents
        .map((e) => resolveRecent(e, byId))
        .filter((x): x is PaletteItem => !!x)
        .slice(0, 5),
    )
    if (recent.length) sections.push({ title: 'Recent', items: recent.map(plain) })
    for (const p of rest) sections.push(...group(uniq(provItems(p)), p.section))
    sections.push(...group(uniq(input.commands), 'Commands'))
    return { sections: sections.filter((s) => s.items.length), loading }
  }

  const fuzzyPool = [...input.commands, ...rest.filter((p) => p.fuzzy).flatMap(provItems)]
  const ranked = rankItems(q, fuzzyPool, { recent: input.recents.map((r) => r.id) }).filter((r) => !seen.has(r.item.id))
  for (const r of ranked) seen.add(r.item.id)
  sections.push(...groupRanked(ranked, { topHit: true, fallback: 'Commands' }))

  for (const p of rest.filter((p) => !p.fuzzy)) {
    const res = input.results[p.id]
    sections.push(...group(uniq(res?.items ?? []), p.section))
    if (res?.loading && !res.items.length) loading = 'Searching this machine…'
  }
  return { sections: sections.filter((s) => s.items.length), loading }
}

/** Fallback rows when nothing matched (keyboard-reachable suggestions). */
export function noResultItems(query: string): PaletteItem[] {
  const q = query.trim()
  const enc = encodeURIComponent(q)
  return [
    { id: 'try:files', title: `Search files for “${q}”`, icon: 'glyph:files', link: `/files?q=${enc}`, section: 'Try', remember: false, runLabel: 'Search' },
    { id: 'try:agents', title: `Search agent sessions for “${q}”`, icon: 'glyph:agents', link: `/agents?q=${enc}`, section: 'Try', remember: false, runLabel: 'Search' },
  ]
}
