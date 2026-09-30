import { createHash } from 'node:crypto'
import { brotliCompressSync, gzipSync, constants } from 'node:zlib'
import { readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import preact from '@preact/preset-vite'
import { defineConfig, type Plugin } from 'vite'

// Fill the service worker's precache list and version from the bundle:
// the entry chunk, its static imports, their CSS and the two fonts.
function serviceWorker(): Plugin {
  let shell: string[] = []
  return {
    name: 'relay-sw',
    apply: 'build',
    generateBundle(_opts, bundle) {
      const files = new Set<string>()
      const add = (name: string) => {
        const c = bundle[name]
        if (!c || files.has(name)) return
        files.add(name)
        if (c.type === 'chunk') {
          for (const i of c.imports) add(i)
          for (const css of c.viteMetadata?.importedCss ?? []) files.add(css)
        }
      }
      for (const [name, c] of Object.entries(bundle)) if (c.type === 'chunk' && c.isEntry) add(name)
      for (const name of Object.keys(bundle)) if (/(mona-sans-latin-standard|jetbrains-mono-latin-wght)-normal[^/]*\.woff2$/.test(name)) files.add(name)
      shell = ['/', '/offline.html', '/manifest.webmanifest', '/boot.js', '/icons/icon.svg', '/icons/icon-192.png', ...[...files].map((f) => `/${f}`)]
    },
    closeBundle: {
      sequential: true,
      order: 'pre',
      handler() {
        const p = resolve(__dirname, '../internal/web/dist/sw.js')
        const version = createHash('sha256').update(shell.join('\n')).digest('hex').slice(0, 12)
        const src = readFileSync(p, 'utf8')
          .replace('__RELAY_VERSION__', version)
          .replace('self.__RELAY_SHELL__ ||', `${JSON.stringify(shell)} ||`)
        writeFileSync(p, src)
      },
    },
  }
}

// Pre-compress built assets so the Go server can serve .br/.gz directly.
function precompress(): Plugin {
  return {
    name: 'relay-precompress',
    apply: 'build',
    closeBundle: {
      sequential: true,
      order: 'post',
      handler() {
      const out = resolve(__dirname, '../internal/web/dist')
      const walk = (dir: string) => {
        for (const name of readdirSync(dir)) {
          const p = join(dir, name)
          if (statSync(p).isDirectory()) { walk(p); continue }
          if (!/\.(js|css|html|svg|json|webmanifest|wasm|txt|map)$/.test(name)) continue
          const buf = readFileSync(p)
          if (buf.length < 1024) continue
          writeFileSync(`${p}.br`, brotliCompressSync(buf, { params: { [constants.BROTLI_PARAM_QUALITY]: 11 } }))
          writeFileSync(`${p}.gz`, gzipSync(buf, { level: 9 }))
        }
      }
      walk(out)
      },
    },
  }
}

// Preload the two Latin variable fonts so text never reflows after first
// paint (they are referenced from styles/fonts.css and emitted hashed).
function fontPreload(): Plugin {
  return {
    name: 'relay-font-preload',
    apply: 'build',
    transformIndexHtml: {
      order: 'post',
      handler(_html, ctx) {
        const files = Object.keys(ctx.bundle ?? {}).filter((f) =>
          /(mona-sans-latin-standard-normal|jetbrains-mono-latin-wght-normal)[^/]*\.woff2$/.test(f),
        )
        return files.map((f) => ({
          tag: 'link',
          attrs: { rel: 'preload', href: `/${f}`, as: 'font', type: 'font/woff2', crossorigin: '' },
          injectTo: 'head' as const,
        }))
      },
    },
  }
}

const relay = process.env.RELAY_DEV_URL || 'http://127.0.0.1:47700'

export default defineConfig({
  plugins: [preact(), fontPreload(), serviceWorker(), precompress()],
  resolve: { alias: { '@': resolve(__dirname, 'src') } },
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    target: 'es2022',
    sourcemap: false,
    cssCodeSplit: true,
    chunkSizeWarningLimit: 900,
  },
  server: {
    port: Number(process.env.VITE_PORT || 47780),
    strictPort: true,
    host: '127.0.0.1',
    // Tests read docs/dev/API.md (mock coverage); the dev server stays scoped to web/.
    fs: process.env.VITEST ? { allow: ['..'] } : undefined,
    proxy: {
      '/api': { target: relay, ws: true, changeOrigin: false },
      '/apps': { target: relay, ws: true },
      '/p/': { target: relay, ws: true },
    },
  },
  test: { environment: 'happy-dom', include: ['src/**/*.test.{ts,tsx}'] },
})
