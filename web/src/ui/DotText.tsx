import { useMemo } from 'preact/hooks'
import { cx } from '../lib/util'
import { dotLayout } from './dotfont'
import './signature.css'

export interface DotTextProps {
  /** Text to draw (A–Z, 0–9 and common punctuation; lowercase → uppercase). */
  text: string
  /** Dot pitch in px. Default 6 → a 7-row line is 42 px tall. */
  pitch?: number
  /** Lit dot colour. Default `currentColor`. */
  color?: string
  /** Draw the unlit grid faintly (default true). */
  grid?: boolean
  /** Dots switch on in a quick left→right scramble when mounted. */
  reveal?: boolean
  class?: string
}

/**
 * Departure-board lettering: a 5×7 dot font rendered as one SVG. The text
 * is exposed to assistive tech as the element's label.
 */
export function DotText({ text, pitch = 6, color, grid = true, reveal = false, class: className }: DotTextProps) {
  const layout = useMemo(() => dotLayout(text), [text])
  const w = layout.cols
  // Deterministic pseudo-random delays so the reveal looks scrambled but
  // renders the same every time (no Math.random in render).
  const delay = (x: number, y: number) => ((x * 37 + y * 101) % 17) * 18 + x * 14
  return (
    <svg
      class={cx('dot-text', reveal && 'dot-text--reveal', className)}
      viewBox={`0 0 ${w} 7`}
      width={w * pitch}
      height={7 * pitch}
      role="img"
      aria-label={text}
      style={color ? { color } : undefined}
    >
      {grid &&
        layout.off.map(([x, y]) => <circle key={`o${x}.${y}`} class="glyph__off" cx={x + 0.5} cy={y + 0.5} r={0.36} />)}
      {layout.on.map(([x, y]) => (
        <circle
          key={`n${x}.${y}`}
          class="glyph__on"
          cx={x + 0.5}
          cy={y + 0.5}
          r={0.42}
          style={reveal ? { animationDelay: `${delay(x, y)}ms` } : undefined}
        />
      ))}
    </svg>
  )
}
