// Connection status for the whole app, derived from the events socket and
// the browser's online flag. Read `connection.value` in components.
import { computed, signal } from '@preact/signals'
import { events, type StatusInfo } from '../api/events'

export type ConnectionState = 'connecting' | 'online' | 'reconnecting' | 'offline'

export interface Connection {
  state: ConnectionState
  /** Failed attempts in a row. */
  failures: number
  /** Next retry (ms epoch) while reconnecting. */
  retryAt?: number
  /** When the state last changed (ms epoch). */
  since: number
}

/** Current connection to the machine. */
export const connection = signal<Connection>({ state: 'connecting', failures: 0, since: Date.now() })

/** True when live events are flowing. */
export const isOnline = computed(() => connection.value.state === 'online')

/** Map socket status + navigator.onLine to a user-facing state (pure; tested). */
export function deriveConnection(
  s: StatusInfo,
  browserOnline: boolean,
  hasConnected: boolean,
): ConnectionState {
  if (!browserOnline) return 'offline'
  if (s.state === 'open') return 'online'
  if (!hasConnected && s.failures < 2) return 'connecting'
  return 'reconnecting'
}

let wired = false
let everConnected = false

/** Start tracking (called once by startLive). */
export function trackConnection(): void {
  if (wired) return
  wired = true
  const update = (s: StatusInfo) => {
    if (s.state === 'open') everConnected = true
    const state = deriveConnection(s, navigator.onLine !== false, everConnected)
    const prev = connection.value
    if (prev.state === state && prev.failures === s.failures && prev.retryAt === s.retryAt) return
    connection.value = {
      state,
      failures: s.failures,
      retryAt: s.retryAt,
      since: prev.state === state ? prev.since : Date.now(),
    }
  }
  events.onStatus(update)
  window.addEventListener('online', () => update(events.getStatus()))
  window.addEventListener('offline', () => update(events.getStatus()))
}

/** Try to reconnect right now. */
export const retryConnection = (): void => events.reconnect()
