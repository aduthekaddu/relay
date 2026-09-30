import type { JSX } from 'preact'
import { cx } from '../lib/util'
import { type GlyphName, glyphDots } from './glyphs'
import './signature.css'

export interface GlyphProps {
  name: GlyphName
  /** Rendered size in px (square). Default 20. */
  size?: number
  /**
   * idle: static; active: dots light top→bottom (40 ms per row) once on
   * mount/when switched to active; pulse: the lit dots breathe (working).
   */
  state?: 'idle' | 'active' | 'pulse'
  /** Colour of lit dots. Default `currentColor`. */
  color?: string
  /** Accessible label; omit for decorative glyphs (aria-hidden). */
  label?: string
  class?: string
  style?: JSX.CSSProperties
}

/** A 7×7 dot-matrix glyph (area identity and a few utility marks). */
export function Glyph({ name, size = 20, state = 'idle', color, label, class: className, style }: GlyphProps) {
  const { on, off } = glyphDots(name)
  return (
    <svg
      class={cx('glyph', `glyph--${state}`, className)}
      viewBox="0 0 7 7"
      width={size}
      height={size}
      role={label ? 'img' : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      style={{ ...(color ? { color } : null), ...style }}
    >
      {off.map(([x, y]) => (
        <circle key={`o${x}${y}`} class="glyph__off" cx={x + 0.5} cy={y + 0.5} r={0.4} />
      ))}
      {on.map(([x, y]) => (
        <circle
          key={`n${x}${y}`}
          class="glyph__on"
          cx={x + 0.5}
          cy={y + 0.5}
          r={0.405}
          style={state === 'active' ? { animationDelay: `${y * 40}ms` } : undefined}
        />
      ))}
    </svg>
  )
}

export interface RelayMarkProps {
  size?: number
  /** Blink the beacon (use for "needs you" in the logo slot). */
  beacon?: boolean
  class?: string
  label?: string
}

/** The Relay mark: a dot-matrix R with the orange signal beacon. */
export function RelayMark({ size = 28, beacon = false, class: className, label = 'Relay' }: RelayMarkProps) {
  const { on, off } = glyphDots('relay')
  return (
    <svg
      class={cx('glyph relay-mark', beacon && 'relay-mark--beacon', className)}
      viewBox="0 0 7 7"
      width={size}
      height={size}
      role="img"
      aria-label={label}
    >
      {off.map(([x, y]) => (
        <circle key={`o${x}${y}`} class="glyph__off" cx={x + 0.5} cy={y + 0.5} r={0.4} />
      ))}
      {on.map(([x, y]) =>
        x === 4 && y === 1 ? (
          <circle key="beacon" class="relay-mark__beacon" cx={x + 0.5} cy={y + 0.5} r={0.5} />
        ) : (
          <circle key={`n${x}${y}`} class="glyph__on" cx={x + 0.5} cy={y + 0.5} r={0.41} />
        ),
      )}
    </svg>
  )
}
