// A small synthetic home directory for the Files mocks.
import type { DirListing, DiskUsage, FileEntry, FileSearchHit } from '../api/types'
import { HOME } from './data'
import { DAY, HOUR, MIN } from './util'

interface Node {
  type: 'file' | 'dir' | 'symlink'
  size?: number
  mtime: number
  text?: string
  mime?: string
  target?: string
  git?: string
}

const tree = new Map<string, Node>()
const trash: FileEntry[] = []

const README = `# relay-demo

A tiny service used to show off Relay. Run it with:

\`\`\`sh
pnpm install
pnpm dev
\`\`\`

- Share links are signed and expire.
- Read-only links can watch but never type.
`

const GO = `package share

import (
\t"crypto/hmac"
\t"crypto/sha256"
\t"time"
)

// Link is a signed, expiring pointer to a session.
type Link struct {
\tID       string
\tExpires  time.Time
\tReadOnly bool
}

// Valid reports whether the link is unexpired at t.
func (l Link) Valid(t time.Time) bool { return t.Before(l.Expires) }

func sign(key, msg []byte) []byte {
\tm := hmac.New(sha256.New, key)
\tm.Write(msg)
\treturn m.Sum(nil)
}
`

const TS = `import { signal } from '@preact/signals'

export const count = signal(0)

export function increment(by = 1): void {
  count.value += by
}
`

function add(path: string, n: Omit<Node, 'mtime'> & { age?: number }) {
  tree.set(path, { ...n, mtime: Date.now() - (n.age ?? 3 * DAY) * 1000 })
}

function build() {
  const H = HOME
  const d = (p: string, age?: number) => add(p, { type: 'dir', age })
  const f = (p: string, size: number, age?: number, extra: Partial<Node> = {}) =>
    add(p, { type: 'file', size, age, ...extra })
  d(H, 60)
  for (const p of ['code', 'Documents', 'Downloads', 'Pictures', '.config', '.ssh']) d(`${H}/${p}`, 2 * HOUR)
  f(`${H}/.bashrc`, 3_812, 40 * DAY, { text: '# ~/.bashrc\nexport EDITOR=nvim\nalias gs="git status"\n' })
  f(`${H}/.zshrc`, 2_204, 12 * DAY, { text: '# ~/.zshrc\nexport PATH="$HOME/.local/bin:$PATH"\n' })
  f(`${H}/todo.md`, 612, 3 * HOUR, {
    text: '# Today\n\n- [x] Ship share links\n- [ ] Review orbit-api PR\n- [ ] Winter newsletter outline\n',
  })
  const R = `${H}/code/relay-demo`
  for (const w of ['relay-demo', 'orbit-api', 'field-notes', 'pixel-garden', 'scratch'])
    d(`${H}/code/${w}`, 30 * MIN)
  for (const p of ['internal', 'internal/share', 'web', 'web/src', 'docs', '.github'])
    d(`${R}/${p}`, 50 * MIN)
  f(`${R}/README.md`, README.length, 2 * DAY, { text: README })
  f(`${R}/go.mod`, 312, 9 * DAY, { text: 'module relay-demo\n\ngo 1.25\n' })
  f(`${R}/package.json`, 1_402, 4 * DAY, {
    text: '{\n  "name": "relay-demo",\n  "private": true,\n  "scripts": { "dev": "vite" }\n}\n',
    git: 'M',
  })
  f(`${R}/internal/share/link.go`, GO.length, 40 * MIN, { text: GO, git: 'M' })
  f(`${R}/internal/share/link_test.go`, 2_910, 38 * MIN, {
    text: 'package share\n\nimport "testing"\n\nfunc TestLink(t *testing.T) {}\n',
    git: 'A',
  })
  f(`${R}/internal/share/sign.go`, 880, 36 * MIN, { text: 'package share\n', git: '?' })
  f(`${R}/web/src/counter.ts`, TS.length, 2 * HOUR, { text: TS })
  f(`${R}/docs/architecture.md`, 8_220, 6 * DAY, {
    text: '# Architecture\n\nOne process serves HTTP; another owns every PTY.\n',
  })
  f(`${R}/docs/diagram.png`, 184_220, 6 * DAY, { mime: 'image/png' })
  f(`${H}/Documents/invoice-2025-09.pdf`, 96_110, 20 * DAY, { mime: 'application/pdf' })
  f(`${H}/Documents/notes.txt`, 1_240, 5 * DAY, {
    text: 'Remember: the station clock runs two minutes fast.\n',
  })
  f(`${H}/Downloads/dataset.csv`, 12_882_004, 1 * DAY, { text: 'id,name,score\n1,alpha,0.91\n2,beta,0.72\n' })
  f(`${H}/Downloads/relay_0.1.0_linux_amd64.tar.gz`, 18_220_441, 2 * DAY, { mime: 'application/gzip' })
  f(`${H}/Downloads/screen-recording.mp4`, 84_442_120, 3 * DAY, { mime: 'video/mp4' })
  for (let i = 1; i <= 9; i++)
    f(`${H}/Pictures/photo-${String(i).padStart(2, '0')}.jpg`, 1_800_000 + i * 91_000, i * DAY, {
      mime: 'image/jpeg',
    })
  f(`${H}/Pictures/mark.svg`, 1_024, 7 * DAY, { mime: 'image/svg+xml' })
  add(`${H}/code/latest`, { type: 'symlink', target: R, age: 2 * DAY })
  d(`${H}/.config/relay`, 3 * DAY)
  f(`${H}/.config/relay/relay.toml`, 902, 3 * DAY, { text: '[server]\nlisten = "127.0.0.1:7700"\n' })
}
build()

const MIME: Record<string, string> = {
  md: 'text/markdown',
  go: 'text/x-go',
  ts: 'text/typescript',
  json: 'application/json',
  mod: 'text/plain',
  txt: 'text/plain',
  csv: 'text/csv',
  toml: 'application/toml',
}

const base = (p: string) => p.slice(p.lastIndexOf('/') + 1)
const parentOf = (p: string) => (p === HOME || p === '/' ? undefined : p.slice(0, p.lastIndexOf('/')) || '/')

/** Expand "~" and strip trailing slashes. */
export function normalize(p: string | null | undefined): string {
  let v = (p ?? '').trim() || HOME
  if (v === '~' || v.startsWith('~/')) v = HOME + v.slice(1)
  v = v.replace(/\/+$/, '') || '/'
  return v
}

function entry(path: string, n: Node): FileEntry {
  const name = base(path)
  const ext = name.includes('.') ? name.slice(name.lastIndexOf('.') + 1).toLowerCase() : ''
  const children = n.type === 'dir' ? [...tree.keys()].filter((k) => parentOf(k) === path).length : undefined
  return {
    name,
    path,
    type: n.type,
    size: n.type === 'dir' ? 4096 : (n.size ?? 0),
    modTime: new Date(n.mtime).toISOString(),
    mode: n.type === 'dir' ? 'drwxr-xr-x' : 'rw-r--r--',
    mime:
      n.type === 'dir'
        ? undefined
        : (n.mime ?? MIME[ext] ?? (n.text ? 'text/plain' : 'application/octet-stream')),
    hidden: name.startsWith('.'),
    target: n.target,
    children,
    git: n.git,
  }
}

export function stat(path: string): FileEntry | null {
  const p = normalize(path)
  const n = tree.get(p)
  return n ? entry(p, n) : null
}

export function list(
  path: string,
  opts: { hidden?: boolean; sort?: string; desc?: boolean; offset?: number; limit?: number },
): DirListing | null {
  const p = normalize(path)
  const n = tree.get(p)
  if (n?.type !== 'dir') return null
  let all = [...tree.entries()].filter(([k]) => parentOf(k) === p).map(([k, v]) => entry(k, v))
  const hiddenCount = all.filter((e) => e.hidden).length
  if (!opts.hidden) all = all.filter((e) => !e.hidden)
  const dirFirst = (a: FileEntry, b: FileEntry) => Number(b.type === 'dir') - Number(a.type === 'dir')
  const key = opts.sort ?? 'name'
  all.sort((a, b) => {
    const c =
      key === 'size'
        ? a.size - b.size
        : key === 'mtime'
          ? a.modTime.localeCompare(b.modTime)
          : a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' })
    return dirFirst(a, b) || (opts.desc ? -c : c)
  })
  const offset = opts.offset ?? 0
  const limit = opts.limit ?? 500
  const gitRoot = p.startsWith(`${HOME}/code/`) ? p.split('/').slice(0, 5).join('/') : undefined
  return {
    path: p,
    parent: parentOf(p),
    entries: all.slice(offset, offset + limit),
    total: all.length,
    offset,
    hidden: hiddenCount,
    gitRoot,
  }
}

export function readText(
  path: string,
): { text: string; size: number; modTime: string; truncated: boolean; encoding: string } | null {
  const p = normalize(path)
  const n = tree.get(p)
  if (n?.type !== 'file' || n.text === undefined) return null
  return {
    text: n.text,
    size: n.text.length,
    modTime: new Date(n.mtime).toISOString(),
    truncated: false,
    encoding: 'utf-8',
  }
}

export function writeText(path: string, text: string, mtime?: string): FileEntry | 'conflict' | null {
  const p = normalize(path)
  const n = tree.get(p)
  if (n && mtime && new Date(n.mtime).toISOString() !== mtime) return 'conflict'
  const node: Node = { ...(n ?? { type: 'file' }), type: 'file', text, size: text.length, mtime: Date.now() }
  tree.set(p, node)
  return entry(p, node)
}

export function mkdir(path: string, type: 'dir' | 'file' = 'dir'): FileEntry | null {
  const p = normalize(path)
  if (tree.has(p) || !tree.has(parentOf(p) ?? '')) return null
  const node: Node =
    type === 'dir'
      ? { type: 'dir', mtime: Date.now() }
      : { type: 'file', size: 0, text: '', mtime: Date.now() }
  tree.set(p, node)
  return entry(p, node)
}

function moveNode(from: string, to: string) {
  for (const [k, v] of [...tree.entries()]) {
    if (k === from || k.startsWith(`${from}/`)) {
      tree.delete(k)
      tree.set(to + k.slice(from.length), v)
    }
  }
}

export function rename(path: string, name: string): FileEntry | null {
  const p = normalize(path)
  if (!tree.has(p) || name.includes('/')) return null
  const to = `${parentOf(p)}/${name}`
  if (tree.has(to)) return null
  moveNode(p, to)
  return stat(to)
}

export function move(from: string[], to: string): FileEntry[] {
  const dir = normalize(to)
  const out: FileEntry[] = []
  for (const f of from) {
    const p = normalize(f)
    if (!tree.has(p)) continue
    const dest = `${dir}/${base(p)}`
    moveNode(p, dest)
    const e = stat(dest)
    if (e) out.push(e)
  }
  return out
}

export function remove(paths: string[], toTrash: boolean): void {
  for (const raw of paths) {
    const p = normalize(raw)
    const e = stat(p)
    if (!e) continue
    if (toTrash) trash.unshift({ ...e, modTime: new Date().toISOString() })
    for (const k of [...tree.keys()]) if (k === p || k.startsWith(`${p}/`)) tree.delete(k)
  }
}

export const trashList = () => trash

export function restore(paths: string[]): void {
  for (const p of paths) {
    const i = trash.findIndex((t) => t.path === p)
    if (i < 0) continue
    const [e] = trash.splice(i, 1)
    tree.set(e.path, { type: e.type === 'dir' ? 'dir' : 'file', size: e.size, mtime: Date.now() })
  }
}

export function emptyTrash(): void {
  trash.length = 0
}

export function usage(path: string): DiskUsage {
  const p = normalize(path)
  const children: Record<string, number> = {}
  let size = 0
  let files = 0
  let dirs = 0
  for (const [k, v] of tree) {
    if (!(k === p || k.startsWith(`${p}/`)) || k === p) continue
    if (v.type === 'dir') dirs++
    else {
      files++
      size += v.size ?? 0
      const rel = k.slice(p.length + 1).split('/')[0]
      children[rel] = (children[rel] ?? 0) + (v.size ?? 0)
    }
  }
  return { path: p, size, files, dirs, children, complete: true, pending: false }
}

export function search(q: string, root: string, content: boolean, limit = 50): FileSearchHit[] {
  const r = normalize(root)
  const lq = q.toLowerCase()
  const hits: FileSearchHit[] = []
  for (const [k, v] of tree) {
    if (!k.startsWith(`${r}/`)) continue
    const name = base(k).toLowerCase()
    if (name.includes(lq)) hits.push({ path: k, score: 1 - name.indexOf(lq) / 40 })
    if (content && v.text) {
      v.text.split('\n').forEach((line, i) => {
        const col = line.toLowerCase().indexOf(lq)
        if (col >= 0)
          hits.push({ path: k, line: i + 1, column: col + 1, preview: line.trim().slice(0, 160), score: 0.5 })
      })
    }
    if (hits.length >= limit) break
  }
  return hits.slice(0, limit)
}

/** All file paths (for the command-center search). */
export const allPaths = () => [...tree.keys()]

/** Placeholder image for thumbnails / raw images: a dot-field SVG. */
export function placeholderImage(path: string, size = 256): string {
  let h = 0
  for (const c of path) h = (h * 31 + c.charCodeAt(0)) >>> 0
  const hue = h % 360
  const dots: string[] = []
  const n = 16
  for (let y = 0; y < n; y++)
    for (let x = 0; x < n; x++) {
      const v = (Math.sin(x * 0.7 + (h % 7)) + Math.cos(y * 0.5 + (h % 5)) + 2) / 4
      dots.push(
        `<circle cx="${x * 16 + 8}" cy="${y * 16 + 8}" r="${(1.5 + v * 5).toFixed(1)}" fill-opacity="${(0.25 + v * 0.7).toFixed(2)}"/>`,
      )
    }
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 256 256"><rect width="256" height="256" fill="hsl(${hue} 30% 12%)"/><g fill="hsl(${(hue + 30) % 360} 80% 70%)">${dots.join('')}</g></svg>`
}

const baselineTree = structuredClone(tree)
export function resetFS(): void {
  tree.clear()
  for (const [path, node] of structuredClone(baselineTree)) tree.set(path, node)
  trash.length = 0
}
