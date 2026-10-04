// HTTP contract mirrors of internal/api/agents.go. CwdUsage is backend only.
import type { TerminalSession } from './types'

export interface AgentHookRequest {
  agent: string
  event: string
  sessionId?: string
  payload?: unknown
}

export interface AgentHookResult {
  action: 'attention' | 'done' | 'ignored'
  terminalId?: string
}

export interface ReindexResponse {
  started: boolean
}

export interface PinWorkspaceRequest {
  path: string
  pinned: boolean
}

export interface GitTaskResponse {
  terminal?: TerminalSession
}
