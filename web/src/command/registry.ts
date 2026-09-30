// Command center registry. Areas contribute commands from their
// routes/<area>/commands.ts (kept tiny: no heavy imports, lazy views);
// extensions contribute live results through providers. The palette UI
// (command/Palette.tsx, lazy) reads both. See docs/dev/UI_KIT.md →
// "Command center".
import type { ComponentType } from 'preact'
import type { AreaId } from '../app/areas'
import type { Status } from '../ui/StatusDot'

export interface CommandContext {
  /** Navigate within the app (client-side). */
  navigate: (path: string) => void
  /** Close the palette. */
  close: () => void
  /** Push a sub-view (Raycast "push"). */
  push: (view: PaletteView) => void
  /** Pop the top sub-view (no-op at the root). */
  pop: () => void
  /** Show a toast. */
  toast: (message: string, kind?: 'info' | 'success' | 'warning' | 'danger') => void
  /** Copy text to the clipboard and confirm with a toast. */
  copy: (text: string, what?: string) => Promise<void>
  /** Argument values collected from inline inputs, by arg name. */
  args: Record<string, string>
  /** Current query text in the search field. */
  query: string
}

export interface CommandArg {
  name: string
  placeholder: string
  optional?: boolean
}

export interface PaletteViewProps {
  ctx: CommandContext
  /** Current query in the palette search field (views can filter by it). */
  query: string
  /** Pop back to the previous view. */
  pop: () => void
}

/**
 * A sub-view pushed on the palette stack. Either a list (`items`, rendered
 * and keyboard-driven by the palette itself, fuzzy-filtered by the query
 * unless `filter: false`) or a custom `component` (lazy) that may render
 * <PaletteList> for keyboard-navigable rows.
 */
export interface PaletteView {
  id: string
  title: string
  /** Placeholder for the search field while this view is on top. */
  placeholder?: string
  items?: (query: string, signal: AbortSignal) => PaletteItem[] | Promise<PaletteItem[]>
  /** Fuzzy-filter `items` by the query (default true). */
  filter?: boolean
  component?: () => Promise<{ default: ComponentType<PaletteViewProps> }>
}

/** An extra action on an item, shown in the action panel (⌘K / →). */
export interface PaletteAction {
  id: string
  title: string
  icon?: string
  /** Display-only hint, e.g. ['mod', 'C']. */
  shortcut?: string[]
  danger?: boolean
  run: (ctx: CommandContext) => void | Promise<void>
}

/** One row in the palette (commands, search results, provider items). */
export interface PaletteItem {
  /** Unique across the palette; also the recents key. */
  id: string
  title: string
  subtitle?: string
  /** Icon string, see ui/Icon ("glyph:terminal", "agent:claude", "folder"). */
  icon?: string
  /** Live status dot instead of / next to the icon. */
  status?: Status
  section?: string
  /** Right-aligned instrument text (time, path, count). */
  accessory?: string
  shortcut?: string[]
  keywords?: string[]
  args?: CommandArg[]
  /** Primary action (Enter). */
  run?: (ctx: CommandContext) => void | Promise<void>
  /** Primary label shown in the footer, default "Open" / "Run". */
  runLabel?: string
  /** Enter pushes this view instead of running. */
  view?: PaletteView
  /** Extra actions; the first one is the ⌘Enter secondary action. */
  actions?: PaletteAction[]
  /** In-app link: default primary action when `run` is absent. */
  link?: string
  /** Remember in "Recent" when run (default true). */
  remember?: boolean
  /** Ranking bonus (higher = earlier for equal fuzzy score). */
  priority?: number
  /** Provider-supplied relevance 0..1 (server results). */
  score?: number
}

export interface Command {
  /** Stable id, "<area>.<verb>", e.g. "terminal.new". */
  id: string
  title: string
  subtitle?: string
  /** Section heading in the root list, e.g. "Terminal". */
  section: string
  area?: AreaId
  keywords?: string[]
  /** lucide icon name (kebab-case), agent id ("agent:claude") or "glyph:<area>". */
  icon?: string
  /** Display-only shortcut hint, e.g. ['mod', 'shift', 'T']. */
  shortcut?: string[]
  /** Hidden unless this returns true. */
  when?: () => boolean
  /** Inline arguments (Raycast-style) shown after the title. */
  args?: CommandArg[]
  run?: (ctx: CommandContext) => void | Promise<void>
  /** Opening this command pushes a sub-view instead of running. */
  view?: PaletteView
  /** Extra actions for the action panel; the first is ⌘Enter. */
  actions?: PaletteAction[]
  /** Higher = earlier in results for equal fuzzy score. */
  priority?: number
  /** Show in the empty-query "Suggestions" section. */
  suggested?: boolean
}

/**
 * A live source of palette items (extensions: calculator, clipboard,
 * snippets, server search…). `search` runs on every query change after
 * `debounce` ms; the previous call's signal is aborted.
 */
export interface PaletteProvider {
  id: string
  /** Default section for returned items. */
  section: string
  /** Only query when the trimmed query has at least this many chars (default 1; 0 = also when empty). */
  minQuery?: number
  /** ms (default 80). 0 runs synchronously for instant providers such as a calculator. */
  debounce?: number
  /** Items are ranked by the palette (true) or kept in provider order (false, default). */
  fuzzy?: boolean
  /** Place this provider's section above commands (e.g. calculator answers). */
  top?: boolean
  search: (query: string, signal: AbortSignal) => PaletteItem[] | Promise<PaletteItem[]>
}

const registry = new Map<string, Command>()
const providers = new Map<string, PaletteProvider>()
const listeners = new Set<() => void>()
const emit = () => {
  for (const l of listeners) l()
}

export function registerCommands(cmds: Command[]): void {
  for (const c of cmds) registry.set(c.id, c)
  emit()
}

export function unregisterCommand(id: string): void {
  registry.delete(id)
  emit()
}

export function allCommands(): Command[] {
  return [...registry.values()].filter((c) => !c.when || c.when())
}

/** Register a live item provider; returns an unregister function. */
export function registerProvider(p: PaletteProvider): () => void {
  providers.set(p.id, p)
  emit()
  return () => {
    if (providers.get(p.id) === p) providers.delete(p.id)
    emit()
  }
}

export function allProviders(): PaletteProvider[] {
  return [...providers.values()]
}

export function onCommandsChanged(fn: () => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

/** Run a command by id (used by keyboard shortcuts and deep links). */
export function commandById(id: string): Command | undefined {
  return registry.get(id)
}

/** The palette row for a command. */
export function commandItem(c: Command): PaletteItem {
  return {
    id: `cmd:${c.id}`,
    title: c.title,
    subtitle: c.subtitle,
    icon: c.icon,
    section: c.section,
    shortcut: c.shortcut,
    keywords: c.keywords,
    args: c.args,
    run: c.run,
    view: c.view,
    actions: c.actions,
    priority: c.priority,
    runLabel: c.view ? 'Open' : 'Run',
  }
}
