import { cx } from '../lib/util'
import './controls.css'

export interface SpinnerProps {
  size?: 'sm' | 'md' | 'lg'
  /** Accessible status text. Default "Loading". */
  label?: string
  class?: string
}

/** Three dots relaying a light — Relay's loading indicator. */
export function Spinner({ size = 'md', label = 'Loading', class: className }: SpinnerProps) {
  return (
    <span class={cx('spinner', `spinner--${size}`, className)} role="status" aria-label={label}>
      <i />
      <i />
      <i />
    </span>
  )
}
