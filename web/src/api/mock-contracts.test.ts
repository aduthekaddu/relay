import { afterEach, describe, expect, it, vi } from 'vitest'
import * as db from '../mocks/data'
import { handle } from '../mocks/handlers'
import * as sockets from '../mocks/sockets'
import type { FileJob } from './files'
import type { CronPreview } from './notify'
import type { ToolboxJob } from './toolbox'
import type { TerminalSession } from './types'

const call = (method: string, path: string, body?: unknown) =>
  handle(method, new URL(`/api/v1/${path}`, 'http://mock.invalid'), body)

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('authorized mock contract repair', () => {
  it.each([
    ['0 2 * * 1-5', 'Europe/Berlin', 'Every weekday at 02:00'],
    ['30 23 * * *', 'UTC', 'Every day at 23:30'],
    ['0 8 * * 1', 'UTC', 'Every Monday at 08:00'],
    ['  0  2 * * *  ', '', 'Every day at 02:00'],
  ])('describes supported cron fixture %s in %s', async (cron, timezone, description) => {
    const response = await call('GET', `schedules/describe?${new URLSearchParams({ cron, timezone })}`)
    const preview: CronPreview = { valid: true, description }
    expect(response.status).toBe(200)
    expect(response.json).toEqual(preview)
    // This fixture does not invent future activation times.
    expect(response.json).not.toHaveProperty('next')
  })

  it.each([
    ['0 9 * * *', 'UTC'],
    ['0 2 * * 1-5', 'fixture/unsupported'],
  ])('reports unavailable evaluation for %s in %s without declaring it invalid', async (cron, timezone) => {
    const response = await call('GET', `schedules/describe?${new URLSearchParams({ cron, timezone })}`)
    expect(response.status).toBe(503)
    expect(response.json).toMatchObject({ error: { code: 'unavailable' } })
    expect(response.json).not.toHaveProperty('valid')
  })

  it('retains the canonical invalid response for a missing cron expression', async () => {
    const response = await call('GET', 'schedules/describe')
    expect(response.status).toBe(200)
    expect(response.json).toEqual({
      valid: false,
      error: 'cron expression is required',
    } satisfies CronPreview)
  })

  it.each([
    ['* * *', 'invalid cron expression: expected exactly 5 fields, found 3: [* * *]'],
    ['61 * * * *', 'invalid cron expression: end of range (61) above maximum (59): 61'],
    ['@every 10s', '@every intervals must be at least a minute'],
    ['@sometimes', 'invalid cron expression: unrecognized descriptor: @sometimes'],
    ['TZ=UTC 0 2 * * *', 'set the timezone field instead of a TZ= prefix'],
    ['CRON_TZ=UTC 0 2 * * *', 'set the timezone field instead of a TZ= prefix'],
  ])('retains the canonical invalid response for source-backed cron fixture %s', async (cron, error) => {
    const response = await call('GET', `schedules/describe?${new URLSearchParams({ cron, timezone: 'UTC' })}`)
    expect(response.status).toBe(200)
    expect(response.json).toEqual({ valid: false, error } satisfies CronPreview)
  })

  it.each(['Mars/Olympus', '../fixture', '/fixture'])(
    'retains the canonical invalid response for rejected timezone %s',
    async (timezone) => {
      const response = await call(
        'GET',
        `schedules/describe?${new URLSearchParams({ cron: '0 2 * * *', timezone })}`,
      )
      expect(response.status).toBe(200)
      expect(response.json).toEqual({
        valid: false,
        error: `unknown timezone ${JSON.stringify(timezone)}`,
      } satisfies CronPreview)
    },
  )

  it('uses the canonical running/done toolbox payload and 202 admission', async () => {
    vi.useFakeTimers()
    const emit = vi.spyOn(sockets, 'emit')
    const tool = db.tools[0]
    const installed = tool.installed
    const terminals = [...db.terminals]
    try {
      const response = await call('POST', `toolbox/${tool.id}/install`)
      const terminal = response.json as TerminalSession
      expect(response.status).toBe(202)
      const running: ToolboxJob = { tool: tool.id, terminalId: terminal.id, state: 'running' }
      expect(emit).toHaveBeenCalledWith('toolbox.job', running)
      await vi.advanceTimersByTimeAsync(4000)
      const done: ToolboxJob = { ...running, state: 'done', exitCode: 0 }
      expect(emit).toHaveBeenCalledWith('toolbox.job', done)
      expect(emit.mock.calls.filter(([type]) => type === 'toolbox.job')).toHaveLength(2)
    } finally {
      tool.installed = installed
      db.terminals.splice(0, db.terminals.length, ...terminals)
    }
  })

  it('returns a typed file job and emits cancellation, retaining repeat-cancel semantics', async () => {
    const emit = vi.spyOn(sockets, 'emit')
    const response = await call('POST', 'files/copy', {
      from: ['/fixture/source'],
      to: '/fixture/destination',
    })
    const job = response.json as FileJob
    expect(response.status).toBe(202)
    expect(job).toMatchObject({ op: 'copy', state: 'running', files: 0, totalFiles: 0 })
    expect(emit).toHaveBeenCalledWith('files.job', job)
    const list = await call('GET', 'files/jobs')
    expect(list.json).toContainEqual(job)
    expect((await call('DELETE', `files/jobs/${job.id}`)).status).toBe(204)
    expect(emit).toHaveBeenLastCalledWith(
      'files.job',
      expect.objectContaining({ id: job.id, state: 'canceled' }),
    )
    expect((await call('DELETE', `files/jobs/${job.id}`)).status).toBe(204)
    expect((await call('DELETE', 'files/jobs/fixture-unknown')).status).toBe(404)
  })
})
