// Unit tests for the site's pure modules (Node strips the TS types).
//   node --test scripts/*.test.mjs
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clockAt, nightPos } from '../src/lib/clock.ts'
import { DOT_H, eachDot, glyph, measure } from '../src/lib/dotfont.ts'
import { DRUM, flapPose, flipPath, MAX_FLIPS } from '../src/lib/flap.ts'
import { COMPOSE, LINES, typed } from '../src/lib/phone-script.ts'

test('flipPath walks the drum forward and caps long trips', () => {
  const cases = [
    { from: 'A', to: 'A', want: [] },
    { from: 'A', to: 'C', want: ['B', 'C'] },
    { from: 'a', to: 'c', want: ['B', 'C'] },
    { from: '●', to: 'A', want: [' ', 'A'] },
    { from: '?', to: 'B', want: ['A', 'B'] },
  ]
  for (const c of cases) assert.deepEqual(flipPath(c.from, c.to), c.want, `${c.from}→${c.to}`)
  const long = flipPath(' ', 'Z')
  assert.equal(long.length, MAX_FLIPS)
  assert.equal(long.at(-1), 'Z')
  for (let i = 1; i < long.length; i++) {
    assert.equal(DRUM.indexOf(long[i]), DRUM.indexOf(long[i - 1]) + 1, 'consecutive drum glyphs')
  }
})

test('flapPose: before, during and after a flip', () => {
  const path = ['B', 'C']
  const before = flapPose('A', path, -10, 60)
  assert.equal(before.moving, false)
  assert.equal(before.top, 'A')

  const falling = flapPose('A', path, 15, 60)
  assert.equal(falling.moving, true)
  assert.equal(falling.top, 'B', 'upper half already shows the incoming glyph')
  assert.equal(falling.bottom, 'A', 'lower half still shows the outgoing glyph')
  assert.ok(falling.topAngle < 0 && falling.topAngle > -90)
  assert.equal(falling.botAngle, 90)

  const landing = flapPose('A', path, 50, 60)
  assert.equal(landing.topAngle, -90)
  assert.ok(landing.botAngle < 90)

  const second = flapPose('A', path, 70, 60)
  assert.equal(second.bottom, 'B')
  assert.equal(second.top, 'C')

  const done = flapPose('A', path, 120, 60)
  assert.deepEqual([done.moving, done.top, done.bottom], [false, 'C', 'C'])
})

test('flapPose landing overshoots then settles flat', () => {
  const angles = []
  for (let t = 30; t < 60; t += 1) angles.push(flapPose('A', ['B'], t, 60).botAngle)
  assert.ok(Math.min(...angles) < 0, 'overshoots past flat')
  assert.ok(Math.abs(flapPose('A', ['B'], 59.99, 60).botAngle) < 0.1)
})

test('clockAt and nightPos are inverse over the night', () => {
  const cases = [
    [0, '22:00'],
    [0.5, '02:30'],
    [1, '07:00'],
    [-1, '22:00'],
    [2, '07:00'],
  ]
  for (const [p, want] of cases) assert.equal(clockAt(p), want)
  for (const t of ['22:00', '23:00', '00:30', '05:00', '07:00']) assert.equal(clockAt(nightPos(t)), t)
  assert.ok(nightPos('23:00') < nightPos('01:00'))
})

test('typed reveals text progressively', () => {
  assert.equal(typed('hello', 0.1, 0.2, 0.4), '')
  assert.equal(typed('hello', 0.2, 0.2, 0.4), 'h')
  assert.equal(typed('hello', 0.32, 0.2, 0.4).length, 3)
  assert.equal(typed('hello', 0.5, 0.2, 0.4), 'hello')
  assert.equal(typed('hello', 0.5, 0.2), 'hello')
})

test('phone script is ordered and inside the timeline', () => {
  for (let i = 1; i < LINES.length; i++) assert.ok(LINES[i].t >= LINES[i - 1].t, `line ${i} in order`)
  for (const l of LINES) {
    assert.ok(l.t >= 0 && l.t <= 1)
    if (l.end !== undefined) assert.ok(l.end > l.t && l.end <= 1)
  }
  assert.ok(COMPOSE.t < COMPOSE.end && COMPOSE.end < COMPOSE.sent)
})

test('dot font measures and draws known glyphs', () => {
  assert.equal(measure(''), 0)
  assert.equal(measure('A'), 5)
  assert.equal(measure('AB'), 11)
  assert.equal(glyph('a').length, DOT_H)
  assert.deepEqual(glyph('?'), glyph(' '), 'unknown glyphs are blank')
  let lit = 0
  let maxX = 0
  eachDot('RELAY', (x) => {
    lit++
    maxX = Math.max(maxX, x)
  })
  assert.ok(lit > 50)
  assert.ok(maxX < measure('RELAY'))
})
