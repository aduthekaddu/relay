import type { SettingsState } from '../api/runtime-settings'

type PatchResult = { ok: true; value: SettingsState } | { ok: false; message: string }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function duration(minutes: number): string {
  if (minutes === 0) return '0s'
  const hours = Math.floor(minutes / 60)
  return hours > 0 ? `${hours}h${minutes % 60}m0s` : `${minutes}m0s`
}

/** Synthetic consumer adoption. No filesystem or real process validation. */
export function patchRuntimeSettings(state: SettingsState, patch: unknown): PatchResult {
  if (!isRecord(patch)) return { ok: false, message: 'Expected a settings object.' }
  const next = structuredClone(state)
  for (const [key, value] of Object.entries(patch)) {
    if (
      ![
        'workspaceRoots',
        'defaultShell',
        'defaultCwd',
        'recordAgents',
        'claudeQuota',
        'idleMinutes',
      ].includes(key)
    )
      return { ok: false, message: 'Unknown or read-only settings field.' }
    if (value === null) continue
    switch (key) {
      case 'workspaceRoots':
        if (
          !Array.isArray(value) ||
          value.length > 32 ||
          !value.every(
            (p: unknown) =>
              typeof p === 'string' &&
              (p.startsWith('/') || p === '~' || p.startsWith('~/')) &&
              !p.includes('\0') &&
              !p.includes('\n'),
          )
        ) {
          return { ok: false, message: 'Use absolute or home paths for workspace roots.' }
        }
        next.workspaceRoots = value
        next.effective.workspaceRoots = [...value]
        break
      case 'defaultShell':
      case 'defaultCwd':
        if (
          typeof value !== 'string' ||
          value.includes('\0') ||
          value.includes('\n') ||
          (value !== '' &&
            !value.startsWith('/') &&
            !(key === 'defaultCwd' && (value === '~' || value.startsWith('~/'))))
        ) {
          return { ok: false, message: 'Use an absolute or supported home path.' }
        }
        next[key] = value
        if (next.effective.terminal && next.effective.terminalStatus === 'next-session')
          next.effective.terminal[key] = value
        break
      case 'recordAgents':
      case 'claudeQuota':
        if (typeof value !== 'boolean') return { ok: false, message: 'Expected a boolean.' }
        next[key] = value
        if (key === 'claudeQuota') next.effective.claudeQuota = value
        if (key === 'recordAgents') {
          next.recordingMode = value ? (next.recordingMode === 'all' ? 'all' : 'agents') : 'off'
          if (next.effective.terminal && next.effective.terminalStatus === 'next-session')
            next.effective.terminal.recordingMode = next.recordingMode
        }
        break
      case 'idleMinutes':
        if (typeof value !== 'number' || !Number.isInteger(value) || value < 0 || value > 10080)
          return { ok: false, message: 'Idle minutes must be between 0 and 10080.' }
        next.idleMinutes = value
        next.effective.codeIdleStop = duration(value)
        next.effective.desktopIdleStop = duration(value)
        break
      default:
        return { ok: false, message: 'Unknown or read-only settings field.' }
    }
  }
  return { ok: true, value: next }
}
