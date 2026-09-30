// The navigation model. Every screen in Relay belongs to exactly one area.
// Areas have a plain-language name, a stable path, their own hue (used for
// the glyph, the active rail marker and the header accent line only) and a
// dot-matrix glyph (see ui/Glyph.tsx). Keep this list short and literal.

export type AreaId =
  | 'home'
  | 'terminal'
  | 'agents'
  | 'files'
  | 'code'
  | 'desktop'
  | 'previews'
  | 'system'
  | 'settings'

export interface Area {
  id: AreaId
  label: string
  path: string
  /** CSS custom property holding the area hue, e.g. var(--area-terminal) */
  hue: string
  /** One short sentence, shown in the command center and empty states. */
  blurb: string
  /** Shown in the mobile tab bar (max 4 + More). */
  mobileTab: boolean
  /** Keyboard chord after pressing G (go-to), e.g. "t" for G then T. */
  goKey: string
}

export const AREAS: Area[] = [
  {
    id: 'home',
    label: 'Home',
    path: '/',
    hue: 'var(--area-home)',
    blurb: 'What needs you, what is running, where you left off.',
    mobileTab: true,
    goKey: 'h',
  },
  {
    id: 'terminal',
    label: 'Terminal',
    path: '/terminal',
    hue: 'var(--area-terminal)',
    blurb: 'Persistent shells and agent sessions that survive disconnects.',
    mobileTab: true,
    goKey: 't',
  },
  {
    id: 'agents',
    label: 'Agents',
    path: '/agents',
    hue: 'var(--area-agents)',
    blurb: 'Every coding agent session, live and past, searchable.',
    mobileTab: true,
    goKey: 'a',
  },
  {
    id: 'files',
    label: 'Files',
    path: '/files',
    hue: 'var(--area-files)',
    blurb: 'Browse, preview, edit, upload and download.',
    mobileTab: true,
    goKey: 'f',
  },
  {
    id: 'code',
    label: 'Code',
    path: '/code',
    hue: 'var(--area-code)',
    blurb: 'VS Code in the browser, on this machine.',
    mobileTab: false,
    goKey: 'c',
  },
  {
    id: 'desktop',
    label: 'Desktop',
    path: '/desktop',
    hue: 'var(--area-desktop)',
    blurb: 'A real desktop with Chrome and Blender, for you and your agents.',
    mobileTab: false,
    goKey: 'd',
  },
  {
    id: 'previews',
    label: 'Previews',
    path: '/previews',
    hue: 'var(--area-previews)',
    blurb: 'Dev servers on this machine, open on any device.',
    mobileTab: false,
    goKey: 'p',
  },
  {
    id: 'system',
    label: 'System',
    path: '/system',
    hue: 'var(--area-system)',
    blurb: 'CPU, memory, disks, processes, services and logs.',
    mobileTab: false,
    goKey: 's',
  },
  {
    id: 'settings',
    label: 'Settings',
    path: '/settings',
    hue: 'var(--area-settings)',
    blurb: 'Security, devices, notifications, tools and appearance.',
    mobileTab: false,
    goKey: ',',
  },
]

export const areaById = (id: AreaId): Area => AREAS.find((a) => a.id === id)!

/** Resolve the area for a pathname (longest prefix wins). */
export function areaForPath(pathname: string): Area {
  let best = AREAS[0]
  for (const a of AREAS) {
    if (a.path === '/') continue
    if (pathname === a.path || pathname.startsWith(`${a.path}/`)) {
      if (a.path.length > (best.path === '/' ? 0 : best.path.length)) best = a
    }
  }
  if (pathname.startsWith('/workspace') || pathname.startsWith('/toolbox')) {
    return pathname.startsWith('/toolbox') ? areaById('settings') : areaById('home')
  }
  return best
}
