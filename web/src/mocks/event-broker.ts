// Synthetic broadcast fixtures. Backend-only topics never reach browsers.
import type { EventMap } from '../api/events'
import type { RelayEvent } from '../api/types'
import * as db from './data'
import type { FakeSocket } from './util'
export const eventSockets = new Set<FakeSocket>()
export function emit<K extends string>(type: K, data?: EventMap[K]): void {
  if (['audit', 'clip.capture', 'auth.session.revoked'].includes(type)) return
  const ev: RelayEvent = { type, at: new Date().toISOString(), data }
  for (const socket of eventSockets) socket.pushText(ev)
}
let liveSeq = 0
/** Push a synthetic notification (also window.__relayMock.notify()). */
export function liveNotification(kind: 'attention' | 'done' = liveSeq % 2 ? 'done' : 'attention'): void {
  const n = {
    id: `n_live${++liveSeq}`,
    kind,
    title: kind === 'attention' ? 'Codex needs you' : 'Claude Code finished',
    body: kind === 'attention' ? 'Approve editing app/limits.py?' : 'Refactor share links — 6 files changed',
    at: new Date().toISOString(),
    read: false,
    link: kind === 'attention' ? '/terminal/t_codex1' : '/terminal/t_claude1',
    agent: kind === 'attention' ? 'codex' : 'claude',
  } as const
  db.notifications.unshift({ ...n })
  emit('notification', n)
}

export function resetStreams(): void {
  eventSockets.clear()
  liveSeq = 0
}
