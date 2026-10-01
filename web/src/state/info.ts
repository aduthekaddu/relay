// Machine info (hostname, version, features). Filled by the `hello` event
// on every (re)connect and by loadInfo() on boot.
import { signal } from '@preact/signals'
import { api } from '../api/client'
import { on } from '../api/events'
import type { Info } from '../api/types'

/** Info about the machine and server; null until loaded. */
export const info = signal<Info | null>(null)

let revision = 0

/** Fetch /api/v1/info (errors leave the previous value). */
export async function loadInfo(): Promise<Info | null> {
  const request = ++revision
  try {
    const next = await api.get<Info>('info')
    if (request === revision) info.value = next
  } catch {
    /* offline or signed out: keep what we have */
  }
  return info.value
}

let wired = false
/** Keep `info` current from hello events (called by startLive). */
export function trackInfo(): void {
  if (wired) return
  wired = true
  on('hello', (data) => {
    revision++
    info.value = data
  })
  on('capabilities.changed', () => {
    void loadInfo()
  })
}
