#!/usr/bin/env node
// House image treatment: source photographs -> Atkinson-dithered dot
// duotone (carbon + bone, orange highlight), at 1x and 2x as AVIF + WebP.
//
//   node scripts/dither.mjs                  # every *.png in $IN (default /tmp/relay-img)
//   node scripts/dither.mjs tower desk       # only these
//   IN=/path/to/sources node scripts/dither.mjs
//
// Outputs into src/assets/img/:
//   <name>.{avif,webp}      1x (cols × PITCH px wide)
//   <name>@2x.{avif,webp}   2x
//   <name>.field.webp       tiny lossless target texture for LedField
//                           (R = tone-mapped luminance, G = orange mask)
//   meta.json               size, aspect and beacon position per image
import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { basename, dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import sharp from 'sharp'

const here = dirname(fileURLToPath(import.meta.url))
const IN = process.env.IN || '/tmp/relay-img'
const OUT = resolve(here, '../src/assets/img')

/** Dot grid columns and CSS pixel pitch at 1x. */
const COLS = 240
const PITCH = 4
/** Field texture width (one texel per ~dot at the densest field pitch). */
const FIELD_W = 256

const CARBON = [11, 11, 12]
const BONE = [237, 233, 224]
const SIGNAL = [255, 91, 31]
/** Per-image tone overrides (black point percentile, gamma, orange floor). */
const TUNE = {
  tower: { black: 0.66, gamma: 0.95 },
  desk: { black: 0.2, gamma: 0.62, orange: 125 },
  rack: { black: 0.35, gamma: 0.7 },
}

/** Off-dots stay faintly visible so the grid always reads. */
const OFF_ALPHA = 0.07

/** Tone curve: the `black` percentile maps to 0, the 99.7th to 1, then a gamma lift. */
export function toneMap(lum, gamma = 0.8, black = 0.4) {
  const sorted = Float32Array.from(lum).sort()
  const hi = sorted[Math.floor(sorted.length * 0.997)] || 1
  // Night images: the dominant dark tone (sky, walls) is the black point.
  const lo = sorted[Math.floor(sorted.length * black)] || 0
  const out = new Float32Array(lum.length)
  const span = Math.max(hi - lo, 1e-3)
  for (let i = 0; i < lum.length; i++) out[i] = Math.min(1, Math.max(0, (lum[i] - lo) / span)) ** gamma
  return out
}

/** Atkinson dither in place on a w×h grid of 0..1 values; returns 0/1 bits. */
export function atkinson(values, w, h, threshold = 0.5) {
  const v = Float32Array.from(values)
  const bits = new Uint8Array(w * h)
  const spread = [
    [1, 0],
    [2, 0],
    [-1, 1],
    [0, 1],
    [1, 1],
    [0, 2],
  ]
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const i = y * w + x
      const on = v[i] >= threshold ? 1 : 0
      bits[i] = on
      const err = (v[i] - on) / 8
      for (const [dx, dy] of spread) {
        const nx = x + dx
        const ny = y + dy
        if (nx >= 0 && nx < w && ny < h) v[ny * w + nx] += err
      }
    }
  }
  return bits
}

/** Warm, bright pixels become the orange highlight. */
export function orangeMask(r, g, b, floor = 70) {
  const lum = 0.2126 * r + 0.7152 * g + 0.0722 * b
  return lum > floor && r > 150 && r > g * 1.35 && r > b * 1.9 ? Math.min(1, (lum - floor) / 90) : 0
}

/** Render the dot grid into an RGB buffer at the given pitch. */
function renderDots(bits, mask, cols, rows, pitch) {
  const W = cols * pitch
  const H = rows * pitch
  const buf = Buffer.alloc(W * H * 3)
  for (let i = 0; i < W * H; i++) buf.set(CARBON, i * 3)
  const r = pitch * 0.38
  const c = (pitch - 1) / 2
  for (let cy = 0; cy < rows; cy++) {
    for (let cx = 0; cx < cols; cx++) {
      const i = cy * cols + cx
      const hot = mask[i] > 0.25
      const color = hot ? SIGNAL : BONE
      const alpha = hot ? 1 : bits[i] ? 0.86 : OFF_ALPHA
      const rad = hot ? r * 1.15 : r
      for (let py = 0; py < pitch; py++) {
        for (let px = 0; px < pitch; px++) {
          const d = Math.hypot(px - c, py - c)
          const cov = Math.min(1, Math.max(0, rad - d + 0.5)) * alpha
          if (cov <= 0) continue
          const o = ((cy * pitch + py) * W + cx * pitch + px) * 3
          for (let k = 0; k < 3; k++) buf[o + k] = Math.round(buf[o + k] * (1 - cov) + color[k] * cov)
        }
      }
    }
  }
  return { buf, W, H }
}

/** Process one source image. */
async function processImage(file) {
  const name = basename(file, '.png')
  const tune = { black: 0.4, gamma: 0.8, orange: 70, ...TUNE[name] }
  const src = sharp(readFileSync(file)).removeAlpha()
  const { width = 1, height = 1 } = await src.metadata()
  const rows = Math.round((COLS * height) / width)
  const { data } = await src.clone().resize(COLS, rows, { fit: 'fill', kernel: 'lanczos3' }).raw().toBuffer({ resolveWithObject: true })
  const lum = new Float32Array(COLS * rows)
  const mask = new Float32Array(COLS * rows)
  let bx = 0
  let by = 0
  let bw = 0
  for (let i = 0; i < COLS * rows; i++) {
    const r = data[i * 3]
    const g = data[i * 3 + 1]
    const b = data[i * 3 + 2]
    lum[i] = 0.2126 * r + 0.7152 * g + 0.0722 * b
    mask[i] = orangeMask(r, g, b, tune.orange)
    const w = mask[i] ** 2
    bx += (i % COLS) * w
    by += Math.floor(i / COLS) * w
    bw += w
  }
  const tone = toneMap(lum, tune.gamma, tune.black)
  const bits = atkinson(tone, COLS, rows)
  for (const scale of [1, 2]) {
    const { buf, W, H } = renderDots(bits, mask, COLS, rows, PITCH * scale)
    const img = sharp(buf, { raw: { width: W, height: H, channels: 3 } })
    const suffix = scale === 2 ? '@2x' : ''
    await img.clone().avif({ quality: 52, effort: 4 }).toFile(join(OUT, `${name}${suffix}.avif`))
    await img.clone().webp({ quality: 78, effort: 5 }).toFile(join(OUT, `${name}${suffix}.webp`))
  }
  // Field texture: R = tone-mapped luminance, G = orange mask, B = 0.
  const fh = Math.round((FIELD_W * height) / width)
  const { data: fd } = await sharp(readFileSync(file))
    .removeAlpha()
    .resize(FIELD_W, fh, { fit: 'fill' })
    .raw()
    .toBuffer({ resolveWithObject: true })
  const fl = new Float32Array(FIELD_W * fh)
  for (let i = 0; i < fl.length; i++) fl[i] = 0.2126 * fd[i * 3] + 0.7152 * fd[i * 3 + 1] + 0.0722 * fd[i * 3 + 2]
  const ft = toneMap(fl, tune.gamma, tune.black * 0.8)
  const field = Buffer.alloc(FIELD_W * fh * 3)
  for (let i = 0; i < fl.length; i++) {
    field[i * 3] = Math.round(ft[i] * 255)
    field[i * 3 + 1] = Math.round(orangeMask(fd[i * 3], fd[i * 3 + 1], fd[i * 3 + 2], tune.orange) * 255)
  }
  await sharp(field, { raw: { width: FIELD_W, height: fh, channels: 3 } })
    .webp({ lossless: true })
    .toFile(join(OUT, `${name}.field.webp`))
  return {
    name,
    width: COLS * PITCH,
    height: rows * PITCH,
    aspect: +(width / height).toFixed(4),
    beacon: bw > 0 ? [+(bx / bw / COLS).toFixed(4), +(by / bw / rows).toFixed(4)] : null,
  }
}

async function main() {
  if (!existsSync(IN)) throw new Error(`no sources in ${IN}; run scripts/gen-images.sh first`)
  mkdirSync(OUT, { recursive: true })
  const want = process.argv.slice(2)
  const files = readdirSync(IN)
    .filter((f) => f.endsWith('.png') && (!want.length || want.includes(basename(f, '.png'))))
    .map((f) => join(IN, f))
  const metaPath = join(OUT, 'meta.json')
  const meta = existsSync(metaPath) ? JSON.parse(readFileSync(metaPath, 'utf8')) : {}
  for (const f of files) {
    const m = await processImage(f)
    meta[m.name] = { width: m.width, height: m.height, aspect: m.aspect, beacon: m.beacon }
    console.log(`dither: ${m.name} ${m.width}×${m.height} beacon=${m.beacon ? m.beacon.join(',') : '-'}`)
  }
  writeFileSync(metaPath, `${JSON.stringify(meta, null, 2)}\n`)
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((e) => {
    console.error(e)
    process.exit(1)
  })
}
