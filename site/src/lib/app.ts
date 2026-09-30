/**
 * Site entry. Runs once per visit; re-initialises page features on every
 * `astro:page-load` (first load and each view-transition navigation).
 *
 *  - Lenis smooth scroll wired into GSAP's ticker (native scroll under
 *    reduced motion)
 *  - the persistent LED field + the section "director"
 *  - line-mask headline reveals (`data-split`), fade-up reveals
 *    (`.will-reveal`), kinetic variable-font headlines (`data-kinetic`)
 *  - lazy islands (`data-island="name"`)
 */
import Lenis from 'lenis'
import { LedField } from './led-field'
import { type Cleanup, type Island, runtime } from './runtime'
import { target } from './targets'

const { gsap, ScrollTrigger, SplitText } = runtime

const islands: Record<string, () => Promise<{ default: Island }>> = {
  flapboard: () => import('../islands/flapboard'),
  'phone-terminal': () => import('../islands/phone-terminal'),
  palette: () => import('../islands/palette'),
  copy: () => import('../islands/copy'),
  home: () => import('../islands/home'),
  tabs: () => import('../islands/tabs'),
  'install-log': () => import('../islands/install-log'),
  nav: () => import('../islands/nav'),
  notification: () => import('../islands/notification'),
  nightshift: () => import('../islands/nightshift'),
}

let cleanups: Cleanup[] = []
let firstLoad = true

function setupLenis() {
  if (runtime.reduced || runtime.lenis) return
  const lenis = new Lenis({ lerp: 0.12, wheelMultiplier: 1, smoothWheel: true, anchors: { offset: 0 } })
  runtime.lenis = lenis
  lenis.on('scroll', ScrollTrigger.update)
  gsap.ticker.add((t) => lenis.raf(t * 1000))
  gsap.ticker.lagSmoothing(0)
}

/** Create the field after first paint so it never blocks text. */
function setupField(): Promise<void> {
  const canvas = document.getElementById('led') as HTMLCanvasElement | null
  if (!canvas || runtime.field) return Promise.resolve()
  return new Promise((resolve) => {
    requestAnimationFrame(() =>
      requestAnimationFrame(() => {
        const field = LedField.create(canvas, { still: runtime.reduced })
        if (field) {
          runtime.field = field
          document.documentElement.classList.add('webgl')
        }
        resolve()
      }),
    )
  })
}

/** Morph the field as field-bearing sections become current. */
function director(boot: boolean): Cleanup {
  const field = runtime.field
  const sections = [...document.querySelectorAll<HTMLElement>('[data-field]')]
  if (!field || sections.length === 0) return () => {}
  const view = () => ({ mobile: runtime.mobile })
  let current: HTMLElement | null = null
  let quietEl: HTMLElement | null = null
  let quietLevel = 0.15
  const updateQuiet = () => {
    if (quietEl) field.setQuiet(quietEl.getBoundingClientRect(), quietLevel)
    else field.setQuiet(null)
  }
  const activate = (el: HTMLElement, duration = 1.2) => {
    if (current === el) return
    current = el
    const name = el.dataset.field!
    if (name === 'manual') el.dispatchEvent(new CustomEvent('field:activate'))
    else void field.setTarget(target(name, view()), duration)
    field.setGain(Number(el.dataset.fieldGain ?? 1))
    quietEl = el.querySelector<HTMLElement>('[data-quiet]')
    quietLevel = Number(quietEl?.dataset.quiet || 0.15)
    updateQuiet()
  }
  const first = sections[0]!
  let booting = boot && Boolean(first.dataset.boot) && !runtime.reduced
  let bootTimer = 0
  if (booting) {
    // Preloader: scramble into the wordmark, then open onto the first shot.
    void field.setTarget({ layers: [{ kind: 'noise', density: 0.5, gain: 0.7 }], ambient: 0 }, 0)
    void field.setTarget(target(first.dataset.boot!, view()), 0.8)
    bootTimer = window.setTimeout(() => {
      booting = false
      // The visitor may have scrolled during the preloader.
      const live = triggers.find((t) => t.isActive)
      activate((live?.trigger as HTMLElement | undefined) ?? first, 1.6)
      ScrollTrigger.refresh()
    }, 1150)
  } else {
    activate(first, boot ? 0.9 : 1.2)
  }
  const triggers = sections.map((el) =>
    ScrollTrigger.create({
      trigger: el,
      start: 'top 55%',
      end: 'bottom 45%',
      onToggle: (self) => {
        if (self.isActive && !booting) activate(el)
      },
      onUpdate: () => {
        if (current === el) updateQuiet()
      },
    }),
  )
  const onResize = () => {
    if (current && !booting) {
      const el = current
      current = null
      activate(el, 0)
    }
  }
  window.addEventListener('resize', onResize)
  return () => {
    clearTimeout(bootTimer)
    window.removeEventListener('resize', onResize)
    for (const t of triggers) t.kill()
  }
}

/** Line-mask reveals for `[data-split]` headlines. */
function splits(boot: boolean): Cleanup {
  const els = [...document.querySelectorAll<HTMLElement>('[data-split]')]
  const made: SplitText[] = []
  for (const el of els) {
    if (runtime.reduced) {
      el.classList.add('is-split')
      continue
    }
    const split = SplitText.create(el, { type: 'lines,words', mask: 'lines', linesClass: 'line-mask', autoSplit: false })
    made.push(split)
    el.classList.add('is-split')
    const delay = Number(el.dataset.splitDelay ?? 0) * (boot ? 1 : 0.35)
    const tween = gsap.from(split.words, {
      yPercent: 115,
      duration: 0.9,
      ease: 'expo.out',
      stagger: 0.035,
      delay,
      paused: el.dataset.split !== 'now',
    })
    if (el.dataset.split !== 'now') {
      ScrollTrigger.create({ trigger: el, start: 'top 85%', once: true, onEnter: () => tween.play() })
    }
  }
  return () => {
    for (const s of made) s.revert()
  }
}

/** Fade-up reveals for `.will-reveal` (batched). */
function reveals(): Cleanup {
  const els = [...document.querySelectorAll<HTMLElement>('.will-reveal')]
  if (runtime.reduced) {
    for (const el of els) el.classList.add('is-in')
    return () => {}
  }
  const io = new IntersectionObserver(
    (entries) => {
      let i = 0
      for (const e of entries) {
        if (!e.isIntersecting) continue
        const el = e.target as HTMLElement
        el.style.transitionDelay = `${Math.min(i++, 6) * 70}ms`
        el.classList.add('is-in')
        io.unobserve(el)
      }
    },
    { rootMargin: '0px 0px -8% 0px' },
  )
  for (const el of els) io.observe(el)
  return () => io.disconnect()
}

/**
 * Kinetic headlines. `data-kinetic="scroll"` stretches the width axis as
 * the headline scrolls away; `data-kinetic="hover"` swells the characters
 * nearest the pointer (desktop only).
 */
function kinetic(): Cleanup {
  if (runtime.reduced) return () => {}
  const offs: Cleanup[] = []
  for (const el of document.querySelectorAll<HTMLElement>('[data-kinetic~="scroll"]')) {
    const tween = gsap.fromTo(
      el,
      { '--wdth': 100, '--wght': 700 },
      {
        '--wdth': 125,
        '--wght': 820,
        ease: 'none',
        scrollTrigger: { trigger: el, start: 'top 30%', end: 'bottom -40%', scrub: 0.6 },
      },
    )
    offs.push(() => tween.scrollTrigger?.kill())
  }
  if (!window.matchMedia('(hover: hover) and (pointer: fine)').matches) return () => offs.forEach((o) => o())
  for (const el of document.querySelectorAll<HTMLElement>('[data-kinetic~="hover"]')) {
    const split = SplitText.create(el, { type: 'chars', charsClass: 'k-char', autoSplit: false })
    const chars = split.chars as HTMLElement[]
    let raf = 0
    let px = 0
    let py = 0
    const apply = () => {
      raf = 0
      for (const c of chars) {
        const r = c.getBoundingClientRect()
        const d = Math.hypot(px - (r.left + r.width / 2), py - (r.top + r.height / 2))
        const k = Math.max(0, 1 - d / 260)
        c.style.setProperty('--k', k.toFixed(3))
      }
    }
    const move = (e: PointerEvent) => {
      px = e.clientX
      py = e.clientY
      if (!raf) raf = requestAnimationFrame(apply)
    }
    const leave = () => {
      for (const c of chars) c.style.setProperty('--k', '0')
    }
    el.addEventListener('pointermove', move)
    el.addEventListener('pointerleave', leave)
    offs.push(() => {
      el.removeEventListener('pointermove', move)
      el.removeEventListener('pointerleave', leave)
      split.revert()
    })
  }
  return () => {
    for (const o of offs) o()
  }
}

/** Mount lazy islands; each returns its own cleanup. */
async function mountIslands(): Promise<void> {
  const els = [...document.querySelectorAll<HTMLElement>('[data-island]')]
  await Promise.all(
    els.map(async (el) => {
      const load = islands[el.dataset.island!]
      if (!load) return
      try {
        const mod = await load()
        const off = await mod.default(el)
        if (off) cleanups.push(off)
      } catch (e) {
        console.error(`island ${el.dataset.island}:`, e)
      }
    }),
  )
  ScrollTrigger.refresh()
}

function teardown() {
  for (const c of cleanups.splice(0)) c()
  for (const t of ScrollTrigger.getAll()) t.kill()
}

async function onPageLoad() {
  const root = document.documentElement
  root.classList.add('js', 'app')
  if (runtime.field) root.classList.add('webgl')
  teardown()
  const boot = firstLoad
  firstLoad = false
  setupLenis()
  if (!boot && !location.hash) runtime.lenis?.scrollTo(0, { immediate: true, force: true })
  cleanups.push(splits(boot), reveals(), kinetic())
  void mountIslands()
  await setupField()
  cleanups.push(director(boot))
  ScrollTrigger.refresh()
}

document.addEventListener('astro:page-load', () => void onPageLoad())
document.addEventListener('astro:before-swap', teardown)
document.addEventListener('astro:after-swap', () => {
  document.documentElement.classList.add('js', 'app')
  if (runtime.field) document.documentElement.classList.add('webgl')
})
