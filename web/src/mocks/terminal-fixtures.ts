// Synthetic terminal admission fixture shared by agent and script mocks.
import type { TerminalSession } from '../api/types'
import * as db from './data'
import { now } from './helpers'
import { emit } from './sockets'
import { newId } from './util'
export function agentTerminal(agent: string, cwd: string, name: string, sessionId?: string): TerminalSession {
  const t: TerminalSession = {
    id: newId('t'),
    name,
    kind: 'agent',
    agent,
    agentSessionId: sessionId,
    command: [agent],
    cwd,
    workspace: cwd,
    cols: 120,
    rows: 34,
    clients: 0,
    activity: 'working',
    recording: true,
    pinned: false,
    createdAt: now(),
    lastOutputAt: now(),
    preview: 'Starting…',
  }
  db.terminals.unshift(t)
  emit('terminal.created', t)
  return t
}
