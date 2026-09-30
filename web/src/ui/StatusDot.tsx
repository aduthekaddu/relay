import type { Activity } from '../api/types'
import { cx } from '../lib/util'
import './signature.css'

export type Status = 'working' | 'needs-you' | 'idle' | 'exited' | 'failed' | 'done' | 'offline'

const LABELS: Record<Status, string> = {
  working: 'Running',
  'needs-you': 'Needs you',
  idle: 'Idle',
  exited: 'Exited',
  failed: 'Failed',
  done: 'Done',
  offline: 'Offline',
}

/** Map a terminal/agent activity (+ attention, exit code) to a Status. */
export function statusOf(activity: Activity | undefined, opts: { attention?: boolean; exitCode?: number } = {}): Status {
  if (opts.attention || activity === 'waiting') return 'needs-you'
  if (activity === 'working') return 'working'
  if (activity === 'exited') return opts.exitCode && opts.exitCode !== 0 ? 'failed' : 'exited'
  return 'idle'
}

export interface StatusDotProps {
  status: Status
  /** Show the words next to the dot (never rely on colour alone in lists). */
  label?: boolean | string
  size?: 'sm' | 'md'
  class?: string
}

/**
 * Semantic status: working (green pulse ring), needs you (orange beacon,
 * blinking), idle (hollow ring), exited (quiet dot), failed (red dot).
 */
export function StatusDot({ status, label = false, size = 'md', class: className }: StatusDotProps) {
  const text = typeof label === 'string' ? label : LABELS[status]
  return (
    <span class={cx('status', `status--${status}`, `status--${size}`, className)}>
      <span class="status__dot" aria-hidden="true" />
      {label ? <span class="status__label">{text}</span> : <span class="sr-only">{text}</span>}
    </span>
  )
}

export const statusLabel = (s: Status): string => LABELS[s]
