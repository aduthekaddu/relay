/**
 * Accessible tabs (WAI-ARIA tabs pattern): arrow keys, Home/End,
 * automatic activation. Without JS every panel is visible in sequence.
 */
import type { Island } from '../lib/runtime'

const tabs: Island = (root) => {
  const list = root.querySelector<HTMLElement>('[role="tablist"]')
  const tabEls = [...root.querySelectorAll<HTMLButtonElement>('[role="tab"]')]
  const panels = tabEls.map((t) => document.getElementById(t.getAttribute('aria-controls') || ''))
  if (!list || tabEls.length === 0) return undefined
  root.dataset.ready = 'true'
  const select = (i: number, focus = false) => {
    tabEls.forEach((t, j) => {
      const on = i === j
      t.setAttribute('aria-selected', String(on))
      t.tabIndex = on ? 0 : -1
      const p = panels[j]
      if (p) p.hidden = !on
    })
    if (focus) tabEls[i]?.focus()
  }
  const initial = Math.max(0, tabEls.findIndex((t) => t.getAttribute('aria-selected') === 'true'))
  select(initial)
  const onClick = (e: Event) => {
    const i = tabEls.indexOf((e.target as HTMLElement).closest('[role="tab"]') as HTMLButtonElement)
    if (i >= 0) select(i)
  }
  const onKey = (e: KeyboardEvent) => {
    const i = tabEls.indexOf(document.activeElement as HTMLButtonElement)
    if (i < 0) return
    const n = tabEls.length
    const next = { ArrowRight: (i + 1) % n, ArrowLeft: (i - 1 + n) % n, Home: 0, End: n - 1 }[e.key]
    if (next === undefined) return
    e.preventDefault()
    select(next, true)
  }
  list.addEventListener('click', onClick)
  list.addEventListener('keydown', onKey)
  return () => {
    list.removeEventListener('click', onClick)
    list.removeEventListener('keydown', onKey)
  }
}

export default tabs
