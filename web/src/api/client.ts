// Thin fetch wrapper for /api/v1. Every feature uses these helpers; do not
// call fetch() on API paths directly.
import type { ErrorBody } from './types'

export const API = '/api/v1'

export class ApiError extends Error {
  status: number
  code: string
  field?: string
  retryIn?: number
  constructor(status: number, code: string, message: string, field?: string, retryIn?: number) {
    super(message)
    this.status = status
    this.code = code
    this.field = field
    this.retryIn = retryIn
  }
}

type Listener = () => void
const unauthorizedListeners = new Set<Listener>()
/** Called whenever an API call returns 401 (session expired / revoked). */
export function onUnauthorized(fn: Listener): () => void {
  unauthorizedListeners.add(fn)
  return () => unauthorizedListeners.delete(fn)
}

/** Mock/test teardown; a devtools fixture reset reloads the app afterwards. */
export function resetUnauthorizedListeners(): void {
  unauthorizedListeners.clear()
}

async function request<T>(method: string, path: string, body?: unknown, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  let payload: BodyInit | undefined
  if (body instanceof Blob || body instanceof ArrayBuffer || body instanceof FormData) {
    payload = body as BodyInit
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  const res = await fetch(path.startsWith('/') ? path : `${API}/${path}`, {
    method,
    body: payload,
    credentials: 'same-origin',
    ...init,
    headers: { ...headers, ...(init?.headers as Record<string, string> | undefined) },
  })
  if (res.status === 401) for (const fn of unauthorizedListeners) fn()
  if (res.status === 204) return undefined as T
  const ct = res.headers.get('Content-Type') || ''
  if (!res.ok) {
    if (ct.includes('application/json')) {
      const e = (await res.json()) as ErrorBody
      throw new ApiError(
        res.status,
        e.error?.code ?? 'error',
        e.error?.message ?? res.statusText,
        e.error?.field,
        e.error?.retryIn,
      )
    }
    throw new ApiError(res.status, 'error', (await res.text()) || res.statusText)
  }
  if (ct.includes('application/json')) return (await res.json()) as T
  return (await res.text()) as unknown as T
}

/** Paths are relative to /api/v1 unless they start with "/". */
export const api = {
  get: <T>(path: string, init?: RequestInit) => request<T>('GET', path, undefined, init),
  post: <T>(path: string, body?: unknown, init?: RequestInit) => request<T>('POST', path, body ?? {}, init),
  put: <T>(path: string, body?: unknown, init?: RequestInit) => request<T>('PUT', path, body, init),
  patch: <T>(path: string, body?: unknown, init?: RequestInit) => request<T>('PATCH', path, body, init),
  del: <T = void>(path: string, init?: RequestInit) => request<T>('DELETE', path, undefined, init),
}

/** Build a query string, skipping undefined/empty values. */
export function qs(params: Record<string, string | number | boolean | undefined | null>): string {
  const u = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '' || v === false) continue
    u.set(k, String(v === true ? 1 : v))
  }
  const s = u.toString()
  return s ? `?${s}` : ''
}

/** Absolute ws(s):// URL for an API path, e.g. wsUrl('terminals/t_1/attach'). */
export function wsUrl(path: string): string {
  const p = path.startsWith('/') ? path : `${API}/${path}`
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}${p}`
}

/** encodeURIComponent for path segments (ids may contain ':'). */
export const seg = (s: string) => encodeURIComponent(s)
