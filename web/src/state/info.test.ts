import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Info } from '../api/types'
import * as db from '../mocks/data'

const callbacks = vi.hoisted(() => new Map<string, (data: unknown) => void>())
const get = vi.hoisted(() => vi.fn())
vi.mock('../api/client', () => ({ api: { get } }))
vi.mock('../api/events', () => ({
  on: (name: string, fn: (data: unknown) => void) => {
    callbacks.set(name, fn)
  },
}))

import { info, loadInfo, trackInfo } from './info'

function snapshot(mode: 'path' | 'subdomain'): Info {
  const value = structuredClone(db.info)
  value.features.previewsMode = mode
  value.capabilities.previews.effectiveMode = mode
  return value
}
function pending(): { promise: Promise<Info>; resolve: (value: Info) => void } {
  let resolve: (value: Info) => void = () => {
    throw new Error('Uninitialized promise')
  }
  const promise = new Promise<Info>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
afterEach(() => {
  get.mockReset()
  info.value = null
})

describe('capability Info updates', () => {
  it('refreshes on owner invalidation and rejects an older HTTP response', async () => {
    trackInfo()
    const old = pending()
    const next = pending()
    get.mockReturnValueOnce(old.promise).mockReturnValueOnce(next.promise)
    const first = loadInfo()
    callbacks.get('capabilities.changed')?.({ feature: 'previews' })
    expect(get).toHaveBeenCalledTimes(2)
    next.resolve(snapshot('subdomain'))
    await next.promise
    await Promise.resolve()
    old.resolve(snapshot('path'))
    await first
    expect(info.value?.capabilities.previews.effectiveMode).toBe('subdomain')
  })
  it('keeps a newer hello snapshot when an earlier request completes', async () => {
    trackInfo()
    const request = pending()
    get.mockReturnValueOnce(request.promise)
    const load = loadInfo()
    callbacks.get('hello')?.(snapshot('subdomain'))
    request.resolve(snapshot('path'))
    await load
    expect(info.value?.capabilities.previews.effectiveMode).toBe('subdomain')
  })
  it('preserves the previous snapshot when a refresh fails', async () => {
    info.value = snapshot('subdomain')
    get.mockRejectedValueOnce(new Error('offline'))
    await loadInfo()
    expect(info.value?.capabilities.previews.effectiveMode).toBe('subdomain')
  })
})
