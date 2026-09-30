import { useEffect, useState } from 'preact/hooks'
import { cx } from '../lib/util'
import { Skeleton } from './layout'
import './misc.css'

export interface QRProps {
  /** Text/URL to encode. */
  value: string
  /** Rendered size in px. Default 176. */
  size?: number
  /** Accessible description, e.g. "QR code for http://…". */
  label?: string
  class?: string
}

interface Matrix {
  n: number
  dark: (r: number, c: number) => boolean
}

type QrFactory = (type: number, level: 'L' | 'M' | 'Q' | 'H') => {
  addData(s: string): void
  make(): void
  getModuleCount(): number
  isDark(r: number, c: number): boolean
}

let lib: Promise<QrFactory> | null = null
function loadLib(): Promise<QrFactory> {
  lib ??= import('qrcode-generator').then((m) => ((m as unknown as { default?: QrFactory }).default ?? (m as unknown as QrFactory)))
  return lib
}

/** Is (r,c) inside one of the three 7×7 finder patterns? */
function inFinder(r: number, c: number, n: number): boolean {
  return (r < 7 && c < 7) || (r < 7 && c >= n - 7) || (r >= n - 7 && c < 7)
}

/**
 * A QR code drawn in Relay's dot language: data modules are dots, finder
 * patterns are rounded squares (so phone cameras still lock on quickly).
 * qrcode-generator is loaded lazily on first use.
 */
export function QR({ value, size = 176, label, class: className }: QRProps) {
  const [m, setM] = useState<Matrix | null>(null)
  useEffect(() => {
    let live = true
    loadLib().then((qrcode) => {
      if (!live) return
      const q = qrcode(0, 'M')
      q.addData(value)
      q.make()
      setM({ n: q.getModuleCount(), dark: (r, c) => q.isDark(r, c) })
    })
    return () => {
      live = false
    }
  }, [value])
  if (!m) return <Skeleton width={size} height={size} radius="12px" class={className} />
  const quiet = 2
  const dim = m.n + quiet * 2
  const dots = []
  for (let r = 0; r < m.n; r++)
    for (let c = 0; c < m.n; c++)
      if (m.dark(r, c) && !inFinder(r, c, m.n)) dots.push(<circle key={`${r}.${c}`} cx={c + quiet + 0.5} cy={r + quiet + 0.5} r={0.46} />)
  const finder = (x: number, y: number) => (
    <g key={`f${x}.${y}`}>
      <rect x={x + quiet + 0.5} y={y + quiet + 0.5} width={6} height={6} rx={1.6} fill="none" stroke="currentColor" stroke-width={1} />
      <rect x={x + quiet + 2} y={y + quiet + 2} width={3} height={3} rx={0.8} />
    </g>
  )
  return (
    <svg
      class={cx('qr', className)}
      viewBox={`0 0 ${dim} ${dim}`}
      width={size}
      height={size}
      role="img"
      aria-label={label ?? `QR code for ${value}`}
    >
      <rect width={dim} height={dim} rx={2} class="qr__bg" />
      <g class="qr__fg">
        {dots}
        {finder(0, 0)}
        {finder(m.n - 7, 0)}
        {finder(0, m.n - 7)}
      </g>
    </svg>
  )
}
