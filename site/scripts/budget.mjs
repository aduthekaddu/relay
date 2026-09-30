#!/usr/bin/env node
// Checks the built site (dist/) against the JS budgets in docs/dev/SITE.md
// and prints per-page gzip sizes for JS, CSS and HTML.
//
//   pnpm build && pnpm budget          # exits 1 when a budget is exceeded
//
// A page's JS is every module script it loads, followed through static
// imports, plus dynamically imported chunks — except island chunks whose
// `data-island` does not occur on that page (they are never fetched).
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { gzipSync } from 'node:zlib'

const here = dirname(fileURLToPath(import.meta.url))
const dist = join(here, '..', 'dist')
const KB = 1024

/** Budgets in gzip bytes, keyed by route; `*` applies to other marketing pages. */
export const BUDGETS = { '/': 160 * KB, '*': 100 * KB }

/** Island names registered in src/lib/app.ts. */
function islandNames() {
  const src = readFileSync(join(here, '..', 'src', 'lib', 'app.ts'), 'utf8')
  return new Set([...src.matchAll(/['"]?([\w-]+)['"]?:\s*\(\)\s*=>\s*import\(/g)].map((m) => m[1]))
}

/** Chunk basename without hash: `phone-terminal.C8ob.js` → `phone-terminal`. */
export function chunkName(file) {
  return file
    .split('/')
    .pop()
    .replace(/\.[\w-]+\.js$/, '')
}

/** Returns [static, dynamic] chunk references (file names) found in `code`. */
export function references(code) {
  const stat = new Set()
  const all = new Set()
  for (const m of code.matchAll(/(?:from|import)\s*["']\.\/([\w@.+-]+\.js)["']/g)) stat.add(m[1])
  for (const m of code.matchAll(/["'](?:\.\/|[\w/]*_a\/)?([\w@.+-]+\.[\w-]{6,}\.js)["']/g)) all.add(m[1])
  return [[...stat], [...all].filter((f) => !stat.has(f))]
}

const gz = (buf) => gzipSync(buf, { level: 9 }).length

function pageJs(html, assets, islands) {
  const present = new Set([...html.matchAll(/data-island="([\w-]+)"/g)].map((m) => m[1]))
  const seen = new Set()
  const queue = [...html.matchAll(/<script[^>]*\ssrc="[^"]*\/([^"/]+\.js)"/g)].map((m) => m[1])
  let bytes = 0
  while (queue.length) {
    const f = queue.shift()
    if (seen.has(f) || !existsSync(join(assets, f))) continue
    seen.add(f)
    const code = readFileSync(join(assets, f))
    bytes += gz(code)
    const [stat, dyn] = references(code.toString('utf8'))
    queue.push(...stat)
    for (const d of dyn) {
      const n = chunkName(d)
      if (islands.has(n) && !present.has(n)) continue
      queue.push(d)
    }
  }
  for (const m of html.matchAll(/<script(?![^>]*\ssrc=)[^>]*>([\s\S]*?)<\/script>/g))
    bytes += gz(Buffer.from(m[1]))
  return { bytes, files: seen.size }
}

function pageCss(html, root) {
  let bytes = 0
  for (const m of html.matchAll(/<link[^>]*rel="stylesheet"[^>]*href="[^"]*\/(_a\/[^"]+\.css)"/g)) {
    const p = join(root, m[1])
    if (existsSync(p)) bytes += gz(readFileSync(p))
  }
  for (const m of html.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)) bytes += gz(Buffer.from(m[1]))
  return bytes
}

function htmlFiles(dir) {
  return readdirSync(dir).flatMap((n) => {
    const p = join(dir, n)
    if (statSync(p).isDirectory()) return htmlFiles(p)
    return n.endsWith('.html') ? [p] : []
  })
}

function main() {
  if (!existsSync(dist)) {
    console.error('dist/ not found: run pnpm build first')
    process.exit(2)
  }
  const assets = join(dist, '_a')
  const islands = islandNames()
  const rows = htmlFiles(dist)
    .map((p) => {
      const route =
        `/${relative(dist, p)
          .replace(/index\.html$/, '')
          .replace(/\.html$/, '')}`.replace(/\/$/, '') || '/'
      const html = readFileSync(p, 'utf8')
      const js = pageJs(html, assets, islands)
      return { route, js, css: pageCss(html, dist), html: gz(Buffer.from(html)) }
    })
    .sort((a, b) => a.route.localeCompare(b.route))

  let failed = 0
  const k = (b) => `${(b / KB).toFixed(1)} KB`.padStart(9)
  console.log('route'.padEnd(34), '      js', '     css', '    html', ' budget')
  for (const r of rows) {
    const docs = r.route.startsWith('/docs') || r.route === '/og'
    const budget = docs ? null : (BUDGETS[r.route] ?? BUDGETS['*'])
    const over = budget != null && r.js.bytes > budget
    if (over) failed++
    const verdict = budget == null ? '     –' : `${over ? '✗' : '✓'} ${Math.round(budget / KB)} KB`
    console.log(r.route.padEnd(34), k(r.js.bytes), k(r.css), k(r.html), verdict)
  }
  if (failed) {
    console.error(`\n${failed} page(s) over the JS budget`)
    process.exit(1)
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) main()
