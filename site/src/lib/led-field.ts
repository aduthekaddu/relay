/**
 * LedField — a live LED dot field in WebGL2.
 *
 * One instanced quad per dot (≤ 20k), positions derived from
 * gl_InstanceID, brightness sampled from two target textures that morph
 * with a per-dot noise dissolve. On top: an interference wave from two
 * "radio sources", a pointer lens, beacon rings, and a quiet rectangle
 * that dims the field wherever the big type sits.
 *
 * Pauses when the tab is hidden or the page says so; renders a single
 * static frame under reduced motion; reports `false` from `create` when
 * WebGL2 is unavailable so the caller can keep the static fallback.
 */
import { type Geometry, type Raster, rasterize, type Target } from './field-targets'

const MAX_DOTS = 20000
const DPR_CAP = 2

const VERT = `#version 300 es
precision highp float;
uniform vec2 uGrid;
uniform float uPitch;
uniform vec2 uRes;
uniform sampler2D uFrom;
uniform sampler2D uTo;
uniform float uMix;
uniform float uTime;
uniform float uAmbient;
uniform float uGain;
uniform vec4 uQuiet;
uniform float uQuietLevel;
uniform vec3 uPointer;
uniform vec4 uRings[3];
uniform vec4 uSources;
uniform vec3 uBeacon;
uniform float uBlink;
out float vBright;
out float vAccent;
out vec2 vUv;

float hash(vec2 p) {
  p = fract(p * vec2(123.34, 456.21));
  p += dot(p, p + 45.32);
  return fract(p.x * p.y);
}

void main() {
  int cols = int(uGrid.x);
  vec2 cell = vec2(float(gl_InstanceID % cols), float(gl_InstanceID / cols));
  vec2 uv = (cell + 0.5) / uGrid;
  vec4 a = texture(uFrom, uv);
  vec4 b = texture(uTo, uv);
  float n = hash(cell);
  float k = smoothstep(n * 0.7, n * 0.7 + 0.3, uMix);
  vec4 t = mix(a, b, k);
  // Dots mid-dissolve flicker like a board re-tuning.
  float mid = 1.0 - abs(k * 2.0 - 1.0);
  float flick = step(0.62, hash(cell + floor(uTime * 20.0))) * mid;

  vec2 p = (cell + 0.5) * uPitch;
  float d1 = distance(p, uSources.xy);
  float d2 = distance(p, uSources.zw);
  float f = 0.045 / uPitch * 8.0;
  float w = sin(d1 * f - uTime * 1.5) + sin(d2 * f * 1.07 - uTime * 1.2);
  float amb = pow(clamp(w * 0.25 + 0.5, 0.0, 1.0), 4.0) * uAmbient;
  // Waves fade with distance so the field breathes rather than buzzes.
  amb *= 0.55 + 0.45 * (1.0 - smoothstep(0.0, uRes.x * 0.9, min(d1, d2)));

  float bright = max(t.r * uGain, amb * 0.5) + flick * 0.28;
  float accent = t.g * uGain;

  float soft = uPitch * 10.0;
  float qx = smoothstep(uQuiet.x - soft, uQuiet.x, p.x) * (1.0 - smoothstep(uQuiet.z, uQuiet.z + soft, p.x));
  float qy = smoothstep(uQuiet.y - soft, uQuiet.y, p.y) * (1.0 - smoothstep(uQuiet.w, uQuiet.w + soft, p.y));
  float q = mix(1.0, uQuietLevel, qx * qy);
  bright *= q;
  accent *= mix(1.0, 0.6, 1.0 - q);

  float lensR = uPitch * 16.0;
  float lens = (1.0 - smoothstep(0.0, lensR, distance(p, uPointer.xy))) * uPointer.z;
  bright += lens * (0.22 + t.r * 0.5);

  for (int i = 0; i < 3; i++) {
    vec4 r = uRings[i];
    if (r.w <= 0.0) continue;
    float radius = r.z * uRes.y * 0.55;
    float band = uPitch * 2.2;
    float ring = exp(-pow((distance(p, r.xy) - radius) / band, 2.0));
    accent += ring * r.w * (1.0 - smoothstep(0.2, 1.6, r.z));
  }

  float bd = distance(p, uBeacon.xy);
  float beacon = (1.0 - smoothstep(uPitch * 0.6, uPitch * 1.6, bd)) * uBeacon.z * uBlink;
  float halo = (1.0 - smoothstep(0.0, uPitch * 7.0, bd)) * uBeacon.z * 0.35 * uBlink;
  accent = max(accent, beacon);
  bright += halo * 0.3;
  accent += halo * 0.4;

  vBright = clamp(0.06 + bright, 0.0, 1.0);
  vAccent = clamp(accent, 0.0, 1.0);
  float lit = max(bright, accent);
  float size = uPitch * (0.26 + 0.16 * clamp(lit, 0.0, 1.0) + lens * 0.14);
  vec2 corner = vec2(float(gl_VertexID & 1), float((gl_VertexID >> 1) & 1)) * 2.0 - 1.0;
  vUv = corner;
  vec2 pos = p + corner * size;
  vec2 clip = pos / uRes * 2.0 - 1.0;
  gl_Position = vec4(clip.x, -clip.y, 0.0, 1.0);
}`

const FRAG = `#version 300 es
precision mediump float;
in float vBright;
in float vAccent;
in vec2 vUv;
out vec4 outColor;
void main() {
  float d = length(vUv);
  float alpha = 1.0 - smoothstep(0.72, 1.0, d);
  vec3 bone = vec3(0.929, 0.914, 0.878);
  vec3 sig = vec3(1.0, 0.357, 0.122);
  float a = clamp(vAccent * 1.5, 0.0, 1.0);
  vec3 col = mix(bone, sig, a);
  float i = max(vBright, vAccent);
  outColor = vec4(col * i * alpha, i * alpha);
}`

/** Options for {@link LedField.create}. */
export interface FieldOptions {
  /** Dot pitch in CSS px (desktop). */
  pitch?: number
  /** Dot pitch in CSS px below 768 px wide. */
  mobilePitch?: number
  /** Render one static frame and never animate. */
  still?: boolean
}

type Rect = [number, number, number, number]

function compile(gl: WebGL2RenderingContext, type: number, src: string): WebGLShader {
  const s = gl.createShader(type)!
  gl.shaderSource(s, src)
  gl.compileShader(s)
  if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(`led-field shader: ${gl.getShaderInfoLog(s)}`)
  return s
}

/** The field. Create with {@link LedField.create}. */
export class LedField {
  private gl: WebGL2RenderingContext
  private prog: WebGLProgram
  private u: Record<string, WebGLUniformLocation | null> = {}
  private tex: [WebGLTexture, WebGLTexture]
  private rasters: [Raster | null, Raster | null] = [null, null]
  private geo: Geometry = { cols: 1, rows: 1, pitch: 8, width: 1, height: 1 }
  private dpr = 1
  private target: Target = { layers: [] }
  private mix = 1
  private mixFrom = 1
  private mixStart = 0
  private mixDur = 1
  private ambient = 0.6
  private ambientTo = 0.6
  private quiet: Rect = [0, 0, 0, 0]
  private quietLevel = 1
  private quietLevelTo = 1
  private gain = 1
  private gainTo = 1
  private pointer = { x: -1e4, y: -1e4, s: 0, to: 0 }
  private rings: { x: number; y: number; t0: number; s: number }[] = []
  private sources: [number, number, number, number] = [0, 0, 0, 0]
  private raf = 0
  private running = false
  private paused = false
  private start = performance.now()
  private token = 0
  private readonly opts: Required<FieldOptions>
  private readonly offs: (() => void)[] = []

  private constructor(
    private canvas: HTMLCanvasElement,
    gl: WebGL2RenderingContext,
    opts: FieldOptions,
  ) {
    this.gl = gl
    this.opts = { pitch: opts.pitch ?? 8, mobilePitch: opts.mobilePitch ?? 11, still: opts.still ?? false }
    const prog = gl.createProgram()!
    gl.attachShader(prog, compile(gl, gl.VERTEX_SHADER, VERT))
    gl.attachShader(prog, compile(gl, gl.FRAGMENT_SHADER, FRAG))
    gl.linkProgram(prog)
    if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) throw new Error(`led-field link: ${gl.getProgramInfoLog(prog)}`)
    this.prog = prog
    for (const name of [
      'uGrid',
      'uPitch',
      'uRes',
      'uFrom',
      'uTo',
      'uMix',
      'uTime',
      'uAmbient',
      'uGain',
      'uQuiet',
      'uQuietLevel',
      'uPointer',
      'uRings',
      'uSources',
      'uBeacon',
      'uBlink',
    ]) {
      this.u[name] = gl.getUniformLocation(prog, name)
    }
    this.tex = [this.makeTexture(), this.makeTexture()]
    gl.bindVertexArray(gl.createVertexArray())
    gl.enable(gl.BLEND)
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA)
    this.listen()
    this.resize()
  }

  /** Create a field on `canvas`; returns null when WebGL2 is unavailable. */
  static create(canvas: HTMLCanvasElement, opts: FieldOptions = {}): LedField | null {
    const gl = canvas.getContext('webgl2', { alpha: true, antialias: false, premultipliedAlpha: true, powerPreference: 'low-power' })
    if (!gl) return null
    try {
      return new LedField(canvas, gl, opts)
    } catch (e) {
      console.warn(e)
      return null
    }
  }

  private makeTexture(): WebGLTexture {
    const gl = this.gl
    const t = gl.createTexture()!
    gl.bindTexture(gl.TEXTURE_2D, t)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, 1, 1, 0, gl.RGBA, gl.UNSIGNED_BYTE, new Uint8Array(4))
    return t
  }

  private upload(i: 0 | 1, r: Raster | null) {
    const gl = this.gl
    gl.bindTexture(gl.TEXTURE_2D, this.tex[i])
    const { cols, rows } = this.geo
    const data = r && r.data.length === cols * rows * 4 ? r.data : new Uint8ClampedArray(cols * rows * 4)
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, cols, rows, 0, gl.RGBA, gl.UNSIGNED_BYTE, data)
  }

  private listen() {
    const on = <K extends keyof WindowEventMap>(t: K, fn: (e: WindowEventMap[K]) => void, o?: AddEventListenerOptions) => {
      window.addEventListener(t, fn, o)
      this.offs.push(() => window.removeEventListener(t, fn))
    }
    let resizeTimer = 0
    on('resize', () => {
      clearTimeout(resizeTimer)
      resizeTimer = window.setTimeout(() => this.resize(), 120)
    })
    on(
      'pointermove',
      (e) => {
        if (e.pointerType === 'touch') return
        this.pointer.x = e.clientX
        this.pointer.y = e.clientY
        this.pointer.to = 1
        this.kick()
      },
      { passive: true },
    )
    on('pointerleave', () => {
      this.pointer.to = 0
    })
    on(
      'pointerdown',
      (e) => {
        if (e.pointerType === 'touch') this.pulse(e.clientX / window.innerWidth, e.clientY / window.innerHeight, 0.5)
      },
      { passive: true },
    )
    const vis = () => (document.hidden ? this.stop() : this.kick())
    document.addEventListener('visibilitychange', vis)
    this.offs.push(() => document.removeEventListener('visibilitychange', vis))
  }

  /** Recompute the grid for the current viewport and re-rasterise. */
  resize() {
    const w = window.innerWidth
    const h = window.innerHeight
    let pitch = w < 768 ? this.opts.mobilePitch : this.opts.pitch
    while (Math.ceil(w / pitch) * Math.ceil(h / pitch) > MAX_DOTS) pitch += 0.5
    const cols = Math.ceil(w / pitch)
    const rows = Math.ceil(h / pitch)
    this.dpr = Math.min(DPR_CAP, window.devicePixelRatio || 1)
    this.canvas.width = Math.round(w * this.dpr)
    this.canvas.height = Math.round(h * this.dpr)
    const changed = cols !== this.geo.cols || rows !== this.geo.rows
    this.geo = { cols, rows, pitch, width: w, height: h }
    this.sources = [w * 0.82, h * 0.3, -w * 0.15, h * 1.2]
    if (changed) void this.retarget(this.target, 0)
    this.kick()
  }

  /** Viewport geometry currently in use. */
  geometry(): Geometry {
    return { ...this.geo }
  }

  /** Morph to a new target over `duration` seconds. */
  async setTarget(target: Target, duration = 1.1): Promise<void> {
    await this.retarget(target, duration)
  }

  private async retarget(target: Target, duration: number) {
    const token = ++this.token
    this.target = target
    const raster = await rasterize(target, this.geo)
    if (token !== this.token) return
    // Freeze the current look as the new "from" (whichever side dominates).
    const current = this.mix >= 0.5 ? this.rasters[1] : this.rasters[0]
    this.rasters = [current, raster]
    this.upload(0, current)
    this.upload(1, raster)
    this.ambientTo = target.ambient ?? 0.6
    if (raster.beacon) {
      this.sources[0] = raster.beacon[0]
      this.sources[1] = raster.beacon[1]
    }
    this.mixFrom = 0
    this.mix = duration <= 0 || this.opts.still ? 1 : 0
    this.mixStart = performance.now()
    this.mixDur = Math.max(0.001, duration) * 1000
    if (duration <= 0) this.ambient = this.ambientTo
    this.kick()
  }

  /** Dim the field inside a rect (CSS px, viewport coords) to `level` (0..1). */
  setQuiet(rect: DOMRect | Rect | null, level = 0.15) {
    if (!rect) {
      this.quietLevelTo = 1
    } else {
      this.quiet = Array.isArray(rect) ? rect : [rect.left, rect.top, rect.right, rect.bottom]
      this.quietLevelTo = level
    }
    this.kick()
  }

  /** Overall field brightness (0 = dark, 1 = full). */
  setGain(g: number) {
    this.gainTo = Math.max(0, Math.min(1.4, g))
    this.kick()
  }

  /** Emit a signal ring from a normalised viewport point. */
  pulse(x = 0.5, y = 0.5, strength = 1) {
    this.rings.push({ x: x * this.geo.width, y: y * this.geo.height, t0: performance.now(), s: strength })
    if (this.rings.length > 3) this.rings.shift()
    this.kick()
  }

  /** Stop animating (e.g. while an opaque section covers the field). */
  setPaused(p: boolean) {
    this.paused = p
    if (p) this.stop()
    else this.kick()
  }

  private kick() {
    if (this.running || this.paused || document.hidden) return
    this.running = true
    this.raf = requestAnimationFrame(this.frame)
  }

  private stop() {
    this.running = false
    cancelAnimationFrame(this.raf)
  }

  private frame = (now: number) => {
    if (!this.running) return
    const dt = 1 / 60
    const ease = (from: number, to: number, rate: number) => from + (to - from) * Math.min(1, dt * rate)
    if (this.mix < 1) {
      const t = Math.min(1, (now - this.mixStart) / this.mixDur)
      this.mix = 1 - (1 - t) ** 3
    }
    this.ambient = ease(this.ambient, this.ambientTo, 3)
    this.quietLevel = ease(this.quietLevel, this.quietLevelTo, 5)
    this.gain = ease(this.gain, this.gainTo, 4)
    this.pointer.s = ease(this.pointer.s, this.pointer.to, 6)
    this.rings = this.rings.filter((r) => now - r.t0 < 2600)
    this.draw(now)
    const settling =
      this.mix < 1 ||
      Math.abs(this.ambient - this.ambientTo) > 0.005 ||
      Math.abs(this.quietLevel - this.quietLevelTo) > 0.005 ||
      Math.abs(this.gain - this.gainTo) > 0.005
    if (this.opts.still && !settling && this.rings.length === 0) {
      this.running = false
      return
    }
    this.raf = requestAnimationFrame(this.frame)
  }

  private draw(now: number) {
    const gl = this.gl
    const { cols, rows, pitch, width, height } = this.geo
    const d = this.dpr
    const t = this.opts.still ? 4 : (now - this.start) / 1000
    gl.viewport(0, 0, this.canvas.width, this.canvas.height)
    gl.clearColor(0, 0, 0, 0)
    gl.clear(gl.COLOR_BUFFER_BIT)
    gl.useProgram(this.prog)
    gl.activeTexture(gl.TEXTURE0)
    gl.bindTexture(gl.TEXTURE_2D, this.tex[0])
    gl.uniform1i(this.u.uFrom!, 0)
    gl.activeTexture(gl.TEXTURE1)
    gl.bindTexture(gl.TEXTURE_2D, this.tex[1])
    gl.uniform1i(this.u.uTo!, 1)
    gl.uniform2f(this.u.uGrid!, cols, rows)
    gl.uniform1f(this.u.uPitch!, pitch * d)
    gl.uniform2f(this.u.uRes!, width * d, height * d)
    gl.uniform1f(this.u.uMix!, this.mix)
    gl.uniform1f(this.u.uTime!, t)
    gl.uniform1f(this.u.uAmbient!, this.opts.still ? this.ambientTo : this.ambient)
    gl.uniform1f(this.u.uGain!, this.gain)
    const q = this.quiet
    gl.uniform4f(this.u.uQuiet!, q[0] * d, q[1] * d, q[2] * d, q[3] * d)
    gl.uniform1f(this.u.uQuietLevel!, this.quietLevel)
    gl.uniform3f(this.u.uPointer!, this.pointer.x * d, this.pointer.y * d, this.opts.still ? 0 : this.pointer.s)
    const rings = new Float32Array(12)
    this.rings.forEach((r, i) => {
      rings.set([r.x * d, r.y * d, (now - r.t0) / 1000, r.s], i * 4)
    })
    gl.uniform4fv(this.u.uRings!, rings)
    const s = this.sources
    gl.uniform4f(this.u.uSources!, s[0] * d, s[1] * d, s[2] * d, s[3] * d)
    const b = this.rasters[1]?.beacon
    gl.uniform3f(this.u.uBeacon!, (b?.[0] ?? -1e4) * d, (b?.[1] ?? -1e4) * d, b ? 1 : 0)
    // Beacon blink: on 0.6 s / off 0.6 s, with a soft edge.
    const phase = (t % 1.2) / 1.2
    gl.uniform1f(this.u.uBlink!, this.opts.still ? 1 : phase < 0.5 ? 1 : 0.18)
    gl.drawArraysInstanced(gl.TRIANGLE_STRIP, 0, 4, cols * rows)
  }

  /** Release GL resources and listeners. */
  destroy() {
    this.stop()
    for (const off of this.offs) off()
    this.gl.getExtension('WEBGL_lose_context')?.loseContext()
  }
}
