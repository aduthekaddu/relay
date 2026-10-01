import { describe, expect, it } from 'vitest'
import type { SettingsState } from '../api/runtime-settings'
import { patchRuntimeSettings } from './runtime-settings'

function fixture(): SettingsState {
  return {
    workspaceRoots: ['/home/fixture/work'],
    defaultShell: '/bin/sh',
    defaultCwd: '/home/fixture',
    recordAgents: true,
    claudeQuota: false,
    idleMinutes: 120,
    recordingMode: 'all',
    effective: {
      workspaceRoots: ['/home/fixture/work'],
      claudeQuota: false,
      codeIdleStop: '2h0m0s',
      desktopIdleStop: '2h0m0s',
      terminal: {
        defaultShell: '/bin/sh',
        defaultCwd: '/home/fixture',
        recordingMode: 'all',
        source: 'file',
      },
      terminalStatus: 'next-session',
    },
    apply: {
      workspaceRoots: 'next-request',
      defaultShell: 'next-session',
      defaultCwd: 'next-session',
      recordAgents: 'next-session',
      claudeQuota: 'next-request',
      idleMinutes: 'next-idle-check',
    },
  }
}

describe('settings consumer contract mocks', () => {
  it('keeps saved and daemon values distinct when adoption is unavailable', () => {
    const state = fixture()
    state.effective.terminalStatus = 'different-config'
    const result = patchRuntimeSettings(state, {
      defaultShell: '/bin/bash',
      idleMinutes: 0,
      claudeQuota: true,
    })
    expect(result.ok).toBe(true)
    if (!result.ok) return
    expect(result.value.defaultShell).toBe('/bin/bash')
    expect(result.value.effective.terminal?.defaultShell).toBe('/bin/sh')
    expect(result.value.effective.codeIdleStop).toBe('0s')
    expect(result.value.effective.claudeQuota).toBe(true)
    expect(state.claudeQuota).toBe(false)
  })
  it('preserves all recording mode and marks future daemon defaults', () => {
    const result = patchRuntimeSettings(fixture(), { recordAgents: true, defaultCwd: '/home/fixture/work' })
    expect(result.ok).toBe(true)
    if (!result.ok) return
    expect(result.value.recordingMode).toBe('all')
    expect(result.value.effective.terminal?.defaultCwd).toBe('/home/fixture/work')
  })
  it.each([{ effective: {} }, { idleMinutes: -1 }, { claudeQuota: 'yes' }, { workspaceRoots: ['relative'] }])(
    'rejects invalid writes without mutating state',
    (patch) => {
      const state = fixture()
      const before = structuredClone(state)
      expect(patchRuntimeSettings(state, patch).ok).toBe(false)
      expect(state).toEqual(before)
    },
  )
})
