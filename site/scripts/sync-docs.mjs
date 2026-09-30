#!/usr/bin/env node
// Mirror the repository's /docs folder into Starlight's content tree.
//
//   ../docs/**            -> src/content/docs/docs/**            (/docs/…)
//   ../docs/dev/**        -> src/content/docs/docs/contributing/** (/docs/contributing/…)
//   ../docs/README.md     -> …/index.md (folder index)
//
// For every Markdown file it
//   * adds `title` (from the first `# Heading`, which is removed) and
//     `description` (from the first paragraph) frontmatter when missing,
//   * adds `editUrl` pointing at the source file on GitHub,
//   * rewrites relative links to other Markdown files into site routes and
//     relative links to files outside /docs into GitHub URLs.
// Non-Markdown files (images, casts) are copied verbatim so relative image
// references keep working. The output tree is disposable and gitignored;
// the script also writes `src/content/docs/docs/.sidebar.json`, which
// astro.config.mjs turns into the docs sidebar.
//
// When /docs has no index page yet, a generated one lists what exists, so
// local builds work before the user guides land.
import { copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { dirname, join, posix, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))

/** Repository coordinates used for edit links and out-of-docs links. */
export const REPO = process.env.SITE_REPO || 'https://github.com/aduthekaddu/relay'
export const BRANCH = process.env.SITE_BRANCH || 'main'

const MD = /\.(md|mdx)$/i

/** Folder name remapping (source segment -> route segment). */
const FOLDER_MAP = { dev: 'contributing' }

/** Human labels for known folders; unknown folders are title-cased. */
const FOLDER_LABELS = {
  guides: 'Guides',
  reference: 'Reference',
  concepts: 'Concepts',
  features: 'Features',
  contributing: 'Contributing',
}

/** Preferred order of top-level pages; everything else follows alphabetically. */
const TOP_ORDER = ['index', 'getting-started', 'quickstart', 'install', 'installation', 'setup', 'faq', 'troubleshooting']

/** Normalise the site base to "/x/" form ("/" for root). */
export function normalizeBase(base) {
  let b = (base || '/').trim()
  if (!b.startsWith('/')) b = `/${b}`
  if (!b.endsWith('/')) b = `${b}/`
  return b.replace(/\/{2,}/g, '/')
}

/** Slugify one path segment the way Starlight/github-slugger does for ids. */
export function slugSegment(seg) {
  return seg
    .toLowerCase()
    .trim()
    .replace(/[^\p{L}\p{N}\s_-]/gu, '')
    .replace(/\s/g, '-')
}

/**
 * Map a path relative to /docs (posix, with extension) to the output path
 * relative to the Starlight docs root and to its route slug.
 * Returns { out, slug } where slug has no leading/trailing slash.
 */
export function mapDocPath(rel) {
  const parts = rel.split('/')
  if (parts.length > 1 && FOLDER_MAP[parts[0]]) parts[0] = FOLDER_MAP[parts[0]]
  const file = parts.pop()
  const isMd = MD.test(file)
  let stem = isMd ? file.replace(MD, '') : file
  if (isMd && /^(readme|index)$/i.test(stem)) stem = 'index'
  const outFile = isMd ? `${stem}.${/\.mdx$/i.test(file) ? 'mdx' : 'md'}` : file
  const out = ['docs', ...parts, outFile].join('/')
  const slugParts = ['docs', ...parts.map(slugSegment)]
  if (isMd && stem !== 'index') slugParts.push(slugSegment(stem))
  return { out, slug: slugParts.join('/') }
}

/** Split optional YAML frontmatter from a Markdown body. */
export function splitFrontmatter(src) {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?/.exec(src)
  if (!m) return { fm: '', body: src }
  return { fm: m[1], body: src.slice(m[0].length) }
}

/** True when the frontmatter defines a top-level key. */
function hasKey(fm, key) {
  return new RegExp(`^${key}\\s*:`, 'm').test(fm)
}

/**
 * Find the first ATX level-1 heading outside fenced code; returns
 * { title, body } with that heading line removed, or { title: '' }.
 */
export function extractTitle(body) {
  const lines = body.split('\n')
  let fence = ''
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const f = /^\s{0,3}(`{3,}|~{3,})/.exec(line)
    if (f) {
      if (!fence) fence = f[1][0]
      else if (f[1][0] === fence) fence = ''
      continue
    }
    if (fence) continue
    const h = /^#\s+(.+?)\s*#*\s*$/.exec(line)
    if (h) {
      lines.splice(i, 1)
      // Drop one blank line left behind by the heading.
      if (lines[i] !== undefined && lines[i].trim() === '') lines.splice(i, 1)
      return { title: stripInline(h[1]), body: lines.join('\n') }
    }
  }
  return { title: '', body }
}

/** Remove inline Markdown so text can live in frontmatter. */
export function stripInline(s) {
  return s
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/`([^`]*)`/g, '$1')
    .replace(/(\*\*|__)(.*?)\1/g, '$2')
    .replace(/(\*|_)(.*?)\1/g, '$2')
    .replace(/<[^>]+>/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}

/** First prose paragraph (not a heading, list, table, quote, code or HTML), max ~160 chars. */
export function extractDescription(body) {
  const blocks = body.replace(/\r/g, '').split(/\n\s*\n/)
  let fence = false
  for (const raw of blocks) {
    const block = raw.trim()
    const fences = (block.match(/^\s*(```|~~~)/gm) || []).length
    if (fence) {
      if (fences % 2 === 1) fence = false
      continue
    }
    if (fences % 2 === 1) {
      fence = true
      continue
    }
    if (!block || fences) continue
    if (/^(#|[-*+]\s|\d+\.\s|\||>|<|!\[|---|===|\[!)/.test(block)) continue
    const text = stripInline(block)
    if (text.length < 20) continue
    if (text.length <= 160) return text
    const cut = text.slice(0, 157)
    return `${cut.slice(0, Math.max(cut.lastIndexOf(' '), 100)).replace(/[,;:.\s]+$/, '')}…`
  }
  return ''
}

/**
 * Rewrite relative links in Markdown (inline `[t](x)` and `![a](x)` and
 * reference definitions `[id]: x`). `fromRel` is the source path relative
 * to /docs. Markdown targets become site routes; targets outside /docs
 * become GitHub URLs; assets inside /docs stay relative (copied alongside).
 */
export function rewriteLinks(body, fromRel, { base }) {
  const b = normalizeBase(base)
  const fromDir = posix.dirname(fromRel)
  const map = (target) => {
    if (!target || /^([a-z][a-z0-9+.-]*:|#|\/\/|\/)/i.test(target)) return target
    const [pathPart, hash = ''] = target.split('#')
    const [path, query = ''] = pathPart.split('?')
    if (!path) return target
    const resolved = posix.normalize(posix.join(fromDir, decodeURI(path)))
    const suffix = (query ? `?${query}` : '') + (hash ? `#${hash}` : '')
    if (resolved.startsWith('../') || resolved === '..') {
      // Outside /docs: link to the file on GitHub.
      const repoPath = posix.normalize(posix.join('docs', resolved))
      const isDir = path.endsWith('/') || !/\.[a-z0-9]+$/i.test(path)
      return `${REPO}/${isDir ? 'tree' : 'blob'}/${BRANCH}/${encodeURI(repoPath.replace(/\/$/, ''))}${suffix}`
    }
    const isMd = MD.test(resolved)
    const isDir = path.endsWith('/') || resolved === '.'
    if (isMd || isDir) {
      const rel = isDir ? posix.join(resolved === '.' ? '' : resolved, 'README.md') : resolved
      const { slug } = mapDocPath(rel.replace(/^\.\//, ''))
      return `${b}${slug}/${suffix}`
    }
    return target
  }
  let fence = ''
  return body
    .split('\n')
    .map((line) => {
      const f = /^\s{0,3}(`{3,}|~{3,})/.exec(line)
      if (f) {
        if (!fence) fence = f[1][0]
        else if (f[1][0] === fence) fence = ''
        return line
      }
      if (fence) return line
      // Leave inline code spans untouched.
      return line
        .split(/(`[^`]*`)/)
        .map((part) =>
          part.startsWith('`')
            ? part
            : part
                .replace(/(!?\[[^\]]*\]\()(<[^>]+>|[^)\s]+)((?:\s+"[^"]*")?\))/g, (_, pre, url, post) => {
                  const bare = url.startsWith('<') ? url.slice(1, -1) : url
                  return `${pre}${map(bare)}${post}`
                })
                .replace(/^(\s{0,3}\[[^\]]+\]:\s*)(\S+)/, (_, pre, url) => `${pre}${map(url)}`),
        )
        .join('')
    })
    .join('\n')
}

/** YAML-safe scalar (JSON strings are valid YAML double-quoted scalars). */
const y = (s) => JSON.stringify(s)

/** Title-case a file stem for pages that have no heading. */
function titleFromStem(stem) {
  if (stem === 'index') return 'Overview'
  return stem.replace(/[-_]+/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
}

/** Transform one Markdown file. `rel` is the path relative to /docs. */
export function transformMarkdown(src, rel, opts) {
  const { fm, body: rawBody } = splitFrontmatter(src.replace(/^\uFEFF/, ''))
  let body = rawBody
  const add = []
  let title = ''
  if (!hasKey(fm, 'title')) {
    const t = extractTitle(body)
    body = t.body
    title = t.title || titleFromStem(posix.basename(mapDocPath(rel).out).replace(MD, ''))
    add.push(`title: ${y(title)}`)
  }
  if (!hasKey(fm, 'description')) {
    const d = extractDescription(body)
    if (d) add.push(`description: ${y(d)}`)
  }
  if (!hasKey(fm, 'editUrl')) add.push(`editUrl: ${y(`${REPO}/edit/${BRANCH}/docs/${rel}`)}`)
  body = rewriteLinks(body, rel, opts)
  const front = [fm.trim(), ...add].filter(Boolean).join('\n')
  return `---\n${front}\n---\n\n${body.replace(/^\s+/, '')}`
}

/** Recursively list files under dir, as posix paths relative to dir. */
function walk(dir, prefix = '') {
  const out = []
  for (const name of readdirSync(dir).sort()) {
    if (name.startsWith('.') || name === 'node_modules') continue
    const abs = join(dir, name)
    const rel = prefix ? `${prefix}/${name}` : name
    if (statSync(abs).isDirectory()) out.push(...walk(abs, rel))
    else out.push(rel)
  }
  return out
}

/** Generated landing page used until /docs has its own index. */
export function fallbackIndex(pages, base) {
  const b = normalizeBase(base)
  const list = pages
    .filter((p) => p.slug !== 'docs')
    .map((p) => `- [${p.title}](${b}${p.slug}/)`)
    .join('\n')
  return `---
title: "Relay documentation"
description: "Install Relay, open your machine from any browser, and get the most out of terminals, agents, files and the command center."
editUrl: false
---

Relay turns your computer or server into a private workspace you can open
from any browser. These docs cover installing it, exposing it safely, and
every feature in the app.

The user guides are being written right now. Until they land, start with
the [install page](${b}install/) and the pages below.

${list || '- Nothing here yet.'}
`
}

/** Build the sidebar manifest from the mapped pages. */
export function buildSidebar(pages) {
  const top = []
  const groups = new Map()
  for (const p of pages) {
    const parts = p.slug.split('/').slice(1)
    if (parts.length <= 1) {
      top.push(p)
      continue
    }
    const key = parts[0]
    if (!groups.has(key)) groups.set(key, [])
    groups.get(key).push(p)
  }
  const rank = (p) => {
    const stem = p.slug.split('/').pop()
    const i = TOP_ORDER.indexOf(p.slug === 'docs' ? 'index' : stem)
    return i === -1 ? 100 : i
  }
  top.sort((a, b) => rank(a) - rank(b) || a.title.localeCompare(b.title))
  const keys = [...groups.keys()].sort((a, b) => {
    if (a === 'contributing') return 1
    if (b === 'contributing') return -1
    return a.localeCompare(b)
  })
  return {
    start: top.map((p) => ({ label: p.slug === 'docs' ? 'Overview' : p.title, slug: p.slug })),
    groups: keys.map((k) => ({
      label: FOLDER_LABELS[k] || titleFromStem(k),
      directory: `docs/${k}`,
      collapsed: k === 'contributing',
    })),
  }
}

/** Run the sync. Returns a summary. */
export function syncDocs({
  src = resolve(here, '../../docs'),
  dest = resolve(here, '../src/content/docs'),
  base = process.env.SITE_BASE ?? '/relay/',
} = {}) {
  const outRoot = join(dest, 'docs')
  rmSync(outRoot, { recursive: true, force: true })
  mkdirSync(outRoot, { recursive: true })
  const files = existsSync(src) ? walk(src) : []
  const pages = []
  let assets = 0
  for (const rel of files) {
    const { out, slug } = mapDocPath(rel)
    const abs = join(dest, ...out.split('/'))
    mkdirSync(dirname(abs), { recursive: true })
    if (MD.test(rel)) {
      const text = transformMarkdown(readFileSync(join(src, ...rel.split('/')), 'utf8'), rel, { base })
      writeFileSync(abs, text)
      const title = /^title:\s*"?(.*?)"?\s*$/m.exec(splitFrontmatter(text).fm)?.[1] ?? slug
      pages.push({ slug, title: title.replace(/\\"/g, '"') })
    } else {
      copyFileSync(join(src, ...rel.split('/')), abs)
      assets++
    }
  }
  if (!pages.some((p) => p.slug === 'docs')) {
    writeFileSync(join(outRoot, 'index.md'), fallbackIndex(pages, base))
    pages.unshift({ slug: 'docs', title: 'Overview' })
  }
  const sidebar = buildSidebar(pages)
  writeFileSync(join(outRoot, '.sidebar.json'), `${JSON.stringify(sidebar, null, 2)}\n`)
  return { pages: pages.length, assets, out: relative(process.cwd(), outRoot).split(sep).join('/') }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const r = syncDocs()
  console.log(`sync-docs: ${r.pages} pages, ${r.assets} assets -> ${r.out}`)
}
