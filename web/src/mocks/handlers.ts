// The single integrator owns this wiring. Features own additive modules.
import { resetUnauthorizedListeners } from '../api/client'
import { events } from '../api/events'
import * as db from './data'
import { resetFS } from './fs'
import { registerModules } from './modules'
import { MockRegistry } from './registry'
import { resetStreams } from './sockets'
import { resetSystem } from './system'
import { type MockMode, resetFakeSockets, resetIds, setModes } from './util'

export { searchAll } from './search-fixtures'

export const registry = new MockRegistry(() => db.auth.state.authenticated)
registerModules(registry)
export const match = (method: string, path: string) => registry.match(method, path)
export const handle = (method: string, url: URL, body: unknown, signal?: AbortSignal, delay = 0) =>
  registry.dispatch(method, url, body, signal, delay)

/** Fresh fixture values, owner registrations, scenarios, sockets, and timers. */
export function resetMocks(modes: MockMode[] = []): void {
  events.reset()
  resetUnauthorizedListeners()
  registry.clear()
  resetFakeSockets()
  resetStreams()
  resetIds()
  setModes(modes)
  db.resetData()
  resetFS()
  resetSystem()
  registerModules(registry)
}
