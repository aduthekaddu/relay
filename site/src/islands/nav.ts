/**
 * Top navigation: mobile disclosure menu (Escape closes, focus returns to
 * the toggle) and a "scrolled" state that adds a backdrop once the page
 * moves.
 */
import type { Island } from '../lib/runtime'

const nav: Island = (root) => {
  const toggle = root.querySelector<HTMLButtonElement>('[data-menu-toggle]')
  const menu = root.querySelector<HTMLElement>('[data-menu]')
  const setOpen = (open: boolean) => {
    if (!toggle || !menu) return
    toggle.setAttribute('aria-expanded', String(open))
    root.dataset.open = open ? 'true' : 'false'
    if (open) menu.querySelector<HTMLElement>('a')?.focus()
  }
  const onToggle = () => setOpen(toggle?.getAttribute('aria-expanded') !== 'true')
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Escape' && root.dataset.open === 'true') {
      setOpen(false)
      toggle?.focus()
    }
  }
  let ticking = false
  const onScroll = () => {
    if (ticking) return
    ticking = true
    requestAnimationFrame(() => {
      root.dataset.scrolled = window.scrollY > 24 ? 'true' : 'false'
      ticking = false
    })
  }
  toggle?.addEventListener('click', onToggle)
  document.addEventListener('keydown', onKey)
  window.addEventListener('scroll', onScroll, { passive: true })
  onScroll()
  return () => {
    toggle?.removeEventListener('click', onToggle)
    document.removeEventListener('keydown', onKey)
    window.removeEventListener('scroll', onScroll)
  }
}

export default nav
