import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import {
  buildSidebar,
  extractDescription,
  extractTitle,
  mapDocPath,
  normalizeBase,
  rewriteLinks,
  syncDocs,
  transformMarkdown,
} from './sync-docs.mjs'

test('normalizeBase', () => {
  for (const [input, want] of [
    ['/relay/', '/relay/'],
    ['relay', '/relay/'],
    ['/', '/'],
    ['', '/'],
    ['//x//', '/x/'],
  ]) {
    assert.equal(normalizeBase(input), want, input)
  }
})

test('mapDocPath', () => {
  const cases = [
    ['README.md', 'docs/index.md', 'docs'],
    ['index.md', 'docs/index.md', 'docs'],
    ['install.md', 'docs/install.md', 'docs/install'],
    ['guides/VPS Setup.md', 'docs/guides/VPS Setup.md', 'docs/guides/vps-setup'],
    ['guides/README.md', 'docs/guides/index.md', 'docs/guides'],
    ['dev/API.md', 'docs/contributing/API.md', 'docs/contributing/api'],
    ['dev/TERMINAL_UX.md', 'docs/contributing/TERMINAL_UX.md', 'docs/contributing/terminal_ux'],
    ['img/shot.png', 'docs/img/shot.png', 'docs/img'],
    ['dev.md', 'docs/dev.md', 'docs/dev'],
  ]
  for (const [rel, out, slug] of cases) {
    const got = mapDocPath(rel)
    assert.equal(got.out, out, rel)
    if (rel.endsWith('.md')) assert.equal(got.slug, slug, rel)
  }
})

test('extractTitle skips fenced code and strips inline markdown', () => {
  const body = '```sh\n# not a title\n```\n\n# The **real** `title`\n\nText.\n'
  const { title, body: rest } = extractTitle(body)
  assert.equal(title, 'The real title')
  assert.ok(!rest.includes('# The'))
  assert.ok(rest.includes('# not a title'))
  assert.deepEqual(extractTitle('no heading here').title, '')
  assert.equal(extractTitle('## Level two only\n').title, '')
})

test('extractDescription', () => {
  const cases = [
    [
      'Relay turns your machine into a workspace you open from any browser.',
      'Relay turns your machine into a workspace you open from any browser.',
    ],
    [
      '## Heading\n\n- a list item that is long enough\n\nA [linked](x.md) paragraph with `code` in it.',
      'A linked paragraph with code in it.',
    ],
    [
      '```\nfenced paragraph that is long enough to count\n\nstill fenced text here\n```\n\nAfter the fence it is prose.',
      'After the fence it is prose.',
    ],
    ['short', ''],
    ['| a | b |\n| - | - |', ''],
  ]
  for (const [input, want] of cases) assert.equal(extractDescription(input), want)
  const long = extractDescription(`${'word '.repeat(60)}end.`)
  assert.ok(long.length <= 160 && long.endsWith('…'), long)
})

test('rewriteLinks', () => {
  const opts = { base: '/relay/' }
  const cases = [
    ['guides/vps.md', '[x](../install.md)', '[x](/relay/docs/install/)'],
    ['guides/vps.md', '[x](tailscale.md#setup)', '[x](/relay/docs/guides/tailscale/#setup)'],
    ['install.md', '[x](dev/API.md)', '[x](/relay/docs/contributing/api/)'],
    ['install.md', '[x](./guides/)', '[x](/relay/docs/guides/)'],
    ['install.md', '[x](README.md)', '[x](/relay/docs/)'],
    [
      'dev/API.md',
      '[x](../../internal/api/types.go)',
      '[x](https://github.com/aduthekaddu/relay/blob/main/internal/api/types.go)',
    ],
    ['dev/API.md', '[x](../../scripts)', '[x](https://github.com/aduthekaddu/relay/tree/main/scripts)'],
    ['install.md', '[x](https://example.com/a.md)', '[x](https://example.com/a.md)'],
    ['install.md', '[x](#anchor)', '[x](#anchor)'],
    ['install.md', '![shot](img/a.png)', '![shot](img/a.png)'],
    ['install.md', '[x](<with space.md> "t")', '[x](/relay/docs/with-space/ "t")'],
    ['install.md', '[ref]: guides/vps.md', '[ref]: /relay/docs/guides/vps/'],
    ['install.md', 'see `[x](a.md)` literally', 'see `[x](a.md)` literally'],
    ['install.md', '```\n[x](a.md)\n```', '```\n[x](a.md)\n```'],
  ]
  for (const [from, input, want] of cases)
    assert.equal(rewriteLinks(input, from, opts), want, `${from}: ${input}`)
  assert.equal(rewriteLinks('[x](a.md)', 'b.md', { base: '/' }), '[x](/docs/a/)')
})

test('transformMarkdown keeps existing frontmatter and adds missing keys', () => {
  const out = transformMarkdown(
    '---\ntitle: Kept\n---\n# Heading stays\n\nA description long enough to use.\n',
    'x.md',
    { base: '/' },
  )
  assert.match(
    out,
    /^---\ntitle: Kept\ndescription: "A description long enough to use."\neditUrl: ".*docs\/x.md"\n---/,
  )
  assert.ok(out.includes('# Heading stays'))
  const gen = transformMarkdown('# Install "Relay"\n\nBody text that is long enough.\n', 'install.md', {
    base: '/',
  })
  assert.match(gen, /title: "Install \\"Relay\\""/)
  assert.ok(!gen.includes('# Install'))
  const untitled = transformMarkdown('Just text without any heading at all.', 'guides/home-server.md', {
    base: '/',
  })
  assert.match(untitled, /title: "Home Server"/)
})

test('buildSidebar orders top pages and puts contributing last', () => {
  const sb = buildSidebar([
    { slug: 'docs/faq', title: 'FAQ' },
    { slug: 'docs', title: 'Overview' },
    { slug: 'docs/contributing/api', title: 'API' },
    { slug: 'docs/install', title: 'Install' },
    { slug: 'docs/guides/vps', title: 'VPS' },
  ])
  assert.deepEqual(
    sb.start.map((s) => s.slug),
    ['docs', 'docs/install', 'docs/faq'],
  )
  assert.deepEqual(
    sb.groups.map((g) => g.label),
    ['Guides', 'Contributing'],
  )
})

test('syncDocs end to end, with and without an index', () => {
  const root = mkdtempSync(join(tmpdir(), 'sync-docs-'))
  try {
    const src = join(root, 'docs')
    const dest = join(root, 'out')
    mkdirSync(join(src, 'dev'), { recursive: true })
    mkdirSync(join(src, 'img'), { recursive: true })
    writeFileSync(
      join(src, 'dev', 'API.md'),
      '# API\n\nThe HTTP contract for every endpoint. See [install](../install.md).\n',
    )
    writeFileSync(join(src, 'install.md'), '# Install\n\nOne line installs Relay on Linux or macOS.\n')
    writeFileSync(join(src, 'img', 'a.png'), 'png')
    let r = syncDocs({ src, dest, base: '/relay/' })
    assert.equal(r.pages, 3)
    assert.equal(r.assets, 1)
    const index = readFileSync(join(dest, 'docs', 'index.md'), 'utf8')
    assert.match(index, /title: "Relay documentation"/)
    assert.ok(index.includes('/relay/docs/install/'))
    const api = readFileSync(join(dest, 'docs', 'contributing', 'API.md'), 'utf8')
    assert.ok(api.includes('(/relay/docs/install/)'))
    assert.ok(existsSync(join(dest, 'docs', 'img', 'a.png')))
    const sb = JSON.parse(readFileSync(join(dest, 'docs', '.sidebar.json'), 'utf8'))
    assert.equal(sb.groups.at(-1).label, 'Contributing')

    writeFileSync(join(src, 'README.md'), '# Welcome\n\nThe real index page written by the docs team.\n')
    r = syncDocs({ src, dest, base: '/' })
    assert.equal(r.pages, 3)
    assert.match(readFileSync(join(dest, 'docs', 'index.md'), 'utf8'), /title: "Welcome"/)

    r = syncDocs({ src: join(root, 'missing'), dest, base: '/' })
    assert.equal(r.pages, 1)
    assert.ok(!existsSync(join(dest, 'docs', 'install.md')), 'stale output is removed')
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})
