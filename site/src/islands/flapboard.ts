/**
 * FlapBoard — a split-flap departures board.
 *
 * Every character is a real four-leaf flap: the upper leaf (old glyph)
 * falls with gravity (ease-in) to 90°, the lower leaf (new glyph) lands
 * from 90° with a small bounce, 60 ms per flip. A change runs through the
 * drum alphabet in order, cascading left→right. Rows change status on a
 * timer while the board is on screen.
 *
 * The engine is time-driven: one rAF loop computes every cell's flip and
 * phase from the clock, so a slow device skips frames instead of slowing
 * the board down, and nothing runs once all flaps have settled.
 *
 * Markup is server-rendered (readable with JS off); this island upgrades
 * each `.flap-cell` in place. Screen readers get each row's plain text.
 */
import { flapPose, flipPath } from '../lib/flap'
import { type Island, onVisible, runtime } from '../lib/runtime'

const FLIP_MS = 60
const CASCADE_MS = 28

interface Cell {
  el: HTMLElement
  /** Glyph the cell ends on once its queue has played. */
  target: string
  /** Glyph shown before the current path starts. */
  from: string
  path: string[]
  t0: number
  /** Cache of the glyph written into each half (top, bot, leafTop, leafBot). */
  shown: [string, string, string, string]
  halves: [HTMLElement, HTMLElement, HTMLElement, HTMLElement]
  flipping: boolean
}

function half(cls: string): HTMLElement {
  const h = document.createElement('span')
  h.className = cls
  h.append(document.createElement('span'))
  return h
}

function write(c: Cell, i: 0 | 1 | 2 | 3, ch: string) {
  if (c.shown[i] === ch) return
  c.shown[i] = ch
  c.halves[i].firstElementChild!.textContent = ch === ' ' ? '\u00a0' : ch
}

function upgrade(el: HTMLElement): Cell {
  const ch = (el.textContent || ' ').replace(/\u00a0/g, ' ').slice(0, 1) || ' '
  el.textContent = ''
  const halves = [
    half('fc-top'),
    half('fc-bot'),
    half('fc-leaf fc-leaf-top'),
    half('fc-leaf fc-leaf-bot'),
  ] as Cell['halves']
  el.append(...halves)
  el.classList.add('is-live')
  const c: Cell = {
    el,
    target: ch,
    from: ch,
    path: [],
    t0: 0,
    shown: ['', '', '', ''],
    halves,
    flipping: false,
  }
  for (const i of [0, 1, 2, 3] as const) write(c, i, ch)
  return c
}

/** Show a cell at time `now`; returns true while it is still moving. */
function render(c: Cell, now: number): boolean {
  const pose = flapPose(c.from, c.path, now - c.t0, FLIP_MS)
  write(c, 0, pose.top)
  write(c, 1, pose.bottom)
  if (pose.moving) {
    write(c, 2, pose.leafTop)
    write(c, 3, pose.leafBot)
    c.halves[2].style.transform = `rotateX(${pose.topAngle.toFixed(1)}deg)`
    c.halves[3].style.transform = `rotateX(${pose.botAngle.toFixed(1)}deg)`
  }
  if (pose.moving !== c.flipping) {
    c.flipping = pose.moving
    c.el.classList.toggle('is-flipping', pose.moving)
  }
  return pose.moving || now < c.t0
}

/** Drives every cell of one board from a single animation loop. */
class Engine {
  private cells = new Set<Cell>()
  private raf = 0

  /** Queue `to` on a cell, starting no earlier than `at`. */
  queue(c: Cell, to: string, at: number, instant: boolean): number {
    if (instant) {
      c.from = to
      c.target = to
      c.path = []
      for (const i of [0, 1, 2, 3] as const) write(c, i, to)
      return at
    }
    const now = performance.now()
    const busyUntil = c.t0 + c.path.length * FLIP_MS
    const start = Math.max(at, busyUntil)
    // A cell still mid-queue restarts from where its queue will end.
    c.from = c.target
    c.path = flipPath(c.target, to)
    c.target = to
    c.t0 = Math.max(start, now)
    this.cells.add(c)
    this.kick()
    return c.t0 + c.path.length * FLIP_MS
  }

  private kick() {
    if (!this.raf) this.raf = requestAnimationFrame(this.frame)
  }

  private frame = (now: number) => {
    this.raf = 0
    for (const c of this.cells) if (!render(c, now)) this.cells.delete(c)
    if (this.cells.size) this.kick()
  }

  stop() {
    cancelAnimationFrame(this.raf)
    this.raf = 0
    this.cells.clear()
  }
}

/** Write `text` into a field of cells; resolves (ms) when it has settled. */
function writeField(engine: Engine, cells: Cell[], text: string, delay: number, instant: boolean): number {
  const padded = text.toUpperCase().padEnd(cells.length, ' ').slice(0, cells.length)
  const now = performance.now()
  let end = now
  cells.forEach((c, i) => {
    end = Math.max(end, engine.queue(c, padded[i]!, now + delay + i * CASCADE_MS, instant))
  })
  return end - now
}

const flapboard: Island = (root) => {
  const rows = [...root.querySelectorAll<HTMLElement>('.flap-row')]
  const cycle: string[][] = JSON.parse(root.dataset.cycle || '[]')
  const statusCol = Number(root.dataset.statusCol ?? -1)
  const interval = Number(root.dataset.interval ?? 3200)
  const engine = new Engine()
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

  const tick = () => {
    if (!visible || statusCol < 0 || model.length === 0) return
    const i = turn++ % model.length
    const m = model[i]!
    const seq = cycle[i]
    let wait = 0
    if (seq && seq.length > 1) {
      m.step = (m.step + 1) % seq.length
      const status = seq[m.step]!
      m.row.dataset.status = status.toLowerCase().replace(/\s+/g, '-')
      wait = writeField(engine, m.fields[statusCol]!, status, 0, instant)
      if (m.sr) m.sr.textContent = `${m.row.dataset.label ?? ''}: ${status.toLowerCase()}`
    }
    timer = window.setTimeout(tick, wait + (instant ? interval * 2 : interval))
  }

  // Opening cascade: every row flips in from blank when first seen.
  let opened = false
  const open = () => {
    if (opened || instant) return
    opened = true
    model.forEach((m, r) => {
      for (const cells of m.fields) {
        const text = cells.map((c) => c.target).join('')
        for (const c of cells) engine.queue(c, ' ', 0, true)
        writeField(engine, cells, text, 120 + r * 90, false)
      }
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
    clearTimeout(timer)
    stopVisible()
    engine.stop()
  }
}

export default flapboard
