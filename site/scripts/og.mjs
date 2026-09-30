#!/usr/bin/env node
// Renders the Open Graph card and the raster favicons into public/.
//
//   pnpm build && pnpm og              # previews dist/ on :47790, shoots /og
//   OG_URL=http://127.0.0.1:47790/relay/og pnpm og   # reuse a running server
//
// The card is the dedicated /og page, captured with scripts/dev/shot.mjs at
// 2x and cropped to 1200x630. Favicons are rasterised from favicon.svg with
// sharp, on the carbon ground so they read on any tab or home screen.
import { spawn, spawnSync } from 'node:child_process'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import sharp from 'sharp'

const here = dirname(fileURLToPath(import.meta.url))
const siteDir = join(here, '..')
const pub = join(siteDir, 'public')
const shot = join(siteDir, '..', 'scripts', 'dev', 'shot.mjs')
const PORT = Number(process.env.OG_PORT || 47790)
const CARBON = '#0b0b0c'

/** Resolves true once url answers with any HTTP status, false on timeout. */
async function waitFor(url, ms) {
  const end = Date.now() + ms
  while (Date.now() < end) {
    try {
      const r = await fetch(url, { signal: AbortSignal.timeout(2000) })
      if (r.status < 500) return true
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 300))
  }
  return false
}

/** Starts `astro preview` on PORT unless OG_URL points at a live server. */
async function server() {
  const base = (process.env.SITE_BASE ?? '/relay/').replace(/^\/?/, '/').replace(/\/?$/, '/')
  const url = process.env.OG_URL || `http://127.0.0.1:${PORT}${base}og`
  if (await waitFor(url, 500)) return { url, stop: () => {} }
  const child = spawn(
    'node',
    ['node_modules/.bin/astro', 'preview', '--port', String(PORT), '--host', '127.0.0.1'],
    {
      cwd: siteDir,
      stdio: 'ignore',
    },
  )
  if (!(await waitFor(url, 20000))) {
    child.kill()
    throw new Error(`preview did not come up at ${url} (run pnpm build first)`)
  }
  return { url, stop: () => child.kill() }
}

async function ogCard(url) {
  const tmp = mkdtempSync(join(tmpdir(), 'relay-og-'))
  try {
    const raw = join(tmp, 'og.png')
    const r = spawnSync('node', [shot, url, raw, '--dpr', '1.3', '--wait', '1500'], { stdio: 'inherit' })
    if (r.status !== 0) throw new Error(`shot.mjs exited with ${r.status}`)
    // shot.mjs captures the 1440x900 viewport; the card is pinned top-left.
    const meta = await sharp(raw).metadata()
    const k = meta.width / 1440
    await sharp(raw)
      .extract({ left: 0, top: 0, width: Math.round(1200 * k), height: Math.round(630 * k) })
      .resize(1200, 630)
      .png({ compressionLevel: 9, palette: true, quality: 90 })
      .toFile(join(pub, 'og.png'))
    console.log('wrote public/og.png')
  } finally {
    rmSync(tmp, { recursive: true, force: true })
  }
}

async function favicons() {
  const svg = join(pub, 'favicon.svg')
  const sizes = [
    ['favicon-32.png', 32, 0.06],
    ['apple-touch-icon.png', 180, 0.18],
    ['icon-192.png', 192, 0.18],
    ['icon-512.png', 512, 0.18],
  ]
  for (const [name, size, padRatio] of sizes) {
    const pad = Math.round(size * padRatio)
    const inner = size - pad * 2
    const mark = await sharp(svg, { density: 72 * Math.ceil(inner / 28) * 2 })
      .resize(inner, inner)
      .png()
      .toBuffer()
    await sharp({ create: { width: size, height: size, channels: 4, background: CARBON } })
      .composite([{ input: mark, left: pad, top: pad }])
      .png({ compressionLevel: 9 })
      .toFile(join(pub, name))
    console.log(`wrote public/${name}`)
  }
}

const skipCard = process.argv.includes('--icons-only')
await favicons()
if (!skipCard) {
  const s = await server()
  try {
    await ogCard(s.url)
  } finally {
    s.stop()
  }
}
