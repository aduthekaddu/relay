import type { ComponentChildren, JSX } from 'preact'
import { cloneElement, isValidElement } from 'preact'
import { useCallback, useEffect, useRef, useState } from 'preact/hooks'
import { cx, nextId } from '../lib/util'
import { type Anchor, type Placement, Portal, useAnchoredPosition, useDismiss, useFocusTrap } from './overlay'
import './overlay.css'

export interface PopoverProps {
  open: boolean
  onClose: () => void
  /** Element (or point) the popover is attached to. */
  anchor: Anchor
  placement?: Placement
  /** Accessible name of the popover dialog. */
  label: string
  /** Trap focus inside (default true); false for non-interactive content. */
  trapFocus?: boolean
  class?: string
  style?: JSX.CSSProperties
  children: ComponentChildren
}

/** A floating panel anchored to an element (notifications, pickers). */
export function Popover({ open, onClose, anchor, placement = 'bottom-end', label, trapFocus = true, class: className, style, children }: PopoverProps) {
  const ref = useRef<HTMLDivElement>(null)
  const pos = useAnchoredPosition(ref, anchor, open, placement)
  useDismiss(ref, open, onClose, { ignore: anchor instanceof HTMLElement ? anchor : null })
  useFocusTrap(ref, open && trapFocus)
  if (!open) return null
  return (
    <Portal>
      <div
        ref={ref}
        role="dialog"
        aria-label={label}
        class={cx('popover', pos.ready && 'is-ready', `popover--${pos.side}`, className)}
        style={{ transform: `translate(${pos.x}px, ${pos.y}px)`, ...style }}
        tabIndex={-1}
      >
        {children}
      </div>
    </Portal>
  )
}

export interface TooltipProps {
  /** Tooltip text (short). */
  content: ComponentChildren
  placement?: Placement
  /** Delay before showing on hover, ms. Default 450. */
  delay?: number
  /** A single element child that receives aria-describedby. */
  children: JSX.Element
}

/**
 * Hover/focus tooltip for pointer devices. Never put essential info only
 * in a tooltip — touch users will not see it.
 */
export function Tooltip({ content, placement = 'top', delay = 450, children }: TooltipProps) {
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const [open, setOpen] = useState(false)
  const timer = useRef<number>(0)
  const id = useRef(nextId('tip')).current
  const ref = useRef<HTMLDivElement>(null)
  const pos = useAnchoredPosition(ref, anchor, open, placement)
  const show = useCallback(
    (e: Event, immediate = false) => {
      const el = e.currentTarget as HTMLElement
      setAnchor(el)
      window.clearTimeout(timer.current)
      timer.current = window.setTimeout(() => setOpen(true), immediate ? 0 : delay)
    },
    [delay],
  )
  const hide = useCallback(() => {
    window.clearTimeout(timer.current)
    setOpen(false)
  }, [])
  useEffect(() => () => window.clearTimeout(timer.current), [])
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && hide()
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, hide])
  if (!isValidElement(children)) return children
  const child = cloneElement(children, {
    'aria-describedby': open ? id : undefined,
    onPointerEnter: (e: PointerEvent) => e.pointerType === 'mouse' && show(e),
    onPointerLeave: hide,
    onFocus: (e: FocusEvent) => {
      if ((e.currentTarget as HTMLElement).matches(':focus-visible')) show(e, true)
    },
    onBlur: hide,
    onPointerDown: hide,
  })
  return (
    <>
      {child}
      {open && (
        <Portal>
          <div
            ref={ref}
            id={id}
            role="tooltip"
            class={cx('tooltip', pos.ready && 'is-ready')}
            style={{ transform: `translate(${pos.x}px, ${pos.y}px)` }}
          >
            {content}
          </div>
        </Portal>
      )}
    </>
  )
}
