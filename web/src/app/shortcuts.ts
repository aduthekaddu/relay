// Global keyboard shortcuts.
//
//   ⌘K / Ctrl+K   command center (toggle)
//   /             command center (when not typing)
//   G then <key>  go to an area (keys from areas.ts, e.g. G T → Terminal)
//   ?             shortcut sheet
//   Esc           closes the top overlay (handled by the overlays)
//
// Screens that own a chord (the terminal uses ⌘K to clear) handle the
// event on their own element and call preventDefault(); the global
// handler ignores events that were already handled.
import { signal } from '@preact/signals'
import { isEditable } from '../lib/util'
import { AREAS } from './areas'

/** Shortcut sheet visibility. */
export const shortcutsOpen = signal(false)

/** One documented shortcut (rendered in the sheet). */
export interface ShortcutDoc {
  keys: string[][]
  label: string
  group: 'General' | 'Go to' | 'Command center'
}

export const SHORTCUTS: ShortcutDoc[] = [
  { keys: [['mod', 'K']], label: 'Open the command center', group: 'General' },
  { keys: [['/']], label: 'Search', group: 'General' },
  { keys: [['?']], label: 'Show keyboard shortcuts', group: 'General' },
  { keys: [['Esc']], label: 'Close / go back', group: 'General' },
  ...AREAS.map((a) => ({ keys: [['G'], [a.goKey.toUpperCase()]], label: a.label, group: 'Go to' as const })),
  { keys: [['up'], ['down']], label: 'Move the selection', group: 'Command center' },
  { keys: [['enter']], label: 'Run the selected item', group: 'Command center' },
  { keys: [['mod', 'enter']], label: 'Secondary action', group: 'Command center' },
  { keys: [['mod', 'K'], ['right']], label: 'Actions for the selected item', group: 'Command center' },
  { keys: [['backspace']], label: 'Back (empty field)', group: 'Command center' },
]

export interface GoSequence {
  /** Feed a key; returns the area path to open, or null. */
  feed: (key: string, now: number) => string | null
  /** True while waiting for the second key. */
  pending: (now: number) => boolean
}

/** "G then key" state machine with a 1.2 s window (pure; tested). */
export function createGoSequence(windowMs = 1200): GoSequence {
  let armedAt = -1
  return {
    feed(key, now) {
      const k = key.toLowerCase()
      if (armedAt >= 0 && now - armedAt <= windowMs) {
        armedAt = -1
        return AREAS.find((a) => a.goKey === k)?.path ?? null
      }
      armedAt = k === 'g' ? now : -1
      return null
    },
    pending: (now) => armedAt >= 0 && now - armedAt <= windowMs,
  }
}

export interface ShortcutActions {
  togglePalette: () => void
  openPalette: () => void
  navigate: (path: string) => void
  /** True while any modal overlay is open (the letter shortcuts pause). */
  modalOpen: () => boolean
}

/** Install the global key handler; returns an uninstall function. */
export function installShortcuts(actions: ShortcutActions, mac: boolean): () => void {
  const go = createGoSequence()
  const onKey = (e: KeyboardEvent) => {
    if (e.defaultPrevented || e.isComposing) return
    const mod = mac ? e.metaKey : e.ctrlKey
    if (mod && !e.altKey && !e.shiftKey && e.key.toLowerCase() === 'k') {
      e.preventDefault()
      actions.togglePalette()
      return
    }
    if (mod || e.altKey || isEditable(e.target) || actions.modalOpen()) return
    if (e.key === '?') {
      e.preventDefault()
      shortcutsOpen.value = !shortcutsOpen.value
      return
    }
    if (e.key === '/') {
      e.preventDefault()
      actions.openPalette()
      return
    }
    if (e.key.length !== 1 && e.key !== ',') return
    const path = go.feed(e.key, performance.now())
    if (path) {
      e.preventDefault()
      actions.navigate(path)
    }
  }
  window.addEventListener('keydown', onKey)
  return () => window.removeEventListener('keydown', onKey)
}
