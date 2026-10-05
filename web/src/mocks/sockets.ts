// Compatibility exports. Socket registrations live with their feature owners.
export { emit, liveNotification, resetStreams } from './event-broker'
export { FakeEvents } from './events-socket'
export { FakeLogs } from './logs-socket'
export { FakeTerminal } from './terminal-socket'

import { FakeSocket } from './util'
/** Explicit unavailable desktop fixture; never live RFB/VNC evidence. */
export class DeadSocket extends FakeSocket {
  constructor(url: string) {
    super(url, { fail: true })
  }
}
