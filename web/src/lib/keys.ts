// Keyboard helpers: display formatting for shortcut hints and a small
// matcher for global shortcuts ("mod+k", "shift+?", "g t").
import { isMac } from './util'

const MAC: Record<string, string> = {
  mod: '⌘',
  cmd: '⌘',
  meta: '⌘',
  ctrl: '⌃',
  alt: '⌥',
  option: '⌥',
  shift: '⇧',
  enter: '↩',
  return: '↩',
  esc: 'Esc',
  escape: 'Esc',
  backspace: '⌫',
  delete: '⌦',
  tab: '⇥',
  up: '↑',
  down: '↓',
  left: '←',
  right: '→',
  space: 'Space',
}
const PC: Record<string, string> = {
  ...MAC,
  mod: 'Ctrl',
  cmd: 'Ctrl',
  meta: 'Win',
  ctrl: 'Ctrl',
  alt: 'Alt',
  option: 'Alt',
  shift: 'Shift',
  enter: 'Enter',
  return: 'Enter',
  backspace: 'Backspace',
  delete: 'Del',
  tab: 'Tab',
}

/** Display labels for a key list, e.g. ['mod','K'] → ['⌘','K'] (Mac) / ['Ctrl','K']. */
export function keyLabels(keys: string[], mac: boolean = isMac): string[] {
  const map = mac ? MAC : PC
  return keys.map((k) => map[k.toLowerCase()] ?? (k.length === 1 ? k.toUpperCase() : k))
}

export interface Chord {
  key: string
  mod: boolean
  shift: boolean
  alt: boolean
}

/** Parse "mod+shift+k" into a chord. "mod" is ⌘ on Mac, Ctrl elsewhere. */
export function parseChord(spec: string): Chord {
  const parts = spec.toLowerCase().split('+')
  const key = parts.pop() || ''
  return { key, mod: parts.includes('mod'), shift: parts.includes('shift'), alt: parts.includes('alt') }
}

/** True when a keyboard event matches the chord. */
export function matchChord(e: KeyboardEvent, c: Chord, mac: boolean = isMac): boolean {
  const mod = mac ? e.metaKey : e.ctrlKey
  if (mod !== c.mod || e.altKey !== c.alt) return false
  // Shift is implied for symbols like "?" — only enforce when asked.
  if (c.shift && !e.shiftKey) return false
  if (!c.shift && e.shiftKey && c.key.length > 1) return false
  const k = e.key.toLowerCase()
  return k === c.key || (c.key.length === 1 && e.code === `Key${c.key.toUpperCase()}`)
}
