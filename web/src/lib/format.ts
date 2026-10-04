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

const zeroTime = Date.parse('0001-01-01T00:00:00Z')
const timestamp = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})$/

/** Validate API time without rewriting its offset or fractional precision. */
export function normalizeTimestamp(iso: string | null | undefined): string | undefined {
  if (!iso) return undefined
  const match = timestamp.exec(iso)
  if (!match) return undefined
  const [, year, month, day, hour, minute, second, fraction, zone] = match
  const y = Number(year)
  const m = Number(month)
  const leap = y % 4 === 0 && (y % 100 !== 0 || y % 400 === 0)
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
  if (
    m < 1 ||
    m > 12 ||
    Number(day) < 1 ||
    Number(day) > days[m - 1] ||
    Number(hour) > 23 ||
    Number(minute) > 59 ||
    Number(second) > 59 ||
    (zone !== 'Z' && (Number(zone.slice(1, 3)) > 23 || Number(zone.slice(4)) > 59))
  )
    return undefined
  const t = Date.parse(iso)
  if (!Number.isFinite(t) || (t === zeroTime && !/[1-9]/.test(fraction ?? ''))) return undefined
  return iso
}

/** API time → "now", "5m ago", "3h ago", "Yesterday", "12 Mar"; unknown → "—". */
export function ago(iso: string | null | undefined, now: number = Date.now()): string {
  const valid = normalizeTimestamp(iso)
  if (!valid || !Number.isFinite(now) || Number.isNaN(new Date(now).getTime())) return '—'
  const t = Date.parse(valid)
  // Date has millisecond resolution. Keep future sub-millisecond fractions unknown too.
  const fraction = timestamp.exec(valid)?.[7] ?? ''
  if (t > now || (t === now && /[1-9]/.test(fraction.slice(3)))) return '—'
  const diff = (now - t) / 1000
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
