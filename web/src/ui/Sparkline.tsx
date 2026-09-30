import { useMemo } from 'preact/hooks'
import { cx } from '../lib/util'
import './signature.css'

export interface SparklineProps {
  values: number[]
  width?: number
  height?: number
  /** Fixed y-range; default: 0 … max(values). */
  min?: number
  max?: number
  /** Stroke colour (default currentColor). */
  color?: string
  /** Fill the area under the line with a soft tint. */
  fill?: boolean
  /** Mark the latest value with a dot. */
  dot?: boolean
  /** Accessible summary, e.g. "CPU over the last hour, now 42%". */
  label?: string
  class?: string
}

/** Compute the SVG path for a sparkline (exported for tests). */
export function sparkPath(values: number[], w: number, h: number, min?: number, max?: number): string {
  if (values.length === 0) return ''
  const lo = min ?? Math.min(0, ...values)
  const hi = max ?? Math.max(...values, lo + 1e-9)
  const span = hi - lo || 1
  const pad = 1.5
  const step = values.length > 1 ? (w - pad * 2) / (values.length - 1) : 0
  let d = ''
  values.forEach((v, i) => {
    const x = pad + i * step
    const y = pad + (h - pad * 2) * (1 - (Math.min(hi, Math.max(lo, v)) - lo) / span)
    d += `${i ? 'L' : 'M'}${x.toFixed(2)} ${y.toFixed(2)}`
  })
  return d
}

/** A tiny line chart for history (metrics, usage). */
export function Sparkline({
  values,
  width = 120,
  height = 32,
  min,
  max,
  color,
  fill = false,
  dot = true,
  label,
  class: className,
}: SparklineProps) {
  const d = useMemo(() => sparkPath(values, width, height, min, max), [values, width, height, min, max])
  const last = d ? d.slice(d.lastIndexOf('L') + 1 || 1).split(' ') : null
  return (
    <svg
      class={cx('sparkline', className)}
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      role={label ? 'img' : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      style={color ? { color } : undefined}
      preserveAspectRatio="none"
    >
      {fill && d && <path class="sparkline__fill" d={`${d}L${width - 1.5} ${height}L1.5 ${height}Z`} />}
      {d && <path class="sparkline__line" d={d} vector-effect="non-scaling-stroke" />}
      {dot && last && values.length > 0 && <circle class="sparkline__dot" cx={last[0]} cy={last[1]} r={2} />}
    </svg>
  )
}
