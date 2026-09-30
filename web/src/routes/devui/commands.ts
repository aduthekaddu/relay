// Command center entries for the /dev/ui kitchen sink.
import type { Command } from '../../command/registry'

export const commands: Command[] = [
  {
    id: 'dev.ui',
    title: 'UI kit',
    subtitle: 'Every component, state and theme (/dev/ui)',
    section: 'Developer',
    icon: 'glyph:spark',
    keywords: ['kitchen sink', 'components', 'design system', 'storybook', 'dev'],
    run: (ctx) => ctx.navigate('/dev/ui'),
  },
]
