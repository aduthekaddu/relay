// Formatting for the instrument layer: durations, relative times, bytes,
// percentages. Pure functions, unit tested.

const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

/** 1536 → "1.5 KB". Binary multiples, one decimal under 10. */
export function bytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '—'
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  const s = i === 0 ? String(Math.round(v)) : v < 10 ? v.toFixed(1) : String(Math.round(v))
  return `${s} ${units[i]}`
}

/** Seconds → "42s", "3m", "1h 12m", "2d 4h". */
export function duration(sec: number): string {
  if (!Number.isFinite(sec) || sec < 0) return '—'
  const s = Math.floor(sec)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return m % 60 ? `${h}h ${m % 60}m` : `${h}h`
  const d = Math.floor(h / 24)
  return h % 24 ? `${d}d ${h % 24}h` : `${d}d`
}

/** ISO time → "now", "5m ago", "3h ago", "Yesterday", "12 Mar". */
export function ago(iso: string | undefined, now: number = Date.now()): string {
  if (!iso) return '—'
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return '—'
  const diff = Math.max(0, (now - t) / 1000)
  if (diff < 45) return 'now'
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`
  const d = new Date(t)
  const n = new Date(now)
  const y = new Date(n.getFullYear(), n.getMonth(), n.getDate() - 1)
  if (d.getFullYear() === y.getFullYear() && d.getMonth() === y.getMonth() && d.getDate() === y.getDate())
    return 'Yesterday'
  if (diff < 7 * 86400) return d.toLocaleDateString(undefined, { weekday: 'short' })
  return d.toLocaleDateString(undefined, {
    day: 'numeric',
    month: 'short',
    year: d.getFullYear() === n.getFullYear() ? undefined : 'numeric',
  })
}

/** 0.4567 → "46%"; values are fractions unless `isPct`. */
export function pct(v: number, isPct = false): string {
  if (!Number.isFinite(v)) return '—'
  return `${Math.round(isPct ? v : v * 100)}%`
}

/** Compact counts: 1234 → "1.2k", 5_600_000 → "5.6M". */
export function compact(n: number): string {
  if (!Number.isFinite(n)) return '—'
  const a = Math.abs(n)
  if (a < 1000) return String(Math.round(n))
  if (a < 1e6) return `${(n / 1e3).toFixed(a < 1e4 ? 1 : 0)}k`
  if (a < 1e9) return `${(n / 1e6).toFixed(a < 1e7 ? 1 : 0)}M`
  return `${(n / 1e9).toFixed(1)}B`
}

/** Replace the home directory prefix with "~". */
export function tildify(path: string, home?: string): string {
  if (!home || !path) return path
  if (path === home) return '~'
  return path.startsWith(`${home}/`) ? `~${path.slice(home.length)}` : path
}
