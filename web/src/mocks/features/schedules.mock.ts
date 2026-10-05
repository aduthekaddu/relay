// Synthetic schedules fixtures; owned registrations and scenario controls.
import type { CronPreview } from '../../api/notify'
import * as db from '../data'
import { body, now, q, qn } from '../helpers'
import { defineMockModule } from '../registry'
import { emit } from '../sockets'
import { fail, newId, noContent, notFound, ok } from '../util'

export default defineMockModule('schedules', (owner) => {
  const route = owner.http
  route('GET', '/schedules', () => ok(owner.scenarios.state.empty ? [] : db.schedules))
  // Preview fixtures follow internal/schedule/describe.go and schedule_test.go.
  // Unlisted expressions/timezones and next-run calculation remain unavailable.
  const cronDescriptions = new Map([
    ['0 2 * * 1-5', 'Every weekday at 02:00'],
    ['30 23 * * *', 'Every day at 23:30'],
    ['0 8 * * 1', 'Every Monday at 08:00'],
    ['0 2 * * *', 'Every day at 02:00'],
  ])
  const cronErrors = new Map([
    ['* * *', 'invalid cron expression: expected exactly 5 fields, found 3: [* * *]'],
    ['61 * * * *', 'invalid cron expression: end of range (61) above maximum (59): 61'],
    ['@every 10s', '@every intervals must be at least a minute'],
    ['@sometimes', 'invalid cron expression: unrecognized descriptor: @sometimes'],
  ])
  route('GET', '/schedules/describe', (r) => {
    const cron = (q(r, 'cron') ?? '').trim().replace(/\s+/g, ' ')
    if (!cron) return ok({ valid: false, error: 'cron expression is required' } satisfies CronPreview)
    if (cron.startsWith('TZ=') || cron.startsWith('CRON_TZ='))
      return ok({
        valid: false,
        error: 'set the timezone field instead of a TZ= prefix',
      } satisfies CronPreview)
    const timezone = q(r, 'timezone') ?? ''
    // Mars/Olympus is the backend's invalid-zone fixture; these path shapes are
    // rejected by Go's time.LoadLocation before consulting any timezone data.
    if (timezone === 'Mars/Olympus' || timezone.includes('..') || /^[/\\]/.test(timezone))
      return ok({ valid: false, error: `unknown timezone ${JSON.stringify(timezone)}` } satisfies CronPreview)
    if (!['', 'UTC', 'Europe/Berlin'].includes(timezone))
      return fail(503, 'unavailable', 'Cron evaluation is unavailable for this mock fixture')
    const error = cronErrors.get(cron)
    if (error) return ok({ valid: false, error } satisfies CronPreview)
    const description = cronDescriptions.get(cron)
    if (!description) return fail(503, 'unavailable', 'Cron evaluation is unavailable for this mock fixture')
    return ok({ valid: true, description } satisfies CronPreview)
  })
  route('GET', '/schedules/{id}', (r) => {
    const schedule = db.schedules.find((x) => x.id === r.params.id)
    return schedule ? ok(schedule) : notFound()
  })
  route('POST', '/schedules', (r) => {
    const s = {
      id: newId('sc'),
      name: 'New schedule',
      cron: '0 2 * * *',
      cwd: db.HOME,
      mode: 'headless' as const,
      enabled: true,
      notify: true,
      ...body<object>(r),
      createdAt: now(),
    }
    db.schedules.push(s)
    return ok(s)
  })
  route('PATCH', '/schedules/{id}', (r) => {
    const s = db.schedules.find((x) => x.id === r.params.id)
    return s ? ok(Object.assign(s, body(r))) : notFound()
  })
  route('DELETE', '/schedules/{id}', (r) => {
    const i = db.schedules.findIndex((x) => x.id === r.params.id)
    if (i >= 0) db.schedules.splice(i, 1)
    return noContent()
  })
  route('POST', '/schedules/{id}/run', (r) => {
    const s = db.schedules.find((x) => x.id === r.params.id)
    if (!s) return notFound()
    const run = { id: newId('sr'), scheduleId: s.id, startedAt: now(), status: 'running' as const }
    db.scheduleRuns.unshift(run)
    s.lastRun = run
    emit('schedule.run', run)
    return ok(run)
  })
  route('GET', '/schedules/{id}/runs', (r) =>
    ok(db.scheduleRuns.filter((x) => x.scheduleId === r.params.id).slice(0, qn(r, 'limit', 20))),
  )
})
