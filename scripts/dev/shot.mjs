#!/usr/bin/env node
// Headless screenshots with system Chrome and an isolated temporary
// profile, so several people (or agents) can capture at once.
//
//   node scripts/dev/shot.mjs <url> <out.jpg|out.png> [--mobile|--tablet] [--full]
//        [--dpr 1]  (default 1: small files for reviewing; use 2 for crisp assets)
//   Prefer .jpg outputs for reviewing (quality 72): they are ~10x smaller than PNG.
//        [--wait ms] [--scroll px] [--dark|--light] [--eval "js"] [--click sel]
//        [--login user:pass]  (signs in through /api/v1/auth/login first)
//
// Multiple shots of one page while scrolling (for scroll-driven sites):
//   node scripts/dev/shot.mjs <url> out-%d.png --frames 0,900,1800,2700
import { chromium } from 'playwright-core'

const args = process.argv.slice(2)
const url = args[0]
const out = args[1]
if (!url || !out) {
  console.error('usage: shot.mjs <url> <out.png> [flags]')
  process.exit(2)
}
const flag = (n) => args.includes(n)
const opt = (n, d) => {
  const i = args.indexOf(n)
  return i >= 0 ? args[i + 1] : d
}
function shotType(file) {
  return /\.jpe?g$/i.test(file) ? { type: 'jpeg', quality: Number(opt('--quality', '72')) } : { type: 'png' }
}
const exe = process.env.CHROME || '/usr/bin/google-chrome'
const browser = await chromium.launch({ executablePath: exe, headless: true, args: ['--no-sandbox', '--use-gl=angle', '--use-angle=swiftshader', '--enable-unsafe-swiftshader'] })
const mobile = flag('--mobile')
const tablet = flag('--tablet')
const ctx = await browser.newContext({
  viewport: mobile ? { width: 390, height: 844 } : tablet ? { width: 820, height: 1180 } : { width: 1440, height: 900 },
  deviceScaleFactor: Number(opt('--dpr', '1')),
  isMobile: mobile,
  hasTouch: mobile || tablet,
  colorScheme: flag('--light') ? 'light' : 'dark',
  userAgent: mobile ? 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' : undefined,
})
const page = await ctx.newPage()
const logs = []
page.on('console', (m) => logs.push(`[${m.type()}] ${m.text()}`))
page.on('pageerror', (e) => logs.push(`[pageerror] ${e.message}`))
const login = opt('--login')
if (login) {
  const [username, password] = login.split(':')
  const origin = new URL(url).origin
  await page.goto(`${origin}/login`, { waitUntil: 'domcontentloaded' })
  const r = await page.evaluate(async ({ username, password }) => {
    const res = await fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username, password, remember: true }) })
    return res.status
  }, { username, password })
  if (r !== 200) logs.push(`[login] status ${r}`)
}
await page.goto(url, { waitUntil: 'networkidle', timeout: 45000 }).catch((e) => logs.push(`[goto] ${e.message}`))
const click = opt('--click')
if (click) await page.click(click).catch((e) => logs.push(`[click] ${e.message}`))
const ev = opt('--eval')
if (ev) await page.evaluate(ev).catch((e) => logs.push(`[eval] ${e.message}`))
await page.waitForTimeout(Number(opt('--wait', '800')))
const frames = opt('--frames')
if (frames) {
  let i = 0
  for (const y of frames.split(',').map(Number)) {
    await page.evaluate((y) => window.scrollTo({ top: y, behavior: 'instant' }), y)
    await page.waitForTimeout(Number(opt('--frame-wait', '900')))
    await page.screenshot({ path: out.replace('%d', String(i++)), ...shotType(out) })
  }
} else {
  const scroll = Number(opt('--scroll', '0'))
  if (scroll) {
    await page.evaluate((y) => window.scrollTo({ top: y, behavior: 'instant' }), scroll)
    await page.waitForTimeout(700)
  }
  await page.screenshot({ path: out, fullPage: flag('--full'), ...shotType(out) })
}
if (logs.length) console.log(logs.slice(0, 40).join('\n'))
await browser.close()
