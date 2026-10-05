// Synthetic toolbox fixtures; owned registrations and scenario controls.
import type { ToolboxJob } from '../../api/toolbox'
import type { TerminalSession } from '../../api/types'
import * as db from '../data'
import { body, now } from '../helpers'
import { defineMockModule } from '../registry'
import { emit } from '../sockets'
import { accepted, newId, notFound, ok } from '../util'

export default defineMockModule('toolbox', (owner) => {
  const route = owner.http
  route('GET', '/toolbox', () => ok(owner.scenarios.state.empty ? [] : db.tools))
  route('POST', '/toolbox/{id}/install', (r) => {
    const tool = db.tools.find((x) => x.id === r.params.id)
    if (!tool) return notFound('Unknown tool')
    const t: TerminalSession = {
      id: newId('t'),
      name: `install ${tool.name}`,
      kind: 'toolbox',
      command: ['sh', '-c', `install ${tool.id}`],
      cwd: db.HOME,
      cols: 120,
      rows: 34,
      clients: 0,
      activity: 'working',
      recording: false,
      pinned: false,
      createdAt: now(),
      lastOutputAt: now(),
    }
    db.terminals.unshift(t)
    emit('terminal.created', t)
    emit('toolbox.job', { tool: tool.id, terminalId: t.id, state: 'running' } satisfies ToolboxJob)
    owner.later(() => {
      tool.installed = true
      Object.assign(t, { activity: 'exited', exitCode: 0, exitedAt: now() })
      emit('terminal.exited', t)
      emit('toolbox.job', {
        tool: tool.id,
        terminalId: t.id,
        state: 'done',
        exitCode: 0,
      } satisfies ToolboxJob)
    }, 4000)
    return accepted(t)
  })
  route('GET', '/toolbox/mcp', () => ok(db.mcpServers))
  route('POST', '/toolbox/mcp/apply', (r) => {
    const b = body<{ server: string; agents: string[]; remove?: boolean }>(r)
    const s = db.mcpServers.find((x) => x.id === b.server)
    if (!s) return notFound('Unknown MCP server')
    for (const a of b.agents ?? []) s.agents[a] = !b.remove
    return ok(s)
  })
})
