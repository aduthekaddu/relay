// Server-side federated search (GET /api/v1/search) as a palette provider.
// Debounced 80 ms by the palette; stale requests are aborted.
import { api, qs } from '../api/client'
import type { SearchResponse, SearchResult, SearchScope } from '../api/types'
import { ago } from '../lib/format'
import type { Status } from '../ui/StatusDot'
import type { PaletteAction, PaletteItem, PaletteProvider } from './registry'

export const SCOPE_LABEL: Record<SearchScope, string> = {
  terminals: 'Terminals',
  agents: 'Agent sessions',
  history: 'In transcripts',
  files: 'Files',
  workspaces: 'Workspaces',
  previews: 'Previews',
  processes: 'Processes',
  snippets: 'Snippets',
  notes: 'Notes',
  scripts: 'Scripts',
}

const SCOPE_ICON: Record<SearchScope, string> = {
  terminals: 'glyph:terminal',
  agents: 'glyph:agents',
  history: 'glyph:agents',
  files: 'file',
  workspaces: 'folder',
  previews: 'glyph:previews',
  processes: 'glyph:system',
  snippets: 'glyph:spark',
  notes: 'file',
  scripts: 'command',
}

const ACTIVITY: Record<string, Status> = { working: 'working', waiting: 'needs-you', idle: 'idle', exited: 'exited' }

/** Map one server result to a palette row (pure; tested). */
export function resultItem(r: SearchResult): PaletteItem {
  const actions: PaletteAction[] = []
  const path = r.meta?.path
  if (r.link) {
    const link = r.link
    actions.push({ id: 'new-tab', title: 'Open in new tab', icon: 'external-link', shortcut: ['mod', 'enter'], run: () => void window.open(link, '_blank', 'noopener') })
    actions.push({ id: 'copy-link', title: 'Copy link', icon: 'copy', run: (ctx) => ctx.copy(new URL(link, location.origin).href, 'Link') })
  }
  if (path) actions.push({ id: 'copy-path', title: 'Copy path', icon: 'copy', shortcut: ['mod', 'shift', 'C'], run: (ctx) => ctx.copy(path, 'Path') })
  const status = r.meta?.activity ? ACTIVITY[r.meta.activity] : undefined
  const agent = r.meta?.agent
  return {
    id: `s:${r.scope}:${r.id}`,
    title: r.title,
    subtitle: r.subtitle,
    icon: r.icon ?? (agent ? `agent:${agent}` : SCOPE_ICON[r.scope]),
    status,
    section: SCOPE_LABEL[r.scope] ?? 'Results',
    accessory: r.at ? ago(r.at) : undefined,
    link: r.link,
    runLabel: 'Open',
    score: r.score,
    actions,
  }
}

/** Order server results: by section best score, then score (pure). */
export function orderResults(results: SearchResult[]): SearchResult[] {
  const best = new Map<string, number>()
  for (const r of results) best.set(r.scope, Math.max(best.get(r.scope) ?? 0, r.score))
  return [...results].sort((a, b) => (best.get(b.scope) ?? 0) - (best.get(a.scope) ?? 0) || a.scope.localeCompare(b.scope) || b.score - a.score)
}

export const serverSearch: PaletteProvider = {
  id: 'server',
  section: 'Results',
  minQuery: 2,
  debounce: 80,
  async search(query, signal) {
    const res = await api.get<SearchResponse>(`search${qs({ q: query, limit: 8 })}`, { signal })
    return orderResults(res.results ?? []).map(resultItem)
  },
}
