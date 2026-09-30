import type { ComponentChildren } from 'preact'
import { useCallback, useEffect, useRef, useState } from 'preact/hooks'
import { cx, haptic } from '../lib/util'
import { Button, type ButtonProps } from './Button'
import { Icon } from './Icon'
import { Kbd } from './layout'
import { type Anchor, type Placement, Portal, useAnchoredPosition, useDismiss, useMedia } from './overlay'
import { Sheet } from './Sheet'
import './overlay.css'

export interface MenuItem {
  id: string
  label: string
  icon?: string
  /** Display-only shortcut, e.g. ['mod','C']. */
  shortcut?: string[]
  /** Quiet text right-aligned (e.g. current value). */
  hint?: string
  danger?: boolean
  disabled?: boolean
  /** Checked state for toggle items (renders a check). */
  checked?: boolean
  onSelect?: () => void
}

/** A separator between groups. */
export const SEPARATOR = { id: '-', label: '-' } as const satisfies MenuItem
const isSep = (i: MenuItem) => i.id === '-' || i.id.startsWith('-')

export interface MenuProps {
  open: boolean
  onClose: () => void
  anchor: Anchor
  items: MenuItem[]
  /** Accessible menu name. */
  label: string
  placement?: Placement
  /** On touch devices menus open as a bottom sheet (default true). */
  sheetOnTouch?: boolean
}

/**
 * Dropdown or context menu. Keyboard: ↑↓ Home End, Enter/Space selects,
 * Esc closes, typing jumps to the first matching item. On touch it becomes
 * a bottom sheet with 48 px rows.
 */
export function Menu({
  open,
  onClose,
  anchor,
  items,
  label,
  placement = 'bottom-start',
  sheetOnTouch = true,
}: MenuProps) {
  const touch = useMedia('(pointer: coarse)')
  const ref = useRef<HTMLDivElement>(null)
  const [active, setActive] = useState(-1)
  const pos = useAnchoredPosition(ref, anchor, open && !(touch && sheetOnTouch), placement)
  useDismiss(ref, open && !(touch && sheetOnTouch), onClose, {
    ignore: anchor instanceof HTMLElement ? anchor : null,
  })

  const enabled = items.map((it, i) => (!isSep(it) && !it.disabled ? i : -1)).filter((i) => i >= 0)
  useEffect(() => {
    if (!open) return
    setActive(enabled[0] ?? -1)
    requestAnimationFrame(() => ref.current?.focus())
  }, [open])

  const select = (it: MenuItem) => {
    if (it.disabled || isSep(it)) return
    onClose()
    haptic()
    // Let the menu close (and focus return) before running the action.
    setTimeout(() => it.onSelect?.(), 0)
  }
  const onKey = (e: KeyboardEvent) => {
    const at = enabled.indexOf(active)
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActive(enabled[(at + 1) % enabled.length])
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActive(enabled[(at - 1 + enabled.length) % enabled.length])
    } else if (e.key === 'Home') {
      e.preventDefault()
      setActive(enabled[0])
    } else if (e.key === 'End') {
      e.preventDefault()
      setActive(enabled[enabled.length - 1])
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      if (items[active]) select(items[active])
    } else if (e.key === 'Tab') {
      onClose()
    } else if (e.key.length === 1 && /\S/.test(e.key)) {
      const k = e.key.toLowerCase()
      const hit = enabled.find((i) => items[i].label.toLowerCase().startsWith(k))
      if (hit !== undefined) setActive(hit)
    }
  }

  if (!open) return null
  if (touch && sheetOnTouch) {
    return (
      <Sheet open={open} onClose={onClose} title={label} side="bottom" bare>
        <div class="menu-sheet" role="menu" aria-label={label}>
          {items.map((it) =>
            isSep(it) ? (
              <hr key={it.id} class="menu__sep" />
            ) : (
              <button
                key={it.id}
                type="button"
                role="menuitem"
                class={cx('menu__item menu__item--touch', it.danger && 'is-danger')}
                disabled={it.disabled}
                onClick={() => select(it)}
              >
                <MenuItemBody it={it} />
              </button>
            ),
          )}
        </div>
      </Sheet>
    )
  }
  return (
    <Portal>
      <div
        ref={ref}
        class={cx('menu', pos.ready && 'is-ready')}
        role="menu"
        aria-label={label}
        tabIndex={-1}
        aria-activedescendant={active >= 0 ? `mi-${items[active]?.id}` : undefined}
        onKeyDown={onKey}
        style={{ transform: `translate(${pos.x}px, ${pos.y}px)` }}
      >
        {items.map((it, i) =>
          isSep(it) ? (
            <hr key={it.id} class="menu__sep" />
          ) : (
            <div
              key={it.id}
              id={`mi-${it.id}`}
              role={it.checked === undefined ? 'menuitem' : 'menuitemcheckbox'}
              aria-checked={it.checked}
              aria-disabled={it.disabled || undefined}
              class={cx(
                'menu__item',
                i === active && 'is-active',
                it.danger && 'is-danger',
                it.disabled && 'is-disabled',
              )}
              onPointerMove={() => !it.disabled && setActive(i)}
              onClick={() => select(it)}
            >
              <MenuItemBody it={it} />
            </div>
          ),
        )}
      </div>
    </Portal>
  )
}

function MenuItemBody({ it }: { it: MenuItem }) {
  return (
    <>
      <span class="menu__icon">
        {it.checked !== undefined
          ? it.checked && <Icon name="check" size={15} />
          : it.icon && <Icon name={it.icon} size={15} />}
      </span>
      <span class="menu__label">{it.label}</span>
      {it.hint && <span class="menu__hint">{it.hint}</span>}
      {it.shortcut && <Kbd keys={it.shortcut} class="menu__kbd" />}
    </>
  )
}

export interface MenuButtonProps extends Omit<ButtonProps, 'onClick'> {
  items: MenuItem[]
  /** Menu accessible name (also the button label for icon buttons). */
  menuLabel: string
  placement?: Placement
  children?: ComponentChildren
}

/** A button that opens a Menu. */
export function MenuButton({
  items,
  menuLabel,
  placement = 'bottom-end',
  children,
  ...btn
}: MenuButtonProps) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLButtonElement>(null)
  const close = useCallback(() => setOpen(false), [])
  return (
    <>
      <Button
        ref={ref}
        aria-haspopup="menu"
        aria-expanded={open}
        label={btn.label ?? menuLabel}
        {...btn}
        onClick={() => setOpen((o) => !o)}
      >
        {children}
      </Button>
      <Menu
        open={open}
        onClose={close}
        anchor={ref.current}
        items={items}
        label={menuLabel}
        placement={placement}
      />
    </>
  )
}

export interface LongPressHandlers {
  onPointerDown: (e: PointerEvent) => void
  onPointerMove: (e: PointerEvent) => void
  onPointerUp: () => void
  onPointerCancel: () => void
  onContextMenu: (e: MouseEvent) => void
}

/**
 * Long-press (touch, 450 ms, cancelled by >8 px movement) and right-click
 * both call `fn` with the point. Prevents the native callout on iOS.
 */
export function useLongPress(fn: (point: { x: number; y: number }) => void, ms = 450): LongPressHandlers {
  const t = useRef<number>(0)
  const start = useRef<{ x: number; y: number } | null>(null)
  const fired = useRef(false)
  const clear = () => {
    window.clearTimeout(t.current)
    start.current = null
  }
  useEffect(() => () => window.clearTimeout(t.current), [])
  return {
    onPointerDown: (e) => {
      if (e.pointerType === 'mouse') return
      fired.current = false
      start.current = { x: e.clientX, y: e.clientY }
      t.current = window.setTimeout(() => {
        if (!start.current) return
        fired.current = true
        haptic(12)
        fn(start.current)
      }, ms)
    },
    onPointerMove: (e) => {
      const s = start.current
      if (s && Math.hypot(e.clientX - s.x, e.clientY - s.y) > 8) clear()
    },
    onPointerUp: clear,
    onPointerCancel: clear,
    onContextMenu: (e) => {
      e.preventDefault()
      if (fired.current) {
        fired.current = false
        return
      }
      fn({ x: e.clientX, y: e.clientY })
    },
  }
}

/**
 * Context menu for any element: spread `handlers` on the target and render
 * `menu` somewhere in the tree.
 */
export function useContextMenu(items: MenuItem[] | (() => MenuItem[]), label = 'Actions') {
  const [point, setPoint] = useState<{ x: number; y: number } | null>(null)
  const handlers = useLongPress((p) => setPoint(p))
  const close = useCallback(() => setPoint(null), [])
  const list = typeof items === 'function' ? (point ? items() : []) : items
  const menu = (
    <Menu open={!!point} onClose={close} anchor={point} items={list} label={label} placement="bottom-start" />
  )
  return { handlers, menu, open: !!point }
}
