import type { ErrorDetail } from '../api/types'
import { abortError } from './lifecycle'

export interface MockScenario {
  /** A fixed delay or a request-arrival-ordered queue of delays, in ms. */
  latency?: number | number[]
  /** Hold requests until loading is cleared, aborted, or reset. */
  loading?: boolean
  /** Owners explicitly implement their contract's empty response shape. */
  empty?: boolean
  /** Only choose statuses/codes supported by the selected endpoint contract. */
  error?: { status: number; detail: ErrorDetail }
  /** Refuse new sockets until cleared; existing sockets are closed by disconnect(). */
  offline?: boolean
}

export class ScenarioControls {
  private value: MockScenario = {}
  private waiting = new Set<() => void>()

  get state(): Readonly<MockScenario> {
    return structuredClone(this.value)
  }

  set(value: MockScenario): void {
    if (value.latency !== undefined) {
      const delays = typeof value.latency === 'number' ? [value.latency] : value.latency
      if (delays.some((n) => !Number.isFinite(n) || n < 0)) throw new Error('Invalid mock latency')
    }
    if (
      value.error &&
      (!Number.isInteger(value.error.status) || value.error.status < 400 || value.error.status > 599)
    )
      throw new Error('Mock error status must be 400–599')
    this.value = { ...this.value, ...structuredClone(value) }
    if (!this.value.loading) for (const release of [...this.waiting]) release()
  }

  takeLatency(fallback: number): number {
    const latency = this.value.latency
    return typeof latency === 'number' ? latency : (latency?.shift() ?? fallback)
  }

  async wait(signal: AbortSignal): Promise<void> {
    if (signal.aborted) throw abortError()
    if (!this.value.loading) return
    await new Promise<void>((resolve, reject) => {
      const finish = () => {
        this.waiting.delete(finish)
        signal.removeEventListener('abort', abort)
        resolve()
      }
      const abort = () => {
        this.waiting.delete(finish)
        signal.removeEventListener('abort', abort)
        reject(abortError())
      }
      this.waiting.add(finish)
      signal.addEventListener('abort', abort, { once: true })
    })
  }

  reset(): void {
    this.value = {}
    for (const release of [...this.waiting]) release()
    this.waiting.clear()
  }
}
