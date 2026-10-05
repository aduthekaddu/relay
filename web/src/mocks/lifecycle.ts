/** Abortable, owned delays. No task survives a reset or a cancelled request. */
export const abortError = () => new DOMException('The operation was aborted.', 'AbortError')

/** Reject promptly even when a body reader or extension is still awaiting I/O. */
export function abortable<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  return new Promise((resolve, reject) => {
    const cleanup = () => signal.removeEventListener('abort', abort)
    const abort = () => {
      cleanup()
      reject(abortError())
    }
    signal.addEventListener('abort', abort, { once: true })
    promise.then(
      (value) => {
        cleanup()
        resolve(value)
      },
      (error) => {
        cleanup()
        reject(error)
      },
    )
    if (signal.aborted) abort()
  })
}

export function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(abortError())
    const finish = () => {
      signal?.removeEventListener('abort', abort)
      resolve()
    }
    const timer = window.setTimeout(finish, ms)
    const abort = () => {
      window.clearTimeout(timer)
      signal?.removeEventListener('abort', abort)
      reject(abortError())
    }
    signal?.addEventListener('abort', abort, { once: true })
  })
}

export function linkSignals(...signals: (AbortSignal | undefined)[]): {
  signal: AbortSignal
  dispose: () => void
} {
  const controller = new AbortController()
  const abort = () => controller.abort()
  for (const signal of signals) {
    if (signal?.aborted) controller.abort()
    else signal?.addEventListener('abort', abort, { once: true })
  }
  return {
    signal: controller.signal,
    dispose: () => {
      for (const signal of signals) signal?.removeEventListener('abort', abort)
    },
  }
}

export class TimerScope {
  private timers = new Set<number>()

  later(fn: () => void, ms: number): void {
    const timer = window.setTimeout(() => {
      this.timers.delete(timer)
      fn()
    }, ms)
    this.timers.add(timer)
  }

  reset(): void {
    for (const timer of this.timers) window.clearTimeout(timer)
    this.timers.clear()
  }
}
