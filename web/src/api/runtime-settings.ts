import type { Settings } from './types'

export type TerminalSettingsStatus = 'next-session' | 'restart-required' | 'different-config' | 'unavailable'

export type SettingsTiming = TerminalSettingsStatus | 'next-request' | 'next-idle-check'

export interface TerminalDefaults {
  defaultShell: string
  defaultCwd: string
  recordingMode: string
  source: string
}

export interface SettingsEffective {
  workspaceRoots: string[]
  claudeQuota: boolean
  codeIdleStop: string
  desktopIdleStop: string
  terminal: TerminalDefaults | null
  terminalStatus: TerminalSettingsStatus
}

/** Editable fields are saved values. Consumer state and timing are read-only. */
export interface SettingsState extends Settings {
  recordingMode: string
  effective: SettingsEffective
  apply: Record<keyof Settings, SettingsTiming>
}
