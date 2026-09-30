/**
 * NotificationCard behaviour: when the card scrolls into view it drops in
 * like a lock-screen push, and the LED field sends a beacon ring from the
 * card's position. Re-arms when it leaves, so the tap happens every time
 * you come back to the shot.
 */
import { type Island, onVisible, runtime } from '../lib/runtime'

const notification: Island = (root) => {
  if (runtime.reduced) {
    root.classList.add('is-in')
    return undefined
  }
  let timer = 0
  const stop = onVisible(root, (visible) => {
    clearTimeout(timer)
    if (!visible) {
      root.classList.remove('is-in')
      return
    }
    timer = window.setTimeout(() => {
      root.classList.add('is-in')
      const r = root.getBoundingClientRect()
      runtime.field?.pulse(
        (r.left + r.width / 2) / window.innerWidth,
        (r.top + r.height / 2) / window.innerHeight,
        1,
      )
    }, 450)
  })
  return () => {
    clearTimeout(timer)
    stop()
  }
}

export default notification
