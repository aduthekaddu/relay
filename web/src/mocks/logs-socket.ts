// Synthetic journal text. No host journal/file is read.
import { FakeSocket } from './util'

// ---------------------------------------------------------------- logs

const LOG_LINES = [
  [6, 'relay.service', 'http: GET /api/v1/terminals 200 1.2ms'],
  [6, 'relay.service', 'live: client connected (Chrome on macOS)'],
  [4, 'relay.service', 'previews: port 4321 appeared (astro)'],
  [6, 'relay-ptyd.service', 'session t_codex1: output 4.1 KiB'],
  [3, 'ollama.service', 'error: failed to bind 127.0.0.1:11434: address in use'],
  [6, 'relay.service', 'notify: pushed attention to 2 devices'],
] as const

/** /api/v1/system/logs — a slow trickle of synthetic journal lines. */
export class FakeLogs extends FakeSocket {
  protected override opened(): void {
    const now = Date.now()
    for (let i = 0; i < 40; i++) {
      const [prio, unit, text] = LOG_LINES[i % LOG_LINES.length]
      this.pushText({
        at: new Date(now - (40 - i) * 7000).toISOString(),
        unit,
        prio,
        text: `[synthetic] ${text}`,
      })
    }
    let i = 0
    this.every(1600, () => {
      const [prio, unit, text] = LOG_LINES[i++ % LOG_LINES.length]
      this.pushText({ at: new Date().toISOString(), unit, prio, text: `[synthetic] ${text}` })
    })
  }
}
