// The login backdrop: a quiet LED dot field that echoes the marketing
// site. Canvas 2D, ~24 fps, DPR ≤ 2, pauses when the tab is hidden or the
// canvas is off-screen, and draws one static frame under reduced motion.
// A slow wave drifts across the grid; the pointer is a soft lens; one
// orange beacon blinks where the signal "lands".
import { useEffect, useRef } from 'preact/hooks'
import { prefersReducedMotion } from '../../lib/util'

interface Palette {
  dot: [number, number, number]
  signal: [number, number, number]
}

function readPalette(el: HTMLElement): Palette {
  const cs = getComputedStyle(el)
  const parse = (v: string, fb: [number, number, number]): [number, number, number] => {
    const m = /^#([0-9a-f]{6})$/i.exec(v.trim())
    if (!m) return fb
    const n = Number.parseInt(m[1], 16)
    return [(n >> 16) & 255, (n >> 8) & 255, n & 255]
  }
  return {
    dot: parse(cs.getPropertyValue('--text'), [237, 233, 224]),
    signal: parse(cs.getPropertyValue('--signal'), [255, 91, 31]),
  }
}

/** Brightness 0..1 of the dot at grid (x, y) at time t (s). Pure; tested. */
export function fieldIntensity(x: number, y: number, t: number, cols: number, rows: number): number {
  const u = x / Math.max(1, cols)
  const v = y / Math.max(1, rows)
  const a = Math.sin(u * 6.2 + t * 0.35) + Math.sin(v * 5.1 - t * 0.27) + Math.sin((u + v) * 4.3 + t * 0.19)
  const wave = (a + 3) / 6 // 0..1
  // Quiet in the middle band, where the sign-in card sits.
  const dx = u - 0.5
  const dy = v - 0.45
  const quiet = Math.min(1, Math.sqrt(dx * dx * 3 + dy * dy * 2.2) * 1.5)
  return wave ** 2.2 * (0.25 + 0.75 * quiet)
}

export function DotField() {
  const ref = useRef<HTMLCanvasElement>(null)
  useEffect(() => {
    const canvas = ref.current
    const ctx = canvas?.getContext('2d')
    if (!canvas || !ctx) return
    const reduced = prefersReducedMotion()
    const pitch = matchMedia('(max-width: 767px)').matches ? 16 : 14
    let pal = readPalette(document.documentElement)
    let cols = 0
    let rows = 0
    let dpr = 1
    let raf = 0
    let last = 0
    let visible = true
    const pointer = { x: -1e4, y: -1e4, tx: -1e4, ty: -1e4 }
    let beacon = { x: 0, y: 0 }

    const resize = () => {
      dpr = Math.min(2, window.devicePixelRatio || 1)
      const w = canvas.clientWidth
      const h = canvas.clientHeight
      canvas.width = Math.round(w * dpr)
      canvas.height = Math.round(h * dpr)
      cols = Math.ceil(w / pitch) + 1
      rows = Math.ceil(h / pitch) + 1
      beacon = { x: Math.round(cols * 0.78), y: Math.round(rows * 0.2) }
    }

    const draw = (t: number) => {
      const w = canvas.width
      const h = canvas.height
      ctx.clearRect(0, 0, w, h)
      const [r, g, b] = pal.dot
      const p = pitch * dpr
      const lensR = 120 * dpr
      pointer.x += (pointer.tx - pointer.x) * 0.12
      pointer.y += (pointer.ty - pointer.y) * 0.12
      for (let y = 0; y < rows; y++) {
        for (let x = 0; x < cols; x++) {
          const cx = x * p + p / 2
          const cy = y * p + p / 2
          let v = fieldIntensity(x, y, t, cols, rows)
          const dx = cx - pointer.x * dpr
          const dy = cy - pointer.y * dpr
          const d2 = dx * dx + dy * dy
          if (d2 < lensR * lensR) v += (1 - Math.sqrt(d2) / lensR) * 0.35
          const alpha = 0.035 + Math.min(1, v) * 0.3
          ctx.fillStyle = `rgba(${r},${g},${b},${alpha.toFixed(3)})`
          const rad = (0.9 + Math.min(1, v) * 0.9) * dpr
          ctx.beginPath()
          ctx.arc(cx, cy, rad, 0, Math.PI * 2)
          ctx.fill()
        }
      }
      // The beacon: blinks 1.2 s, with a ring that expands through the field.
      const on = reduced || Math.floor(t / 0.6) % 2 === 0
      const [sr, sg, sb] = pal.signal
      const bx = beacon.x * p + p / 2
      const by = beacon.y * p + p / 2
      ctx.fillStyle = `rgba(${sr},${sg},${sb},${on ? 1 : 0.28})`
      ctx.beginPath()
      ctx.arc(bx, by, 2.6 * dpr, 0, Math.PI * 2)
      ctx.fill()
      if (!reduced) {
        const phase = (t % 2.4) / 2.4
        ctx.strokeStyle = `rgba(${sr},${sg},${sb},${(0.35 * (1 - phase)).toFixed(3)})`
        ctx.lineWidth = dpr
        ctx.beginPath()
        ctx.arc(bx, by, (4 + phase * 60) * dpr, 0, Math.PI * 2)
        ctx.stroke()
      }
    }

    const frame = (now: number) => {
      raf = 0
      if (!visible || document.visibilityState !== 'visible') return
      if (now - last >= 41) {
        last = now
        draw(now / 1000)
      }
      raf = requestAnimationFrame(frame)
    }
    const start = () => {
      if (reduced) {
        draw(12)
        return
      }
      if (!raf) raf = requestAnimationFrame(frame)
    }

    resize()
    start()
    const ro = new ResizeObserver(() => {
      resize()
      if (reduced) draw(12)
    })
    ro.observe(canvas)
    const io = new IntersectionObserver(([e]) => {
      visible = e.isIntersecting
      if (visible) start()
    })
    io.observe(canvas)
    const onVis = () => document.visibilityState === 'visible' && start()
    const onMove = (e: PointerEvent) => {
      if (e.pointerType !== 'mouse') return
      const rect = canvas.getBoundingClientRect()
      pointer.tx = e.clientX - rect.left
      pointer.ty = e.clientY - rect.top
    }
    const onTheme = new MutationObserver(() => {
      pal = readPalette(document.documentElement)
      if (reduced) draw(12)
    })
    onTheme.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
    document.addEventListener('visibilitychange', onVis)
    window.addEventListener('pointermove', onMove, { passive: true })
    return () => {
      cancelAnimationFrame(raf)
      ro.disconnect()
      io.disconnect()
      onTheme.disconnect()
      document.removeEventListener('visibilitychange', onVis)
      window.removeEventListener('pointermove', onMove)
    }
  }, [])
  return <canvas ref={ref} class="login-field" aria-hidden="true" />
}
