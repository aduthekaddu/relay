import type { ComponentChildren } from 'preact'
import { useEffect, useRef, useState } from 'preact/hooks'
import { cx, haptic, isTouch } from '../lib/util'
import { Icon } from './Icon'
import './controls.css'
import './misc.css'

export interface ConfirmButtonProps {
  /** Runs once the user has confirmed. */
  onConfirm: () => void | Promise<void>
  /** Idle label, e.g. "Delete". */
  children: ComponentChildren
  /** Label while waiting for the second click (desktop). Default "Click again to confirm". */
  confirmLabel?: string
  /** Label while holding (touch). Default "Hold to confirm". */
  holdLabel?: string
  /** Hold duration on touch, ms. Default 900. */
  holdMs?: number
  icon?: string
  size?: 'sm' | 'md' | 'lg'
  disabled?: boolean
  class?: string
}

/**
 * Destructive action guard. Touch: press and hold until the fill completes
 * (release early to cancel). Pointer/keyboard: first click arms, a second
 * click within 3 s confirms; Esc or blur disarms.
 */
export function ConfirmButton({
  onConfirm,
  children,
  confirmLabel = 'Click again to confirm',
  holdLabel = 'Hold to confirm',
  holdMs = 900,
  icon,
  size = 'md',
  disabled,
  class: className,
}: ConfirmButtonProps) {
  const [armed, setArmed] = useState(false)
  const [holding, setHolding] = useState(false)
  const [busy, setBusy] = useState(false)
  const timer = useRef(0)
  const touchMode = useRef(false)

  useEffect(() => () => window.clearTimeout(timer.current), [])

  const fire = async () => {
    window.clearTimeout(timer.current)
    setArmed(false)
    setHolding(false)
    haptic(20)
    setBusy(true)
    try {
      await onConfirm()
    } finally {
      setBusy(false)
    }
  }

  const onPointerDown = (e: PointerEvent) => {
    if (disabled || busy) return
    touchMode.current = e.pointerType !== 'mouse' || isTouch()
    if (!touchMode.current) return
    e.preventDefault()
    setHolding(true)
    haptic()
    timer.current = window.setTimeout(fire, holdMs)
  }
  const cancelHold = () => {
    if (!holding) return
    window.clearTimeout(timer.current)
    setHolding(false)
  }
  const onClick = () => {
    if (disabled || busy || touchMode.current) return
    if (armed) {
      fire()
      return
    }
    setArmed(true)
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setArmed(false), 3000)
  }

  const label = holding ? holdLabel : armed ? confirmLabel : children
  return (
    <button
      type="button"
      class={cx(
        'btn btn--danger',
        `btn--${size}`,
        'confirm-btn',
        armed && 'is-armed',
        holding && 'is-holding',
        className,
      )}
      style={{ '--hold': `${holdMs}ms` }}
      disabled={disabled || busy}
      aria-live="polite"
      onPointerDown={onPointerDown}
      onPointerUp={cancelHold}
      onPointerLeave={cancelHold}
      onPointerCancel={cancelHold}
      onContextMenu={(e) => e.preventDefault()}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === 'Escape' && armed) {
          e.stopPropagation()
          setArmed(false)
        }
      }}
      onBlur={() => setArmed(false)}
    >
      <span class="confirm-btn__fill" aria-hidden="true" />
      {icon && <Icon name={icon} size={size === 'sm' ? 14 : 16} class="btn__icon" />}
      <span class="btn__label">{label}</span>
    </button>
  )
}
