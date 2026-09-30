// Small shared helpers with no dependencies. Keep this file tiny: it is
// part of the initial bundle.

/** Join class names, skipping falsy values. */
export function cx(...parts: unknown[]): string {
  let out = ''
  for (const p of parts) if (p && typeof p === 'string') out = out ? `${out} ${p}` : p
  return out
}

/** Read JSON from localStorage; returns `fallback` on any error. */
export function load<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key)
    return raw === null ? fallback : (JSON.parse(raw) as T)
  } catch {
    return fallback
  }
}

/** Write JSON to localStorage; quota and privacy-mode errors are ignored. */
export function save(key: string, value: unknown): void {
  try {
    localStorage.setItem(key, JSON.stringify(value))
  } catch {
    /* storage full or disabled: the value simply isn't persisted */
  }
}

/** True on Apple platforms (⌘ is the primary modifier). */
export const isMac: boolean =
  typeof navigator !== 'undefined' && /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent)

/** True when the primary input is touch. */
export function isTouch(): boolean {
  return typeof matchMedia !== 'undefined' && matchMedia('(pointer: coarse)').matches
}

/** True when the user asked for reduced motion (OS or Relay setting). */
export function prefersReducedMotion(): boolean {
  if (typeof document !== 'undefined' && document.documentElement.dataset.motion === 'reduce') return true
  return typeof matchMedia !== 'undefined' && matchMedia('(prefers-reduced-motion: reduce)').matches
}

/** A short haptic tick on devices that support it (Android). */
export function haptic(ms = 8): void {
  try {
    navigator.vibrate?.(ms)
  } catch {
    /* unsupported */
  }
}

/** Clamp n into [lo, hi]. */
export const clamp = (n: number, lo: number, hi: number): number => Math.min(hi, Math.max(lo, n))

/** True when the event target is a text-editing element. */
export function isEditable(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false
  if (el.isContentEditable) return true
  const tag = el.tagName
  if (tag === 'TEXTAREA' || tag === 'SELECT') return true
  if (tag !== 'INPUT') return false
  const type = (el as HTMLInputElement).type
  return !['checkbox', 'radio', 'button', 'submit', 'reset', 'range', 'color', 'file'].includes(type)
}

let uid = 0
/** A document-unique id for aria wiring. */
export function nextId(prefix = 'r'): string {
  uid += 1
  return `${prefix}-${uid}`
}
