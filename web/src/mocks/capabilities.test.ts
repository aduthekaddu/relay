import { afterEach, describe, expect, it, vi } from 'vitest'
import goContract from '../../../internal/api/capabilities.go?raw'
import type { AppCapability, PreviewCapability } from '../api/capabilities'
import { setManagedState, setPreviewCapability } from './capabilities'
import * as db from './data'
import { handle } from './handlers'

function fixture() {
  return structuredClone({ info: db.info, apps: db.apps, desktop: db.desktop, previews: db.previews })
}
function jsonFields(name: string): string[] {
  const body = goContract.match(new RegExp(`type ${name} struct \\{([\\s\\S]*?)\\n\\}`))?.[1]
  if (!body) throw new Error(`Missing Go contract ${name}`)
  return [...body.matchAll(/json:"([^",]+)/g)].map((m) => m[1]).sort()
}

describe('feature capability contract', () => {
  it('mirrors Go JSON fields in typed browser fixtures', () => {
    const app: AppCapability = {
      enabled: true,
      available: true,
      state: 'stopped',
      missing: [],
      implementation: 'code-server',
      source: 'standalone',
    }
    const preview: PreviewCapability = {
      configuredMode: 'auto',
      effectiveMode: 'subdomain',
      host: 'dev.example.test',
      port: '47790',
      detection: 'verified',
      checkedAt: '2026-01-01T00:00:00Z',
    }
    expect(Object.keys(app).sort()).toEqual(jsonFields('AppCapability'))
    expect(Object.keys(preview).sort()).toEqual(jsonFields('PreviewCapability'))
    expect(Object.keys(db.info.capabilities).sort()).toEqual(jsonFields('Capabilities'))
    expect(jsonFields('CapabilityChange')).toEqual(['feature'])
    for (const feature of ['code', 'desktop'] as const) {
      expect(db.apps.find((a) => a.id === feature)?.capability).toEqual(db.info.capabilities[feature])
    }
    expect(db.desktop.capability).toEqual(db.info.capabilities.desktop)
  })
  it.each(['code', 'desktop'] as const)('keeps all %s lifecycle readers consistent', (feature) => {
    const f = fixture()
    for (const state of ['stopped', 'starting', 'running', 'failed', 'stopped'] as const) {
      const cap = setManagedState(f.info, f.apps, f.desktop, feature, state)
      expect(cap.state).toBe(state)
      expect(f.info.features[feature]).toBe(true)
      const app = f.apps.find((a) => a.id === feature)
      expect(app?.capability).toEqual(cap)
      expect(app?.state).toBe(state === 'failed' ? 'error' : state)
      if (state === 'failed') expect(app?.error).toBeTruthy()
      else expect(app?.error).toBeUndefined()
      if (feature === 'desktop') {
        expect(f.desktop.capability).toEqual(cap)
        expect(f.desktop.state).toBe(state === 'failed' ? 'stopped' : state)
      }
    }
    f.info.capabilities[feature] = { enabled: false, available: true, state: 'disabled', missing: [] }
    expect(setManagedState(f.info, f.apps, f.desktop, feature, 'running').state).toBe('disabled')
    expect(f.info.features[feature]).toBe(false)
    f.info.capabilities[feature] = {
      enabled: true,
      available: false,
      state: 'unavailable',
      missing: ['fixture'],
    }
    expect(setManagedState(f.info, f.apps, f.desktop, feature, 'stopped').state).toBe('unavailable')
    expect(setManagedState(f.info, f.apps, f.desktop, feature, 'running').state).toBe('running')
    expect(f.info.features[feature]).toBe(false)
  })
  it('refreshes mode and all URLs after DNS decisions change', () => {
    const f = fixture()
    f.info.publicUrl = 'https://relay.example.test:47790'
    for (const mode of ['path', 'subdomain', 'path', 'off'] as const) {
      setPreviewCapability(f.info, f.previews, {
        configuredMode: 'auto',
        effectiveMode: mode,
        host: 'dev.example.test',
        port: '47792',
        detection: mode === 'subdomain' ? 'verified' : 'lookup-failed',
      })
      expect(f.info.features.previewsMode).toBe(mode)
      for (const p of f.previews) {
        const want =
          mode === 'off'
            ? ''
            : mode === 'path'
              ? `https://relay.example.test:47790/p/${p.port}/`
              : `https://${p.port}.dev.example.test:47792/`
        expect(p.url).toBe(want)
      }
    }
  })
})

describe('managed mock endpoints', () => {
  afterEach(() => {
    vi.useRealTimers()
  })
  it('updates Info, Apps and Desktop and cancels a pending start on stop', async () => {
    vi.useFakeTimers()
    const call = (path: string) =>
      handle('POST', new URL(`/api/v1/${path}`, 'http://mock.invalid'), undefined)
    expect((await call('desktop/start')).status).toBe(200)
    expect(db.info.capabilities.desktop.state).toBe('starting')
    expect(db.desktop.state).toBe('starting')
    expect((await call('apps/desktop/stop')).status).toBe(200)
    vi.runAllTimers()
    expect(db.info.capabilities.desktop.state).toBe('stopped')
    expect(db.apps.find((a) => a.id === 'desktop')?.state).toBe('stopped')
    expect(db.desktop.state).toBe('stopped')
    expect(db.desktop.display).toBe(':7')
    expect((await call('apps/code/stop')).status).toBe(200)
    expect((await call('apps/code/start')).status).toBe(200)
    expect(db.info.capabilities.code.state).toBe('starting')
    vi.runAllTimers()
    expect(db.info.capabilities.code.state).toBe('running')
    expect((await call('apps/code/stop')).status).toBe(200)
    expect(db.info.capabilities.code.state).toBe('stopped')
  })
})

describe('managed mock configuration and removal gates', () => {
  it('hides disabled Code and keeps starts idempotent after removal', async () => {
    const before = structuredClone(db.info.capabilities.code)
    const call = (method: string, path: string) =>
      handle(method, new URL(`/api/v1/${path}`, 'http://mock.invalid'), undefined)
    try {
      db.info.capabilities.code = { enabled: false, available: true, state: 'disabled', missing: [] }
      const list = await call('GET', 'apps')
      expect(list.status).toBe(200)
      expect(list.json).toEqual(expect.arrayContaining([expect.objectContaining({ id: 'desktop' })]))
      expect(list.json).toEqual(expect.not.arrayContaining([expect.objectContaining({ id: 'code' })]))
      expect((await call('POST', 'apps/code/start')).status).toBe(404)
      db.info.capabilities.code = {
        enabled: true,
        available: false,
        state: 'running',
        missing: ['code-binary'],
      }
      setManagedState(db.info, db.apps, db.desktop, 'code', 'running')
      expect((await call('POST', 'apps/code/start')).status).toBe(200)
      expect(db.info.capabilities.code.state).toBe('running')
      expect(db.info.features.code).toBe(false)
      db.info.capabilities.code = {
        enabled: true,
        available: false,
        state: 'unavailable',
        missing: ['code-binary'],
      }
      expect((await call('POST', 'apps/code/start')).status).toBe(503)
    } finally {
      db.info.capabilities.code = before
      if (
        before.state === 'starting' ||
        before.state === 'running' ||
        before.state === 'failed' ||
        before.state === 'stopped'
      ) {
        setManagedState(db.info, db.apps, db.desktop, 'code', before.state)
      }
    }
  })
})
