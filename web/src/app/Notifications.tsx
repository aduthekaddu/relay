// Notifications bell + inbox. Desktop: a popover under the bell; phones:
// a bottom sheet. Items deep-link (and are marked read on open).
import { signal } from '@preact/signals'
import { useRef } from 'preact/hooks'
import type { Notification, NotificationKind } from '../api/types'
import { ago } from '../lib/format'
import { cx } from '../lib/util'
import {
  markAllRead,
  markRead,
  notifications,
  notificationsLoaded,
  removeNotification,
  unreadCount,
} from '../state/notifications'
import { AgentMark } from '../ui/AgentMark'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { EmptyState } from '../ui/layout'
import { useMedia } from '../ui/overlay'
import { Popover } from '../ui/Popover'
import { Sheet } from '../ui/Sheet'
import { StatusDot } from '../ui/StatusDot'
import { navigate } from './nav'

/** Inbox visibility (the bell toggles it; `notifications` command opens it). */
export const inboxOpen = signal(false)

const KIND_ICON: Record<NotificationKind, string> = {
  attention: '',
  done: 'check',
  exited: 'terminal',
  preview: 'glyph:previews',
  security: 'shield-check',
  system: 'info',
  schedule: 'clock',
  custom: 'bell',
}

const KIND_LABEL: Record<NotificationKind, string> = {
  attention: 'Needs you',
  done: 'Done',
  exited: 'Exited',
  preview: 'Preview',
  security: 'Security',
  system: 'System',
  schedule: 'Schedule',
  custom: 'Message',
}

function KindMark({ n }: { n: Notification }) {
  if (n.kind === 'attention') return <StatusDot status="needs-you" />
  const tone =
    n.severity === 'danger'
      ? 'danger'
      : n.severity === 'warning'
        ? 'warn'
        : n.kind === 'done' || n.severity === 'success'
          ? 'ok'
          : 'neutral'
  return (
    <span class={cx('inbox-kind', `inbox-kind--${tone}`)}>
      <Icon name={KIND_ICON[n.kind] || 'bell'} size={14} />
    </span>
  )
}

export function BellButton({ size = 'md' }: { size?: 'md' | 'lg' }) {
  const ref = useRef<HTMLButtonElement>(null)
  const n = unreadCount.value
  const narrow = useMedia('(max-width: 767px)')
  return (
    <>
      <button
        ref={ref}
        type="button"
        class={cx('bell', size === 'lg' && 'bell--lg', inboxOpen.value && 'is-open')}
        aria-label={n ? `Notifications, ${n} unread` : 'Notifications'}
        aria-haspopup="dialog"
        aria-expanded={inboxOpen.value}
        onClick={() => {
          inboxOpen.value = !inboxOpen.value
        }}
      >
        <Icon name="bell" size={18} />
        {n > 0 && <span class="bell__count tnum">{n > 99 ? '99+' : n}</span>}
      </button>
      {narrow ? (
        <Sheet
          open={inboxOpen.value}
          onClose={() => (inboxOpen.value = false)}
          title="Notifications"
          side="bottom"
        >
          <Inbox />
        </Sheet>
      ) : (
        <Popover
          open={inboxOpen.value}
          onClose={() => (inboxOpen.value = false)}
          anchor={ref.current}
          label="Notifications"
          class="inbox-pop"
          placement="bottom-end"
        >
          <Inbox header />
        </Popover>
      )}
    </>
  )
}

function open(n: Notification) {
  inboxOpen.value = false
  if (!n.read) void markRead([n.id])
  if (n.link) navigate(n.link)
}

/** The inbox list (used in the popover and the mobile sheet). */
export function Inbox({ header = false }: { header?: boolean }) {
  const list = notifications.value
  const unread = unreadCount.value
  return (
    <div class="inbox">
      <div class={cx('inbox__bar', !header && 'inbox__bar--sheet')}>
        {header && <h2 class="inbox__title">Notifications</h2>}
        <span class="inbox__meta">{unread ? `${unread} unread` : list.length ? 'All read' : ''}</span>
        {unread > 0 && (
          <Button variant="ghost" size="sm" icon="check" onClick={() => void markAllRead()}>
            Mark all read
          </Button>
        )}
      </div>
      {!notificationsLoaded.value && !list.length ? (
        <div class="inbox__loading">
          <span class="skeleton" style={{ height: 14, width: '70%' }} />
          <span class="skeleton" style={{ height: 14, width: '52%' }} />
        </div>
      ) : list.length === 0 ? (
        <EmptyState
          size="sm"
          glyph="bell"
          title="All clear"
          body="When an agent needs you or a task finishes, it shows up here."
        />
      ) : (
        <ul class="inbox__list">
          {list.map((n) => (
            <li
              key={n.id}
              class={cx(
                'inbox-item',
                !n.read && 'is-unread',
                n.kind === 'attention' && !n.read && 'is-attention',
              )}
            >
              <button type="button" class="inbox-item__main" onClick={() => open(n)}>
                <span class="inbox-item__mark">
                  <KindMark n={n} />
                </span>
                <span class="inbox-item__text">
                  <span class="inbox-item__title">{n.title}</span>
                  {n.body && <span class="inbox-item__body">{n.body}</span>}
                  <span class="inbox-item__meta">
                    {n.agent && <AgentMark agent={n.agent} size="sm" />}
                    <span>{KIND_LABEL[n.kind]}</span>
                    <span aria-hidden="true">·</span>
                    <time dateTime={n.at}>{ago(n.at)}</time>
                  </span>
                </span>
                {!n.read && <span class="inbox-item__dot" role="img" aria-label="Unread" />}
              </button>
              <button
                type="button"
                class="inbox-item__remove"
                aria-label={`Dismiss “${n.title}”`}
                onClick={() => void removeNotification(n.id)}
              >
                <Icon name="x" size={14} />
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
