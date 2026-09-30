/**
 * Shared runtime for islands: GSAP (with ScrollTrigger + SplitText), the
 * Lenis instance, the persistent LED field, and motion preferences. One
 * copy lives for the whole visit (Astro view transitions keep modules).
 */
import { gsap } from 'gsap'
import { ScrollTrigger } from 'gsap/ScrollTrigger'
import { SplitText } from 'gsap/SplitText'
import type Lenis from 'lenis'
import type { LedField } from './led-field'

gsap.registerPlugin(ScrollTrigger, SplitText)

const reducedQuery = window.matchMedia('(prefers-reduced-motion: reduce)')

export const runtime: {
  gsap: typeof gsap
  ScrollTrigger: typeof ScrollTrigger
  SplitText: typeof SplitText
  lenis: Lenis | null
  field: LedField | null
  /** True when the user asked for reduced motion. */
  reduced: boolean
  /** True for coarse pointers / narrow screens. */
  mobile: boolean
} = {
  gsap,
  ScrollTrigger,
  SplitText,
  lenis: null,
  field: null,
  reduced: reducedQuery.matches,
  mobile: window.matchMedia('(max-width: 767px)').matches,
}

reducedQuery.addEventListener('change', (e) => {
  runtime.reduced = e.matches
})
window.matchMedia('(max-width: 767px)').addEventListener('change', (e) => {
  runtime.mobile = e.matches
})

/** A cleanup function returned by island initialisers. */
export type Cleanup = () => void

/** Island initialiser: mount on `el`, return a cleanup. */
export type Island = (el: HTMLElement) => Cleanup | undefined | Promise<Cleanup | undefined>

/** Run `fn` when `el` first comes near the viewport; returns a cancel. */
export function whenNear(el: Element, fn: () => void, margin = '200px'): Cleanup {
  const io = new IntersectionObserver(
    (entries) => {
      if (entries.some((e) => e.isIntersecting)) {
        io.disconnect()
        fn()
      }
    },
    { rootMargin: margin },
  )
  io.observe(el)
  return () => io.disconnect()
}

/** Track visibility of `el`, calling `fn(visible)` on changes. */
export function onVisible(el: Element, fn: (visible: boolean) => void): Cleanup {
  const io = new IntersectionObserver((entries) => {
    for (const e of entries) fn(e.isIntersecting)
  })
  io.observe(el)
  return () => io.disconnect()
}

/** Promise that resolves after `ms`, or rejects silently when aborted. */
export function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(new DOMException('aborted', 'AbortError'))
    const t = window.setTimeout(resolve, ms)
    signal?.addEventListener('abort', () => {
      clearTimeout(t)
      reject(new DOMException('aborted', 'AbortError'))
    })
  })
}
