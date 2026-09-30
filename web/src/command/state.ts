// Palette open/close state and recent items. Tiny and eager: the shell
// imports it; the Palette UI itself is lazy (command/Palette.tsx).
import { signal } from '@preact/signals'
import { load, save } from '../lib/util'
import { pushRecent } from './rank'
import type { PaletteItem, PaletteView } from './registry'

export interface PaletteRequest {
  /** Pre-filled query. */
  query?: string
  /** Open directly into a sub-view. */
  view?: PaletteView
}

/** Non-null while the palette is open. */
export const palette = signal<PaletteRequest | null>(null)

export function openPalette(req: PaletteRequest = {}): void {
  palette.value = req
}

export function closePalette(): void {
  palette.value = null
}

export function togglePalette(): void {
  palette.value = palette.value ? null : {}
}

/** Serializable memory of a run item (commands resolve by id; others by link). */
export interface RecentEntry {
  id: string
  title: string
  subtitle?: string
  icon?: string
  link?: string
  at: number
}

const KEY = 'relay.palette.recent'
const MAX = 8

function valid(v: unknown): v is RecentEntry[] {
  return Array.isArray(v) && v.every((e) => e && typeof e.id === 'string' && typeof e.title === 'string')
}

/** Recently run items, newest first. */
export const recents = signal<RecentEntry[]>((() => {
  const v = load<unknown>(KEY, [])
  return valid(v) ? v.slice(0, MAX) : []
})())

/** Remember an item after it ran (skips items with remember: false). */
export function rememberItem(item: PaletteItem): void {
  if (item.remember === false) return
  if (!item.id.startsWith('cmd:') && !item.link) return
  const entry: RecentEntry = { id: item.id, title: item.title, subtitle: item.subtitle, icon: item.icon, link: item.link, at: Date.now() }
  const order = pushRecent(recents.value.map((r) => r.id), item.id, MAX)
  const byId = new Map(recents.value.map((r) => [r.id, r]))
  byId.set(item.id, entry)
  recents.value = order.map((id) => byId.get(id)!).filter(Boolean)
  save(KEY, recents.value)
}

export function forgetRecent(id: string): void {
  recents.value = recents.value.filter((r) => r.id !== id)
  save(KEY, recents.value)
}

export function clearRecents(): void {
  recents.value = []
  save(KEY, [])
}
