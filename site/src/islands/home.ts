/**
 * Home page "film" direction: the pinned, scrubbed shots.
 *
 *  - `[data-shot="screens"]` pins while the field draws phone → tablet →
 *    laptop and the matching fact lights up;
 *  - `[data-shot="terminal"]` pins while the scroll scrubs the scripted
 *    phone session and its callouts;
 *  - `[data-shot="tap"]` emits a beacon ring on every "needs you" beat
 *    while it is the current shot.
 *
 * Reduced motion: nothing pins; every shot shows its final frame.
 */
import type { PhoneController } from './phone-terminal'
import { type Cleanup, type Island, runtime } from '../lib/runtime'
import { target } from '../lib/targets'

const SCREENS = ['screens-phone', 'screens-tablet', 'screens-laptop'] as const

function screens(root: HTMLElement): Cleanup {
  const shot = root.querySelector<HTMLElement>('[data-shot="screens"]')
  const stage = shot?.querySelector<HTMLElement>('[data-stage]')
  if (!shot || !stage) return () => {}
  const steps = [...shot.querySelectorAll<HTMLElement>('[data-step]')]
  let index = -1
  const show = (i: number, force = false) => {
    if (i === index && !force) return
    index = i
    steps.forEach((s, j) => s.classList.toggle('is-on', j === i))
    shot.dataset.step = String(i)
    void runtime.field?.setTarget(target(SCREENS[i]!, { mobile: runtime.mobile }), force ? 0.9 : 1.1)
  }
  const st = runtime.ScrollTrigger.create({
    trigger: stage,
    start: 'top top',
    end: () => `+=${window.innerHeight * (runtime.mobile ? 1.6 : 2.2)}`,
    pin: true,
    pinSpacing: true,
    anticipatePin: 1,
    onUpdate: (self) => show(Math.min(2, Math.floor(self.progress * 3))),
    onToggle: (self) => {
      if (self.isActive) show(Math.min(2, Math.floor(self.progress * 3)), true)
    },
  })
  // The director hands this shot over to us whenever it becomes current.
  const onActivate = () => show(Math.max(0, index), true)
  shot.addEventListener('field:activate', onActivate)
  return () => {
    shot.removeEventListener('field:activate', onActivate)
    st.kill()
  }
}

function terminal(root: HTMLElement): Cleanup {
  const shot = root.querySelector<HTMLElement>('[data-shot="terminal"]')
  const stage = shot?.querySelector<HTMLElement>('[data-stage]')
  const phone = shot?.querySelector<HTMLElement>('.phone')
  if (!shot || !stage || !phone) return () => {}
  let ctl: PhoneController | undefined
  let pending = 0
  void import('./phone-terminal').then((m) => {
    ctl = m.phoneController(phone)
    ctl?.render(pending)
  })
  const bar = shot.querySelector<HTMLElement>('[data-progress]')
  const st = runtime.ScrollTrigger.create({
    trigger: stage,
    start: 'top top',
    end: () => `+=${window.innerHeight * (runtime.mobile ? 2.4 : 3)}`,
    pin: true,
    pinSpacing: true,
    anticipatePin: 1,
    scrub: true,
    onUpdate: (self) => {
      // Hold the finished frame for the last 8 % of the pin.
      const p = Math.min(1, self.progress / 0.92)
      pending = p
      ctl?.render(p)
      bar?.style.setProperty('--p', p.toFixed(4))
    },
  })
  return () => st.kill()
}

function tap(root: HTMLElement): Cleanup {
  const shot = root.querySelector<HTMLElement>('[data-shot="tap"]')
  if (!shot) return () => {}
  let timer = 0
  const st = runtime.ScrollTrigger.create({
    trigger: shot,
    start: 'top 55%',
    end: 'bottom 45%',
    onToggle: (self) => {
      clearInterval(timer)
      if (!self.isActive) return
      runtime.field?.pulse(0.5, 0.36, 0.9)
      timer = window.setInterval(() => runtime.field?.pulse(0.5, 0.36, 0.7), 2400)
    },
  })
  return () => {
    clearInterval(timer)
    st.kill()
  }
}

const home: Island = (root) => {
  if (runtime.reduced) {
    root.classList.add('is-still')
    return undefined
  }
  const offs = [screens(root), terminal(root), tap(root)]
  runtime.ScrollTrigger.refresh()
  return () => {
    for (const off of offs) off()
  }
}

export default home
