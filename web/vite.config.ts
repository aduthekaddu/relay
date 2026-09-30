import { brotliCompressSync, gzipSync, constants } from 'node:zlib'
import { readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import preact from '@preact/preset-vite'
import { defineConfig, type Plugin } from 'vite'

// Pre-compress built assets so the Go server can serve .br/.gz directly.
function precompress(): Plugin {
  return {
    name: 'relay-precompress',
    apply: 'build',
    closeBundle() {
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
  }
}

const relay = process.env.RELAY_DEV_URL || 'http://127.0.0.1:47700'

export default defineConfig({
  plugins: [preact(), precompress()],
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
    proxy: {
      '/api': { target: relay, ws: true, changeOrigin: false },
      '/apps': { target: relay, ws: true },
      '/p/': { target: relay, ws: true },
    },
  },
  test: { environment: 'happy-dom', include: ['src/**/*.test.{ts,tsx}'] },
})
