// Theme preference: Carbon (dark), Paper (light) or Auto (follow the OS).
// public/boot.js applies the stored preference before first paint; this
// module keeps it in sync afterwards.
import { signal } from '@preact/signals'
import { load, save } from '../lib/util'

export type ThemePref = 'carbon' | 'paper' | 'auto'
export type Theme = 'carbon' | 'paper'

const KEY = 'relay.theme'
const mq = typeof matchMedia !== 'undefined' ? matchMedia('(prefers-color-scheme: light)') : null

const validPref = (v: unknown): v is ThemePref => v === 'carbon' || v === 'paper' || v === 'auto'

/** Resolve a preference against the OS scheme (pure; tested). */
export function resolveTheme(pref: ThemePref, osLight: boolean): Theme {
  return pref === 'auto' ? (osLight ? 'paper' : 'carbon') : pref
}

const initialPref: ThemePref = (() => {
  // ?theme=paper|carbon|auto overrides for this page load (screenshots, links).
  const q = typeof location !== 'undefined' ? new URLSearchParams(location.search).get('theme') : null
  if (validPref(q)) return q
  const v = load<unknown>(KEY, 'carbon')
  return validPref(v) ? v : 'carbon'
})()

/** The user's choice. */
export const themePref = signal<ThemePref>(initialPref)
/** The theme actually shown. */
export const theme = signal<Theme>(resolveTheme(initialPref, !!mq?.matches))

function apply(): void {
  const t = resolveTheme(themePref.value, !!mq?.matches)
  theme.value = t
  const root = document.documentElement
  root.dataset.theme = t
  root.dataset.themePref = themePref.value
  const color = t === 'paper' ? '#f3f0e8' : '#0b0b0c'
  for (const m of document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')) m.content = color
}

/** Change and persist the theme preference. */
export function setTheme(pref: ThemePref): void {
  themePref.value = pref
  save(KEY, pref)
  // Cross-fade the whole page when supported (skipped for reduced motion).
  const doc = document as Document & { startViewTransition?: (cb: () => void) => unknown }
  if (doc.startViewTransition && !matchMedia('(prefers-reduced-motion: reduce)').matches)
    doc.startViewTransition(apply)
  else apply()
}

/** Carbon ⇄ Paper (from Auto, flips the currently shown theme). */
export function toggleTheme(): void {
  setTheme(theme.value === 'carbon' ? 'paper' : 'carbon')
}

let wired = false
/** Follow OS changes and other tabs (called once at boot). */
export function trackTheme(): void {
  if (wired) return
  wired = true
  apply()
  mq?.addEventListener('change', apply)
  window.addEventListener('storage', (e) => {
    if (e.key !== KEY || !e.newValue) return
    try {
      const v = JSON.parse(e.newValue)
      if (validPref(v)) {
        themePref.value = v
        apply()
      }
    } catch {
      /* ignore */
    }
  })
}
