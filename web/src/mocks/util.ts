// Mock infrastructure: mode flags, responses, a seeded RNG and a fake
// WebSocket. Dev only (VITE_MOCK=1); never shipped in production builds.

/** Scenario switches, set with ?mock=<mode>[,<mode>] and kept for the tab. */
export type MockMode = 'signed-out' | 'setup' | 'totp' | 'offline' | 'empty' | 'slow' | 'live' | 'down'

const KEY = 'relay.mock'

function readModes(): Set<MockMode> {
  const q = new URLSearchParams(location.search).get('mock')
  if (q !== null) {
    sessionStorage.setItem(KEY, q)
  }
  const raw = q ?? sessionStorage.getItem(KEY) ?? ''
  return new Set(raw.split(',').map((s) => s.trim()).filter(Boolean) as MockMode[])
}

export const modes = readModes()
export const has = (m: MockMode) => modes.has(m)

/** Change modes at runtime (window.__relayMock.mode('empty')). */
export function setModes(list: MockMode[]): void {
  modes.clear()
  for (const m of list) modes.add(m)
  sessionStorage.setItem(KEY, list.join(','))
}

// ---------------------------------------------------------------- time

export const NOW = Date.now()
/** ISO timestamp `sec` seconds before page load (negative = future). */
export const before = (sec: number) => new Date(NOW - sec * 1000).toISOString()
export const MIN = 60
export const HOUR = 3600
export const DAY = 86400

// ---------------------------------------------------------------- rng

/** Deterministic PRNG (mulberry32) so fixtures are stable between reloads. */
export function rng(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

export const pick = <T>(r: () => number, list: readonly T[]): T => list[Math.floor(r() * list.length)]

let idSeq = 1000
export const newId = (prefix: string) => `${prefix}_${(++idSeq).toString(36)}`

// ---------------------------------------------------------------- responses

export interface MockResponse {
  status?: number
  json?: unknown
  text?: string
  body?: BodyInit
  headers?: Record<string, string>
  /** NDJSON lines streamed with a small delay between them. */
  stream?: unknown[]
  streamDelay?: number
}

export const ok = (json: unknown): MockResponse => ({ status: 200, json })
export const noContent = (): MockResponse => ({ status: 204 })
export const accepted = (json: unknown = { ok: true }): MockResponse => ({ status: 202, json })
export const fail = (status: number, code: string, message: string, extra: Record<string, unknown> = {}): MockResponse => ({
  status,
  json: { error: { code, message, ...extra } },
})
export const notFound = (what = 'Not found') => fail(404, 'not_found', what)

export function toResponse(r: MockResponse): Response {
  const status = r.status ?? 200
  const headers = new Headers(r.headers)
  if (status === 204) return new Response(null, { status, headers })
  if (r.stream) {
    headers.set('Content-Type', 'application/x-ndjson')
    const lines = r.stream
    const delay = r.streamDelay ?? 30
    const enc = new TextEncoder()
    const body = new ReadableStream<Uint8Array>({
      async start(ctrl) {
        for (const line of lines) {
          await sleep(delay)
          ctrl.enqueue(enc.encode(`${JSON.stringify(line)}\n`))
        }
        ctrl.close()
      },
    })
    return new Response(body, { status, headers })
  }
  if (r.json !== undefined) {
    headers.set('Content-Type', 'application/json')
    headers.set('Cache-Control', 'no-store')
    return new Response(JSON.stringify(r.json), { status, headers })
  }
  if (r.text !== undefined) {
    if (!headers.has('Content-Type')) headers.set('Content-Type', 'text/plain; charset=utf-8')
    return new Response(r.text, { status, headers })
  }
  return new Response(r.body ?? null, { status, headers })
}

export const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))

/** Realistic network latency (longer with ?mock=slow). */
export function latency(): number {
  return has('slow') ? 600 + Math.random() * 600 : 35 + Math.random() * 90
}

// ---------------------------------------------------------------- fake socket

type Listener = (ev: Event) => void

/**
 * Minimal in-page WebSocket. Subclasses implement onClientText /
 * onClientBinary and call pushText / pushBinary / end.
 */
export class FakeSocket extends EventTarget {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  readonly CONNECTING = 0
  readonly OPEN = 1
  readonly CLOSING = 2
  readonly CLOSED = 3

  readyState = 0
  binaryType: BinaryType = 'blob'
  bufferedAmount = 0
  extensions = ''
  protocol = ''
  readonly url: string
  onopen: Listener | null = null
  onmessage: ((ev: MessageEvent) => void) | null = null
  onclose: ((ev: CloseEvent) => void) | null = null
  onerror: Listener | null = null
  protected timers: number[] = []

  constructor(url: string, opts: { fail?: boolean } = {}) {
    super()
    this.url = url
    window.setTimeout(() => {
      if (opts.fail) {
        this.fire('error', new Event('error'))
        this.end(1006, '')
        return
      }
      this.readyState = 1
      this.fire('open', new Event('open'))
      this.opened()
    }, 40 + Math.random() * 80)
  }

  /** Called once open; subclasses start talking here. */
  protected opened(): void {}
  protected onClientText(_text: string): void {}
  protected onClientBinary(_data: Uint8Array): void {}

  send(data: string | ArrayBufferLike | Blob | ArrayBufferView): void {
    if (this.readyState !== 1) return
    if (typeof data === 'string') this.onClientText(data)
    else if (data instanceof Blob) void data.arrayBuffer().then((b) => this.onClientBinary(new Uint8Array(b)))
    else if (ArrayBuffer.isView(data)) this.onClientBinary(new Uint8Array(data.buffer, data.byteOffset, data.byteLength))
    else this.onClientBinary(new Uint8Array(data as ArrayBuffer))
  }

  close(code = 1000, reason = ''): void {
    if (this.readyState >= 2) return
    this.readyState = 2
    window.setTimeout(() => this.end(code, reason), 10)
  }

  protected every(ms: number, fn: () => void): void {
    this.timers.push(window.setInterval(() => this.readyState === 1 && fn(), ms))
  }

  protected later(ms: number, fn: () => void): void {
    this.timers.push(window.setTimeout(() => this.readyState === 1 && fn(), ms))
  }

  pushText(obj: unknown): void {
    if (this.readyState !== 1) return
    this.fire('message', new MessageEvent('message', { data: typeof obj === 'string' ? obj : JSON.stringify(obj) }))
  }

  pushBinary(bytes: Uint8Array | string): void {
    if (this.readyState !== 1) return
    const u8 = typeof bytes === 'string' ? new TextEncoder().encode(bytes) : bytes
    const data = this.binaryType === 'arraybuffer' ? u8.slice().buffer : new Blob([u8.slice()])
    this.fire('message', new MessageEvent('message', { data }))
  }

  end(code = 1000, reason = ''): void {
    if (this.readyState === 3) return
    for (const t of this.timers) {
      window.clearInterval(t)
      window.clearTimeout(t)
    }
    this.readyState = 3
    this.fire('close', new CloseEvent('close', { code, reason, wasClean: code === 1000 }))
  }

  private fire(type: 'open' | 'message' | 'close' | 'error', ev: Event): void {
    const h = (this as unknown as Record<string, ((e: Event) => void) | null>)[`on${type}`]
    h?.call(this, ev)
    this.dispatchEvent(ev)
  }
}
