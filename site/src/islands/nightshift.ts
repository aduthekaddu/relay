/**
 * NightShiftTimeline behaviour: the scroll position scrubs a "now" cursor
 * from 22:00 to 07:00; runs light up as the cursor passes them and the
 * morning report appears at dawn.
 */
import { type Island, runtime } from '../lib/runtime'

const START_MIN = 22 * 60
const SPAN_MIN = 9 * 60

/** Clock label for a 0..1 position on the night. */
export function clockAt(p: number): string {
  const m = Math.round(START_MIN + Math.max(0, Math.min(1, p)) * SPAN_MIN) % (24 * 60)
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`
}

const nightshift: Island = (root) => {
  if (runtime.reduced) return undefined
  const runs = [...root.querySelectorAll<HTMLElement>('.night-run')]
  const now = root.querySelector<HTMLElement>('[data-now]')
  const set = (p: number) => {
    root.style.setProperty('--p', p.toFixed(4))
    if (now) now.textContent = clockAt(p)
    for (const r of runs) r.classList.toggle('is-done', p >= Number(r.dataset.at))
    root.classList.toggle('is-dawn', p >= 0.985)
  }
  root.classList.add('is-live')
  set(0)
  const st = runtime.ScrollTrigger.create({
    trigger: root,
    start: 'top 78%',
    end: 'bottom 38%',
    scrub: true,
    onUpdate: (self) => set(self.progress),
  })
  return () => {
    st.kill()
    root.classList.remove('is-live', 'is-dawn')
  }
}

export default nightshift
