// Synthetic apps fixtures; owned registrations and scenario controls.
import { setManagedState } from '../capabilities'
import * as db from '../data'
import { body, now } from '../helpers'
import { defineMockModule } from '../registry'
import { DeadSocket, emit } from '../sockets'
import { fail, noContent, notFound, ok } from '../util'

export default defineMockModule('apps', (owner) => {
  owner.socket('/desktop/ws', ({ url }) => new DeadSocket(url.href))
  const route = owner.http
  route('GET', '/apps', () =>
    ok(db.apps.filter((app) => app.id !== 'code' || db.info.capabilities.code.enabled)),
  )
  const starts = { code: 0, desktop: 0 }
  function managedState(feature: 'code' | 'desktop', state: 'starting' | 'running' | 'stopped'): void {
    setManagedState(db.info, db.apps, db.desktop, feature, state)
    const app = db.apps.find((item) => item.id === feature)
    if (app) emit('app.state', app)
    if (feature === 'desktop') emit('desktop.state', db.desktop)
    emit('capabilities.changed', { feature })
  }
  function managedAction(feature: 'code' | 'desktop', action: 'start' | 'stop'): void {
    const current = db.info.capabilities[feature].state
    if (action === 'start' && (current === 'starting' || current === 'running')) return
    const attempt = ++starts[feature]
    managedState(feature, action === 'start' ? 'starting' : 'stopped')
    if (action === 'start')
      owner.later(
        () => {
          if (starts[feature] !== attempt) return
          managedState(feature, 'running')
        },
        feature === 'desktop' ? 1800 : 1500,
      )
  }
  for (const action of ['start', 'stop'] as const) {
    route('POST', `/apps/{id}/${action}`, (r) => {
      const a = db.apps.find((x) => x.id === r.params.id)
      if (!a || (a.id === 'code' && !db.info.capabilities.code.enabled)) return notFound('No such app')
      if (a.id === 'code' || a.id === 'desktop') {
        const capability = db.info.capabilities[a.id]
        const active = capability.state === 'running' || (a.id === 'code' && capability.state === 'starting')
        if (action === 'start' && (!capability.enabled || (!capability.available && !active))) {
          return fail(503, 'unavailable', 'Prerequisites are unavailable.')
        }
        managedAction(a.id, action)
        return ok(a)
      }
      if (!a.installed) return fail(409, 'not_installed', a.installHint ?? 'Not installed.')
      a.state = action === 'start' ? 'starting' : 'stopped'
      if (action === 'start')
        owner.later(() => {
          a.state = 'running'
          a.since = now()
          emit('app.state', a)
        }, 1500)
      emit('app.state', a)
      return ok(a)
    })
  }
  route('GET', '/desktop', () => ok(db.desktop))
  route('POST', '/desktop/start', () => {
    if (
      !db.desktop.capability.enabled ||
      (!db.desktop.capability.available && db.desktop.capability.state !== 'running')
    ) {
      return fail(503, 'unavailable', 'Desktop prerequisites are unavailable.')
    }
    managedAction('desktop', 'start')
    return ok(db.desktop)
  })
  route('POST', '/desktop/stop', () => {
    managedAction('desktop', 'stop')
    db.desktop.viewers = 0
    return ok(db.desktop)
  })
  route('POST', '/desktop/launch', (r) => {
    const app = db.desktop.apps.find((a) => a.id === body<{ app: string }>(r).app)
    if (app) app.running = true
    return ok(db.desktop)
  })
  let desktopClip = ''
  route('POST', '/desktop/clipboard', (r) => {
    desktopClip = body<{ text: string }>(r).text ?? ''
    return noContent()
  })
  route('GET', '/desktop/clipboard', () => ok({ text: desktopClip }))
  route('POST', '/desktop/resize', (r) => ok(Object.assign(db.desktop, body(r))))
  owner.onReset(() => {
    starts.code = starts.desktop = 0
    desktopClip = ''
  })
})
