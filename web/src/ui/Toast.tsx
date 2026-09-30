// Toasts: a global store plus the <Toaster/> host rendered once by the
// shell. Call toast() from anywhere (no hook needed).
import { signal } from '@preact/signals'
import { useEffect, useRef } from 'preact/hooks'
import { cx } from '../lib/util'
import { Icon } from './Icon'
import { Portal } from './overlay'
import './overlay.css'

export type ToastKind = 'info' | 'success' | 'warning' | 'danger' | 'attention'

export interface ToastAction {
  label: string
  onClick: () => void
}

export interface ToastOptions {
  kind?: ToastKind
  /** Second line. */
  body?: string
  action?: ToastAction
  /** ms before auto-dismiss; 0 = sticky. Default 4500 (7000 with an action). */
  duration?: number
  /** Replace an existing toast with the same id instead of stacking. */
  id?: string
}

export interface ToastItem extends Required<Pick<ToastOptions, 'kind' | 'duration'>> {
  id: string
  message: string
  body?: string
  action?: ToastAction
  createdAt: number
}

/** Visible toasts, newest last. */
export const toasts = signal<ToastItem[]>([])
const MAX = 4
let seq = 0

/** Show a toast; returns its id. */
export function toast(message: string, opts: ToastOptions = {}): string {
  const id = opts.id ?? `t${++seq}`
  const item: ToastItem = {
    id,
    message,
    body: opts.body,
    action: opts.action,
    kind: opts.kind ?? 'info',
    duration: opts.duration ?? (opts.action ? 7000 : 4500),
    createdAt: Date.now(),
  }
  const rest = toasts.value.filter((t) => t.id !== id)
  toasts.value = [...rest, item].slice(-MAX)
  return id
}

/** Dismiss a toast by id. */
export function dismissToast(id: string): void {
  toasts.value = toasts.value.filter((t) => t.id !== id)
}

const ICONS: Record<ToastKind, string> = {
  info: 'info',
  success: 'check',
  warning: 'triangle-alert',
  danger: 'circle-alert',
  attention: 'bell',
}

function ToastView({ t }: { t: ToastItem }) {
  const ref = useRef<HTMLDivElement>(null)
  const timer = useRef(0)
  const drag = useRef<{ x: number; dx: number } | null>(null)
  const arm = () => {
    window.clearTimeout(timer.current)
    if (t.duration > 0) timer.current = window.setTimeout(() => dismissToast(t.id), t.duration)
  }
  useEffect(() => {
    arm()
    return () => window.clearTimeout(timer.current)
  }, [t.createdAt])
  const onDown = (e: PointerEvent) => {
    if ((e.target as HTMLElement).closest('button')) return
    drag.current = { x: e.clientX, dx: 0 }
    ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
    window.clearTimeout(timer.current)
  }
  const onMove = (e: PointerEvent) => {
    const d = drag.current
    if (!d || !ref.current) return
    d.dx = e.clientX - d.x
    ref.current.style.transform = `translateX(${d.dx}px)`
    ref.current.style.opacity = String(Math.max(0, 1 - Math.abs(d.dx) / 220))
  }
  const onUp = () => {
    const d = drag.current
    drag.current = null
    if (!d || !ref.current) return
    if (Math.abs(d.dx) > 90) {
      ref.current.style.transition = 'transform 160ms ease-out, opacity 160ms'
      ref.current.style.transform = `translateX(${Math.sign(d.dx) * 400}px)`
      ref.current.style.opacity = '0'
      window.setTimeout(() => dismissToast(t.id), 160)
    } else {
      ref.current.style.transition = 'transform 200ms var(--ease-out), opacity 200ms'
      ref.current.style.transform = ''
      ref.current.style.opacity = ''
      arm()
    }
  }
  return (
    <div
      ref={ref}
      class={cx('toast', `toast--${t.kind}`)}
      role={t.kind === 'danger' || t.kind === 'attention' ? 'alert' : 'status'}
      onPointerDown={onDown}
      onPointerMove={onMove}
      onPointerUp={onUp}
      onPointerCancel={onUp}
      onMouseEnter={() => window.clearTimeout(timer.current)}
      onMouseLeave={arm}
    >
      <span class="toast__icon">
        {t.kind === 'attention' ? <span class="status status--needs-you"><span class="status__dot" /></span> : <Icon name={ICONS[t.kind]} size={16} />}
      </span>
      <div class="toast__text">
        <p class="toast__msg">{t.message}</p>
        {t.body && <p class="toast__body">{t.body}</p>}
      </div>
      {t.action && (
        <button
          type="button"
          class="toast__action"
          onClick={() => {
            t.action?.onClick()
            dismissToast(t.id)
          }}
        >
          {t.action.label}
        </button>
      )}
      <button type="button" class="toast__close" aria-label="Dismiss" onClick={() => dismissToast(t.id)}>
        <Icon name="x" size={14} />
      </button>
    </div>
  )
}

/** Renders the toast stack. Mount once (the app shell does). */
export function Toaster() {
  const list = toasts.value
  return (
    <Portal>
      <section class="toaster" aria-label="Notifications" aria-live="polite">
        {list.map((t) => (
          <ToastView key={t.id} t={t} />
        ))}
      </section>
    </Portal>
  )
}
