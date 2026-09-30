/**
 * Target textures for the LED field. A target is a list of layers drawn
 * into a tiny canvas with one texel per dot: red = bone brightness,
 * green = signal (orange) brightness. Layers add up ("lighter").
 *
 * Positions are normalised to the viewport: x/y in 0..1 are the layer
 * centre, `h` is a height as a fraction of the viewport height.
 */
import { DOT_H, eachDot, measure } from './dotfont'

export type Layer =
  | { kind: 'dotfont'; text: string; x?: number; y?: number; h?: number; w?: number; accent?: boolean; gain?: number }
  | { kind: 'text'; text: string; x?: number; y?: number; h?: number; weight?: number; accent?: boolean; gain?: number }
  | { kind: 'image'; src: string; focus?: [number, number]; gain?: number; beacon?: [number, number] | null }
  | { kind: 'device'; device: 'phone' | 'tablet' | 'laptop'; x?: number; y?: number; h?: number; gain?: number }
  | { kind: 'ring'; x?: number; y?: number; r?: number; accent?: boolean; gain?: number }
  | { kind: 'noise'; density?: number; gain?: number }

/** A full target: layers plus field-wide settings. */
export interface Target {
  layers: Layer[]
  /** Interference-wave strength, 0..1 (default 0.6). */
  ambient?: number
  /** Beacon position (normalised viewport coords) or null for none. */
  beacon?: [number, number] | null
}

/** Viewport and grid geometry used when rasterising a target. */
export interface Geometry {
  cols: number
  rows: number
  /** CSS px per dot. */
  pitch: number
  /** Viewport in CSS px. */
  width: number
  height: number
}

/** Result of rasterising: RGBA texels (cols×rows) + resolved anchors. */
export interface Raster {
  data: Uint8ClampedArray
  /** Beacon in CSS px, if any layer or the target defines one. */
  beacon: [number, number] | null
}

const images = new Map<string, Promise<HTMLImageElement | null>>()

/** Load (once) an image used by image layers; resolves null on error. */
export function loadImage(src: string): Promise<HTMLImageElement | null> {
  let p = images.get(src)
  if (!p) {
    p = new Promise((resolve) => {
      const img = new Image()
      img.decoding = 'async'
      img.onload = () => resolve(img)
      img.onerror = () => resolve(null)
      img.src = src
    })
    images.set(src, p)
  }
  return p
}

/** Preload every image referenced by a target. */
export function preload(target: Target): Promise<unknown> {
  return Promise.all(target.layers.flatMap((l) => (l.kind === 'image' ? [loadImage(l.src)] : [])))
}

/** Cover-fit an image into the viewport keeping `focus` visible. */
function coverRect(iw: number, ih: number, vw: number, vh: number, focus: [number, number]) {
  const s = Math.max(vw / iw, vh / ih)
  const w = iw * s
  const h = ih * s
  const x = Math.min(0, Math.max(vw - w, vw / 2 - focus[0] * w))
  const y = Math.min(0, Math.max(vh - h, vh / 2 - focus[1] * h))
  return { x, y, w, h }
}

function channel(accent: boolean | undefined, gain = 1): string {
  const v = Math.round(255 * Math.min(1, Math.max(0, gain)))
  return accent ? `rgb(0 ${v} 0)` : `rgb(${v} 0 0)`
}

function drawDevice(ctx: CanvasRenderingContext2D, l: Extract<Layer, { kind: 'device' }>, g: Geometry) {
  const cx = (l.x ?? 0.7) * g.cols
  const cy = (l.y ?? 0.5) * g.rows
  const h = (l.h ?? 0.62) * g.rows
  const ratio = l.device === 'phone' ? 0.48 : l.device === 'tablet' ? 0.74 : 1.55
  const w = h * ratio
  const x = Math.round(cx - w / 2)
  const y = Math.round(cy - h / 2)
  const r = l.device === 'phone' ? w * 0.16 : l.device === 'tablet' ? w * 0.06 : 2
  ctx.lineWidth = 1
  ctx.strokeStyle = channel(false, l.gain ?? 1)
  if (l.device === 'laptop') {
    const sh = h * 0.8
    ctx.beginPath()
    ctx.roundRect(x + 0.5, y + 0.5, w, sh, 2)
    ctx.stroke()
    // Base.
    ctx.beginPath()
    ctx.moveTo(x - w * 0.08, y + sh + 2.5)
    ctx.lineTo(x + w * 1.08, y + sh + 2.5)
    ctx.stroke()
  } else {
    ctx.beginPath()
    ctx.roundRect(x + 0.5, y + 0.5, w, h, r)
    ctx.stroke()
  }
  // Screen content: a few terminal-like lines, one orange "needs you" row.
  const sx = x + Math.max(2, w * 0.1)
  const sw = w - Math.max(4, w * 0.2)
  const top = y + (l.device === 'phone' ? h * 0.14 : h * 0.1)
  const lines = l.device === 'laptop' ? 9 : l.device === 'tablet' ? 12 : 10
  const step = ((l.device === 'laptop' ? h * 0.8 : h) * 0.72) / lines
  for (let i = 0; i < lines; i++) {
    const len = sw * (0.35 + ((i * 37) % 11) / 16)
    const accent = i === lines - 3
    ctx.fillStyle = channel(accent, accent ? 1 : 0.42)
    ctx.fillRect(Math.round(sx), Math.round(top + i * step), Math.max(1, Math.round(Math.min(sw, len))), 1)
  }
}

/** Rasterise a target for the given geometry. Image layers must be preloaded. */
export async function rasterize(target: Target, g: Geometry): Promise<Raster> {
  const canvas = document.createElement('canvas')
  canvas.width = g.cols
  canvas.height = g.rows
  const ctx = canvas.getContext('2d', { willReadFrequently: true })!
  ctx.fillStyle = '#000'
  ctx.fillRect(0, 0, g.cols, g.rows)
  ctx.globalCompositeOperation = 'lighter'
  let beacon: [number, number] | null = target.beacon ? [target.beacon[0] * g.width, target.beacon[1] * g.height] : null
  for (const l of target.layers) {
    switch (l.kind) {
      case 'image': {
        const img = await loadImage(l.src)
        if (!img) break
        const focus = l.focus ?? [0.5, 0.5]
        const r = coverRect(img.naturalWidth, img.naturalHeight, g.cols, g.rows, focus)
        ctx.globalAlpha = Math.min(1, l.gain ?? 1)
        ctx.imageSmoothingEnabled = true
        ctx.drawImage(img, r.x, r.y, r.w, r.h)
        ctx.globalAlpha = 1
        if (l.beacon && target.beacon === undefined) {
          beacon = [(r.x + l.beacon[0] * r.w) * g.pitch, (r.y + l.beacon[1] * r.h) * g.pitch]
        }
        break
      }
      case 'dotfont': {
        const units = measure(l.text)
        // Size one font pixel in dots from the requested height or width.
        const byH = ((l.h ?? 0.3) * g.rows) / DOT_H
        const byW = l.w ? (l.w * g.cols) / units : Number.POSITIVE_INFINITY
        const k = Math.max(1, Math.floor(Math.min(byH, byW)))
        const ox = Math.round((l.x ?? 0.5) * g.cols - (units * k) / 2)
        const oy = Math.round((l.y ?? 0.5) * g.rows - (DOT_H * k) / 2)
        ctx.fillStyle = channel(l.accent, l.gain ?? 1)
        const rad = k > 2 ? k * 0.46 : k / 2
        eachDot(l.text, (x, y) => {
          ctx.beginPath()
          ctx.arc(ox + x * k + k / 2, oy + y * k + k / 2, rad, 0, Math.PI * 2)
          ctx.fill()
        })
        break
      }
      case 'text': {
        const px = Math.max(4, (l.h ?? 0.3) * g.rows)
        ctx.font = `${l.weight ?? 800} ${px}px ${getComputedStyle(document.documentElement).getPropertyValue('--font-sans') || 'sans-serif'}`
        ctx.textAlign = 'center'
        ctx.textBaseline = 'middle'
        ctx.fillStyle = channel(l.accent, l.gain ?? 1)
        ctx.fillText(l.text, (l.x ?? 0.5) * g.cols, (l.y ?? 0.5) * g.rows)
        break
      }
      case 'device':
        drawDevice(ctx, l, g)
        break
      case 'ring': {
        ctx.lineWidth = 1
        ctx.strokeStyle = channel(l.accent, l.gain ?? 1)
        ctx.beginPath()
        ctx.arc((l.x ?? 0.5) * g.cols, (l.y ?? 0.5) * g.rows, (l.r ?? 0.2) * g.rows, 0, Math.PI * 2)
        ctx.stroke()
        break
      }
      case 'noise': {
        const d = l.density ?? 0.3
        ctx.fillStyle = channel(false, l.gain ?? 0.8)
        for (let i = 0; i < g.cols * g.rows * d; i++) {
          ctx.fillRect(Math.floor(Math.random() * g.cols), Math.floor(Math.random() * g.rows), 1, 1)
        }
        break
      }
    }
  }
  const data = ctx.getImageData(0, 0, g.cols, g.rows).data
  return { data, beacon }
}
