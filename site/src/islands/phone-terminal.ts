/**
 * PhoneTerminal — renders the scripted session at a timeline position.
 * `render(p)` is deterministic, so the home page can scrub it with the
 * scroll position; `data-autoplay` plays it on a loop while visible.
 * Server markup is the final frame (readable without JS).
 */
import { COMPOSE, NEEDS } from '../lib/phone-script'
import { type Island, onVisible, runtime } from '../lib/runtime'

/** Controller attached to the element for other islands. */
export interface PhoneController {
  render(p: number): void
}

const controllers = new WeakMap<HTMLElement, PhoneController>()

/** Get (or wait for) the controller of a phone element. */
export function phoneController(el: HTMLElement): PhoneController | undefined {
  return controllers.get(el)
}

/** Portion of text visible at p for a line typing between t and end. */
export function typed(text: string, p: number, t: number, end?: number): string {
  if (p < t) return ''
  if (end === undefined || p >= end) return text
  const k = (p - t) / (end - t)
  return text.slice(0, Math.max(1, Math.round(text.length * k)))
}

const phoneTerminal: Island = (root) => {
  const lines = [...root.querySelectorAll<HTMLElement>('[data-line]')].map((el) => ({
    el,
    t: Number(el.dataset.t),
    end: el.dataset.end ? Number(el.dataset.end) : undefined,
    segs: [...el.querySelectorAll<HTMLElement>('[data-seg]')].map((s) => ({ s, text: s.textContent || '' })),
  }))
  const compose = root.querySelector<HTMLElement>('[data-compose-text]')
  const placeholder = root.querySelector<HTMLElement>('[data-compose-placeholder]')
  const send = root.querySelector<HTMLElement>('[data-send]')
  const status = root.querySelector<HTMLElement>('[data-status]')
  const statusLabel = root.querySelector<HTMLElement>('[data-status-label]')
  const callouts = [...document.querySelectorAll<HTMLElement>(`[data-callout-for="${root.id}"]`)]
  let last = -1

  const render = (p: number) => {
    if (Math.abs(p - last) < 0.0005) return
    last = p
    for (const l of lines) {
      const on = p >= l.t
      l.el.hidden = !on
      if (!on) continue
      if (l.end !== undefined) {
        // Type across all segments as one string.
        const full = l.segs.map((s) => s.text).join('')
        let shown = typed(full, p, l.t, l.end).length
        for (const s of l.segs) {
          const n = Math.min(shown, s.text.length)
          s.s.textContent = s.text.slice(0, n)
          shown -= n
        }
        l.el.classList.toggle('is-typing', p < l.end)
      }
    }
    const text = p >= COMPOSE.sent ? '' : typed(COMPOSE.text, p, COMPOSE.t, COMPOSE.end)
    if (compose) compose.textContent = text
    if (placeholder) placeholder.hidden = text.length > 0
    root.classList.toggle('is-composing', p >= COMPOSE.t && p < COMPOSE.sent)
    send?.classList.toggle('is-ready', p >= COMPOSE.end && p < COMPOSE.sent)
    const needs = p >= NEEDS.from && p < NEEDS.to
    if (status) status.className = `dot ${needs ? 'dot-needs' : 'dot-ok'}`
    if (statusLabel) statusLabel.textContent = needs ? 'Needs you' : 'Running'
    root.dataset.keybar = p >= 0.08 ? 'up' : 'down'
    let active = ''
    for (const c of callouts) {
      const t = Number(c.dataset.t)
      const on = p >= t
      c.classList.toggle('is-on', on)
      if (on) active = c.dataset.callout || ''
    }
    for (const c of callouts) c.classList.toggle('is-current', c.dataset.callout === active)
    root.dataset.hl = active
  }
  controllers.set(root, { render })

  if (runtime.reduced || root.dataset.autoplay === undefined) {
    render(root.dataset.autoplay === undefined && !runtime.reduced ? 0 : 1)
    return () => controllers.delete(root)
  }
  // Autoplay: 14 s timeline + 3 s hold, only while on screen.
  const DURATION = 14000
  const HOLD = 3000
  let raf = 0
  let t0 = 0
  let offset = 0
  const loop = (now: number) => {
    if (!t0) t0 = now - offset
    const e = (now - t0) % (DURATION + HOLD)
    offset = e
    render(Math.min(1, e / DURATION))
    raf = requestAnimationFrame(loop)
  }
  const stop = onVisible(root, (v) => {
    cancelAnimationFrame(raf)
    t0 = 0
    if (v) raf = requestAnimationFrame(loop)
  })
  return () => {
    stop()
    cancelAnimationFrame(raf)
    controllers.delete(root)
  }
}

export default phoneTerminal
