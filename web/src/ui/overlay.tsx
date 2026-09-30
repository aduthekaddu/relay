// Overlay infrastructure shared by Popover, Menu, Dialog, Sheet, Palette:
// a portal layer outside #app, a modal stack that makes the app inert
// while a modal is open, focus trapping/restoration, outside-click and
// Escape dismissal, and anchored positioning.
import type { ComponentChildren, RefObject, VNode } from 'preact'
import { createPortal } from 'preact/compat'
import { useEffect, useLayoutEffect, useState } from 'preact/hooks'

let layerRoot: HTMLElement | null = null

/** The element overlays are portalled into (created on first use). */
export function layers(): HTMLElement {
  if (layerRoot?.isConnected) return layerRoot
  layerRoot = document.getElementById('layers') ?? document.createElement('div')
  layerRoot.id = 'layers'
  if (!layerRoot.isConnected) document.body.appendChild(layerRoot)
  return layerRoot
}

/** Render children into the overlay layer. */
export function Portal({ children }: { children: ComponentChildren }) {
  return createPortal(children as VNode, layers())
}

// ---------------------------------------------------------------- modal stack

const stack: symbol[] = []

function syncInert() {
  const app = document.getElementById('app')
  if (app) app.inert = stack.length > 0
  document.documentElement.classList.toggle('has-modal', stack.length > 0)
}

/** While `active`, the app behind overlays is inert (no focus, no clicks). */
export function useModal(active: boolean): void {
  useEffect(() => {
    if (!active) return
    const token = Symbol('modal')
    stack.push(token)
    syncInert()
    return () => {
      const i = stack.indexOf(token)
      if (i >= 0) stack.splice(i, 1)
      syncInert()
    }
  }, [active])
}

/** True when `el` belongs to the top-most modal (Escape handling). */
export function isTopModal(el: HTMLElement | null): boolean {
  if (!el) return false
  const all = layers().querySelectorAll('[data-modal]')
  return all.length > 0 && all[all.length - 1] === el
}

// ---------------------------------------------------------------- focus

const FOCUSABLE =
  'a[href],button:not([disabled]),input:not([disabled]):not([type=hidden]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"]),[contenteditable="true"]'

/** Focusable descendants in DOM order. */
export function focusables(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(
    (el) => el.offsetParent !== null || el === document.activeElement,
  )
}

/**
 * Trap Tab inside `ref` while active; focus the first focusable (or the
 * element with [data-autofocus]) on open; restore focus on close.
 */
export function useFocusTrap(ref: RefObject<HTMLElement>, active: boolean): void {
  useLayoutEffect(() => {
    if (!active) return
    const root = ref.current
    if (!root) return
    const previous = document.activeElement as HTMLElement | null
    const first = root.querySelector<HTMLElement>('[data-autofocus]') ?? focusables(root)[0] ?? root
    requestAnimationFrame(() => first.focus({ preventScroll: true }))
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Tab') return
      const list = focusables(root)
      if (list.length === 0) {
        e.preventDefault()
        return
      }
      const i = list.indexOf(document.activeElement as HTMLElement)
      if (e.shiftKey && (i <= 0 || i === -1)) {
        e.preventDefault()
        list[list.length - 1].focus()
      } else if (!e.shiftKey && i === list.length - 1) {
        e.preventDefault()
        list[0].focus()
      }
    }
    root.addEventListener('keydown', onKey)
    return () => {
      root.removeEventListener('keydown', onKey)
      if (previous?.isConnected) previous.focus({ preventScroll: true })
    }
  }, [active])
}

/** Close on Escape and on pointerdown outside `ref` (and outside `ignore`). */
export function useDismiss(
  ref: RefObject<HTMLElement>,
  active: boolean,
  onClose: () => void,
  opts: { outside?: boolean; ignore?: RefObject<HTMLElement | null> | HTMLElement | null } = {},
): void {
  useEffect(() => {
    if (!active) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return
      const el = ref.current
      if (el?.hasAttribute('data-modal') && !isTopModal(el)) return
      e.preventDefault()
      e.stopPropagation()
      onClose()
    }
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node
      if (ref.current?.contains(t)) return
      const ig =
        opts.ignore && 'current' in opts.ignore ? opts.ignore.current : (opts.ignore as HTMLElement | null)
      if (ig?.contains(t)) return
      onClose()
    }
    document.addEventListener('keydown', onKey, true)
    if (opts.outside !== false) document.addEventListener('pointerdown', onDown, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      document.removeEventListener('pointerdown', onDown, true)
    }
  }, [active, onClose])
}

// ---------------------------------------------------------------- positioning

export type Placement =
  | 'bottom-start'
  | 'bottom-end'
  | 'bottom'
  | 'top-start'
  | 'top-end'
  | 'top'
  | 'right-start'
  | 'left-start'

export interface Rect {
  left: number
  top: number
  width: number
  height: number
}

/**
 * Position a floating box of size (w,h) next to `anchor`, flipping to the
 * other side when it does not fit and clamping inside the viewport with an
 * 8 px margin. Pure — exported for tests.
 */
export function place(
  anchor: Rect,
  w: number,
  h: number,
  placement: Placement,
  vw: number,
  vh: number,
  gap = 6,
): { x: number; y: number; side: 'top' | 'bottom' | 'left' | 'right' } {
  const m = 8
  const [side0, align = 'center'] = placement.split('-') as [string, string?]
  let side = side0 as 'top' | 'bottom' | 'left' | 'right'
  let x = 0
  let y = 0
  const below = anchor.top + anchor.height + gap
  const above = anchor.top - gap - h
  if (side === 'bottom' && below + h > vh - m && above >= m) side = 'top'
  else if (side === 'top' && above < m && below + h <= vh - m) side = 'bottom'
  if (side === 'right' && anchor.left + anchor.width + gap + w > vw - m) side = 'left'
  else if (side === 'left' && anchor.left - gap - w < m) side = 'right'
  if (side === 'bottom' || side === 'top') {
    y = side === 'bottom' ? below : above
    x =
      align === 'start'
        ? anchor.left
        : align === 'end'
          ? anchor.left + anchor.width - w
          : anchor.left + anchor.width / 2 - w / 2
  } else {
    x = side === 'right' ? anchor.left + anchor.width + gap : anchor.left - gap - w
    y = align === 'start' ? anchor.top : anchor.top + anchor.height / 2 - h / 2
  }
  x = Math.max(m, Math.min(x, vw - w - m))
  y = Math.max(m, Math.min(y, vh - h - m))
  return { x: Math.round(x), y: Math.round(y), side }
}

/** Anchor: an element, or a point (context menus). */
export type Anchor = HTMLElement | { x: number; y: number } | null

function anchorRect(a: Anchor): Rect | null {
  if (!a) return null
  if (a instanceof HTMLElement) {
    const r = a.getBoundingClientRect()
    return { left: r.left, top: r.top, width: r.width, height: r.height }
  }
  return { left: a.x, top: a.y, width: 0, height: 0 }
}

/** Keep a floating element positioned against its anchor while open. */
export function useAnchoredPosition(
  floating: RefObject<HTMLElement>,
  anchor: Anchor,
  open: boolean,
  placement: Placement,
): { x: number; y: number; side: string; ready: boolean } {
  const [pos, setPos] = useState({ x: -9999, y: -9999, side: 'bottom', ready: false })
  useLayoutEffect(() => {
    if (!open) {
      setPos((p) => (p.ready ? { ...p, ready: false } : p))
      return
    }
    const update = () => {
      const el = floating.current
      const r = anchorRect(anchor)
      if (!el || !r) return
      const vv = window.visualViewport
      const vw = vv?.width ?? window.innerWidth
      const vh = vv?.height ?? window.innerHeight
      const p = place(r, el.offsetWidth, el.offsetHeight, placement, vw, vh)
      setPos({ ...p, ready: true })
    }
    update()
    window.addEventListener('resize', update)
    window.addEventListener('scroll', update, true)
    const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(update) : null
    if (floating.current) ro?.observe(floating.current)
    return () => {
      window.removeEventListener('resize', update)
      window.removeEventListener('scroll', update, true)
      ro?.disconnect()
    }
  }, [open, anchor, placement])
  return pos
}

/** Reactive media query. */
export function useMedia(query: string): boolean {
  const [on, setOn] = useState(() => typeof matchMedia !== 'undefined' && matchMedia(query).matches)
  useEffect(() => {
    const mq = matchMedia(query)
    const fn = () => setOn(mq.matches)
    fn()
    mq.addEventListener('change', fn)
    return () => mq.removeEventListener('change', fn)
  }, [query])
  return on
}
