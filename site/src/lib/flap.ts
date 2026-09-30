/**
 * Split-flap physics, as pure functions of time (no DOM), so the board
 * can be driven from one animation loop and unit-tested.
 */

/** Drum order (a real board can only go forward through it). */
export const DRUM = ' ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-:.●'

/** Most flips a single change may take (long trips skip ahead). */
export const MAX_FLIPS = 8

/** The sequence of glyphs a flap shows going from `from` to `to`. */
export function flipPath(from: string, to: string, max = MAX_FLIPS): string[] {
  const n = DRUM.length
  const a = Math.max(0, DRUM.indexOf(from.toUpperCase()))
  const b = Math.max(0, DRUM.indexOf(to.toUpperCase()))
  let steps = (b - a + n) % n
  if (steps === 0) return []
  const path: string[] = []
  const start = steps > max ? (b - max + n) % n : a
  steps = Math.min(steps, max)
  for (let i = 1; i <= steps; i++) path.push(DRUM[(start + i) % n]!)
  return path
}

/** What each half of a flap shows at one instant. */
export interface FlapPose {
  /** Static upper half (the incoming glyph once a flip starts). */
  top: string
  /** Static lower half (the outgoing glyph until the leaf lands). */
  bottom: string
  /** Falling upper leaf (outgoing glyph). */
  leafTop: string
  /** Landing lower leaf (incoming glyph). */
  leafBot: string
  /** Degrees; 0 = flat, −90 = edge-on after falling. */
  topAngle: number
  /** Degrees; 90 = edge-on before landing, 0 = landed. */
  botAngle: number
  moving: boolean
}

const easeIn = (t: number) => t * t * t

/**
 * Pose of a flap `elapsed` ms after it started playing `path` from
 * `from`, with `flipMs` per flip: the upper leaf falls under gravity for
 * the first half, the lower leaf lands with a small overshoot in the
 * second half.
 */
export function flapPose(from: string, path: string[], elapsed: number, flipMs: number): FlapPose {
  const last = path.length ? path[path.length - 1]! : from
  if (elapsed < 0 || path.length === 0 || elapsed >= path.length * flipMs) {
    const ch = elapsed < 0 ? from : last
    return { top: ch, bottom: ch, leafTop: ch, leafBot: ch, topAngle: 0, botAngle: 90, moving: false }
  }
  const i = Math.floor(elapsed / flipMs)
  const p = (elapsed - i * flipMs) / flipMs
  const prev = i === 0 ? from : path[i - 1]!
  const next = path[i]!
  let topAngle = -90
  let botAngle = 90
  if (p < 0.5) {
    topAngle = -90 * easeIn(p / 0.5)
  } else {
    const q = (p - 0.5) / 0.5
    // Land past flat (−8°) then settle back: the paper-thin bounce.
    botAngle = q < 0.78 ? 90 - 98 * (1 - (1 - q / 0.78) ** 2) : -8 + 8 * ((q - 0.78) / 0.22)
  }
  return { top: next, bottom: prev, leafTop: prev, leafBot: next, topAngle, botAngle, moving: true }
}
