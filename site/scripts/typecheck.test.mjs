import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

// The site's ambient types (astro:content, `?url` imports, import.meta.env)
// come only from Astro's generated `.astro/types.d.ts`. That directory is
// gitignored, and tsc silently skips a missing `include` entry, so a clean
// checkout typechecks without them unless `typecheck` generates them first.
const read = (path) => readFileSync(new URL(path, import.meta.url), 'utf8')
const GENERATED = '.astro/types.d.ts'

test('tsconfig includes the generated Astro types entrypoint', () => {
  const tsconfig = JSON.parse(read('../tsconfig.json'))
  assert.ok(tsconfig.include.includes(GENERATED), `tsconfig.include must list ${GENERATED}`)
})

test('the generated types are not committed', () => {
  const ignored = read('../.gitignore')
    .split('\n')
    .map((l) => l.trim())
  assert.ok(ignored.includes('.astro/'), '.astro/ is expected to be gitignored')
})

test('typecheck generates Astro types before running tsc', () => {
  const { scripts } = JSON.parse(read('../package.json'))
  const steps = scripts.typecheck.split('&&').map((s) => s.trim())
  const sync = steps.indexOf('astro sync')
  const tsc = steps.findIndex((s) => s.startsWith('tsc '))
  assert.ok(sync !== -1, `typecheck must run astro sync: ${scripts.typecheck}`)
  assert.ok(tsc > sync, `typecheck must run tsc after astro sync: ${scripts.typecheck}`)
})
