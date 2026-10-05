// Synthetic in-browser backend (VITE_MOCK=1). No server/provider/device proof.
import { handle, registry, resetMocks } from './handlers'
import { abortable, abortError, linkSignals } from './lifecycle'
import { emit, liveNotification } from './sockets'
import { has, latency, type MockMode, modes, setModes, toResponse } from './util'

let uninstall: (() => void) | undefined

async function readBody(init: RequestInit | undefined, req: Request | null): Promise<unknown> {
  const b =
    init?.body ??
    (req && req.method !== 'GET' && req.method !== 'HEAD' ? await req.clone().blob() : undefined)
  if (b === undefined || b === null) return undefined
  if (typeof b === 'string') {
    try {
      return JSON.parse(b)
    } catch {
      return b
    }
  }
  if (b instanceof Blob) {
    if (b.type.includes('json')) return JSON.parse(await b.text())
    return b
  }
  return b
}

/** Idempotent installation; returned cleanup restores the exact original globals. */
export function installMocks(): () => void {
  if (uninstall) return uninstall
  const realFetch = window.fetch
  const RealWS = window.WebSocket
  const previousHelpers = Object.getOwnPropertyDescriptor(window, '__relayMock')

  window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const req = input instanceof Request ? input : null
    const url = new URL(req ? req.url : String(input), location.href)
    if (url.origin !== location.origin || !url.pathname.startsWith('/api/v1/'))
      return realFetch.call(window, input, init)
    const method = (init?.method ?? req?.method ?? 'GET').toUpperCase()
    const linked = linkSignals(registry.signal, init?.signal ?? req?.signal)
    try {
      if (linked.signal.aborted) throw abortError()
      const body = await abortable(readBody(init, req), linked.signal)
      if (linked.signal.aborted) throw abortError()
      if (has('down')) throw new TypeError('Failed to fetch')
      const result = await handle(method, url, body, linked.signal, latency())
      // HEAD has GET admission and headers, with no response body or stream timer.
      if (method === 'HEAD') {
        const response = toResponse({ ...result, stream: undefined })
        linked.dispose()
        return new Response(null, { status: response.status, headers: response.headers })
      }
      if (result.stream) return toResponse(result, linked.signal, linked.dispose)
      linked.dispose()
      return toResponse(result)
    } catch (error) {
      linked.dispose()
      throw error
    }
  }

  const Patched = function (this: unknown, url: string | URL, protocols?: string | string[]) {
    const u = new URL(String(url), location.href)
    if (u.protocol === 'http:') u.protocol = 'ws:'
    if (u.protocol === 'https:') u.protocol = 'wss:'
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    if (u.protocol === protocol && u.host === location.host && u.pathname.startsWith('/api/v1/'))
      return registry.socket(u, protocols)
    return new RealWS(url, protocols)
  } as unknown as typeof WebSocket
  Object.assign(Patched, { CONNECTING: 0, OPEN: 1, CLOSING: 2, CLOSED: 3 })
  Patched.prototype = RealWS.prototype
  window.WebSocket = Patched

  const helpers = {
    synthetic: true,
    modes: () => [...modes],
    mode: (...m: MockMode[]) => {
      setModes(m)
      location.reload()
    },
    feature: (owner: string) => registry.feature(owner),
    disconnect: (owner?: string) => registry.disconnect(owner),
    reset: (...m: MockMode[]) => {
      resetMocks(m)
      location.reload()
    },
    inventory: () => registry.inventory(),
    notify: liveNotification,
    emit,
  }
  Object.defineProperty(window, '__relayMock', { configurable: true, value: helpers })
  uninstall = () => {
    resetMocks()
    window.fetch = realFetch
    window.WebSocket = RealWS
    if (previousHelpers) Object.defineProperty(window, '__relayMock', previousHelpers)
    else Reflect.deleteProperty(window, '__relayMock')
    uninstall = undefined
  }
  console.info('[relay] synthetic mock backend on — window.__relayMock for owner controls; no live evidence')
  return uninstall
}
