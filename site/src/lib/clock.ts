/** Night-shift clock: positions on the 22:00 → 07:00 timeline. */

const START_MIN = 22 * 60
const SPAN_MIN = 9 * 60

/** Clock label (HH:MM) for a 0..1 position on the night. */
export function clockAt(p: number): string {
  const m = Math.round(START_MIN + Math.max(0, Math.min(1, p)) * SPAN_MIN) % (24 * 60)
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`
}

/** 0..1 position of an HH:MM time on the night (times before 22:00 are the next morning). */
export function nightPos(t: string): number {
  const [h = 0, m = 0] = t.split(':').map(Number)
  const min = (h < 22 ? h + 24 : h) * 60 + m
  return Math.max(0, Math.min(1, (min - START_MIN) / SPAN_MIN))
}
