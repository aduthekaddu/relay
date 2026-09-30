/**
 * Named LED-field targets. Sections reference them with
 * `data-field="<name>"`; the director morphs the field when a section
 * becomes current. Targets are functions of the viewport so the mobile
 * composition can differ from desktop.
 */
import desk from '../assets/img/desk.field.webp?url'
import meta from '../assets/img/meta.json'
import rack from '../assets/img/rack.field.webp?url'
import road from '../assets/img/road.field.webp?url'
import tower from '../assets/img/tower.field.webp?url'
import train from '../assets/img/train.field.webp?url'
import windows from '../assets/img/windows.field.webp?url'
import type { Target } from './field-targets'

type Meta = Record<string, { beacon: [number, number] | null }>
const m = meta as unknown as Meta

/** Images whose single warm light is a point source that should blink as the beacon. */
const POINT_LIGHTS = new Set(['tower', 'rack', 'windows'])

const image = (src: string, name: string, gain: number, focus: [number, number]) =>
  ({
    kind: 'image',
    src,
    gain,
    focus,
    beacon: POINT_LIGHTS.has(name) ? (m[name]?.beacon ?? null) : null,
  }) as const

/** Viewport facts a target may depend on. */
export interface View {
  mobile: boolean
}

export const TARGETS: Record<string, (v: View) => Target> = {
  boot: (v) => ({
    layers: [{ kind: 'dotfont', text: 'RELAY', h: v.mobile ? 0.16 : 0.34, w: 0.82, y: 0.47 }],
    ambient: 0,
  }),
  hero: (v) => ({
    layers: [image(tower, 'tower', 1, v.mobile ? [0.78, 0.4] : [0.72, 0.42])],
    ambient: 0.75,
  }),
  'screens-phone': (v) => ({
    layers: [
      {
        kind: 'device',
        device: 'phone',
        x: v.mobile ? 0.5 : 0.74,
        y: v.mobile ? 0.76 : 0.52,
        h: v.mobile ? 0.34 : 0.6,
      },
    ],
    ambient: 0.22,
  }),
  'screens-tablet': (v) => ({
    layers: [
      {
        kind: 'device',
        device: 'tablet',
        x: v.mobile ? 0.5 : 0.72,
        y: v.mobile ? 0.76 : 0.52,
        h: v.mobile ? 0.32 : 0.62,
      },
    ],
    ambient: 0.22,
  }),
  'screens-laptop': (v) => ({
    layers: [
      {
        kind: 'device',
        device: 'laptop',
        x: v.mobile ? 0.5 : 0.7,
        y: v.mobile ? 0.78 : 0.52,
        h: v.mobile ? 0.2 : 0.5,
      },
    ],
    ambient: 0.22,
  }),
  quiet: () => ({ layers: [], ambient: 0.28 }),
  dark: () => ({ layers: [], ambient: 0.05 }),
  tap: () => ({ layers: [], ambient: 0.08, beacon: [0.5, 0.2] }),
  command: (v) => ({
    layers: [
      {
        kind: 'text',
        text: '⌘K',
        h: v.mobile ? 0.26 : 0.72,
        x: v.mobile ? 0.62 : 0.7,
        y: v.mobile ? 0.8 : 0.52,
        gain: 0.2,
      },
    ],
    ambient: 0.18,
  }),
  running: (v) => ({
    layers: [image(desk, 'desk', 0.55, v.mobile ? [0.2, 0.4] : [0.3, 0.45])],
    ambient: 0.12,
  }),
  night: (v) => ({ layers: [image(road, 'road', 0.85, v.mobile ? [0.72, 0.6] : [0.6, 0.55])], ambient: 0.1 }),
  locked: (v) => ({ layers: [image(rack, 'rack', 0.55, v.mobile ? [0.8, 0.3] : [0.7, 0.4])], ambient: 0.08 }),
  light: () => ({ layers: [image(windows, 'windows', 0.42, [0.5, 0.5])], ambient: 0.05 }),
  // Bookend: the relay tower again, quieter, as the signal signs off.
  install: (v) => ({
    layers: [image(tower, 'tower', 0.5, v.mobile ? [0.85, 0.3] : [0.86, 0.36])],
    ambient: 0.3,
  }),
  // Product page openers.
  'page-terminal': (v) => ({
    layers: [image(train, 'train', 0.8, v.mobile ? [0.35, 0.5] : [0.4, 0.5])],
    ambient: 0.1,
  }),
  'page-agents': (v) => ({
    layers: [image(windows, 'windows', 0.6, v.mobile ? [0.5, 0.45] : [0.4, 0.46])],
    ambient: 0.08,
  }),
  'page-command': (v) => ({
    layers: [
      {
        kind: 'text',
        text: '⌘K',
        h: v.mobile ? 0.24 : 0.7,
        x: v.mobile ? 0.6 : 0.72,
        y: v.mobile ? 0.3 : 0.5,
        gain: 0.45,
      },
    ],
    ambient: 0.3,
  }),
  'page-desktop': (v) => ({
    layers: [image(desk, 'desk', 0.85, v.mobile ? [0.16, 0.4] : [0.2, 0.42])],
    ambient: 0.1,
  }),
  'page-security': (v) => ({
    layers: [image(rack, 'rack', 0.8, v.mobile ? [0.8, 0.28] : [0.72, 0.4])],
    ambient: 0.06,
  }),
  'page-install': (v) => ({
    layers: [image(road, 'road', 0.9, v.mobile ? [0.72, 0.6] : [0.62, 0.55])],
    ambient: 0.12,
  }),
  'no-signal': (v) => ({
    layers: [
      { kind: 'noise', density: 0.05, gain: 0.5 },
      { kind: 'dotfont', text: 'NO SIGNAL', h: v.mobile ? 0.07 : 0.2, w: 0.86, y: 0.42 },
    ],
    ambient: 0.1,
  }),
}

/** Resolve a target by name (unknown names fall back to `quiet`). */
export function target(name: string, v: View): Target {
  return (TARGETS[name] ?? TARGETS.quiet!)(v)
}
