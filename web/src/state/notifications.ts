// The notification inbox: list, unread count, mark read, remove. Live
// `notification` events prepend and raise an in-app toast for kinds that
// need the user (unless they are already looking at the linked page).
import { computed, signal } from '@preact/signals'
import { api, qs, seg } from '../api/client'
import { on } from '../api/events'
import type { Notification } from '../api/types'
import { toast } from '../ui/Toast'

/** Newest first, at most 100 kept in memory. */
export const notifications = signal<Notification[]>([])
export const notificationsLoaded = signal(false)

/** Count of unread notifications (drives the bell badge). */
export const unreadCount = computed(() => notifications.value.reduce((n, x) => n + (x.read ? 0 : 1), 0))

const LIMIT = 100

/** Merge incoming notifications into a list (dedupe by id, newest first). Pure. */
export function mergeNotifications(list: Notification[], incoming: Notification[]): Notification[] {
  const byId = new Map(list.map((n) => [n.id, n]))
  for (const n of incoming) byId.set(n.id, n)
  return [...byId.values()].sort((a, b) => b.at.localeCompare(a.at)).slice(0, LIMIT)
}

export async function loadNotifications(): Promise<void> {
  try {
    const list = await api.get<Notification[]>(`notifications${qs({ limit: 50 })}`)
    notifications.value = mergeNotifications([], list)
    notificationsLoaded.value = true
  } catch {
    /* keep */
  }
}

function applyRead(ids: string[] | 'all'): void {
  notifications.value = notifications.value.map((n) =>
    ids === 'all' || ids.includes(n.id) ? { ...n, read: true } : n,
  )
}

/** Mark some notifications read (optimistic; reverts on failure). */
export async function markRead(ids: string[]): Promise<void> {
  if (ids.length === 0) return
  const before = notifications.value
  applyRead(ids)
  try {
    await api.post('notifications/read', { ids })
  } catch {
    notifications.value = before
  }
}

export async function markAllRead(): Promise<void> {
  const before = notifications.value
  applyRead('all')
  try {
    await api.post('notifications/read', { all: true })
  } catch {
    notifications.value = before
  }
}

export async function removeNotification(id: string): Promise<void> {
  const before = notifications.value
  notifications.value = before.filter((n) => n.id !== id)
  try {
    await api.del(`notifications/${seg(id)}`)
  } catch {
    notifications.value = before
  }
}

/** Kinds that raise an in-app toast when they arrive. */
const TOAST_KINDS = new Set(['attention', 'done', 'security', 'preview', 'schedule', 'exited'])

let navigate: ((url: string) => void) | null = null
/** The shell provides client-side navigation for toast actions. */
export function setNotificationNavigator(fn: (url: string) => void): void {
  navigate = fn
}

let wired = false
/** Wire live events (called by startLive). */
export function trackNotifications(): void {
  if (wired) return
  wired = true
  on('notification', (n) => {
    notifications.value = mergeNotifications(notifications.value, [n])
    if (!TOAST_KINDS.has(n.kind) || document.visibilityState !== 'visible') return
    if (n.link && location.pathname + location.search === n.link) return
    toast(n.title, {
      id: `n:${n.id}`,
      kind:
        n.kind === 'attention'
          ? 'attention'
          : n.severity === 'danger'
            ? 'danger'
            : n.severity === 'warning'
              ? 'warning'
              : n.kind === 'done'
                ? 'success'
                : 'info',
      body: n.body,
      action: n.link
        ? {
            label: n.kind === 'attention' ? 'Open' : 'View',
            onClick: () => {
              void markRead([n.id])
              if (n.link) navigate ? navigate(n.link) : location.assign(n.link)
            },
          }
        : undefined,
    })
  })
  on('notification.read', (d) => applyRead(d.all ? 'all' : (d.ids ?? [])))
  on('hello', () => {
    void loadNotifications()
  })
}
