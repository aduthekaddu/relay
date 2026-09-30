// The live events client: one WebSocket to /api/v1/events per tab.
//
//   connectEvents()                 start (idempotent; the shell calls it)
//   on('terminal.updated', fn)      typed handler → unsubscribe
//   subscribe('metrics')            refcounted topic → unsubscribe
//   setVisiblePath(path)            tells the server what is on screen
//   onStatus(fn)                    connection lifecycle
//
// Reconnects with jittered exponential backoff (0.5 s → 10 s), resends
// topics and visibility after every reconnect, pings every 25 s and treats
// 65 s of silence as a dead connection.
import { wsUrl } from './client'
import type { ClientEvent, EventType, Info, Notification, RelayEvent, TerminalSession } from './types'

/** Payload type per event (unknown for events owned by later features). */
export interface EventMap {
  hello: Info
  'terminal.created': TerminalSession
  'terminal.updated': TerminalSession
  'terminal.exited': TerminalSession
  'terminal.removed': { id: string }
  notification: Notification
  'notification.read': { ids?: string[]; all?: boolean }
  [k: string]: unknown
}

export type EventName = EventType | '*'
export type Handler<K extends string> = (data: K extends keyof EventMap ? EventMap[K] : unknown, ev: RelayEvent) => void

export type SocketState = 'idle' | 'connecting' | 'open' | 'closed'
export interface StatusInfo {
  state: SocketState
  /** Consecutive failed attempts since the last successful open. */
  failures: number
  /** When the next attempt is scheduled (ms epoch), while closed. */
  retryAt?: number
}

/** Delay before reconnect attempt `n` (0-based): 0.5 s · 2ⁿ, max 10 s, ±20% jitter. */
export function backoffDelay(n: number, rand: number = Math.random()): number {
  const base = Math.min(10_000, 500 * 2 ** Math.min(n, 5))
  return Math.round(base * (0.8 + rand * 0.4))
}

type SocketFactory = (url: string) => WebSocket

const PING_MS = 25_000
const STALE_MS = 65_000

class LiveEvents {
  private ws: WebSocket | null = null
  private handlers = new Map<string, Set<Handler<string>>>()
  private statusFns = new Set<(s: StatusInfo) => void>()
  private topics = new Map<string, number>()
  private status: StatusInfo = { state: 'idle', failures: 0 }
  private retryTimer = 0
  private pingTimer = 0
  private lastMessage = 0
  private path = typeof location !== 'undefined' ? location.pathname : '/'
  private started = false
  private factory: SocketFactory = (url) => new WebSocket(url)

  /** Replace how sockets are created (tests). */
  setFactory(fn: SocketFactory): void {
    this.factory = fn
  }

  connect(): void {
    if (this.started) return
    this.started = true
    document.addEventListener('visibilitychange', this.onVisibility)
    window.addEventListener('online', this.onOnline)
    this.open()
  }

  disconnect(): void {
    this.started = false
    document.removeEventListener('visibilitychange', this.onVisibility)
    window.removeEventListener('online', this.onOnline)
    window.clearTimeout(this.retryTimer)
    window.clearInterval(this.pingTimer)
    const ws = this.ws
    this.ws = null
    if (ws) {
      ws.onclose = null
      ws.close(1000)
    }
    this.setStatus({ state: 'idle', failures: 0 })
  }

  /** Force an immediate reconnect attempt (e.g. "Retry now"). */
  reconnect(): void {
    if (!this.started) return
    window.clearTimeout(this.retryTimer)
    if (this.ws && this.ws.readyState <= 1) this.ws.close(4000)
    else this.open()
  }

  on<K extends EventName>(type: K, fn: Handler<K>): () => void {
    let set = this.handlers.get(type)
    if (!set) this.handlers.set(type, (set = new Set()))
    set.add(fn as Handler<string>)
    return () => set?.delete(fn as Handler<string>)
  }

  onStatus(fn: (s: StatusInfo) => void): () => void {
    this.statusFns.add(fn)
    fn(this.status)
    return () => this.statusFns.delete(fn)
  }

  getStatus(): StatusInfo {
    return this.status
  }

  subscribe(topic: string): () => void {
    const n = this.topics.get(topic) ?? 0
    this.topics.set(topic, n + 1)
    if (n === 0) this.send({ type: 'subscribe', topics: [topic] })
    let done = false
    return () => {
      if (done) return
      done = true
      const left = (this.topics.get(topic) ?? 1) - 1
      if (left <= 0) {
        this.topics.delete(topic)
        this.send({ type: 'unsubscribe', topics: [topic] })
      } else this.topics.set(topic, left)
    }
  }

  setVisiblePath(path: string): void {
    this.path = path
    this.sendVisibility()
  }

  /** Emit an event locally, as if the server sent it (mocks, optimistic UI). */
  dispatch(ev: RelayEvent): void {
    for (const key of [ev.type, '*']) {
      const set = this.handlers.get(key)
      if (!set) continue
      for (const fn of set) {
        try {
          fn(ev.data as never, ev)
        } catch (err) {
          console.error(`[events] handler for ${ev.type} failed`, err)
        }
      }
    }
  }

  // ------------------------------------------------------------ internals

  private open(): void {
    if (!this.started) return
    this.setStatus({ ...this.status, state: 'connecting', retryAt: undefined })
    let ws: WebSocket
    try {
      ws = this.factory(wsUrl('events'))
    } catch {
      this.scheduleRetry()
      return
    }
    this.ws = ws
    ws.onopen = () => {
      this.lastMessage = Date.now()
      this.setStatus({ state: 'open', failures: 0 })
      if (this.topics.size) this.send({ type: 'subscribe', topics: [...this.topics.keys()] })
      this.sendVisibility()
      window.clearInterval(this.pingTimer)
      this.pingTimer = window.setInterval(this.tick, PING_MS)
    }
    ws.onmessage = (m) => {
      this.lastMessage = Date.now()
      if (typeof m.data !== 'string') return
      let ev: RelayEvent
      try {
        ev = JSON.parse(m.data) as RelayEvent
      } catch {
        return
      }
      if (ev && typeof ev.type === 'string') this.dispatch(ev)
    }
    ws.onclose = () => {
      window.clearInterval(this.pingTimer)
      if (this.ws !== ws) return
      this.ws = null
      const wasOpen = this.status.state === 'open'
      this.status = { ...this.status, failures: wasOpen ? 0 : this.status.failures + 1 }
      this.scheduleRetry()
    }
    ws.onerror = () => {
      /* onclose follows; nothing to do here */
    }
  }

  private scheduleRetry(): void {
    if (!this.started) return
    const delay = navigator.onLine === false ? 30_000 : backoffDelay(this.status.failures)
    window.clearTimeout(this.retryTimer)
    this.retryTimer = window.setTimeout(() => this.open(), delay)
    this.setStatus({ ...this.status, state: 'closed', retryAt: Date.now() + delay })
  }

  private tick = () => {
    if (Date.now() - this.lastMessage > STALE_MS) {
      this.ws?.close(4001)
      return
    }
    this.send({ type: 'ping' })
  }

  private send(msg: ClientEvent): void {
    if (this.ws?.readyState === 1) this.ws.send(JSON.stringify(msg))
  }

  private sendVisibility(): void {
    this.send({ type: 'visibility', visible: document.visibilityState === 'visible', path: this.path })
  }

  private onVisibility = () => {
    this.sendVisibility()
    if (document.visibilityState === 'visible' && this.status.state === 'closed') this.reconnect()
  }

  private onOnline = () => {
    if (this.status.state === 'closed') this.reconnect()
  }

  private setStatus(s: StatusInfo): void {
    this.status = s
    for (const fn of this.statusFns) fn(s)
  }
}

/** The app-wide events connection. */
export const events = new LiveEvents()

export const connectEvents = (): void => events.connect()
export const on = <K extends EventName>(type: K, fn: Handler<K>): (() => void) => events.on(type, fn)
export const subscribe = (topic: string): (() => void) => events.subscribe(topic)
export const setVisiblePath = (path: string): void => events.setVisiblePath(path)
