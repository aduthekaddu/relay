// Relay marketing site + docs.
//
//   SITE_URL   origin the site is served from (default GitHub Pages)
//   SITE_BASE  path prefix (default /relay/; use / for a custom domain)
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import starlight from '@astrojs/starlight'
import { defineConfig } from 'astro/config'
import { normalizeBase, syncDocs } from './scripts/sync-docs.mjs'

const site = process.env.SITE_URL || 'https://aduthekaddu.github.io'
const base = normalizeBase(process.env.SITE_BASE ?? '/relay/')

const manifestPath = fileURLToPath(new URL('./src/content/docs/docs/.sidebar.json', import.meta.url))
if (!existsSync(manifestPath)) syncDocs({ base })
const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))

const sidebar = [
  { label: 'Start here', items: manifest.start.map(({ label, slug }) => ({ label, slug })) },
  ...manifest.groups.map((g) => ({ label: g.label, collapsed: g.collapsed, items: [{ autogenerate: { directory: g.directory } }] })),
]

export default defineConfig({
  site,
  base,
  trailingSlash: 'ignore',
  output: 'static',
  prefetch: { prefetchAll: false, defaultStrategy: 'hover' },
  build: { inlineStylesheets: 'auto', assets: '_a' },
  devToolbar: { enabled: false },
  vite: {
    build: { target: 'es2022' },
  },
  integrations: [
    starlight({
      title: 'Relay docs',
      description: 'Install Relay and open your machine from any browser.',
      logo: { src: './src/assets/brand/mark.svg', alt: 'Relay' },
      favicon: '/favicon.svg',
      social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/aduthekaddu/relay' }],
      customCss: ['./src/styles/starlight.css'],
      disable404Route: true,
      lastUpdated: false,
      pagefind: true,
      sidebar,
      components: {
        SiteTitle: './src/components/docs/SiteTitle.astro',
      },
      head: [
        { tag: 'meta', attrs: { name: 'theme-color', content: '#0b0b0c' } },
        { tag: 'meta', attrs: { property: 'og:image', content: `${site}${base}og.png` } },
      ],
    }),
  ],
})
