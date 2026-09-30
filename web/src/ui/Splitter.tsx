import type { ComponentChildren } from 'preact'
import { useRef, useState } from 'preact/hooks'
import { clamp, cx, load, save } from '../lib/util'
import './misc.css'

export interface SplitterProps {
  /** 'row' = side by side (vertical divider), 'column' = stacked. */
  direction?: 'row' | 'column'
  /** Initial size of the first pane as a fraction (0–1). Default 0.5. */
  initial?: number
  /** Minimum pane size in px. Default 160. */
  min?: number
  /** Persist the ratio in localStorage under this key. */
  storageKey?: string
  /** Accessible name for the divider. */
  label?: string
  first: ComponentChildren
  second: ComponentChildren
  class?: string
}

/**
 * Two resizable panes with a draggable divider. The divider is a focusable
 * separator: ←/→ (or ↑/↓) nudge by 2%, Home/End jump to the limits,
 * double-click resets.
 */
export function Splitter({
  direction = 'row',
  initial = 0.5,
  min = 160,
  storageKey,
  label = 'Resize panes',
  first,
  second,
  class: className,
}: SplitterProps) {
  const [ratio, setRatio] = useState(() => (storageKey ? load(`relay.split.${storageKey}`, initial) : initial))
  const root = useRef<HTMLDivElement>(null)
  const row = direction === 'row'

  const limit = (r: number) => {
    const el = root.current
    const total = el ? (row ? el.clientWidth : el.clientHeight) : 1000
    const lo = Math.min(0.5, min / Math.max(total, 1))
    return clamp(r, lo, 1 - lo)
  }
  const commit = (r: number) => {
    const v = limit(r)
    setRatio(v)
    if (storageKey) save(`relay.split.${storageKey}`, v)
  }
  const onPointerDown = (e: PointerEvent) => {
    const el = root.current
    if (!el) return
    e.preventDefault()
    const target = e.currentTarget as HTMLElement
    target.setPointerCapture(e.pointerId)
    const rect = el.getBoundingClientRect()
    const move = (ev: PointerEvent) =>
      commit(row ? (ev.clientX - rect.left) / rect.width : (ev.clientY - rect.top) / rect.height)
    const up = () => {
      target.removeEventListener('pointermove', move)
      target.removeEventListener('pointerup', up)
      target.removeEventListener('pointercancel', up)
    }
    target.addEventListener('pointermove', move)
    target.addEventListener('pointerup', up)
    target.addEventListener('pointercancel', up)
  }
  const onKey = (e: KeyboardEvent) => {
    const step = e.shiftKey ? 0.1 : 0.02
    const dec = row ? 'ArrowLeft' : 'ArrowUp'
    const inc = row ? 'ArrowRight' : 'ArrowDown'
    if (e.key === dec) commit(ratio - step)
    else if (e.key === inc) commit(ratio + step)
    else if (e.key === 'Home') commit(0)
    else if (e.key === 'End') commit(1)
    else return
    e.preventDefault()
  }
  return (
    <div ref={root} class={cx('splitter', `splitter--${direction}`, className)} style={{ '--ratio': ratio }}>
      <div class="splitter__pane">{first}</div>
      {/* biome-ignore lint/a11y/useSemanticElements: a focusable separator is the ARIA pattern for splitters */}
      <div
        class="splitter__handle"
        role="separator"
        tabIndex={0}
        aria-label={label}
        aria-orientation={row ? 'vertical' : 'horizontal'}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(ratio * 100)}
        onPointerDown={onPointerDown}
        onKeyDown={onKey}
        onDblClick={() => commit(initial)}
      />
      <div class="splitter__pane">{second}</div>
    </div>
  )
}
