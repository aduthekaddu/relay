// Command center registry. Areas contribute commands from their
// routes/<area>/commands.ts (kept tiny: no heavy imports, lazy views).
// The palette UI (command/Palette.tsx) reads this registry.
import type { ComponentType } from 'preact'
import type { AreaId } from '../app/areas'

export interface CommandContext {
  /** Navigate within the app (client-side). */
  navigate: (path: string) => void
  /** Close the palette. */
  close: () => void
  /** Push a sub-view (Raycast "push"). */
  push: (view: PaletteView) => void
  /** Show a toast. */
  toast: (message: string, kind?: 'info' | 'success' | 'warning' | 'danger') => void
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

export interface PaletteView {
  id: string
  title: string
  /** Placeholder for the search field while this view is on top. */
  placeholder?: string
  component: () => Promise<{ default: ComponentType<PaletteViewProps> }>
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
  /** Higher = earlier in results for equal fuzzy score. */
  priority?: number
}

const registry = new Map<string, Command>()
const listeners = new Set<() => void>()

export function registerCommands(cmds: Command[]): void {
  for (const c of cmds) registry.set(c.id, c)
  for (const l of listeners) l()
}

export function unregisterCommand(id: string): void {
  registry.delete(id)
  for (const l of listeners) l()
}

export function allCommands(): Command[] {
  return [...registry.values()].filter((c) => !c.when || c.when())
}

export function onCommandsChanged(fn: () => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

/** Run a command by id (used by keyboard shortcuts and deep links). */
export function commandById(id: string): Command | undefined {
  return registry.get(id)
}
