/**
 * Install transcript: prints the server-rendered lines one by one (with a
 * short "typing" phase for the command line) the first time the log is
 * on screen. Every line is present in the markup, so no-JS and reduced
 * motion simply show the finished transcript.
 */
import { type Island, runtime, sleep, whenNear } from '../lib/runtime'

const installLog: Island = (root) => {
  const lines = [...root.querySelectorAll<HTMLElement>('[data-log]')]
  if (runtime.reduced || lines.length === 0) return undefined
  const ctrl = new AbortController()
  for (const l of lines) l.classList.add('is-pending')
  const play = async () => {
    for (const l of lines) {
      const wait = Number(l.dataset.log || 220)
      if (l.dataset.type !== undefined) {
        const text = l.textContent || ''
        l.textContent = ''
        l.classList.remove('is-pending')
        for (let i = 1; i <= text.length; i += 2) {
          l.textContent = text.slice(0, i)
          await sleep(14, ctrl.signal)
        }
        l.textContent = text
      } else {
        l.classList.remove('is-pending')
      }
      await sleep(wait, ctrl.signal)
    }
  }
  const cancel = whenNear(root, () => void play().catch(() => {}), '-15% 0px')
  return () => {
    cancel()
    ctrl.abort()
    for (const l of lines) l.classList.remove('is-pending')
  }
}

export default installLog
