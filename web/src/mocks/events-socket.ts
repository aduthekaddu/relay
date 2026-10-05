// Synthetic public event snapshots and subscription stream; no server proof.
import type { ClientEvent } from '../api/types'
import * as db from './data'
import { eventSockets, liveNotification } from './event-broker'
import { metricsAt } from './system'
import { FakeSocket, has } from './util'
/** /api/v1/events */
export class FakeEvents extends FakeSocket {
  private topics = new Set<string>()
  private visible = true

  constructor(
    url: string,
    private empty = false,
  ) {
    super(url, { fail: has('offline') })
  }

  protected override opened(): void {
    eventSockets.add(this)
    this.pushText({ type: 'hello', at: new Date().toISOString(), data: db.info })
    for (const t of this.empty ? [] : db.terminals)
      this.pushText({ type: 'terminal.updated', at: new Date().toISOString(), data: t })
    this.every(1000, () => {
      if (this.topics.has('metrics') && this.visible)
        this.pushText({ type: 'metrics', at: new Date().toISOString(), data: metricsAt(Date.now()) })
    })
    // Keep working sessions visibly alive.
    this.every(3500, () => {
      for (const t of this.empty ? [] : db.terminals) {
        if (t.activity !== 'working') continue
        t.lastOutputAt = new Date().toISOString()
        this.pushText({ type: 'terminal.updated', at: t.lastOutputAt, data: t })
      }
    })
    if (has('live')) this.every(20_000, () => liveNotification())
  }

  protected override onClientText(text: string): void {
    let msg: ClientEvent
    try {
      msg = JSON.parse(text) as ClientEvent
    } catch {
      return
    }
    if (!msg || typeof msg !== 'object') return
    if (msg.type === 'ping') this.pushText({ type: 'pong', at: new Date().toISOString() })
    if (msg.type === 'visibility') this.visible = msg.visible ?? false
    if (
      msg.topics !== undefined &&
      (!Array.isArray(msg.topics) || msg.topics.some((topic) => typeof topic !== 'string'))
    )
      return
    if (msg.type === 'subscribe')
      for (const t of msg.topics ?? []) {
        if (this.topics.size < 16 && /^[a-z][a-z0-9._-]{0,63}$/.test(t)) this.topics.add(t)
      }
    if (msg.type === 'unsubscribe') for (const t of msg.topics ?? []) this.topics.delete(t)
  }

  override end(code?: number, reason?: string): void {
    eventSockets.delete(this)
    this.topics.clear()
    super.end(code, reason)
  }
}
