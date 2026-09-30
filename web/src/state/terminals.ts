// Live terminal sessions, keyed by id. Kept current by terminal.* events and
// re-fetched after every (re)connect so nothing goes stale while offline.
import { computed, signal } from '@preact/signals'
import { api } from '../api/client'
import { on } from '../api/events'
import type { TerminalSession } from '../api/types'

/** All known terminal sessions by id (includes exited and importable tmux). */
export const terminals = signal<ReadonlyMap<string, TerminalSession>>(new Map())

/** True once the first list has loaded. */
export const terminalsLoaded = signal(false)

const rank = (t: TerminalSession) =>
  t.attention || t.activity === 'waiting' ? 0 : t.activity === 'working' ? 1 : t.activity === 'idle' ? 2 : 3

/** Sort: needs you → working → idle → exited; pinned first within a group; newest first. */
export function sortTerminals(list: TerminalSession[]): TerminalSession[] {
  return [...list].sort(
    (a, b) =>
      rank(a) - rank(b) ||
      Number(b.pinned) - Number(a.pinned) ||
      (b.lastOutputAt ?? b.createdAt).localeCompare(a.lastOutputAt ?? a.createdAt),
  )
}

/** Sessions sorted for display, excluding importable tmux placeholders. */
export const terminalList = computed(() =>
  sortTerminals([...terminals.value.values()].filter((t) => t.meta?.importable !== '1')),
)

/** Sessions waiting on the user. */
export const needsYou = computed(() =>
  terminalList.value.filter((t) => t.activity !== 'exited' && (!!t.attention || t.activity === 'waiting')),
)

/** Sessions currently producing output. */
export const working = computed(() => terminalList.value.filter((t) => t.activity === 'working'))

/** Insert or replace one session. */
export function upsertTerminal(t: TerminalSession): void {
  const next = new Map(terminals.value)
  next.set(t.id, t)
  terminals.value = next
}

/** Remove a session by id. */
export function removeTerminal(id: string): void {
  if (!terminals.value.has(id)) return
  const next = new Map(terminals.value)
  next.delete(id)
  terminals.value = next
}

/** Replace the whole set from GET /terminals. */
export async function loadTerminals(): Promise<void> {
  try {
    const list = await api.get<TerminalSession[]>('terminals')
    terminals.value = new Map(list.map((t) => [t.id, t]))
    terminalsLoaded.value = true
  } catch {
    /* keep the last known list */
  }
}

let wired = false
/** Wire terminal.* events (called by startLive). */
export function trackTerminals(): void {
  if (wired) return
  wired = true
  on('terminal.created', upsertTerminal)
  on('terminal.updated', upsertTerminal)
  on('terminal.exited', upsertTerminal)
  on('terminal.removed', (d) => removeTerminal(d.id))
  on('hello', () => {
    void loadTerminals()
  })
}
