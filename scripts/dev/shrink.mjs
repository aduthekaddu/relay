#!/usr/bin/env node
// Make a review-sized copy of any image (≤ 1600 px per side, JPEG) using
// headless Chrome — no native image libraries needed.
//   node scripts/dev/shrink.mjs <in.(png|jpg|webp|avif)> <out.jpg> [maxPx]
import { readFileSync } from 'node:fs'
import { extname } from 'node:path'
import { chromium } from 'playwright-core'

const [input, out, maxArg] = process.argv.slice(2)
if (!input || !out) {
  console.error('usage: shrink.mjs <in> <out.jpg> [maxPx]')
  process.exit(2)
}
const max = Math.min(Number(maxArg || 1600), 1900)
const mime = { '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.webp': 'image/webp', '.avif': 'image/avif', '.gif': 'image/gif' }[extname(input).toLowerCase()] || 'image/png'
const src = `data:${mime};base64,${readFileSync(input).toString('base64')}`
const browser = await chromium.launch({ executablePath: process.env.CHROME || '/usr/bin/google-chrome', headless: true, args: ['--no-sandbox'] })
const page = await browser.newPage()
await page.setContent(`<img id=i src="${src}">`)
const { w, h } = await page.evaluate(async () => {
  const i = document.getElementById('i')
  await i.decode()
  return { w: i.naturalWidth, h: i.naturalHeight }
})
const k = Math.min(1, max / Math.max(w, h))
const W = Math.max(1, Math.round(w * k))
const H = Math.max(1, Math.round(h * k))
await page.setViewportSize({ width: W, height: H })
await page.setContent(`<style>html,body{margin:0}img{display:block;width:${W}px;height:${H}px}</style><img id=i src="${src}">`)
await page.evaluate(() => document.getElementById('i').decode())
await page.screenshot({ path: out, type: 'jpeg', quality: 80, clip: { x: 0, y: 0, width: W, height: H } })
console.log(`${input} ${w}x${h} -> ${out} ${W}x${H}`)
await browser.close()
