// Client-side navigation with View Transitions.
//
// preact-iso owns the history stack and handles <a href> clicks itself.
// To animate between areas with the View Transitions API we must let the
// browser snapshot the *old* DOM before Preact renders the new route, so
// while a transition is being prepared Preact's render queue is held
// (options.debounceRendering) and released inside the transition's update
// callback. Only area changes animate; moving within an area (folders,
// tabs) swaps instantly so browsing stays snappy.
import { options } from 'preact'
import { prefersReducedMotion } from '../lib/util'
import { areaForPath } from './areas'

type RouteFn = (url: string, replace?: boolean) => void

let routeFn: RouteFn | null = null
let held: Array<() => void> | null = null
let committed: (() => void) | null = null

const prevDebounce = options.debounceRendering
options.debounceRendering = (cb) => {
  if (held) held.push(cb)
  else if (prevDebounce) prevDebounce(cb)
  else queueMicrotask(cb)
}

type VTDocument = Omit<Document, 'startViewTransition'> & {
  startViewTransition?: (cb: () => Promise<void> | void) => { finished: Promise<void> }
}

/** The shell registers preact-iso's route() here once mounted. */
export function setRouter(fn: RouteFn): void {
  routeFn = fn
}

/** The shell calls this when a route has rendered (Router onRouteChange). */
export function routeCommitted(): void {
  const fn = committed
  committed = null
  fn?.()
}

/** True when moving from `from` to `to` should animate (area change). Pure. */
export function shouldTransition(from: string, to: string): boolean {
  const a = new URL(from, 'https://x.invalid').pathname
  const b = new URL(to, 'https://x.invalid').pathname
  if (a === b) return false
  return areaForPath(a).id !== areaForPath(b).id
}

function canAnimate(): boolean {
  const doc = document as unknown as VTDocument
  return (
    typeof doc.startViewTransition === 'function' &&
    !prefersReducedMotion() &&
    document.visibilityState === 'visible'
  )
}

/**
 * Hold renders, start a view transition, and release the renders inside
 * it. `change` performs the navigation (history update + state change).
 */
function transition(change: () => void): void {
  const doc = document as unknown as VTDocument
  const mine: Array<() => void> = []
  held = mine
  // Safety net: never hold renders for long, whatever the browser does.
  window.setTimeout(() => {
    if (held !== mine) return
    held = null
    for (const cb of mine) cb()
  }, 600)
  const html = document.documentElement
  html.classList.add('vt-nav')
  try {
    const vt = doc.startViewTransition?.(
      () =>
        new Promise<void>((resolve) => {
          const queue = held ?? []
          held = null
          const t = window.setTimeout(done, 420) // lazy route still loading: stop waiting
          function done() {
            window.clearTimeout(t)
            committed = null
            resolve()
          }
          committed = done
          for (const cb of queue) cb()
        }),
    )
    vt?.finished.finally(() => html.classList.remove('vt-nav'))
    if (!vt) html.classList.remove('vt-nav')
  } catch {
    html.classList.remove('vt-nav')
    const queue = held ?? []
    held = null
    for (const cb of queue) cb()
  }
  change()
}

/**
 * Classify a navigation target: same-origin paths stay in the app,
 * http(s) URLs on other origins (e.g. preview subdomains) open in a new
 * tab, anything else (javascript:, data:, garbage) is dropped. Pure.
 */
export function navTarget(url: string, origin: string): { kind: 'app' | 'external'; href: string } | null {
  let u: URL
  try {
    u = new URL(url, origin)
  } catch {
    return null
  }
  if (u.protocol !== 'https:' && u.protocol !== 'http:') return null
  if (u.origin === origin) return { kind: 'app', href: u.pathname + u.search + u.hash }
  return { kind: 'external', href: u.href }
}

/** Navigate inside the app (animated across areas). */
export function navigate(raw: string, opts: { replace?: boolean } = {}): void {
  const t = navTarget(raw, location.origin)
  if (!t) return
  if (t.kind === 'external') {
    window.open(t.href, '_blank', 'noopener')
    return
  }
  const url = t.href
  const from = location.pathname + location.search
  const go = () => {
    if (routeFn) routeFn(url, opts.replace)
    else location.assign(url)
  }
  if (routeFn && shouldTransition(from, url) && canAnimate()) transition(go)
  else go()
}

/**
 * Capture-phase click listener: when an in-app link leads to another area,
 * begin the view transition before preact-iso (bubble phase) routes. The
 * event is left untouched so element handlers and preact-iso still run.
 */
export function interceptLinks(): () => void {
  const onClick = (e: MouseEvent) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
    const a = (e.composedPath() as Element[]).find((el) => el instanceof HTMLAnchorElement) as
      | HTMLAnchorElement
      | undefined
    if (!a?.href || a.origin !== location.origin || a.download || (a.target && a.target !== '_self')) return
    const href = a.getAttribute('href') || ''
    if (href.startsWith('#') || a.dataset.native !== undefined) return
    const to = a.pathname + a.search
    if (!shouldTransition(location.pathname + location.search, to) || !canAnimate()) return
    // preact-iso performs the actual route in its own (bubble) listener.
    transition(() => {})
  }
  window.addEventListener('click', onClick, true)
  return () => window.removeEventListener('click', onClick, true)
}
