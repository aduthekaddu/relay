import assert from 'node:assert/strict'
import { test } from 'node:test'
import { chunkName, references } from './budget.mjs'

test('chunkName strips directory and hash', () => {
  const cases = [
    ['phone-terminal.C8obwALA.js', 'phone-terminal'],
    ['_a/home.LvPAkyKx.js', 'home'],
    ['Base.astro_astro_type_script_index_0_lang.BIajjMET.js', 'Base.astro_astro_type_script_index_0_lang'],
  ]
  for (const [input, want] of cases) assert.equal(chunkName(input), want)
})

test('references separates static and dynamic imports', () => {
  const code = [
    'import{g as a}from"./ui-core.BZ5VjqyC.js";',
    'import"./nav.CMl7WuRh.js";',
    'const m=["_a/flapboard.BQJQY_fv.js","_a/ui-core.BZ5VjqyC.js"];',
    'b(()=>import("./home.LvPAkyKx.js"),m);',
    'const s="not-a-chunk.js";',
  ].join('')
  const [stat, dyn] = references(code)
  assert.deepEqual(stat.sort(), ['nav.CMl7WuRh.js', 'ui-core.BZ5VjqyC.js'])
  assert.deepEqual(dyn.sort(), ['flapboard.BQJQY_fv.js', 'home.LvPAkyKx.js'])
})
