/**
 * FlapBoard — a split-flap departures board.
 *
 * Every character is a real four-leaf flap: the upper leaf (old glyph)
 * falls with gravity (ease-in) to 90°, the lower leaf (new glyph) lands
 * from 90° with a small bounce, 60 ms per flip. A change runs through the
 * drum alphabet in order, cascading left→right. Rows change status on a
 * timer while the board is on screen.
 *
 * Markup is server-rendered (readable with JS off); this island upgrades
 * each `.flap-cell` in place. Screen readers get one polite update per
 * row through the row's `.sr-only` text.
 */
import { type Island, onVisible, runtime } from '../lib/runtime'

/** Drum order (a real board can only go forward through it). */
export const DRUM = ' ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-:.●'
const FLIP_MS = 60
const MAX_FLIPS = 12
const CASCADE_MS = 28

/** The sequence of glyphs a flap shows going from `from` to `to`. */
export function flipPath(from: string, to: string, max = MAX_FLIPS): string[] {
  const n = DRUM.length
  const a = Math.max(0, DRUM.indexOf(from.toUpperCase()))
  const b = Math.max(0, DRUM.indexOf(to.toUpperCase()))
  let steps = (b - a + n) % n
  if (steps === 0) return []
  const path: string[] = []
  const start = steps > max ? (b - max + n) % n : a
  steps = Math.min(steps, max)
  for (let i = 1; i <= steps; i++) path.push(DRUM[(start + i) % n]!)
  return path
}

interface Cell {
  el: HTMLElement
  ch: string
  top: HTMLElement
  bot: HTMLElement
  leafTop: HTMLElement
  leafBot: HTMLElement
  busy: Promise<void>
}

function half(cls: string, ch: string): HTMLElement {
  const h = document.createElement('span')
  h.className = cls
  const g = document.createElement('span')
  g.textContent = ch
  h.append(g)
  return h
}

function setGlyph(h: HTMLElement, ch: string) {
  h.firstElementChild!.textContent = ch === ' ' ? '\u00a0' : ch
}

function upgrade(el: HTMLElement): Cell {
  const ch = (el.textContent || ' ').slice(0, 1)
  el.textContent = ''
  const top = half('fc-top', ch)
  const bot = half('fc-bot', ch)
  const leafTop = half('fc-leaf fc-leaf-top', ch)
  const leafBot = half('fc-leaf fc-leaf-bot', ch)
  el.append(top, bot, leafTop, leafBot)
  el.classList.add('is-live')
  for (const h of [top, bot, leafTop, leafBot]) setGlyph(h, ch)
  return { el, ch, top, bot, leafTop, leafBot, busy: Promise.resolve() }
}

async function flipOnce(c: Cell, next: string): Promise<void> {
  setGlyph(c.top, next)
  setGlyph(c.leafTop, c.ch)
  setGlyph(c.leafBot, next)
  setGlyph(c.bot, c.ch)
  c.el.classList.add('is-flipping')
  const fall = c.leafTop.animate([{ transform: 'rotateX(0deg)' }, { transform: 'rotateX(-90deg)' }], {
    duration: FLIP_MS * 0.5,
    easing: 'cubic-bezier(0.55, 0, 1, 0.45)',
    fill: 'forwards',
  })
  await fall.finished
  const land = c.leafBot.animate(
    [{ transform: 'rotateX(90deg)' }, { transform: 'rotateX(-8deg)', offset: 0.78 }, { transform: 'rotateX(0deg)' }],
    { duration: FLIP_MS * 0.5 + 16, easing: 'cubic-bezier(0.2, 0.8, 0.3, 1)', fill: 'forwards' },
  )
  await land.finished
  setGlyph(c.bot, next)
  fall.cancel()
  land.cancel()
  c.el.classList.remove('is-flipping')
  c.ch = next
}

function flipTo(c: Cell, target: string, delay: number, instant: boolean): Promise<void> {
  c.busy = c.busy.then(async () => {
    if (instant) {
      for (const h of [c.top, c.bot, c.leafTop, c.leafBot]) setGlyph(h, target)
      c.ch = target
      return
    }
    if (delay) await new Promise((r) => setTimeout(r, delay))
    for (const ch of flipPath(c.ch, target)) await flipOnce(c, ch)
  })
  return c.busy
}

/** Write `text` into a field of cells (padded/truncated to its width). */
function writeField(cells: Cell[], text: string, baseDelay: number, instant: boolean): Promise<void> {
  const padded = text.toUpperCase().padEnd(cells.length, ' ').slice(0, cells.length)
  return Promise.all(cells.map((c, i) => flipTo(c, padded[i]!, baseDelay + i * CASCADE_MS, instant))).then(() => {})
}

const flapboard: Island = (root) => {
  const rows = [...root.querySelectorAll<HTMLElement>('.flap-row')]
  const cycle: string[][] = JSON.parse(root.dataset.cycle || '[]')
  const statusCol = Number(root.dataset.statusCol ?? -1)
  const interval = Number(root.dataset.interval ?? 3200)
  const model = rows.map((row) => ({
    row,
    sr: row.querySelector<HTMLElement>('.sr-only'),
    fields: [...row.querySelectorAll<HTMLElement>('.flap-field')].map((f) =>
      [...f.querySelectorAll<HTMLElement>('.flap-cell')].map(upgrade),
    ),
    step: 0,
  }))
  const instant = runtime.reduced
  let visible = false
  let timer = 0
  let turn = 0
  let alive = true

  const tick = async () => {
    if (!alive || !visible || statusCol < 0 || model.length === 0) return
    const i = turn++ % model.length
    const m = model[i]!
    const seq = cycle[i]
    if (seq && seq.length > 1) {
      m.step = (m.step + 1) % seq.length
      const status = seq[m.step]!
      m.row.dataset.status = status.toLowerCase().replace(/\s+/g, '-')
      await writeField(m.fields[statusCol]!, status, 0, instant)
      if (m.sr) m.sr.textContent = `${m.row.dataset.label ?? ''}: ${status.toLowerCase()}`
    }
    if (alive && visible) timer = window.setTimeout(tick, instant ? interval * 2 : interval)
  }

  // Opening cascade: every row flips in from blank when first seen.
  let opened = false
  const open = () => {
    if (opened) return
    opened = true
    if (instant) return
    model.forEach((m, r) => {
      m.fields.forEach((cells) => {
        const text = cells.map((c) => c.ch).join('')
        for (const c of cells) {
          c.ch = ' '
          for (const h of [c.top, c.bot, c.leafTop, c.leafBot]) setGlyph(h, ' ')
        }
        void writeField(cells, text, r * 90, false)
      })
    })
  }

  const stopVisible = onVisible(root, (v) => {
    visible = v
    clearTimeout(timer)
    if (v) {
      open()
      timer = window.setTimeout(tick, instant ? interval : interval * 0.8)
    }
  })
  return () => {
    alive = false
    clearTimeout(timer)
    stopVisible()
  }
}

export default flapboard
