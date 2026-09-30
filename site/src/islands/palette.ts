/**
 * PaletteDemo behaviour: types each scripted query into the command
 * center, animates its results in, "presses" Enter on the best match and
 * shows what happened. Runs only while on screen; reduced motion shows
 * the server-rendered first query and stops.
 */
import { GLYPHS, type PaletteItem, type PaletteQuery } from '../lib/palette-data'
import { type Island, onVisible, runtime, sleep } from '../lib/runtime'

/** Build one result row. */
export function itemEl(it: PaletteItem, active: boolean): HTMLLIElement {
  const li = document.createElement('li')
  li.className = `pal-item${active ? ' is-active' : ''}`
  li.dataset.glyph = it.glyph
  const parts: [string, string][] = [
    ['pal-glyph', GLYPHS[it.glyph]],
    ['pal-title', it.title],
    ['pal-sub', it.sub],
  ]
  if (it.hint) parts.push(['kbd', it.hint])
  for (const [cls, text] of parts) {
    const s = document.createElement('span')
    s.className = cls
    s.textContent = text
    li.append(s)
  }
  return li
}

const palette: Island = (root) => {
  const queries: PaletteQuery[] = JSON.parse(root.dataset.queries || '[]')
  const q = root.querySelector<HTMLElement>('[data-q]')
  const list = root.querySelector<HTMLElement>('[data-list]')
  const toast = root.querySelector<HTMLElement>('[data-toast]')
  const group = root.querySelector<HTMLElement>('[data-group]')
  if (!q || !list || !toast || queries.length === 0 || runtime.reduced) return undefined
  let ctrl: AbortController | null = null
  let index = 0

  const play = async (signal: AbortSignal) => {
    for (;;) {
      const query = queries[index % queries.length]!
      toast.textContent = ''
      toast.classList.remove('is-on')
      // Clear the previous query like a user selecting all + typing.
      root.classList.add('is-clearing')
      await sleep(260, signal)
      q.textContent = ''
      list.replaceChildren()
      if (group) group.textContent = 'Recent'
      root.classList.remove('is-clearing')
      await sleep(320, signal)
      for (let i = 1; i <= query.q.length; i++) {
        q.textContent = query.q.slice(0, i)
        // Once a few characters are in, results start to narrow.
        if (i === Math.min(3, query.q.length) || i === query.q.length) {
          list.replaceChildren(...query.items.map((it, j) => itemEl(it, j === 0)))
          if (group) group.textContent = query.items[0]?.glyph === 'calc' ? 'Calculator' : 'Best match'
          list.classList.remove('is-in')
          void list.offsetWidth
          list.classList.add('is-in')
        }
        await sleep(48 + Math.random() * 70, signal)
      }
      await sleep(850, signal)
      list.firstElementChild?.classList.add('is-pressed')
      await sleep(200, signal)
      toast.textContent = query.done
      toast.classList.add('is-on')
      await sleep(1900, signal)
      index++
    }
  }

  const stop = onVisible(root, (visible) => {
    ctrl?.abort()
    ctrl = null
    if (!visible) return
    ctrl = new AbortController()
    play(ctrl.signal).catch(() => {})
  })
  return () => {
    stop()
    ctrl?.abort()
  }
}

export default palette
