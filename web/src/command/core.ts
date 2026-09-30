// Built-in commands owned by the command center itself (navigation,
// appearance, account). Extensions (clipboard, snippets, notes, quick AI,
// calculator, scripts) are added by the command-center feature.
import { AREAS } from '../app/areas'
import type { Command } from './registry'

export const commands: Command[] = AREAS.map((a) => ({
  id: `go.${a.id}`,
  title: `Go to ${a.label}`,
  subtitle: a.blurb,
  section: 'Navigate',
  area: a.id,
  icon: `glyph:${a.id}`,
  keywords: [a.label.toLowerCase(), 'go', 'open'],
  shortcut: ['G', a.goKey.toUpperCase()],
  run: (ctx) => ctx.navigate(a.path),
}))
