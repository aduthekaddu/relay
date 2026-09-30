/** Scripted queries for the command-center demo. */
export interface PaletteItem {
  glyph: 'agents' | 'terminal' | 'previews' | 'system' | 'files' | 'ai' | 'calc' | 'clip'
  title: string
  sub: string
  hint: string
}

export interface PaletteQuery {
  q: string
  items: PaletteItem[]
  /** What happens on Enter. */
  done: string
}

export const QUERIES: PaletteQuery[] = [
  {
    q: 'resume auth',
    items: [
      { glyph: 'agents', title: 'Resume “refactor-auth”', sub: 'Claude Code · ~/api · 2 h ago', hint: '↵' },
      { glyph: 'agents', title: 'Resume “auth-tests”', sub: 'Codex · ~/api · yesterday', hint: '' },
      { glyph: 'files', title: 'src/auth/session.ts', sub: '~/api', hint: '' },
    ],
    done: 'Resumed in a new terminal',
  },
  {
    q: 'kill :3000',
    items: [
      { glyph: 'system', title: 'Stop process on :3000', sub: 'node · next dev · pid 48213', hint: '↵' },
      { glyph: 'previews', title: 'Preview :3000', sub: 'Next.js · ~/web', hint: '' },
    ],
    done: 'Stopped next dev (pid 48213)',
  },
  {
    q: 'open preview 5173',
    items: [
      { glyph: 'previews', title: 'Open preview :5173', sub: 'Vite · https://5173.your-box', hint: '↵' },
      { glyph: 'previews', title: 'Show QR for :5173', sub: 'Open it on your phone', hint: '⌘ Q' },
    ],
    done: 'Opened https://5173.your-box',
  },
  {
    q: 'ask why is CI red',
    items: [
      { glyph: 'ai', title: 'Ask Codex', sub: 'Quick AI · uses your own CLI login', hint: '↵' },
      { glyph: 'ai', title: 'Ask Claude Code', sub: 'Quick AI · headless', hint: '' },
      { glyph: 'terminal', title: 'Open CI logs in a terminal', sub: 'gh run view --log-failed', hint: '' },
    ],
    done: '“The lint job fails on web/src/app.ts:42 …”',
  },
  {
    q: '12 GB in MiB',
    items: [{ glyph: 'calc', title: '11,444.09 MiB', sub: 'Calculator · copy with ↵', hint: '↵' }],
    done: 'Copied 11444.09',
  },
]
