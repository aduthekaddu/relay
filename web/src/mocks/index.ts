// In-browser mock backend for UI work without a server (pnpm dev:mock).
//
//   VITE_MOCK=1 pnpm dev            → every /api/v1 call and socket is faked
//   ?mock=signed-out | setup | totp  → auth scenarios (login screen)
//   ?mock=empty                      → no terminals, sessions, notifications
//   ?mock=offline                    → the events socket never connects
//   ?mock=slow                       → 0.6–1.2 s latency (loading states)
//   ?mock=live                       → a notification every 20 s
//   ?mock=down                       → every API call fails (unreachable)
// Modes combine with commas and stick for the tab (sessionStorage);
// ?mock= (empty) resets. window.__relayMock exposes helpers in devtools.
import { handle } from './handlers'
import { DeadSocket, emit, FakeEvents, FakeLogs, FakeTerminal, liveNotification } from './sockets'
import { has, latency, type MockMode, modes, setModes, sleep, toResponse } from './util'

let installed = false

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

/** Patch fetch and WebSocket (idempotent). */
export function installMocks(): void {
  if (installed) return
  installed = true
  const realFetch = window.fetch.bind(window)
  const RealWS = window.WebSocket

  window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const req = input instanceof Request ? input : null
    const url = new URL(req ? req.url : String(input), location.href)
    if (url.origin !== location.origin || !url.pathname.startsWith('/api/v1/')) return realFetch(input, init)
    const method = (init?.method ?? req?.method ?? 'GET').toUpperCase()
    const signal = init?.signal ?? req?.signal
    await sleep(latency())
    if (signal?.aborted) throw new DOMException('The operation was aborted.', 'AbortError')
    if (has('down')) throw new TypeError('Failed to fetch')
    const res = await handle(method, url, await readBody(init, req))
    if (signal?.aborted) throw new DOMException('The operation was aborted.', 'AbortError')
    return toResponse(res)
  }

  const Patched = function (this: unknown, url: string | URL, protocols?: string | string[]) {
    const u = new URL(String(url), location.href)
    const p = u.pathname
    if (p === '/api/v1/events') return new FakeEvents(u.href)
    if (/^\/api\/v1\/terminals\/[^/]+\/attach$/.test(p)) return new FakeTerminal(u.href)
    if (p === '/api/v1/system/logs') return new FakeLogs(u.href)
    if (p.startsWith('/api/v1/')) return new DeadSocket(u.href)
    return new RealWS(url, protocols)
  } as unknown as typeof WebSocket
  Object.assign(Patched, { CONNECTING: 0, OPEN: 1, CLOSING: 2, CLOSED: 3 })
  Patched.prototype = RealWS.prototype
  window.WebSocket = Patched

  const api = {
    modes: () => [...modes],
    /** e.g. __relayMock.mode('signed-out'); reloads the page. */
    mode: (...m: MockMode[]) => {
      setModes(m)
      location.reload()
    },
    notify: liveNotification,
    emit,
  }
  ;(window as unknown as { __relayMock: typeof api }).__relayMock = api
  console.info(
    `[relay] mock backend on${modes.size ? ` (${[...modes].join(', ')})` : ''} — window.__relayMock for helpers`,
  )
}
