// URL helpers shared by the auth gate and the login screen.

/**
 * Sanitise a post-login redirect target. Only same-origin, absolute paths
 * are allowed; anything else (other origins, protocol-relative `//host`,
 * backslash tricks, javascript: URLs, the login page itself) becomes "/".
 */
export function safeNext(raw: string | null | undefined): string {
  if (!raw) return '/'
  let v: string
  try {
    v = decodeURIComponent(raw)
  } catch {
    return '/'
  }
  v = v.trim()
  if (!v.startsWith('/') || v.startsWith('//') || v.startsWith('/\\') || /[\u0000-\u001f\\]/.test(v))
    return '/'
  try {
    const u = new URL(v, 'https://relay.invalid')
    if (u.origin !== 'https://relay.invalid') return '/'
    if (u.pathname === '/login' || u.pathname.startsWith('/login/')) return '/'
    return u.pathname + u.search + u.hash
  } catch {
    return '/'
  }
}

/** The login URL that returns to `current` afterwards. */
export function loginUrl(current: string): string {
  const next = safeNext(current)
  return next === '/' ? '/login' : `/login?next=${encodeURIComponent(next)}`
}
