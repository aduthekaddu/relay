// Synthetic notify fixtures; owned registrations and scenario controls.
import type { Notification } from '../../api/types'
import * as db from '../data'
import { body, now, q, qn } from '../helpers'
import { defineMockModule } from '../registry'
import { emit } from '../sockets'
import { newId, noContent, ok } from '../util'

export default defineMockModule('notify', (owner) => {
  const route = owner.http
  route<unknown, Notification[]>('GET', '/notifications', (r) => {
    const unread = q(r, 'unread') === '1' || q(r, 'unread') === 'true'
    return ok(
      (owner.scenarios.state.empty ? [] : db.notifications)
        .filter((n) => !unread || !n.read)
        .slice(0, qn(r, 'limit', 50)),
    )
  })
  route('POST', '/notifications/read', (r) => {
    const b = body<{ ids?: string[]; all?: boolean }>(r)
    const ids = db.notifications.filter((n) => b.all || b.ids?.includes(n.id)).map((n) => n.id)
    for (const n of db.notifications) if (ids.includes(n.id)) n.read = true
    emit('notification.read', { ids, ...(b.all ? { all: true } : {}) })
    return noContent()
  })
  route('DELETE', '/notifications/{id}', (r) => {
    const i = db.notifications.findIndex((n) => n.id === r.params.id)
    if (i >= 0) db.notifications.splice(i, 1)
    return noContent()
  })
  route('POST', '/notify', (r) => {
    const b = body<{
      title: string
      body?: string
      kind?: string
      link?: string
      agent?: string
      severity?: string
    }>(r)
    const n = {
      id: newId('n'),
      kind: (b.kind ?? 'custom') as Notification['kind'],
      title: b.title,
      body: b.body,
      at: now(),
      read: false,
      link: b.link,
      agent: b.agent,
      severity: b.severity as Notification['severity'],
    }
    db.notifications.unshift(n)
    emit('notification', n)
    return ok(n)
  })
  route('GET', '/notify/settings', () => ok(db.notifySettings))
  route('PATCH', '/notify/settings', (r) => ok(Object.assign(db.notifySettings, body(r))))
  route('GET', '/push/key', () => ok({ publicKey: db.notifySettings.vapidKey }))
  route('POST', '/push/subscribe', () => {
    db.notifySettings.devices++
    return noContent()
  })
  route('POST', '/push/unsubscribe', () => noContent())
  route('POST', '/push/test', () => {
    const n = {
      id: newId('n'),
      kind: 'system' as const,
      title: 'Test notification',
      body: 'Synthetic inbox notification; no device delivery was attempted.',
      at: now(),
      read: false,
      severity: 'info' as const,
    }
    db.notifications.unshift(n)
    emit('notification', n)
    return noContent()
  })
})
