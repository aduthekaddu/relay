// The signal line: a 2 px bar across the very top of the viewport in the
// current area hue. Route loads sweep it left→right (the progress
// indicator); while a terminal in view is working, a soft light travels
// along it. This is the "pulse" motif — it is used nowhere else.
import { signal } from '@preact/signals'
import { useEffect, useRef, useState } from 'preact/hooks'
import { cx } from '../lib/util'

type Phase = 'idle' | 'loading' | 'done'

/** Route-progress phase, driven by the Router's load hooks. */
export const routePhase = signal<Phase>('idle')

let doneTimer = 0
/** A lazy route started loading. */
export function routeLoadStart(): void {
  window.clearTimeout(doneTimer)
  routePhase.value = 'loading'
}
/** A route finished rendering (after a load, or an instant route change). */
export function routeLoadEnd(): void {
  if (routePhase.value === 'idle') {
    // Instant navigation: play a quick full sweep.
    routePhase.value = 'loading'
    requestAnimationFrame(() => {
      routePhase.value = 'done'
    })
  } else routePhase.value = 'done'
  window.clearTimeout(doneTimer)
  doneTimer = window.setTimeout(() => {
    routePhase.value = 'idle'
  }, 520)
}

export function SignalLine({ working }: { working: boolean }) {
  const phase = routePhase.value
  // Re-trigger the sweep animation on every navigation.
  const [run, setRun] = useState(0)
  const last = useRef<Phase>('idle')
  useEffect(() => {
    if (phase === 'loading' && last.current !== 'loading') setRun((r) => r + 1)
    last.current = phase
  }, [phase])
  return (
    <div class={cx('signal-line', working && 'signal-line--working')} aria-hidden="true">
      <span key={run} class={cx('signal-line__sweep', `is-${phase}`)} />
    </div>
  )
}
