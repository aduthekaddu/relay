// highlight.js core with languages loaded on demand. Import this module
// dynamically (CodeBlock and Markdown do) — it is not in the shell bundle.
import hljs from 'highlight.js/lib/core'

type LangModule = { default: Parameters<typeof hljs.registerLanguage>[1] }

const LOADERS: Record<string, () => Promise<LangModule>> = {
  bash: () => import('highlight.js/lib/languages/bash'),
  css: () => import('highlight.js/lib/languages/css'),
  diff: () => import('highlight.js/lib/languages/diff'),
  dockerfile: () => import('highlight.js/lib/languages/dockerfile'),
  go: () => import('highlight.js/lib/languages/go'),
  ini: () => import('highlight.js/lib/languages/ini'),
  javascript: () => import('highlight.js/lib/languages/javascript'),
  json: () => import('highlight.js/lib/languages/json'),
  markdown: () => import('highlight.js/lib/languages/markdown'),
  python: () => import('highlight.js/lib/languages/python'),
  rust: () => import('highlight.js/lib/languages/rust'),
  shell: () => import('highlight.js/lib/languages/shell'),
  sql: () => import('highlight.js/lib/languages/sql'),
  typescript: () => import('highlight.js/lib/languages/typescript'),
  xml: () => import('highlight.js/lib/languages/xml'),
  yaml: () => import('highlight.js/lib/languages/yaml'),
  makefile: () => import('highlight.js/lib/languages/makefile'),
  c: () => import('highlight.js/lib/languages/c'),
  cpp: () => import('highlight.js/lib/languages/cpp'),
  java: () => import('highlight.js/lib/languages/java'),
  ruby: () => import('highlight.js/lib/languages/ruby'),
  php: () => import('highlight.js/lib/languages/php'),
  swift: () => import('highlight.js/lib/languages/swift'),
  kotlin: () => import('highlight.js/lib/languages/kotlin'),
  lua: () => import('highlight.js/lib/languages/lua'),
}

const ALIASES: Record<string, string> = {
  sh: 'bash',
  zsh: 'bash',
  console: 'shell',
  js: 'javascript',
  jsx: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  html: 'xml',
  svg: 'xml',
  htm: 'xml',
  vue: 'xml',
  yml: 'yaml',
  toml: 'ini',
  conf: 'ini',
  md: 'markdown',
  py: 'python',
  rs: 'rust',
  golang: 'go',
  patch: 'diff',
  docker: 'dockerfile',
  make: 'makefile',
  h: 'c',
  hpp: 'cpp',
  cc: 'cpp',
  rb: 'ruby',
  kt: 'kotlin',
  jsonc: 'json',
}

/** Normalise a language name / extension to a loadable id, or null. */
export function resolveLang(lang: string | undefined): string | null {
  if (!lang) return null
  const l = lang.toLowerCase().replace(/^\./, '')
  const id = ALIASES[l] ?? l
  return id in LOADERS ? id : null
}

/** Guess a language from a file name. */
export function langFromPath(path: string): string | null {
  const base = path.split('/').pop() ?? ''
  if (/^dockerfile$/i.test(base)) return 'dockerfile'
  if (/^makefile$/i.test(base)) return 'makefile'
  const ext = base.includes('.') ? base.split('.').pop() : undefined
  return resolveLang(ext)
}

const loaded = new Set<string>()

async function ensure(id: string): Promise<void> {
  if (loaded.has(id)) return
  const mod = await LOADERS[id]()
  hljs.registerLanguage(id, mod.default)
  loaded.add(id)
  // TypeScript's grammar extends JavaScript's; xml is needed inside markdown.
  if (id === 'typescript') await ensure('javascript')
}

/**
 * Highlight `code`; resolves to HTML (escaped by highlight.js) or null when
 * the language is unknown. Inputs over 200 KB are not highlighted.
 */
export async function highlight(code: string, lang: string | undefined): Promise<string | null> {
  const id = resolveLang(lang)
  if (!id || code.length > 200_000) return null
  await ensure(id)
  return hljs.highlight(code, { language: id, ignoreIllegals: true }).value
}
