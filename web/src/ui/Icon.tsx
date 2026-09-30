// Icon: one component for every icon string used in Relay.
//
//   "glyph:<name>"  → dot-matrix Glyph (area identity)
//   "agent:<id>"    → AgentMark
//   "<lucide-name>" → a lucide-preact icon registered with registerIcons()
//
// lucide icons are registered explicitly so the bundle only contains the
// ones in use: `registerIcons({ 'git-branch': GitBranch })` from the module
// that needs them (usually an area's commands.ts).

import {
  ArrowRight,
  Bell,
  BellOff,
  Check,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  Clock,
  Command as CommandIcon,
  Copy,
  CornerDownLeft,
  Download,
  Ellipsis,
  ExternalLink,
  Eye,
  EyeOff,
  File,
  Folder,
  Info,
  Keyboard,
  KeyRound,
  LogOut,
  Monitor,
  Moon,
  Plus,
  RefreshCw,
  Search,
  Settings,
  ShieldCheck,
  Sun,
  SunMoon,
  Terminal as TerminalIcon,
  Trash2,
  TriangleAlert,
  Upload,
  WifiOff,
  X,
} from 'lucide-preact'
import type { ComponentType } from 'preact'
import { cx } from '../lib/util'
import { AgentMark } from './AgentMark'
import { Glyph } from './Glyph'
import { isGlyph } from './glyphs'

interface LucideProps {
  size?: number | string
  strokeWidth?: number | string
  class?: string
  'aria-hidden'?: boolean
}
export type IconComponent = ComponentType<LucideProps>

const registry = new Map<string, IconComponent>(
  Object.entries({
    'arrow-right': ArrowRight,
    bell: Bell,
    'bell-off': BellOff,
    check: Check,
    'chevron-left': ChevronLeft,
    'chevron-right': ChevronRight,
    'circle-alert': CircleAlert,
    clock: Clock,
    command: CommandIcon,
    copy: Copy,
    'corner-down-left': CornerDownLeft,
    download: Download,
    ellipsis: Ellipsis,
    'external-link': ExternalLink,
    eye: Eye,
    'eye-off': EyeOff,
    file: File,
    folder: Folder,
    info: Info,
    keyboard: Keyboard,
    'key-round': KeyRound,
    'log-out': LogOut,
    monitor: Monitor,
    moon: Moon,
    plus: Plus,
    'refresh-cw': RefreshCw,
    search: Search,
    settings: Settings,
    'shield-check': ShieldCheck,
    sun: Sun,
    'sun-moon': SunMoon,
    terminal: TerminalIcon,
    'trash-2': Trash2,
    'triangle-alert': TriangleAlert,
    upload: Upload,
    'wifi-off': WifiOff,
    x: X,
  }) as Array<[string, IconComponent]>,
)

/** Make lucide icons available by kebab-case name (e.g. "git-branch"). */
export function registerIcons(icons: Record<string, IconComponent>): void {
  for (const [k, v] of Object.entries(icons)) registry.set(k, v)
}

export interface IconProps {
  /** "glyph:<area>", "agent:<id>" or a registered lucide name. */
  name: string
  /** px; lucide default 16, glyphs 16, agent marks sm. */
  size?: number
  /** Accessible label; icons are decorative (aria-hidden) without one. */
  label?: string
  class?: string
}

/** Render any Relay icon string. Unknown names render a neutral dot. */
export function Icon({ name, size = 16, label, class: className }: IconProps) {
  if (name.startsWith('glyph:')) {
    const g = name.slice(6)
    if (isGlyph(g)) return <Glyph name={g} size={size} label={label} class={className} />
  }
  if (name.startsWith('agent:')) return <AgentMark agent={name.slice(6)} size="sm" class={className} />
  const C = registry.get(name)
  const cls = cx('icon', className)
  if (!C)
    return (
      <span class={cx(cls, 'icon--missing')} style={{ width: size, height: size }} aria-hidden="true">
        <i />
      </span>
    )
  return (
    <span class={cls} {...(label ? { role: 'img', 'aria-label': label } : { 'aria-hidden': true })}>
      <C size={size} strokeWidth={1.75} aria-hidden />
    </span>
  )
}
