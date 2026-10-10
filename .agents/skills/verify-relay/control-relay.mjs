#!/usr/bin/env node
// control-relay: launch, check, drive and clean up an isolated relay instance,
// and record evidence bundles under .proof/. Node 20+, no dependencies.
// Every command prints one JSON object on stdout. Run with --help for examples.
// Edit CONFIG for this repo; keep the rest generic so upkeep stays cheap.

import { spawn, spawnSync } from 'node:child_process'
import crypto from 'node:crypto'
import fs from 'node:fs'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { createRequire } from 'node:module'

const CONFIG = {
  repo: 'relay',
  // Short prefix for run directories and tmux sessions, lowercase letters only, e.g. "rv".
  abbr: 'rv',
  // Inclusive loopback port range reserved for verification, e.g. "47700-47799".
  portRange: '47700-47799',
  // Optional build run once by `up` in the repo root (sh -c). Empty skips it.
  buildCmd: 'make web && go build -trimpath -o bin/relay-verify ./cmd/relay',
  // Long-running processes `up` starts, in order. In cmd and buildCmd, {port} {home} {run}
  // {repo} become quoted references to CONTROL_PORT, CONTROL_HOME, CONTROL_RUN and
  // CONTROL_REPO, so paths with spaces or shell characters stay one argument; write them
  // bare or inside double quotes, never inside single quotes. In env values they are
  // substituted as plain text. ready: { http: '/path' } | { tcp: true } | { log: 'regex' }.
  // pidFile: a file (relative to {home}) where a daemon the service detaches writes its pid;
  // the tool adopts that pid so `down` stops it too. expect: text that must appear in the
  // process command line before the tool will signal it.
  //
  // serve detaches ptyd with setsid. The lock file holds that pid. `down` and `restart`
  // adopt it. The shell prelude writes relay.toml so this run does not browse the
  // account home, scan agent transcripts, or start the desktop or code-server.
  // On macOS /tmp is a symlink. The files root is the physical scratch path.
  services: [
    {
      name: 'serve',
      cmd: 'sh -c \'mkdir -p "$CONTROL_HOME/config" "$CONTROL_HOME/scratch" && umask 077 && scratch=$(cd "$CONTROL_HOME/scratch" && pwd -P) && cat > "$CONTROL_HOME/config/relay.toml" <<EOF\n[server]\nlisten = "127.0.0.1:$CONTROL_PORT"\n\n[files]\nroot = "$scratch"\n\n[agents]\nworkspace_roots = ["$scratch"]\nhooks = false\nindex_history = false\n\n[desktop]\nenabled = false\n\n[code]\nenabled = false\n\n[previews]\nmode = "off"\nEOF\nexec bin/relay-verify serve --debug\'',
      env: { RELAY_LISTEN: '127.0.0.1:{port}', RELAY_DEV: '1' },
      ready: { http: '/api/v1/health' },
      pidFile: 'run/ptyd.lock',
      expect: 'relay-verify',
    },
  ],
  // Env var that holds the isolated data directory; set for services and `term`.
  homeEnv: 'RELAY_HOME',
  // Extra env for `term run` and `term tmux` (for example how the CLI finds the server).
  cliEnv: { RELAY_SOCKET: '{home}/run/relay.sock' },
  // Inherited env vars that point at the user's real instance (comma separated).
  // If any is set, up/api/browser/term refuse to run.
  realInstanceEnv: 'RELAY_SOCKET,RELAY_SESSION,RELAY_CONFIG',
  // Freshness: the build artifact must be newer than every file under these sources.
  freshness: { artifact: 'bin/relay-verify', sources: 'cmd,internal,web/src' },
  // Paths that must exist for the real UI rather than a placeholder (comma separated).
  requiredAssets: 'internal/web/dist/assets',
  // Folders (relative to the repo) that may hold playwright-core for `browser`.
  playwrightFrom: ['.', 'scripts/dev', 'web'],
  readyTimeoutMs: 30000,
  // Unix socket paths are limited (about 104 bytes on macOS), so runs live under a short root.
  runRoot: '/tmp',
}

const KIT = 'kaddu-kit' // fixed id required by the proof manifest schema
const EXIT = { ok: 0, failed: 1, usage: 2, refused: 3, unavailable: 4 }
const BOOL_FLAGS = new Set(['help', 'dry-run', 'ok', 'fail', 'readback', 'pty', 'enter', 'keep-home', 'mobile', 'full'])

// ---------- small helpers ----------

function isPlaceholder(v) {
  return typeof v === 'string' && /\{\{[a-z_]+\}\}/.test(v)
}
function cfg(v) {
  return isPlaceholder(v) ? '' : v
}
function list(v) {
  return cfg(v).split(',').map((s) => s.trim()).filter(Boolean)
}
function out(obj, code = EXIT.ok) {
  // Everything printed passes through redact(), including error tails and log excerpts.
  process.stdout.write(JSON.stringify(redact(obj), null, 2) + '\n')
  process.exit(code)
}
function fail(error, code = EXIT.failed, extra = {}) {
  out({ ok: false, error, ...extra }, code)
}
function parseArgs(argv) {
  const pos = []
  const opts = {}
  let rest = []
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]
    if (a === '--') {
      rest = argv.slice(i + 1)
      break
    }
    if (a.startsWith('--')) {
      const eq = a.indexOf('=')
      const key = eq > 0 ? a.slice(2, eq) : a.slice(2)
      let val
      if (eq > 0) val = a.slice(eq + 1)
      else if (BOOL_FLAGS.has(key)) val = true
      else if (i + 1 < argv.length) val = argv[++i]
      else fail(`--${key} needs a value`, EXIT.usage)
      if (key in opts) opts[key] = [].concat(opts[key], val)
      else opts[key] = val
    } else pos.push(a)
  }
  return { pos, opts, rest }
}
function one(v) {
  return Array.isArray(v) ? v[v.length - 1] : v
}
function many(v) {
  return v === undefined ? [] : [].concat(v)
}
function run(cmd, args, options = {}) {
  const r = spawnSync(cmd, args, { encoding: 'utf8', ...options })
  return { code: r.status, stdout: r.stdout || '', stderr: r.stderr || '', error: r.error }
}
function have(bin) {
  return run('/bin/sh', ['-c', `command -v ${bin}`]).code === 0
}
function gitIn(repo, args, options = {}) {
  return spawnSync('git', ['-C', repo, ...args], { maxBuffer: 1 << 30, ...options })
}
function gitText(repo, args) {
  const r = gitIn(repo, args, { encoding: 'utf8' })
  return r.status === 0 ? r.stdout.trim() : ''
}
function sub(s, vars) {
  return String(s).replace(/\{(port|home|run|repo)\}/g, (_, k) => String(vars[k]))
}
// For sh -c strings: replace {port} {home} {run} {repo} with references to CONTROL_* env
// vars, quoted outside double quotes, so no value is ever parsed by the shell.
function shSub(cmd) {
  const s = String(cmd)
  let res = ''
  let q = null
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (c === '\\' && q !== "'") {
      res += c + (s[i + 1] ?? '')
      i++
      continue
    }
    if (c === "'" && q !== '"') q = q === "'" ? null : "'"
    else if (c === '"' && q !== "'") q = q === '"' ? null : '"'
    else {
      const m = /^\{(port|home|run|repo)\}/.exec(s.slice(i))
      if (m) {
        if (q === "'") throw new Error(`{${m[1]}} cannot be used inside single quotes in: ${s}`)
        const ref = `\${CONTROL_${m[1].toUpperCase()}}`
        res += q === '"' ? ref : `"${ref}"`
        i += m[0].length - 1
        continue
      }
    }
    res += c
  }
  return res
}
function controlEnv(vars) {
  return { CONTROL_PORT: String(vars.port), CONTROL_HOME: vars.home, CONTROL_RUN: vars.run, CONTROL_REPO: vars.repo }
}
function nowStamp() {
  return new Date().toISOString().replace(/[-:]/g, '').replace(/\.\d+Z$/, 'Z')
}
function readJSON(file, fallback = null) {
  try {
    return JSON.parse(fs.readFileSync(file, 'utf8'))
  } catch {
    return fallback
  }
}
function writeJSON(file, obj) {
  fs.mkdirSync(path.dirname(file), { recursive: true })
  fs.writeFileSync(file, JSON.stringify(obj, null, 2) + '\n')
}

// ---------- secrets ----------

const SECRET_KEY = /^(.*[_-])?(token|secret|password|passwd|authorization|cookie|api[_-]?key|access[_-]?key|private[_-]?key)$/i
// Redaction mirrors the proof checker's own redact(), rule for rule: known
// token shapes are replaced whole by ***, then credential values after a key (KEY=value,
// KEY: value, KEY value, KEY is value) become ***, keeping the key. Prose such as
// "password reset" or "password=false" is kept. The template's tests feed it the checker's
// published samples and run the checker's scanner over the result.
const TOKEN_PATTERNS = [
  /-----BEGIN (?:[A-Z]+ )?PRIVATE KEY(?: BLOCK)?-----/g,
  /\b(?:AKIA|ASIA)[0-9A-Z]{16}\b/g,
  /\bgh[pousr]_[A-Za-z0-9]{36,}\b/g,
  /\bgithub_pat_[A-Za-z0-9_]{50,}\b/g,
  /\bglpat-[A-Za-z0-9_-]{20,}/g,
  /\bnpm_[A-Za-z0-9]{36}\b/g,
  /\bsk-ant-[A-Za-z0-9_-]{20,}/g,
  /\bsk-(?:proj-|svcacct-|admin-)?[A-Za-z0-9_-]{20,}/g,
  /\bxai-[A-Za-z0-9]{40,}/g,
  /\bAIza[0-9A-Za-z_-]{35}\b/g,
  /\bxox[baprs]-[A-Za-z0-9-]{10,}/g,
  /\b(?:sk|rk)_live_[A-Za-z0-9]{20,}/g,
  /\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}/g,
  /\bauthorization\s*:\s*(?:bearer|basic|token)\s+[A-Za-z0-9._~+/=-]{8,}/gi,
  /\b[a-z][a-z0-9+.-]*:\/\/[^\s/:@]+:[^\s/@]{3,}@/g,
]
const PRIVATE_KEY_BLOCK = /-----BEGIN [A-Z ]*PRIVATE KEY[A-Z ]*-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY[A-Z ]*-----/g
const KEY_NAMES = 'password|passwd|pwd|passphrase|secret|secret[_-]?key|client[_-]?secret|token|access[_-]?token|auth[_-]?token|refresh[_-]?token|api[_-]?key|access[_-]?key|private[_-]?key'
const KEY_RX = `(?<![A-Za-z0-9])(?<key>${KEY_NAMES})(?![A-Za-z0-9_])`
const ASSIGN_QUOTED = new RegExp(KEY_RX + `(?<sep>["']?\\s*[:=]\\s*)(?<val>"[^"\\n]*"|'[^'\\n]*')`, 'gid')
const ASSIGN_TIGHT = new RegExp(KEY_RX + `(?<sep>["']?\\s*=\\s*|:)(?<val><[^>\\n]*>|[^\\s"',;]+)`, 'gid')
const ASSIGN_LOOSE = new RegExp(KEY_RX + `(?<sep>["']?:\\s+|\\s+is\\s+|\\s+)(?<val><[^>\\n]*>|[^\\s"',;]+)`, 'gid')
const BEARER = /\b(?<key>bearer|basic)(?<sep>\s+)(?<val>[A-Za-z0-9._~+/=-]{16,})/gid
const NOT_SECRET = new Set(['false', 'true', 'null', 'none', 'nil', 'undefined', 'required', 'optional', 'yes', 'no', 'empty', 'unset', 'redacted', 'hidden', 'masked', 'string', 'str', 'bool', 'int', 'env'])
const PLACEHOLDER = /^(?:\*+|\$.*|<.*>|%s|\{\{.*\}\}|\{.*\}|x{4,}|\.\.\.)$/i
function stripQuotes(v) {
  return v.length >= 2 && v[0] === v[v.length - 1] && `"'`.includes(v[0]) ? v.slice(1, -1) : v
}
function isValuePlaceholder(v) {
  const s = stripQuotes(v).trim()
  return !s || NOT_SECRET.has(s.toLowerCase()) || PLACEHOLDER.test(s)
}
function looksSecret(value, minLen = 6) {
  const v = stripQuotes(value)
  if (isValuePlaceholder(v) || v.length < minLen) return false
  if (TOKEN_PATTERNS.some((re) => new RegExp(re.source, re.flags.replace('g', '')).test(v))) return true
  const alpha = /\p{L}/u.test(v)
  const digit = /\p{N}/u.test(v)
  const symbol = /[!@#$%^&*+=?~|]/.test(v)
  return (alpha && (digit || symbol)) || (v.length >= 24 && /^[\p{L}\p{N}]+$/u.test(v.replace(/[_-]/g, '')))
}
function assignmentSpans(line, minTight, minLoose) {
  const spans = []
  const val = (m) => m.indices.groups.val
  for (const m of line.matchAll(ASSIGN_QUOTED)) if (!isValuePlaceholder(m.groups.val) && stripQuotes(m.groups.val).length >= minTight) spans.push(val(m))
  for (const m of line.matchAll(ASSIGN_TIGHT)) {
    const v = m.groups.val
    if (!isValuePlaceholder(v) && v.length >= minTight && (minTight < 8 || looksSecret(v, minTight))) spans.push(val(m))
  }
  for (const m of line.matchAll(ASSIGN_LOOSE)) if (looksSecret(m.groups.val, minLoose)) spans.push(val(m))
  for (const m of line.matchAll(BEARER)) if (looksSecret(m.groups.val, 16) || /\d/.test(m.groups.val)) spans.push(val(m))
  return spans
}
function replaceSpans(line, spans) {
  const sorted = [...new Map(spans.map(([s, e]) => [`${s}:${e}`, [s, e]])).values()].sort((a, b) => a[0] - b[0] || a[1] - b[1])
  let res = ''
  let last = 0
  for (const [s, e] of sorted) {
    if (s < last) continue
    res += line.slice(last, s) + '***'
    last = e
  }
  return res + line.slice(last)
}
function redactLine(line) {
  for (const re of TOKEN_PATTERNS) line = line.replace(re, '***')
  return replaceSpans(line, assignmentSpans(line, 3, 6))
}
function redactText(s) {
  return String(s).replace(PRIVATE_KEY_BLOCK, '***').split('\n').map(redactLine).join('\n')
}
function redact(v) {
  if (Array.isArray(v)) return v.map(redact)
  if (v && typeof v === 'object') {
    const o = {}
    for (const [k, x] of Object.entries(v)) o[k] = SECRET_KEY.test(k) && x != null && x !== '' ? '<REDACTED>' : redact(x)
    return o
  }
  return typeof v === 'string' ? redactText(v) : v
}

// ---------- repo, state and runs ----------

function repoRoot(opts) {
  const start = path.resolve(one(opts.repo) || process.cwd())
  const top = gitText(start, ['rev-parse', '--show-toplevel'])
  return top || start
}
function repoName(root) {
  return cfg(CONFIG.repo) || path.basename(root)
}
function stateDir(root) {
  return path.join(root, '.proof', '.control')
}
// A run id is the only input used to locate a run directory. Paths stored in the state
// file are informational and never trusted for deletion or writes.
const RUN_ID = /^[a-z0-9]{6,}$/
const MARKER = '.control-run'
function runPaths(id) {
  if (!RUN_ID.test(String(id))) throw new Error(`invalid run id: ${JSON.stringify(id)}`)
  const runDir = path.join(CONFIG.runRoot, id)
  return { runDir, home: path.join(runDir, 'home'), logs: path.join(runDir, 'logs') }
}
function loadRun(root, opts) {
  const dir = stateDir(root)
  const id = one(opts.run) || (fs.existsSync(path.join(dir, 'current')) ? fs.readFileSync(path.join(dir, 'current'), 'utf8').trim() : '')
  if (!id) return null
  if (!RUN_ID.test(id)) fail(`invalid run id ${JSON.stringify(id)} in ${path.join(dir, 'current')}; refusing to use it`, EXIT.usage)
  const state = readJSON(path.join(dir, `${id}.json`))
  if (!state) return null
  if (state.runId !== id) fail(`state file for ${id} names a different run (${JSON.stringify(state.runId)}); refusing to use it`, EXIT.usage)
  return { ...state, ...runPaths(id) }
}
function latestRun(root) {
  let files = []
  try {
    files = fs.readdirSync(stateDir(root)).filter((f) => /^[a-z0-9]{6,}\.json$/.test(f))
  } catch {
    return null
  }
  const runs = files.map((f) => [f, readJSON(path.join(stateDir(root), f))]).filter(([f, r]) => r && RUN_ID.test(String(r.runId)) && `${r.runId}.json` === f).map(([, r]) => r)
  runs.sort((a, b) => String(b.startedAt).localeCompare(String(a.startedAt)))
  return runs[0] || null
}
// The run directory may be removed only when it is a real directory (not a symlink) whose
// canonical path is a direct child of the canonical run root and which holds the marker
// `up` wrote for this run id. Returns null when removal is safe, else the reason.
function unsafeToRemove(id) {
  let runDir
  try {
    ;({ runDir } = runPaths(id))
  } catch (e) {
    return e.message
  }
  let st
  try {
    st = fs.lstatSync(runDir)
  } catch {
    return 'missing'
  }
  if (st.isSymbolicLink()) return `${runDir} is a symlink`
  if (!st.isDirectory()) return `${runDir} is not a directory`
  const realRoot = fs.realpathSync(CONFIG.runRoot)
  const real = fs.realpathSync(runDir)
  if (path.dirname(real) !== realRoot || path.basename(real) !== id) return `${real} is not a direct child of ${realRoot}`
  const marker = readJSON(path.join(runDir, MARKER))
  if (!marker || marker.runId !== id) return `${runDir} has no ownership marker for run ${id}`
  return null
}
function saveRun(root, state) {
  writeJSON(path.join(stateDir(root), `${state.runId}.json`), state)
}
function inheritedRealEnv() {
  return list(CONFIG.realInstanceEnv).filter((k) => process.env[k] !== undefined)
}
function refuseIfRealEnv(opts) {
  const set = inheritedRealEnv()
  if (set.length && !opts['dry-run']) {
    const unset = set.map((k) => `-u ${k}`).join(' ')
    fail(`refusing: ${set.join(', ')} is set and points at the real instance`, EXIT.refused, {
      fix: `env ${unset} node ${path.relative(process.cwd(), process.argv[1]) || process.argv[1]} ${process.argv.slice(2).join(' ')}`,
    })
  }
  return set
}
function cleanEnv(extra) {
  const env = { ...process.env }
  for (const k of list(CONFIG.realInstanceEnv)) delete env[k]
  return { ...env, ...extra }
}
// Returns null when ps prints nothing: the process is gone, or ps is unavailable or denied.
// Callers must not read null as "dead"; see identify().
function procInfo(pid) {
  const lstart = run('ps', ['-o', 'lstart=', '-p', String(pid)]).stdout.trim()
  if (!lstart) return null
  const command = run('ps', ['-o', 'command=', '-p', String(pid)]).stdout.trim()
  const pgid = Number(run('ps', ['-o', 'pgid=', '-p', String(pid)]).stdout.trim()) || 0
  return { lstart, command, pgid }
}
// True when lsof shows the process holding a file inside this run's directory (its log,
// home, socket or lock). The run directory has a random name, so this proves ownership
// when ps cannot.
function holdsRunFile(pid, runDir) {
  if (!runDir || !have('lsof')) return false
  let real
  try {
    real = fs.realpathSync(runDir)
  } catch {
    return false
  }
  const r = run('lsof', ['-nP', '-p', String(pid), '-Fn'])
  return r.stdout.split('\n').some((l) => l.startsWith('n') && (l.slice(1) === real || l.slice(1).startsWith(real + path.sep)))
}
function alive(pid) {
  try {
    process.kill(pid, 0)
    return true
  } catch (e) {
    return e.code === 'EPERM'
  }
}
// Decide whether pid still belongs to this run. same: true only with proof: the start time
// and command recorded at launch (ps), or an open file inside the run directory (lsof).
// gone: true only when the process does not exist. Anything else is unproven, and callers
// neither signal it nor report it as stopped.
function sameProcess(rec, runDir) {
  const now = procInfo(rec.pid)
  if (now && rec.lstart) {
    if (now.lstart !== rec.lstart) return { same: false, why: 'pid reused (start time differs)' }
    if (rec.expect && !now.command.includes(rec.expect)) return { same: false, why: `command lacks "${rec.expect}"` }
    return { same: true, pgid: now.pgid, how: 'ps' }
  }
  if (!alive(rec.pid)) return { same: false, gone: true, why: 'not running' }
  if (holdsRunFile(rec.pid, runDir)) return { same: true, pgid: rec.group ? rec.pid : 0, how: 'lsof' }
  return { same: false, why: 'alive, but its identity cannot be proven (ps gave no start time and lsof shows no file of this run)' }
}
function portRange() {
  const m = /^(\d+)\s*-\s*(\d+)$/.exec(cfg(CONFIG.portRange))
  return m ? [Number(m[1]), Number(m[2])] : null
}
function portFree(port) {
  return new Promise((resolve) => {
    const srv = net.createServer()
    srv.once('error', () => resolve(false))
    srv.listen(port, '127.0.0.1', () => srv.close(() => resolve(true)))
  })
}
async function pickPort() {
  const r = portRange()
  if (!r) return null
  const [lo, hi] = r
  const span = hi - lo + 1
  const start = crypto.randomInt(span)
  for (let i = 0; i < span; i++) {
    const p = lo + ((start + i) % span)
    if (await portFree(p)) return p
  }
  return null
}
async function poll(check, timeoutMs, label) {
  const until = Date.now() + timeoutMs
  let wait = 100
  for (;;) {
    const r = await check()
    if (r) return r
    if (Date.now() > until) throw new Error(`timed out after ${timeoutMs} ms waiting for ${label}`)
    await new Promise((res) => setTimeout(res, wait))
    wait = Math.min(wait * 2, 1000)
  }
}
function tail(file, n = 20) {
  try {
    return fs.readFileSync(file, 'utf8').split('\n').slice(-n).join('\n')
  } catch {
    return ''
  }
}

// ---------- up / doctor / down ----------

async function cmdUp(root, opts) {
  const blocked = refuseIfRealEnv(opts)
  const missing = ['portRange'].filter((k) => !cfg(CONFIG[k]))
  for (const s of CONFIG.services) if (!cfg(s.cmd)) missing.push(`services.${s.name}.cmd`)
  if (missing.length) fail(`CONFIG is not filled in: ${missing.join(', ')}`, EXIT.usage)
  const existing = loadRun(root, {})
  if (existing && existing.services.length && existing.services.every((s) => sameProcess(s, existing.runDir).same)) {
    out({ ok: true, already_up: true, run: existing.runId, port: existing.port, home: existing.home, url: `http://127.0.0.1:${existing.port}` })
  }
  const runId = `${cfg(CONFIG.abbr) || 'cv'}${crypto.randomBytes(4).toString('hex')}`
  const { runDir, home, logs } = runPaths(runId)
  if (opts['dry-run']) {
    out({
      ok: true,
      dry_run: true,
      would: {
        refuse_because: blocked,
        port_from: cfg(CONFIG.portRange),
        run_dir: runDir,
        env: controlEnv({ port: '<port>', home, run: runDir, repo: root }),
        build: cfg(CONFIG.buildCmd) ? shSub(CONFIG.buildCmd) : null,
        services: CONFIG.services.map((s) => ({ name: s.name, cmd: `exec ${shSub(cfg(s.cmd))}`, ready: s.ready })),
      },
    })
  }
  const port = await pickPort()
  if (!port) fail(`no free port in ${cfg(CONFIG.portRange)}`)
  // Create the run directory exclusively (never reuse or follow an existing path), then
  // mark it as ours; `down` removes only a marked direct child of the run root.
  fs.mkdirSync(CONFIG.runRoot, { recursive: true })
  fs.mkdirSync(runDir, { mode: 0o700 })
  writeJSON(path.join(runDir, MARKER), { runId, repo: root, createdAt: new Date().toISOString() })
  fs.mkdirSync(logs)
  fs.mkdirSync(home)
  const vars = { port, home, run: runDir, repo: root }
  const baseEnv = cleanEnv({ ...controlEnv(vars), ...(cfg(CONFIG.homeEnv) ? { [CONFIG.homeEnv]: home } : {}) })
  const state = { runId, repo: root, runDir, home, port, startedAt: new Date().toISOString(), services: [], tmux: [] }
  fs.mkdirSync(stateDir(root), { recursive: true })
  saveRun(root, state)
  fs.writeFileSync(path.join(stateDir(root), 'current'), runId)
  if (cfg(CONFIG.buildCmd)) {
    const log = path.join(logs, 'build.log')
    const r = spawnSync('/bin/sh', ['-c', shSub(CONFIG.buildCmd)], { cwd: root, env: baseEnv, encoding: 'utf8', maxBuffer: 1 << 28 })
    fs.writeFileSync(log, redactText((r.stdout || '') + (r.stderr || '')))
    if (r.status !== 0) fail('build failed', EXIT.failed, { run: runId, log, tail: tail(log), hint: 'run "down" to remove the run directory' })
    state.build = { cmd: CONFIG.buildCmd, log, head: gitText(root, ['rev-parse', 'HEAD']) }
  }
  for (const s of CONFIG.services) {
    const log = path.join(logs, `${s.name}.log`)
    const fd = fs.openSync(log, 'a')
    const env = { ...baseEnv }
    for (const [k, v] of Object.entries(s.env || {})) env[k] = sub(v, vars)
    const child = spawn('/bin/sh', ['-c', `exec ${shSub(s.cmd)}`], { cwd: root, env, detached: true, stdio: ['ignore', fd, fd] })
    let exited = null
    child.on('exit', (code, sig) => (exited = { code, sig }))
    child.unref()
    // detached: the child leads its own process group, so pgid equals its pid.
    const rec = { name: s.name, pid: child.pid, lstart: '', command: '', expect: cfg(s.expect), log, adopted: false, group: true }
    state.services.push(rec)
    saveRun(root, state)
    fs.writeFileSync(path.join(stateDir(root), 'current'), runId)
    rec.lstart = procInfo(child.pid)?.lstart || ''
    try {
      await poll(async () => {
        if (exited) throw new Error(`${s.name} exited early (${JSON.stringify(exited)})`)
        return isReady(s.ready || {}, port, log)
      }, CONFIG.readyTimeoutMs, `${s.name} to be ready`)
    } catch (e) {
      saveRun(root, state)
      fail(e.message, EXIT.failed, { run: runId, log, tail: redactText(tail(log)), hint: `run "down" to stop what started` })
    }
    rec.command = procInfo(child.pid)?.command || ''
    if (s.pidFile) {
      const pidPath = path.join(home, sub(s.pidFile, vars))
      const pid = Number((fs.existsSync(pidPath) ? fs.readFileSync(pidPath, 'utf8') : '').trim().split(/\s+/)[0])
      if (pid > 1 && alive(pid)) {
        const info = procInfo(pid)
        state.services.push({ name: `${s.name}-daemon`, pid, lstart: info?.lstart || '', command: info?.command || '', expect: '', log, adopted: true, group: false })
      }
    }
    saveRun(root, state)
  }
  out({ ok: true, run: runId, port, home, url: `http://127.0.0.1:${port}`, services: state.services.map(({ name, pid, adopted }) => ({ name, pid, adopted })), logs })
}

async function isReady(ready, port, log) {
  if (ready.http && !isPlaceholder(ready.http)) {
    try {
      const r = await fetch(`http://127.0.0.1:${port}${ready.http}`, { signal: AbortSignal.timeout(1000), redirect: 'manual' })
      return r.status < 500
    } catch {
      return false
    }
  }
  if (ready.log) return new RegExp(ready.log).test(tail(log, 200))
  return !(await portFree(port))
}

function newestMtime(dir) {
  let newest = 0
  const walk = (d) => {
    let entries = []
    try {
      entries = fs.readdirSync(d, { withFileTypes: true })
    } catch {
      return
    }
    for (const e of entries) {
      if (e.name === 'node_modules' || e.name === '.git' || e.name === 'dist') continue
      const p = path.join(d, e.name)
      if (e.isDirectory()) walk(p)
      else newest = Math.max(newest, fs.statSync(p).mtimeMs)
    }
  }
  walk(dir)
  return newest
}

function doctorChecks(root) {
  const checks = []
  const add = (name, ok, detail, level = 'fail') => checks.push({ name, ok, level: ok ? 'pass' : level, detail })
  const major = Number(process.versions.node.split('.')[0])
  add('node', major >= 20, `node ${process.versions.node} (need 20+)`)
  const unfilled = [...new Set(JSON.stringify(CONFIG).match(/\{\{[a-z_]+\}\}/g) || [])]
  add('config', unfilled.length === 0, unfilled.length ? `unfilled placeholders (use '' for unused ones): ${unfilled.join(', ')}` : 'filled')
  const env = inheritedRealEnv()
  add('inherited_env', env.length === 0, env.length ? `${env.join(', ')} set: it points at the real instance; unset it` : 'none of the real-instance vars are set')
  const ignored = gitIn(root, ['check-ignore', '-q', '.proof/x']).status === 0
  add('proof_ignored', ignored, ignored ? '.proof/ is gitignored' : '.proof/ is not gitignored; add it before recording evidence', 'warn')
  const art = cfg(CONFIG.freshness.artifact)
  let stale = null
  if (art) {
    const a = path.join(root, art)
    const srcNewest = Math.max(0, ...list(CONFIG.freshness.sources).map((d) => newestMtime(path.join(root, d))))
    stale = !fs.existsSync(a) || fs.statSync(a).mtimeMs < srcNewest
    add('build_fresh', !stale, stale ? `${art} is missing or older than its sources; rebuild` : `${art} is newer than its sources`)
  }
  const assets = list(CONFIG.requiredAssets)
  if (assets.length) {
    const absent = assets.filter((p) => !fs.existsSync(path.join(root, p)))
    add('assets', absent.length === 0, absent.length ? `missing (placeholder UI likely): ${absent.join(', ')}` : 'present')
  }
  const chrome = chromePath()
  add('chrome', fs.existsSync(chrome), `${chrome}${process.env.CHROME ? ' (from CHROME)' : ''}`, 'info')
  return { checks, stale }
}

async function cmdDoctor(root, opts) {
  if (opts['dry-run']) {
    out({
      ok: true,
      dry_run: true,
      repo: root,
      would_check: ['node', 'config', 'inherited_env', 'proof_ignored', 'build_fresh', 'assets', 'chrome', 'owned_processes', 'port_owner'],
      config: { repo: repoName(root), portRange: cfg(CONFIG.portRange) || null, services: CONFIG.services.map((s) => s.name), realInstanceEnv: list(CONFIG.realInstanceEnv) },
    })
  }
  const { checks } = doctorChecks(root)
  const state = loadRun(root, opts)
  if (!state) checks.push({ name: 'run', ok: true, level: 'info', detail: 'no run is up' })
  else {
    for (const s of state.services) {
      const r = sameProcess(s, state.runDir)
      checks.push({ name: `process:${s.name}`, ok: r.same, level: r.same ? 'pass' : 'fail', detail: r.same ? `pid ${s.pid} is ours (${r.how})` : `pid ${s.pid}: ${r.why}` })
    }
    const listening = !(await portFree(state.port))
    let owner = 'unknown (lsof missing)'
    let ours = listening
    if (have('lsof')) {
      const pids = run('lsof', ['-nP', `-iTCP:${state.port}`, '-sTCP:LISTEN', '-t']).stdout.split(/\s+/).filter(Boolean).map(Number)
      const mine = new Set(state.services.map((s) => s.pid))
      ours = pids.length > 0 && pids.every((p) => mine.has(p) || mine.has(procInfo(p)?.pgid))
      owner = pids.join(',') || 'nobody'
    }
    checks.push({ name: 'port_owner', ok: listening && ours, level: listening && ours ? 'pass' : 'fail', detail: `port ${state.port} listener: ${owner}` })
  }
  const ok = checks.every((c) => c.ok || c.level !== 'fail')
  out({ ok, repo: root, run: state?.runId || null, checks }, ok ? EXIT.ok : EXIT.failed)
}

async function cmdDown(root, opts) {
  const state = loadRun(root, opts)
  if (!state) out({ ok: true, nothing_to_do: true, detail: 'no run is up' })
  // Stop started services newest first, then the daemons they detached.
  const order = [...state.services].reverse().sort((a, b) => Number(a.adopted) - Number(b.adopted))
  const plan = order.map((s) => ({ name: s.name, pid: s.pid, ...sameProcess(s, state.runDir) }))
  const removal = unsafeToRemove(state.runId)
  if (opts['dry-run']) out({ ok: true, dry_run: true, would_stop: plan.map(({ name, pid, same, why }) => ({ name, pid, ours: same, why })), would_remove: opts['keep-home'] || removal ? [] : [state.runDir], keep_reason: removal && removal !== 'missing' ? removal : undefined, tmux: state.tmux })
  const killed = []
  const leftovers = []
  for (const t of state.tmux || []) if (have('tmux')) run('tmux', ['kill-session', '-t', t])
  for (const s of order) {
    const check = sameProcess(s, state.runDir)
    if (!check.same) {
      if (!check.gone) leftovers.push(`${s.name} pid ${s.pid}: ${check.why}; not signalled`)
      continue
    }
    const target = check.pgid === s.pid ? -s.pid : s.pid
    try {
      process.kill(target, 'SIGTERM')
    } catch {}
    try {
      await poll(async () => !alive(s.pid), 5000, `${s.name} to exit`)
    } catch {
      try {
        process.kill(target, 'SIGKILL')
      } catch {}
      try {
        await poll(async () => !alive(s.pid), 2000, `${s.name} to die`)
      } catch {
        leftovers.push(`${s.name} pid ${s.pid} survived SIGKILL`)
        continue
      }
    }
    killed.push(s.pid)
  }
  const portsFree = await portFree(state.port)
  if (!portsFree) leftovers.push(`port ${state.port} still has a listener`)
  // Keep the logs (redacted) under .proof/.control/, then remove the run directory only if
  // it is provably ours.
  const keptLogs = path.join(stateDir(root), `${state.runId}-logs`)
  if (!removal) copyLogsRedacted(state.logs, keptLogs)
  if (!opts['keep-home']) {
    if (!removal) fs.rmSync(state.runDir, { recursive: true, force: true })
    else if (removal !== 'missing') leftovers.push(`run directory not removed: ${removal}`)
  }
  state.cleanup = { killed_pids: killed, ports_free: portsFree, leftovers, at: new Date().toISOString(), logs: keptLogs }
  saveRun(root, state)
  fs.rmSync(path.join(stateDir(root), 'current'), { force: true })
  out({ ok: leftovers.length === 0, run: state.runId, killed_pids: killed, ports_free: portsFree, leftovers, logs_kept: keptLogs }, leftovers.length ? EXIT.failed : EXIT.ok)
}

function copyLogsRedacted(from, to) {
  let names = []
  try {
    names = fs.readdirSync(from, { withFileTypes: true })
  } catch {
    return
  }
  fs.mkdirSync(to, { recursive: true })
  for (const e of names) {
    if (!e.isFile()) continue
    try {
      fs.writeFileSync(path.join(to, e.name), redactText(fs.readFileSync(path.join(from, e.name), 'utf8')))
    } catch {}
  }
}


async function cmdRestart(root, opts) {
  refuseIfRealEnv(opts)
  const state = loadRun(root, opts)
  if (!state) fail('no run is up; run "up" first')
  const svc = CONFIG.services.find((s) => s.name === 'serve') || CONFIG.services[0]
  const rec = state.services.find((s) => s.name === svc.name && !s.adopted)
  if (!rec) fail('no serve process is recorded for this run')
  const check = sameProcess(rec, state.runDir)
  if (!check.same) fail(`serve is not the process this run started: ${check.why}`)
  if (opts['dry-run']) out({ ok: true, dry_run: true, would_restart: { name: rec.name, pid: rec.pid, signal: check.pgid === rec.pid ? 'group' : 'pid' } })
  const target = check.pgid === rec.pid ? -rec.pid : rec.pid
  try { process.kill(target, 'SIGTERM') } catch {}
  try {
    await poll(async () => !alive(rec.pid), 5000, 'serve to exit')
  } catch {
    try { process.kill(target, 'SIGKILL') } catch {}
    await poll(async () => !alive(rec.pid), 2000, 'serve to die')
  }
  await poll(async () => portFree(state.port), 5000, 'the port to be free')
  const vars = { port: state.port, home: state.home, run: state.runDir, repo: root }
  const baseEnv = cleanEnv({ ...controlEnv(vars), ...(cfg(CONFIG.homeEnv) ? { [CONFIG.homeEnv]: state.home } : {}) })
  const log = rec.log
  const fd = fs.openSync(log, 'a')
  const env = { ...baseEnv }
  for (const [k, v] of Object.entries(svc.env || {})) env[k] = sub(v, vars)
  const child = spawn('/bin/sh', ['-c', `exec ${shSub(svc.cmd)}`], { cwd: root, env, detached: true, stdio: ['ignore', fd, fd] })
  let exited = null
  child.on('exit', (code, sig) => (exited = { code, sig }))
  child.unref()
  rec.pid = child.pid
  rec.lstart = ''
  rec.command = ''
  rec.adopted = false
  rec.group = true
  saveRun(root, state)
  try {
    await poll(async () => {
      if (exited) throw new Error(`serve exited early (${JSON.stringify(exited)})`)
      return isReady(svc.ready || {}, state.port, log)
    }, CONFIG.readyTimeoutMs, 'serve to be ready again')
  } catch (e) {
    saveRun(root, state)
    fail(e.message, EXIT.failed, { run: state.runId, log, tail: redactText(tail(log)), hint: 'run "down" to stop what started' })
  }
  rec.lstart = procInfo(child.pid)?.lstart || ''
  rec.command = procInfo(child.pid)?.command || ''
  if (svc.pidFile) {
    const pidPath = path.join(state.home, sub(svc.pidFile, vars))
    const pid = Number((fs.existsSync(pidPath) ? fs.readFileSync(pidPath, 'utf8') : '').trim().split(/\s+/)[0])
    if (pid > 1 && alive(pid)) {
      const info = procInfo(pid) || { lstart: '', command: '' }
      const adopted = state.services.find((s) => s.adopted && s.name === `${svc.name}-daemon`)
      const fresh = { name: `${svc.name}-daemon`, pid, lstart: info.lstart || '', command: info.command || '', expect: '', log, adopted: true, group: false }
      if (!adopted) state.services.push(fresh)
      else if (adopted.pid === pid) {
        adopted.lstart = fresh.lstart || adopted.lstart
        adopted.command = fresh.command || adopted.command
      } else if (alive(adopted.pid)) state.services.push(fresh)
      else Object.assign(adopted, fresh)
    }
  }
  saveRun(root, state)
  out({ ok: true, restarted: true, run: state.runId, port: state.port, services: state.services.map(({ name, pid, adopted }) => ({ name, pid, adopted })) })
}

// ---------- drivers ----------

function getPath(obj, dotted) {
  return dotted.split('.').reduce((o, k) => (o == null ? undefined : o[k]), obj)
}

async function cmdApi(root, opts, pos) {
  refuseIfRealEnv(opts)
  const [method = 'GET', urlPath] = pos
  if (!urlPath || !urlPath.startsWith('/')) fail('usage: api <METHOD> </path> [--data json|@file] [--header "K: V"] [--bearer name] [--capture name=field.path] [--out file]', EXIT.usage)
  const state = loadRun(root, opts)
  if (!state) fail('no run is up; run "up" first')
  const headers = {}
  for (const h of many(opts.header)) {
    const i = h.indexOf(':')
    if (i > 0) headers[h.slice(0, i).trim()] = h.slice(i + 1).trim()
  }
  const secretsFile = path.join(state.runDir, 'secrets.json')
  const secrets = readJSON(secretsFile, {})
  if (opts.bearer) {
    const v = secrets[one(opts.bearer)] ?? process.env[one(opts.bearer)]
    if (!v) fail(`no captured secret or env var named ${one(opts.bearer)}`, EXIT.usage)
    headers.Authorization = `Bearer ${v}`
  }
  let body
  if (opts.data !== undefined) {
    const d = String(one(opts.data))
    body = d.startsWith('@') ? fs.readFileSync(d.slice(1), 'utf8') : d
    headers['Content-Type'] ||= 'application/json'
  }
  const url = `http://127.0.0.1:${state.port}${urlPath}`
  if (opts['dry-run']) out({ ok: true, dry_run: true, would_send: { method: method.toUpperCase(), url, headers: redact(headers), body: body ? redactText(body) : null } })
  let res
  try {
    res = await fetch(url, { method: method.toUpperCase(), headers, body, signal: AbortSignal.timeout(15000), redirect: 'manual' })
  } catch (e) {
    fail(`request failed: ${e.message}`, EXIT.failed, { url })
  }
  const text = await res.text()
  let parsed = text
  try {
    parsed = JSON.parse(text)
  } catch {}
  for (const c of many(opts.capture)) {
    const [name, field] = String(c).split('=')
    const v = typeof parsed === 'object' ? getPath(parsed, field) : undefined
    if (v !== undefined) secrets[name] = v
  }
  if (opts.capture) {
    fs.writeFileSync(secretsFile, JSON.stringify(secrets), { mode: 0o600 })
  }
  const result = { ok: res.status < 400, method: method.toUpperCase(), path: urlPath, status: res.status, content_type: res.headers.get('content-type'), body: redact(parsed) }
  if (opts.out) writeJSON(path.resolve(one(opts.out)), result)
  out(result, result.ok ? EXIT.ok : EXIT.failed)
}

function chromePath() {
  if (process.env.CHROME) return process.env.CHROME
  // This checkout is driven with Brave. Google Chrome.app is installed and must not be launched.
  if (process.platform === 'darwin') return '/Applications/Brave Browser.app/Contents/MacOS/Brave Browser'
  return '/usr/bin/google-chrome'
}

async function cmdBrowser(root, opts, pos) {
  refuseIfRealEnv(opts)
  const [action, urlPath = '/'] = pos
  if (!['shot', 'text'].includes(action)) fail('usage: browser shot|text </path> [--out file] [--wait-for css] [--storage state.json] [--mobile] [--full]', EXIT.usage)
  let pw = null
  for (const dir of CONFIG.playwrightFrom) {
    try {
      pw = createRequire(path.join(root, dir, 'package.json'))('playwright-core')
      break
    } catch {}
  }
  if (!pw) fail('playwright-core is not installed in this repo, so browser driving is unavailable; drive through api or term instead', EXIT.unavailable)
  const state = loadRun(root, opts)
  if (!state) fail('no run is up; run "up" first')
  const exe = chromePath()
  if (!fs.existsSync(exe)) fail(`Chrome not found at ${exe}; set CHROME`, EXIT.unavailable)
  if (action === 'shot' && !opts.out) fail('browser shot needs --out <file.png>', EXIT.usage)
  const url = `http://127.0.0.1:${state.port}${urlPath}`
  const browser = await pw.chromium.launch({ executablePath: exe, headless: true })
  const logs = []
  let result
  try {
    const ctx = await browser.newContext({
      viewport: opts.mobile ? { width: 390, height: 844 } : { width: 1440, height: 900 },
      storageState: opts.storage ? path.resolve(one(opts.storage)) : undefined,
    })
    const page = await ctx.newPage()
    page.on('console', (m) => logs.push(`[${m.type()}] ${m.text()}`))
    page.on('pageerror', (e) => logs.push(`[pageerror] ${e.message}`))
    await page.goto(url, { waitUntil: 'networkidle', timeout: 30000 })
    if (opts['wait-for']) await page.waitForSelector(one(opts['wait-for']), { timeout: 15000 })
    result = { ok: true, action, url, title: await page.title(), console: logs.map(redactText) }
    if (action === 'shot') {
      await page.screenshot({ path: path.resolve(one(opts.out)), fullPage: Boolean(opts.full) })
      result.out = path.resolve(one(opts.out))
    } else {
      const loc = page.locator('body')
      result.text = redactText(typeof loc.ariaSnapshot === 'function' ? await loc.ariaSnapshot() : await loc.innerText())
      if (opts.out) fs.writeFileSync(path.resolve(one(opts.out)), result.text)
    }
  } finally {
    // out() exits the process, so close the browser before printing anything.
    await browser.close()
  }
  out(result)
}

function shQuote(s) {
  return `'${String(s).replace(/'/g, `'\\''`)}'`
}

async function cmdTerm(root, opts, pos, rest) {
  refuseIfRealEnv(opts)
  const [mode, action, ...args] = pos
  const state = loadRun(root, opts)
  const vars = state ? { port: state.port, home: state.home, run: state.runDir, repo: root } : null
  const env = cleanEnv({})
  if (vars) {
    if (cfg(CONFIG.homeEnv)) env[CONFIG.homeEnv] = vars.home
    for (const [k, v] of Object.entries(CONFIG.cliEnv)) env[k] = sub(v, vars)
  }
  if (mode === 'run') {
    if (!rest.length) fail('usage: term run [--pty] [--timeout ms] [--out file] -- <command> [args...]', EXIT.usage)
    let cmd = rest[0]
    let cargs = rest.slice(1)
    if (opts.pty) {
      // BSD script echoes the closed stdin as "^D" before the output; it is stripped below.
      if (process.platform === 'darwin') [cmd, cargs] = ['script', ['-q', '/dev/null', ...rest]]
      else [cmd, cargs] = ['script', ['-q', '-e', '-c', rest.map(shQuote).join(' '), '/dev/null']]
    }
    const r = spawnSync(cmd, cargs, { cwd: root, env, encoding: 'utf8', timeout: Number(one(opts.timeout) || 60000), maxBuffer: 1 << 26, stdio: ['ignore', 'pipe', 'pipe'] })
    const result = { ok: r.status === 0, argv: redact(rest), exit: r.status, signal: r.signal, stdout: redactText((r.stdout || '').replace(/^\^D\x08\x08/, '')), stderr: redactText(r.stderr || ''), isolated_home: vars?.home || null }
    if (opts.out) writeJSON(path.resolve(one(opts.out)), result)
    out(result, result.ok ? EXIT.ok : EXIT.failed)
  }
  if (mode === 'tmux') {
    if (!have('tmux')) fail('tmux is not installed; use "term run --pty" or the app API instead', EXIT.unavailable)
    if (!state) fail('no run is up; run "up" first')
    const name = `${state.runId}-${one(opts.name) || 'main'}`
    if (action === 'new') {
      const envArgs = Object.entries(env).filter(([k]) => k === CONFIG.homeEnv || k in CONFIG.cliEnv).map(([k, v]) => `${k}=${v}`)
      const unsets = list(CONFIG.realInstanceEnv).flatMap((k) => ['-u', k])
      const cmdline = ['env', ...unsets, ...envArgs, ...(rest.length ? rest : [process.env.SHELL || '/bin/sh'])].map(shQuote).join(' ')
      const r = run('tmux', ['new-session', '-d', '-s', name, '-x', '200', '-y', '50', cmdline], { cwd: root })
      if (r.code !== 0) fail(`tmux new-session failed: ${r.stderr.trim()}`)
      state.tmux = [...new Set([...(state.tmux || []), name])]
      saveRun(root, state)
      out({ ok: true, session: name })
    }
    if (action === 'send') {
      const text = args.join(' ')
      run('tmux', ['send-keys', '-t', name, '-l', text])
      if (opts.enter) run('tmux', ['send-keys', '-t', name, 'Enter'])
      out({ ok: true, session: name, sent: redactText(text), enter: Boolean(opts.enter) })
    }
    if (action === 'capture' || action === 'wait') {
      const grab = () => run('tmux', ['capture-pane', '-p', '-t', name, '-S', '-2000']).stdout
      let screen = grab()
      if (action === 'wait') {
        const re = new RegExp(args.join(' '))
        try {
          screen = await poll(async () => {
            const s = grab()
            return re.test(s) ? s : null
          }, Number(one(opts.timeout) || 15000), `screen to match ${re}`)
        } catch (e) {
          fail(e.message, EXIT.failed, { screen: redactText(screen) })
        }
      }
      const result = { ok: true, session: name, screen: redactText(screen) }
      if (opts.out) fs.writeFileSync(path.resolve(one(opts.out)), result.screen)
      out(result)
    }
    if (action === 'kill') {
      run('tmux', ['kill-session', '-t', name])
      state.tmux = (state.tmux || []).filter((t) => t !== name)
      saveRun(root, state)
      out({ ok: true, killed: name })
    }
  }
  fail('usage: term run [...] -- <cmd> | term tmux new|send|wait|capture|kill', EXIT.usage)
}

// ---------- evidence ----------

const LEVELS = ['L1', 'L2', 'L3', 'L4']

// Must match the proof checker's dirty_hash: 'clean' when `git status` (ignoring .proof/)
// is empty, else sha256 over the diff against HEAD, then each untracked file as
// NUL, path, NUL, content (or "link:" and the target for a symlink), in byte order.
function dirtyHash(root) {
  const ex = ['--', '.', ':(exclude).proof']
  const st = gitIn(root, ['status', '--porcelain', '--untracked-files=all', ...ex], { encoding: 'utf8' })
  if (st.status !== 0) throw new Error(`git status failed in ${root}`)
  if (!st.stdout.trim()) return 'clean'
  const h = crypto.createHash('sha256')
  const diffArgs = ['-c', 'core.quotepath=true', '-c', 'diff.noprefix=false', '-c', 'diff.mnemonicPrefix=false', 'diff', '--no-color', '--no-ext-diff', '--binary', 'HEAD', ...ex]
  h.update(gitIn(root, diffArgs).stdout || Buffer.alloc(0))
  const raw = gitIn(root, ['ls-files', '--others', '--exclude-standard', '-z', ...ex]).stdout || Buffer.alloc(0)
  const files = []
  let from = 0
  for (let i = 0; i < raw.length; i++) {
    if (raw[i] === 0) {
      if (i > from) files.push(raw.subarray(from, i))
      from = i + 1
    }
  }
  files.sort(Buffer.compare)
  const nul = Buffer.from([0])
  for (const rel of files) {
    h.update(Buffer.concat([nul, rel, nul]))
    const full = path.join(root, rel.toString('utf8'))
    if (fs.lstatSync(full).isSymbolicLink()) h.update(Buffer.concat([Buffer.from('link:'), Buffer.from(fs.readlinkSync(full))]))
    else h.update(fs.readFileSync(full))
  }
  return h.digest('hex')
}
function baseSha(root, ref) {
  if (ref) return gitText(root, ['rev-parse', ref])
  for (const r of ['origin/HEAD', 'origin/main', 'origin/master', 'main', 'master']) {
    const mb = gitText(root, ['merge-base', 'HEAD', r])
    if (mb) return mb
  }
  return gitText(root, ['rev-parse', 'HEAD'])
}
function bundlePath(root, opts) {
  const b = one(opts.bundle) || (fs.existsSync(path.join(stateDir(root), 'bundle')) ? fs.readFileSync(path.join(stateDir(root), 'bundle'), 'utf8').trim() : '')
  if (!b) fail('no bundle: pass --bundle <dir> or run "evidence init" first', EXIT.usage)
  const dir = path.resolve(root, b)
  if (!fs.existsSync(path.join(dir, 'manifest.json'))) fail(`not a bundle: ${dir}`, EXIT.usage)
  return dir
}

function cmdEvidence(root, opts, pos) {
  const [action] = pos
  if (action === 'init') {
    const slug = String(one(opts.slug) || '')
    const claim = String(one(opts.claim) || '')
    const features = many(opts.feature).flatMap((f) => String(f).split(',')).map((f) => f.trim()).filter(Boolean)
    if (!/^[a-z0-9][a-z0-9-]{0,40}$/.test(slug) || !claim || !features.length) fail('usage: evidence init --slug <slug> --claim "<one falsifiable sentence>" --feature <id>[,<id>] [--entry "<how reached>"] [--level L3] [--required-level L4] [--base <ref>]', EXIT.usage)
    if (features.some((f) => !/^[a-z0-9][a-z0-9._-]*$/.test(f))) fail('feature ids are lowercase letters, digits, dot, underscore, hyphen', EXIT.usage)
    const level = String(one(opts.level) || 'L2')
    const required = String(one(opts['required-level']) || 'L3')
    if (!LEVELS.includes(level) || !LEVELS.includes(required)) fail('levels are L1, L2, L3 or L4', EXIT.usage)
    const head = gitText(root, ['rev-parse', 'HEAD'])
    if (!head) fail('not a git checkout with commits', EXIT.usage)
    const dir = path.join(root, '.proof', `${nowStamp()}-${slug}`)
    if (opts['dry-run']) out({ ok: true, dry_run: true, would_create: dir })
    fs.mkdirSync(path.join(dir, 'steps'), { recursive: true })
    const state = loadRun(root, opts)
    const ignored = gitIn(root, ['check-ignore', '-q', '.proof/x']).status === 0
    const manifest = {
      kit: KIT,
      schema_version: 1,
      repo: repoName(root),
      claim,
      feature_ids: features,
      entry_points: many(opts.entry).map(String),
      base_sha: baseSha(root, one(opts.base)),
      head_sha: head,
      dirty_hash: dirtyHash(root),
      level,
      required_level: required,
      verdict: 'INCONCLUSIVE',
      steps: [],
      cleanup: { killed_pids: [], evidence_kept: true },
      created_at: new Date().toISOString(),
    }
    if (opts.tool) manifest.tool = { name: String(one(opts.tool)) }
    if (state) {
      const { stale } = doctorChecks(root)
      manifest.doctor = { owned_pids: state.services.map((s) => s.pid), port: state.port, home: state.home, build: state.build?.head || '', stale_assets: Boolean(stale) }
    }
    writeJSON(path.join(dir, 'manifest.json'), manifest)
    fs.mkdirSync(stateDir(root), { recursive: true })
    fs.writeFileSync(path.join(dir, 'claim.md'), `# Claim\n\n${claim}\n\n- Features: ${features.join(', ')}\n- Entry points: ${manifest.entry_points.join('; ') || 'n/a'}\n- Target level: ${level} (required ${required})\n- Head: ${head} (${manifest.dirty_hash})\n- Base: ${manifest.base_sha}\n`)
    fs.writeFileSync(path.join(stateDir(root), 'bundle'), path.relative(root, dir))
    out({ ok: true, bundle: path.relative(root, dir), proof_ignored: ignored, warning: ignored ? undefined : '.proof/ is not gitignored' })
  }
  if (action === 'add') {
    const dir = bundlePath(root, opts)
    const m = readJSON(path.join(dir, 'manifest.json'))
    const actionText = String(one(opts.action) || '')
    const assertText = String(one(opts.assert) || '')
    if (!actionText || !assertText || !opts.artifact) fail('usage: evidence add --action "<what was done>" --assert "<what was checked>" --artifact <file> [--expect-contains text] [--expected v --observed v] [--exit n --expect-exit n] [--ok|--fail] [--readback]. Every step needs an artifact: save the output, response or screenshot to a file first.', EXIT.usage)
    const n = m.steps.length + 1
    const step = { n, action: actionText, assert: assertText }
    let artifactText = null
    if (opts.artifact) {
      const src = path.resolve(one(opts.artifact))
      if (!fs.existsSync(src)) fail(`artifact not found: ${src}`, EXIT.usage)
      const name = `${String(n).padStart(2, '0')}-${one(opts.name) || path.basename(src)}`.replace(/[^A-Za-z0-9._-]/g, '_')
      const dest = path.join(dir, 'steps', name)
      const buf = fs.readFileSync(src)
      const isText = !buf.subarray(0, 8000).includes(0)
      if (isText) {
        artifactText = redactText(buf.toString('utf8'))
        fs.writeFileSync(dest, artifactText)
      } else fs.copyFileSync(src, dest)
      step.artifact = path.relative(dir, dest)
    }
    if (opts.argv) step.argv = redact(JSON.parse(String(one(opts.argv))))
    if (opts.exit !== undefined) step.exit = Number(one(opts.exit))
    const verdicts = []
    if (opts['expect-contains'] !== undefined) {
      const want = String(one(opts['expect-contains']))
      step.expected = want
      step.observed = artifactText === null ? null : artifactText.includes(want) ? want : 'absent'
      verdicts.push(artifactText !== null && artifactText.includes(want))
    }
    if (opts['expect-exit'] !== undefined) verdicts.push(step.exit === Number(one(opts['expect-exit'])))
    if (opts.expected !== undefined && opts.observed !== undefined) {
      step.expected = String(one(opts.expected))
      step.observed = String(one(opts.observed))
      verdicts.push(step.expected === step.observed)
    }
    if (opts.ok) verdicts.push(true)
    if (opts.fail) verdicts.push(false)
    if (!verdicts.length) fail('state how the step is judged: --expect-contains, --expect-exit, --expected/--observed, or --ok/--fail', EXIT.usage)
    step.ok = verdicts.every(Boolean)
    if (opts.readback) step.side_effect_readback = true
    m.steps.push(step)
    writeJSON(path.join(dir, 'manifest.json'), m)
    out({ ok: true, bundle: path.relative(root, dir), step })
  }
  if (action === 'close') {
    const dir = bundlePath(root, opts)
    const m = readJSON(path.join(dir, 'manifest.json'))
    const notes = []
    if (opts.confound) notes.push(String(one(opts.confound)))
    if (!m.steps.length) fail('a bundle needs at least one step', EXIT.usage)
    for (const s of m.steps) if (s.artifact && !fs.existsSync(path.join(dir, s.artifact))) notes.push(`artifact missing: ${s.artifact}`)
    if (opts['base-run']) {
      const src = path.resolve(one(opts['base-run']))
      const bm = readJSON(path.join(src, 'manifest.json'))
      if (!bm) fail(`base run is not a bundle: ${src}`, EXIT.usage)
      fs.cpSync(src, path.join(dir, 'base'), { recursive: true })
      m.base_run = { sha: bm.head_sha, verdict: bm.verdict, bundle: 'base' }
      // The base run defines what the change is compared against.
      if (m.base_sha !== bm.head_sha) {
        notes.push(`base_sha set to the base run's commit ${bm.head_sha} (was ${m.base_sha})`)
        m.base_sha = bm.head_sha
      }
    }
    const head = gitText(root, ['rev-parse', 'HEAD'])
    const dirty = dirtyHash(root)
    if (head !== m.head_sha || dirty !== m.dirty_hash) notes.push('the working tree changed during the run')
    const state = latestRun(root)
    if (state?.cleanup) {
      m.cleanup = { killed_pids: state.cleanup.killed_pids, ports_free: state.cleanup.ports_free, evidence_kept: true }
      if (state.cleanup.leftovers.length) m.cleanup.leftovers = state.cleanup.leftovers
    } else if (state) {
      m.cleanup = { killed_pids: [], evidence_kept: true, leftovers: [`run ${state.runId} is still up; run "down" before "close"`] }
    }
    // The checker requires every leftover to appear verbatim in confound.
    for (const left of m.cleanup.leftovers || []) notes.push(`cleanup leftover: ${left}`)
    if (!m.required_level) m.required_level = 'L3'
    // The level is what the steps support, never more than claimed.
    let level = m.level
    // A read-back step counts toward the level whether it passed or failed: a failing L3 or
    // L4 run is still a valid record at that level (never a pass). VERIFIED needs every step ok.
    const has = { artifact: m.steps.some((s) => s.artifact), readback: m.steps.some((s) => s.side_effect_readback) }
    const cap = (to, why) => {
      if (LEVELS.indexOf(level) > LEVELS.indexOf(to)) {
        notes.push(`level lowered from ${level} to ${to}: ${why}`)
        level = to
      }
    }
    if (level === 'L4' && m.base_run?.verdict !== 'NOT VERIFIED') cap('L3', 'L4 needs a base run that says NOT VERIFIED')
    if (!has.readback) cap('L2', 'no step reads the side effect back (--readback)')
    if (!has.artifact) cap('L1', 'no step saved an artifact')
    m.level = level
    const anyFail = m.steps.some((s) => !s.ok)
    const enough = LEVELS.indexOf(level) >= LEVELS.indexOf(m.required_level || 'L1')
    const clean = !notes.some((x) => x.startsWith('artifact missing') || x.startsWith('the working tree changed')) && !(m.cleanup.leftovers || []).length
    m.verdict = anyFail ? 'NOT VERIFIED' : enough && clean ? 'VERIFIED' : 'INCONCLUSIVE'
    if (notes.length) m.confound = notes.join('; ')
    else delete m.confound
    writeJSON(path.join(dir, 'manifest.json'), m)
    const lines = m.steps.map((s) => `- Step ${s.n} ${s.ok ? 'ok' : 'FAILED'}: ${s.assert}${s.artifact ? ` (${s.artifact})` : ''}`)
    fs.writeFileSync(path.join(dir, 'verdict.md'), `# ${m.verdict}\n\n${m.claim}\n\nLevel ${m.level} (required ${m.required_level}) at ${m.head_sha} (${m.dirty_hash}).${m.base_run ? ` Base ${m.base_run.sha}: ${m.base_run.verdict}.` : ''}\n\n${lines.join('\n')}\n${m.confound ? `\nConfound: ${m.confound}\n` : ''}`)
    fs.rmSync(path.join(stateDir(root), 'bundle'), { force: true })
    out({ ok: m.verdict === 'VERIFIED', bundle: path.relative(root, dir), verdict: m.verdict, level: m.level, required_level: m.required_level, confound: m.confound || null }, m.verdict === 'VERIFIED' ? EXIT.ok : EXIT.failed)
  }
  fail('usage: evidence init|add|close (see --help)', EXIT.usage)
}

// ---------- main ----------

const HELP = `control-${cfg(CONFIG.repo) || '<repo>'}: drive an isolated instance and record evidence. Prints JSON.

Usage: node control-${cfg(CONFIG.repo) || '<repo>'}.mjs [--repo DIR] <command> [options]

Commands:
  up [--dry-run]                 Build (if configured), start services on a free port in an
                                 isolated home, wait until ready by polling. Idempotent.
  doctor [--dry-run]             Read-only health: config, inherited env, build freshness,
                                 assets, owned processes, port owner. Run it first.
  down [--dry-run] [--keep-home] Stop only the processes this tool started (identity checked),
                                 keep logs under .proof/.control/, remove the run directory.
  restart [--dry-run]            Stop the owned serve process and start it again. The detached
                                 session daemon is adopted, not signalled, then re-read from
                                 its pid file. Health is polled. Sessions are not restarted.
  api METHOD /path [...]         Call the owned instance. --data json|@file, --header "K: V",
                                 --capture name=field.path (stores a secret, prints it redacted),
                                 --bearer name, --out file, --dry-run.
  browser shot|text /path        System Chrome via the repo's playwright-core, if it has one.
                                 --out file, --wait-for css, --storage state.json, --mobile.
  term run [--pty] -- CMD...     Run a command with the isolated env; capture exit and output.
  term tmux new|send|wait|capture|kill   Drive a TUI in tmux (when tmux is installed).
  evidence init|add|close        Record a .proof/<UTC>-<slug>/ bundle; close computes the verdict.

Global: --repo DIR (checkout to run against; default: this git repo), --run ID, --help.
Exit codes: 0 ok or VERIFIED, 1 failed or not verified, 2 usage, 3 refused (real-instance env), 4 driver unavailable.

Examples:
  node control.mjs doctor
  node control.mjs up
  node control.mjs api GET /health --out /tmp/health.json
  node control.mjs evidence init --slug login --claim "A new user can sign in" --feature auth --level L3 --required-level L3
  node control.mjs evidence add --action "POST /login" --assert "returns 200" --artifact /tmp/login.json --expect-contains '"status": 200'
  node control.mjs evidence add --action "GET /me after login" --assert "shows the user" --artifact /tmp/me.json --expect-contains fixture --readback
  node control.mjs term run -- ./bin/app ls --json
  node control.mjs restart
  node control.mjs down
  node control.mjs evidence close
  node control.mjs --repo /tmp/app-base up      # L4: drive the base commit with this tool
`

async function main() {
  const { pos, opts, rest } = parseArgs(process.argv.slice(2))
  const [command, ...args] = pos
  if (opts.help || !command || command === 'help') {
    process.stdout.write(HELP)
    process.exit(EXIT.ok)
  }
  const root = repoRoot(opts)
  if (command === 'up') return cmdUp(root, opts)
  if (command === 'doctor') return cmdDoctor(root, opts)
  if (command === 'down') return cmdDown(root, opts)
  if (command === 'restart') return cmdRestart(root, opts)
  if (command === 'api') return cmdApi(root, opts, args)
  if (command === 'browser') return cmdBrowser(root, opts, args)
  if (command === 'term') return cmdTerm(root, opts, args, rest)
  if (command === 'evidence') return cmdEvidence(root, opts, args)
  fail(`unknown command: ${command} (try --help)`, EXIT.usage)
}

main().catch((e) => fail(e.message || String(e)))
