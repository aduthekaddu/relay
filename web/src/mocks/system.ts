// Moving system metrics and a synthetic process table.
import type { Metrics, Process } from '../api/types'
import { HOME, HOST } from './data'
import { before, DAY, HOUR, MIN, rng } from './util'

const CORES = 8
const GiB = 1024 ** 3
const r = rng(42)

/** Metrics at time `t` (ms): smooth waves + noise, so charts look alive. */
export function metricsAt(t: number): Metrics {
  const s = t / 1000
  const wave = (period: number, phase = 0) => (Math.sin((s / period) * Math.PI * 2 + phase) + 1) / 2
  const noise = () => (r() - 0.5) * 8
  const cpu = Math.max(3, Math.min(97, 22 + wave(90) * 30 + wave(13, 1) * 14 + noise()))
  const perCore = Array.from({ length: CORES }, (_, i) => Math.max(0, Math.min(100, cpu + Math.sin(s / 7 + i) * 18 + (r() - 0.5) * 16)))
  const used = (9.4 + wave(240) * 3.1 + (r() - 0.5) * 0.2) * GiB
  return {
    at: new Date(t).toISOString(),
    cpu: { percent: Math.round(cpu * 10) / 10, perCore: perCore.map((v) => Math.round(v)), cores: CORES, model: 'Synthetic 8-Core @ 3.6GHz', tempC: Math.round(48 + cpu / 4) },
    memory: { total: 32 * GiB, used, available: 32 * GiB - used, cached: 6.2 * GiB, swapTotal: 8 * GiB, swapUsed: (0.6 + wave(600) * 0.4) * GiB },
    disks: [
      { mount: '/', fs: 'ext4', total: 512 * GiB, used: 318.4 * GiB, free: 193.6 * GiB, readBps: Math.max(0, wave(20) * 12e6 + noise() * 1e5), writeBps: Math.max(0, wave(30, 2) * 6e6 + noise() * 1e5) },
      { mount: '/mnt/data', fs: 'xfs', total: 2048 * GiB, used: 1710 * GiB, free: 338 * GiB, readBps: wave(60) * 2e6, writeBps: wave(45) * 1e6 },
    ],
    net: { rxBps: Math.max(0, wave(17) * 2.4e6 + noise() * 2e4), txBps: Math.max(0, wave(23, 1) * 0.8e6 + noise() * 1e4), rxTotal: 812 * GiB, txTotal: 144 * GiB },
    gpu: [{ name: 'Synthetic GPU 16GB', util: Math.round(wave(50) * 40), memUsed: 3.1 * GiB, memTotal: 16 * GiB, tempC: 51 }],
    uptimeSec: 12 * DAY + 5 * HOUR + Math.floor(s % 3600),
    load: [Math.round((cpu / 100) * CORES * 100) / 100, Math.round((cpu / 110) * CORES * 100) / 100, Math.round((cpu / 125) * CORES * 100) / 100],
    procs: 412,
    host: { hostname: HOST, os: 'Ubuntu 24.04 LTS', kernel: '6.8.0-45-generic', arch: 'x86_64', virt: 'kvm' },
  }
}

/** One sample every 10 s for the last `minutes`. */
export function history(minutes: number): Metrics[] {
  const n = Math.min(360, Math.max(1, Math.round((minutes * 60) / 10)))
  const now = Date.now()
  return Array.from({ length: n }, (_, i) => metricsAt(now - (n - 1 - i) * 10_000))
}

const NAMES: Array<[string, string, boolean?]> = [
  ['relay', 'relay serve', true],
  ['relay', 'relay ptyd', true],
  ['node', 'node /home/dev/code/relay-demo/node_modules/.bin/vite'],
  ['claude', 'claude'],
  ['codex', 'codex'],
  ['gemini', 'node /home/dev/.local/bin/gemini'],
  ['zsh', '-zsh'],
  ['uvicorn', 'python -m uvicorn app.main:app --reload --port 8000'],
  ['astro', 'node astro dev --port 4321'],
  ['code-server', 'code-server --socket /run/user/1000/relay/code.sock'],
  ['redis-server', 'redis-server 127.0.0.1:6379'],
  ['syncthing', 'syncthing serve --no-browser'],
  ['ollama', 'ollama serve'],
  ['gopls', 'gopls -mode=stdio'],
  ['tsserver', 'node tsserver.js --useInferredProjectPerProjectRoot'],
  ['esbuild', 'esbuild --service=0.25.0 --ping'],
  ['rg', 'rg --json -i share'],
  ['systemd', '/lib/systemd/systemd --user', true],
  ['dbus-daemon', 'dbus-daemon --session', true],
  ['pipewire', '/usr/bin/pipewire'],
]

export const processes: Process[] = NAMES.map(([name, cmd, prot], i) => ({
  pid: 1000 + i * 37 + (i > 2 ? 46000 : 0),
  ppid: i < 2 ? 1 : 1000,
  name,
  cmd: cmd.replace('/home/dev', HOME),
  user: 'dev',
  cpu: Math.round((i < 6 ? 18 - i * 2.5 : 1.8 / (i - 4)) * 10) / 10,
  rss: Math.round((i < 6 ? 420 - i * 50 : 90 / (i - 4)) * 1024 * 1024),
  memPct: Math.round((i < 6 ? 1.4 - i * 0.15 : 0.3 / (i - 4)) * 100) / 100,
  state: i % 5 === 0 ? 'R' : 'S',
  threads: 1 + ((i * 7) % 24),
  startedAt: before((i + 1) * 40 * MIN),
  terminal: ['claude', 'codex', 'gemini', 'zsh'].includes(name) ? `t_${name}` : undefined,
  protected: !!prot,
}))
