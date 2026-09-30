import type { ComponentChildren } from 'preact'
import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks'
import { animateSpring } from '../lib/spring'
import { cx, prefersReducedMotion } from '../lib/util'
import { Button } from './Button'
import { Portal, useDismiss, useFocusTrap, useMedia, useModal } from './overlay'
import './overlay.css'

export interface SheetProps {
  open: boolean
  onClose: () => void
  /** Heading; also the accessible name. */
  title: ComponentChildren
  /** Accessible name when `title` is not plain text. */
  label?: string
  /**
   * 'auto' (default): bottom sheet below 768 px, right side sheet above.
   * 'bottom' / 'right' force one form.
   */
  side?: 'auto' | 'bottom' | 'right'
  /** Side-sheet width in px (default 420). */
  width?: number
  /** Sticky footer (actions). */
  footer?: ComponentChildren
  /** Hide the visible title row (still labelled). */
  bare?: boolean
  class?: string
  children: ComponentChildren
}

/**
 * Modal sheet. On phones it rises from the bottom with a spring and can be
 * dragged down by its handle to dismiss; on desktop it slides in from the
 * right. Always has a visible close button.
 */
export function Sheet({
  open,
  onClose,
  title,
  label,
  side = 'auto',
  width = 420,
  footer,
  bare,
  class: className,
  children,
}: SheetProps) {
  const narrow = useMedia('(max-width: 767px)')
  const form: 'bottom' | 'right' = side === 'auto' ? (narrow ? 'bottom' : 'right') : side
  const [mounted, setMounted] = useState(open)
  const panel = useRef<HTMLDivElement>(null)
  const scrim = useRef<HTMLDivElement>(null)
  const offset = useRef(0) // px away from the resting position
  const cancel = useRef<() => void>(() => {})

  useModal(mounted && open)
  useFocusTrap(panel, mounted && open)
  useDismiss(panel, mounted && open, onClose, { outside: false })

  useEffect(() => {
    if (open) setMounted(true)
  }, [open])

  const extent = () => {
    const el = panel.current
    if (!el) return 400
    return form === 'bottom' ? el.offsetHeight + 24 : el.offsetWidth + 24
  }
  const apply = (x: number) => {
    offset.current = x
    const el = panel.current
    if (!el) return
    el.style.transform = form === 'bottom' ? `translate3d(0, ${x}px, 0)` : `translate3d(${x}px, 0, 0)`
    if (scrim.current) scrim.current.style.opacity = String(Math.max(0, 1 - x / extent()))
  }
  const animateTo = (to: number, velocity = 0, done?: () => void) => {
    cancel.current()
    cancel.current = animateSpring(offset.current, to, apply, {
      velocity,
      stiffness: 380,
      damping: 36,
      reduced: prefersReducedMotion(),
      onDone: done,
    })
  }

  // Enter / exit.
  useLayoutEffect(() => {
    if (!mounted) return
    if (open) {
      apply(extent())
      animateTo(0)
    } else {
      animateTo(extent(), 0, () => setMounted(false))
    }
    return () => cancel.current()
  }, [open, mounted, form])

  // Drag to dismiss (bottom form only).
  const drag = useRef<{ y: number; t: number; start: number; vy: number } | null>(null)
  const onPointerDown = (e: PointerEvent) => {
    if (form !== 'bottom' || e.button !== 0) return
    cancel.current()
    drag.current = { y: e.clientY, t: performance.now(), start: offset.current, vy: 0 }
    ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
  }
  const onPointerMove = (e: PointerEvent) => {
    const d = drag.current
    if (!d) return
    const now = performance.now()
    const next = d.start + (e.clientY - d.y)
    // Rubber-band when pulled up past the resting point.
    const prev = offset.current
    apply(next < 0 ? next / 4 : next)
    // Velocity in px/s from the last move (smoothed).
    const dt = Math.max(1, now - d.t)
    d.vy = d.vy * 0.4 + ((offset.current - prev) / dt) * 1000 * 0.6
    d.t = now
  }
  const onPointerUp = () => {
    const d = drag.current
    drag.current = null
    if (!d) return
    const h = extent()
    const fling = d.vy > 900
    if (offset.current > h * 0.3 || fling) animateTo(h, Math.max(d.vy, 0), onClose)
    else animateTo(0, d.vy)
  }

  if (!mounted) return null
  const name = label ?? (typeof title === 'string' ? title : undefined)
  return (
    <Portal>
      <div class={cx('sheet-layer', `sheet-layer--${form}`)}>
        <div ref={scrim} class="scrim" onClick={onClose} aria-hidden="true" />
        <div
          ref={panel}
          class={cx('sheet', `sheet--${form}`, className)}
          role="dialog"
          aria-modal="true"
          aria-label={name}
          data-modal
          tabIndex={-1}
          style={form === 'right' ? { width: `min(${width}px, 100vw)` } : undefined}
        >
          {form === 'bottom' && (
            <div
              class="sheet__grab"
              onPointerDown={onPointerDown}
              onPointerMove={onPointerMove}
              onPointerUp={onPointerUp}
              onPointerCancel={onPointerUp}
              aria-hidden="true"
            >
              <span class="sheet__handle" />
            </div>
          )}
          <header class={cx('sheet__head', bare && 'sr-only')}>
            <h2 class="sheet__title">{title}</h2>
            <Button variant="icon" icon="x" label="Close" onClick={onClose} class="sheet__close" />
          </header>
          <div class="sheet__body">{children}</div>
          {footer && <footer class="sheet__foot">{footer}</footer>}
        </div>
      </div>
    </Portal>
  )
}
