// A tiny critically-damped-ish spring integrator for sheets and drags.
// Physics runs in fixed 1/240 s substeps so results do not depend on the
// display's frame rate.

export interface SpringOptions {
  stiffness?: number
  damping?: number
  mass?: number
  /** Initial velocity in units per second. */
  velocity?: number
  /** Settle thresholds. */
  restDelta?: number
  restSpeed?: number
}

export interface SpringState {
  x: number
  v: number
  done: boolean
}

/** Advance a spring from state `s` toward `to` by `dt` seconds. Pure. */
export function stepSpring(s: SpringState, to: number, dt: number, o: SpringOptions = {}): SpringState {
  const k = o.stiffness ?? 380
  const c = o.damping ?? 36
  const m = o.mass ?? 1
  const h = 1 / 240
  let { x, v } = s
  let t = dt
  while (t > 0) {
    const step = Math.min(h, t)
    const a = (-k * (x - to) - c * v) / m
    v += a * step
    x += v * step
    t -= step
  }
  const done = Math.abs(x - to) < (o.restDelta ?? 0.5) && Math.abs(v) < (o.restSpeed ?? 5)
  return { x: done ? to : x, v: done ? 0 : v, done }
}

/**
 * Animate from `from` to `to`, calling `onUpdate` each frame. Returns a
 * cancel function. With reduced motion, jumps straight to the end.
 */
export function animateSpring(
  from: number,
  to: number,
  onUpdate: (x: number) => void,
  opts: SpringOptions & { onDone?: () => void; reduced?: boolean } = {},
): () => void {
  if (opts.reduced) {
    onUpdate(to)
    opts.onDone?.()
    return () => {}
  }
  let s: SpringState = { x: from, v: opts.velocity ?? 0, done: false }
  let last = performance.now()
  let raf = 0
  const tick = (now: number) => {
    const dt = Math.min(0.064, (now - last) / 1000)
    last = now
    s = stepSpring(s, to, dt, opts)
    onUpdate(s.x)
    if (s.done) {
      opts.onDone?.()
      return
    }
    raf = requestAnimationFrame(tick)
  }
  raf = requestAnimationFrame(tick)
  return () => cancelAnimationFrame(raf)
}
