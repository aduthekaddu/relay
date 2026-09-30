import { clamp, cx } from '../lib/util'
import './signature.css'

export interface DotMeterProps {
  /** Fraction 0–1. */
  value: number
  /** Number of dots. Default 10. */
  dots?: number
  /** Fraction at which lit dots turn warn / danger. Defaults 0.75 / 0.9. */
  warn?: number
  danger?: number
  /** Colour for normal lit dots (default --text). Use an area hue inside areas. */
  color?: string
  /** Accessible name, e.g. "CPU". */
  label: string
  /** Visible value text after the dots (e.g. "42%"). */
  valueText?: string
  size?: 'sm' | 'md'
  class?: string
}

/** A row of dots as a meter — CPU, memory, disk at a glance. */
export function DotMeter({
  value,
  dots = 10,
  warn = 0.75,
  danger = 0.9,
  color,
  label,
  valueText,
  size = 'md',
  class: className,
}: DotMeterProps) {
  const v = clamp(Number.isFinite(value) ? value : 0, 0, 1)
  const lit = Math.round(v * dots)
  const tone = v >= danger ? 'danger' : v >= warn ? 'warn' : 'ok'
  return (
    <span
      class={cx('dot-meter', `dot-meter--${size}`, `dot-meter--${tone}`, className)}
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(v * 100)}
      aria-valuetext={valueText}
      style={color ? { '--meter': color } : undefined}
    >
      <span class="dot-meter__dots" aria-hidden="true">
        {Array.from({ length: dots }, (_, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: fixed-length decorative row
          <i key={i} class={i < lit ? 'on' : undefined} />
        ))}
      </span>
      {valueText && <span class="dot-meter__value">{valueText}</span>}
    </span>
  )
}
