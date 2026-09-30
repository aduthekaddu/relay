/**
 * The scripted phone terminal session (see docs/dev/TERMINAL_UX.md).
 * Times are positions on a 0..1 timeline; `end` makes a line type out
 * between `t` and `end`. Segments carry a colour class.
 */
export type Tone = 'dim' | 'ok' | 'sig' | 'acc' | 'fg' | 'bold'

export interface ScriptLine {
  t: number
  end?: number
  segs: [Tone, string][]
}

export const SESSION = { agent: 'claude', task: 'refactor-auth' }

export const LINES: ScriptLine[] = [
  { t: 0.0, end: 0.07, segs: [['ok', '~/api'], ['dim', ' (refactor-auth) '], ['fg', '$ claude']] },
  { t: 0.09, segs: [['acc', '✻ '], ['bold', 'Claude Code'], ['dim', '  ~/api']] },
  {
    t: 0.12,
    end: 0.22,
    segs: [['dim', '> '], ['fg', 'Move the session checks into middleware and keep the tests green.']],
  },
  { t: 0.26, segs: [['acc', '● '], ['fg', 'Read '], ['dim', 'src/auth/session.ts']] },
  { t: 0.3, segs: [['acc', '● '], ['fg', 'Read '], ['dim', 'src/http/router.ts']] },
  { t: 0.35, segs: [['acc', '● '], ['fg', 'Edit '], ['dim', 'src/auth/middleware.ts '], ['ok', '+48'], ['sig', ' −12']] },
  { t: 0.41, segs: [['acc', '● '], ['fg', 'Run '], ['dim', 'pnpm test']] },
  { t: 0.47, segs: [['ok', '  ✓ 212 passed'], ['dim', ' · 4.1 s']] },
  { t: 0.52, segs: [['acc', '● '], ['fg', 'Edit '], ['dim', 'src/http/router.ts '], ['ok', '+6'], ['sig', ' −19']] },
  { t: 0.6, segs: [['sig', '? '], ['bold', 'Push refactor-auth to origin?']] },
  { t: 0.62, segs: [['dim', '  1 Yes  2 Always  3 No']] },
  { t: 0.86, segs: [['dim', '> '], ['fg', 'yes, and open a PR']] },
  { t: 0.9, segs: [['acc', '● '], ['fg', 'Run '], ['dim', 'git push -u origin refactor-auth']] },
  { t: 0.95, segs: [['ok', '  ✓ '], ['fg', 'Opened PR #214']] },
]

/** Compose bar typing window and text. */
export const COMPOSE = { t: 0.7, end: 0.82, sent: 0.85, text: 'yes, and open a PR' }

/** When the session waits on the user. */
export const NEEDS = { from: 0.6, to: 0.85 }

/** Callouts (home page shot) with the phone region they point at. */
export const CALLOUTS: { t: number; id: string; title: string; body: string; side: 'left' | 'right' }[] = [
  { t: 0.12, id: 'keys', title: 'Keys you actually need', body: 'Esc, Tab, sticky Ctrl, arrows, | ~ / — one row, 44 px each.', side: 'left' },
  { t: 0.3, id: 'swipe', title: 'Swipe to move the cursor', body: 'Drag the key bar like a trackpad; up and down walks history.', side: 'right' },
  { t: 0.46, id: 'delete', title: '⌘⌫ deletes the line — like your Mac', body: 'Mac and Windows editing shortcuts translate to what shells expect.', side: 'left' },
  { t: 0.64, id: 'paste', title: 'Paste a screenshot, get a path', body: 'Camera roll or clipboard → uploaded → path typed for the agent.', side: 'right' },
  { t: 0.84, id: 'persist', title: 'Close the tab — it keeps running', body: 'Sessions live on the machine. Reopen anywhere, right where you were.', side: 'left' },
]

/** Keys on the mobile key bar. */
export const KEYS = ['esc', 'tab', 'ctrl', 'alt', '↑', '↓', '←', '→', '|', '~', '/', '-', '^C']
