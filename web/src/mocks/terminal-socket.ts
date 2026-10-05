// Synthetic terminal replay/echo. No PTY, process, daemon, or provider is started.
import type { TermClientMsg, TerminalSession } from '../api/types'
import * as db from './data'
import { FakeSocket } from './util'

// ---------------------------------------------------------------- terminal

const ESC = '\x1b['
const c = (code: string, s: string) => `${ESC}${code}m${s}${ESC}0m`

function banner(t: TerminalSession): string {
  const lines = [
    c('2', `[synthetic mock] # ${t.name} — restored from the session daemon`),
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
  private attached = false

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
    this.attached = true
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
    if (this.term && this.attached) {
      this.attached = false
      this.term.clients = Math.max(0, this.term.clients - 1)
    }
    super.end(code, reason)
  }
}
