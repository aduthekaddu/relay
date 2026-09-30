// Built-in commands owned by the command center itself: navigation,
// appearance, account and app-level actions, plus the server search
// provider. Extensions (clipboard, snippets, notes, Quick AI, calculator,
// scripts) register their own commands/providers — see UI_KIT.md.
import { AREAS } from '../app/areas'
import { shortcutsOpen } from '../app/shortcuts'
import { signOut } from '../state/auth'
import { setTheme, type ThemePref, themePref } from '../state/theme'
import { type Command, type PaletteView, registerProvider } from './registry'
import { serverSearch } from './search'

const THEMES: Array<{ id: ThemePref; title: string; icon: string; subtitle: string }> = [
  { id: 'carbon', title: 'Carbon', icon: 'moon', subtitle: 'Dark' },
  { id: 'paper', title: 'Paper', icon: 'sun', subtitle: 'Light' },
  { id: 'auto', title: 'Auto', icon: 'sun-moon', subtitle: 'Follow the system' },
]

const themeView: PaletteView = {
  id: 'theme',
  title: 'Theme',
  placeholder: 'Choose a theme…',
  items: () =>
    THEMES.map((t) => ({
      id: `theme:${t.id}`,
      title: t.title,
      subtitle: t.subtitle,
      icon: t.icon,
      accessory: themePref.value === t.id ? 'Current' : undefined,
      remember: false,
      runLabel: 'Use theme',
      run: (ctx) => {
        setTheme(t.id)
        ctx.close()
      },
    })),
}

const navigation: Command[] = AREAS.map((a) => ({
  id: `go.${a.id}`,
  title: a.label,
  subtitle: a.blurb,
  section: 'Go to',
  area: a.id,
  icon: `glyph:${a.id}`,
  keywords: [a.label.toLowerCase(), 'go', 'open', 'area'],
  shortcut: ['G', a.goKey.toUpperCase()],
  suggested: a.id !== 'settings',
  run: (ctx) => ctx.navigate(a.path),
}))

const app: Command[] = [
  {
    id: 'app.theme',
    title: 'Change theme',
    subtitle: 'Carbon, Paper or Auto',
    section: 'Appearance',
    icon: 'sun-moon',
    keywords: ['dark', 'light', 'appearance', 'mode', 'carbon', 'paper'],
    view: themeView,
  },
  ...THEMES.map<Command>((t) => ({
    id: `app.theme.${t.id}`,
    title: `Use ${t.title} theme`,
    subtitle: t.subtitle,
    section: 'Appearance',
    icon: t.icon,
    keywords: ['theme', t.subtitle.toLowerCase(), 'appearance'],
    when: () => themePref.value !== t.id,
    run: (ctx) => {
      setTheme(t.id)
      ctx.close()
    },
  })),
  {
    id: 'app.shortcuts',
    title: 'Keyboard shortcuts',
    section: 'Help',
    icon: 'keyboard',
    keywords: ['keys', 'hotkeys', 'help', 'bindings'],
    shortcut: ['?'],
    run: (ctx) => {
      ctx.close()
      shortcutsOpen.value = true
    },
  },
  {
    id: 'app.reload',
    title: 'Reload Relay',
    subtitle: 'Fetch the latest app from the machine',
    section: 'App',
    icon: 'refresh-cw',
    keywords: ['refresh', 'restart', 'update'],
    run: () => location.reload(),
  },
  {
    id: 'app.signout',
    title: 'Sign out',
    subtitle: 'End this session on this device',
    section: 'Account',
    icon: 'log-out',
    keywords: ['logout', 'log out', 'lock', 'exit'],
    run: (ctx) => {
      ctx.close()
      void signOut()
    },
  },
]

export const commands: Command[] = [...navigation, ...app]

let providersOn = false
/** Register the built-in providers (idempotent). */
export function registerCoreProviders(): void {
  if (providersOn) return
  providersOn = true
  registerProvider(serverSearch)
}
