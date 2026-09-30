/** Prefix a site-relative path with the configured base (`/relay/`). */
export function href(path = ''): string {
  const base = import.meta.env.BASE_URL.endsWith('/') ? import.meta.env.BASE_URL : `${import.meta.env.BASE_URL}/`
  return `${base}${path.replace(/^\//, '')}`
}

/** True when `current` (a pathname) is at or below `path` (site-relative). */
export function isCurrent(current: string, path: string): boolean {
  const target = href(path)
  const a = current.endsWith('/') ? current : `${current}/`
  return a === target || (path !== '' && a.startsWith(target))
}
