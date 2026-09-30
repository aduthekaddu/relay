// Keyboard-aware viewport. Exposes the visual viewport as CSS variables so
// immersive screens (terminal) can size themselves above the software
// keyboard: --vvh (visible height) and --kb (keyboard/occluded inset).
// Works with `interactive-widget=resizes-content` in the viewport meta:
// Chrome on Android then resizes the layout viewport itself and --kb
// stays 0; iOS Safari only shrinks the visual viewport, so --kb matters.

let wired = false

/** Start tracking (idempotent). */
export function trackViewport(): void {
  if (wired || typeof window === 'undefined') return
  wired = true
  const root = document.documentElement.style
  const vv = window.visualViewport
  let frame = 0
  const update = () => {
    frame = 0
    const h = vv ? vv.height : window.innerHeight
    const kb = vv ? Math.max(0, window.innerHeight - vv.height - vv.offsetTop) : 0
    root.setProperty('--vvh', `${Math.round(h)}px`)
    root.setProperty('--kb', `${Math.round(kb)}px`)
    document.documentElement.toggleAttribute('data-keyboard', kb > 120)
  }
  const schedule = () => {
    if (!frame) frame = requestAnimationFrame(update)
  }
  update()
  vv?.addEventListener('resize', schedule)
  vv?.addEventListener('scroll', schedule)
  window.addEventListener('resize', schedule)
}
