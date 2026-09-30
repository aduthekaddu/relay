// Fake WebSockets: the live events stream, terminal attach and log tail.
import type { ClientEvent, EventType, RelayEvent, TermClientMsg, TerminalSession } from '../api/types'
import * as db from './data'
import { metricsAt } from './system'
import { FakeSocket, has } from './util'

const sockets = new Set<FakeEvents>()

/** Broadcast an event to every open events socket. */
export function emit<T>(type: EventType, data?: T): void {
  const ev: RelayEvent<T> = { type, at: new Date().toISOString(), data }
  for (const s of sockets) s.pushText(ev)
}

/** /api/v1/events */
export class FakeEvents extends FakeSocket {
  private topics = new Set<string>()

  constructor(url: string) {
    super(url, { fail: has('offline') })
  }

  protected override opened(): void {
    sockets.add(this)
    this.pushText({ type: 'hello', at: new Date().toISOString(), data: db.info })
    for (const t of db.terminals)
      this.pushText({ type: 'terminal.updated', at: new Date().toISOString(), data: t })
    this.every(1000, () => {
      if (this.topics.has('metrics'))
        this.pushText({ type: 'metrics', at: new Date().toISOString(), data: metricsAt(Date.now()) })
    })
    // Keep working sessions visibly alive.
    this.every(3500, () => {
      for (const t of db.terminals) {
        if (t.activity !== 'working') continue
        t.lastOutputAt = new Date().toISOString()
        this.pushText({ type: 'terminal.updated', at: t.lastOutputAt, data: t })
      }
    })
    if (has('live')) this.every(20_000, () => liveNotification())
  }

  protected override onClientText(text: string): void {
    let msg: ClientEvent
    try {
      msg = JSON.parse(text) as ClientEvent
    } catch {
      return
    }
    if (msg.type === 'subscribe') for (const t of msg.topics ?? []) this.topics.add(t)
    if (msg.type === 'unsubscribe') for (const t of msg.topics ?? []) this.topics.delete(t)
  }

  override end(code?: number, reason?: string): void {
    sockets.delete(this)
    super.end(code, reason)
  }
}

let liveSeq = 0
/** Push a synthetic notification (also window.__relayMock.notify()). */
export function liveNotification(kind: 'attention' | 'done' = liveSeq % 2 ? 'done' : 'attention'): void {
  const n = {
    id: `n_live${++liveSeq}`,
    kind,
    title: kind === 'attention' ? 'Codex needs you' : 'Claude Code finished',
    body: kind === 'attention' ? 'Approve editing app/limits.py?' : 'Refactor share links — 6 files changed',
    at: new Date().toISOString(),
    read: false,
    link: kind === 'attention' ? '/terminal/t_codex1' : '/terminal/t_claude1',
    agent: kind === 'attention' ? 'codex' : 'claude',
  } as const
  db.notifications.unshift({ ...n })
  emit('notification', n)
}

// ---------------------------------------------------------------- terminal

const ESC = '\x1b['
const c = (code: string, s: string) => `${ESC}${code}m${s}${ESC}0m`

function banner(t: TerminalSession): string {
  const lines = [
    c('2', `# ${t.name} — restored from the session daemon`),
    '',
    `${c('32', 'dev@atlas')} ${c('34', t.cwd.replace(db.HOME, '~'))} ${c('2', '%')} ${t.command.join(' ')}`,
  ]
  if (t.kind === 'agent') {
    lines.push(
      '',
      c('1', `╭─ ${t.agent} ─────────────────────────────────────────────╮`),
      `│ ${c('2', 'cwd')} ${t.cwd.replace(db.HOME, '~')}`,
      c('1', '╰──────────────────────────────────────────────────────╯'),
      '',
      `${c('38;5;209', '●')} Reading ${c('1', 'internal/share/link.go')}`,
      `${c('38;5;209', '●')} Editing ${c('1', 'internal/share/sign.go')} ${c('32', '+42')} ${c('31', '-7')}`,
      '',
      t.attention
        ? `${c('33', '?')} ${t.attention.message ?? 'Waiting for input'} ${c('2', '(y/n)')}`
        : `${c('2', '›')} ${t.preview ?? ''}`,
    )
  } else if (t.preview) {
    lines.push(t.preview)
  }
  return `${lines.join('\r\n')}\r\n`
}

/** /api/v1/terminals/{id}/attach — replays a banner, then echoes input. */
export class FakeTerminal extends FakeSocket {
  private term: TerminalSession | undefined
  private line = ''

  constructor(url: string) {
    super(url)
    const id = decodeURIComponent(new URL(url, location.href).pathname.split('/')[4] ?? '')
    this.term = db.terminals.find((t) => t.id === id)
  }

  protected override opened(): void {
    const t = this.term
    if (!t) {
      this.pushText({ t: 'error', message: 'Session not found' })
      this.end(4404, 'not found')
      return
    }
    t.clients++
    this.pushText({ t: 'hello', session: t, cols: t.cols, rows: t.rows, readOnly: false })
    this.pushText({ t: 'replay-begin' })
    this.pushBinary(banner(t))
    this.pushText({ t: 'replay-end' })
    if (t.activity === 'exited') {
      this.pushText({ t: 'exit', code: t.exitCode ?? 0 })
      return
    }
    if (t.activity === 'working') {
      let n = 0
      this.every(900, () => {
        n++
        this.pushBinary(
          `${c('2', new Date().toLocaleTimeString())} ${t.kind === 'agent' ? `${c('38;5;209', '●')} thinking… step ${n}` : `GET /api/items ${c('32', '200')} ${(4 + (n % 9)).toString()}ms`}\r\n`,
        )
      })
    }
    this.pushBinary(`${c('32', '$')} `)
  }

  protected override onClientText(text: string): void {
    let msg: TermClientMsg
    try {
      msg = JSON.parse(text) as TermClientMsg
    } catch {
      return
    }
    if (msg.t === 'ping') this.pushText({ t: 'pong' })
    if (msg.t === 'resize' && this.term && msg.cols && msg.rows) {
      this.term.cols = msg.cols
      this.term.rows = msg.rows
      this.pushText({ t: 'resize', cols: msg.cols, rows: msg.rows })
    }
  }

  protected override onClientBinary(data: Uint8Array): void {
    const s = new TextDecoder().decode(data)
    for (const ch of s) {
      if (ch === '\r') {
        const cmd = this.line.trim()
        this.line = ''
        this.pushBinary('\r\n')
        if (cmd) this.pushBinary(`${c('2', `mock: ran “${cmd}”`)}\r\n`)
        this.pushBinary(`${c('32', '$')} `)
      } else if (ch === '\x7f') {
        if (this.line) {
          this.line = this.line.slice(0, -1)
          this.pushBinary('\b \b')
        }
      } else if (ch === '\x03') {
        this.line = ''
        this.pushBinary(`^C\r\n${c('32', '$')} `)
      } else if (ch >= ' ') {
        this.line += ch
        this.pushBinary(ch)
      }
    }
  }

  override end(code?: number, reason?: string): void {
    if (this.term && this.readyState === 1) this.term.clients = Math.max(0, this.term.clients - 1)
    super.end(code, reason)
  }
}

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
      this.pushText({ at: new Date(now - (40 - i) * 7000).toISOString(), unit, prio, text })
    }
    let i = 0
    this.every(1600, () => {
      const [prio, unit, text] = LOG_LINES[i++ % LOG_LINES.length]
      this.pushText({ at: new Date().toISOString(), unit, prio, text })
    })
  }
}

/** Sockets without a mock (desktop VNC): fail like an unreachable service. */
export class DeadSocket extends FakeSocket {
  constructor(url: string) {
    super(url, { fail: true })
  }
}
