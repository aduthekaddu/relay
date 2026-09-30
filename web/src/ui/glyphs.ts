// 7×7 dot-matrix bitmaps. Each glyph is seven rows of seven characters:
// "#" = lit dot, "." = off dot (drawn faintly so the grid stays visible).
// Area glyphs identify an area before its name is read; utility glyphs
// are for places where a dot icon reads better than a stroke icon.

export const GLYPHS = {
  // ---- areas
  home: ['...#...', '..#.#..', '.#...#.', '#######', '.#...#.', '.#.#.#.', '.#.#.#.'],
  terminal: ['.......', '.#.....', '..#....', '...#...', '..#....', '.#..###', '.......'],
  agents: ['...#...', '...#...', '..###..', '#######', '..###..', '...#...', '...#...'],
  files: ['.......', '###....', '#..####', '#.....#', '#.....#', '#######', '.......'],
  code: ['..#.#..', '.#...#.', '.#...#.', '#.....#', '.#...#.', '.#...#.', '..#.#..'],
  desktop: ['#######', '#######', '#.....#', '#.....#', '#######', '...#...', '.#####.'],
  previews: ['.......', '..###..', '.#...#.', '#..#..#', '.#...#.', '..###..', '.......'],
  system: ['.....#.', '.....#.', '...#.#.', '...#.#.', '.#.#.#.', '.#.#.#.', '#######'],
  settings: ['..###..', '.#...#.', '#...#.#', '#..#..#', '#.....#', '.#...#.', '..###..'],
  // ---- utility
  relay: ['.......', '.####..', '.#..#..', '.###...', '.#.#...', '.#..#..', '.......'],
  bell: ['...#...', '..###..', '.#...#.', '.#...#.', '#.....#', '#######', '...#...'],
  search: ['.###...', '#...#..', '#...#..', '#...#..', '.###...', '....#..', '.....#.'],
  plus: ['.......', '...#...', '...#...', '.#####.', '...#...', '...#...', '.......'],
  check: ['.......', '......#', '.....#.', '#...#..', '.#.#...', '..#....', '.......'],
  close: ['.......', '.#...#.', '..#.#..', '...#...', '..#.#..', '.#...#.', '.......'],
  alert: ['...#...', '...#...', '...#...', '...#...', '...#...', '.......', '...#...'],
  spark: ['.......', '...#...', '..#.#..', '.#...#.', '..#.#..', '...#...', '.......'],
  empty: ['#.#.#.#', '.......', '#.....#', '.......', '#.....#', '.......', '#.#.#.#'],
} as const satisfies Record<string, readonly string[]>

export type GlyphName = keyof typeof GLYPHS

/** Parsed glyph: coordinates of every dot, lit or not. */
export interface GlyphDots {
  on: Array<[number, number]>
  off: Array<[number, number]>
}

const cache = new Map<string, GlyphDots>()

/** Parse a bitmap into dot coordinates [col, row]. Cached per name. */
export function glyphDots(name: GlyphName): GlyphDots {
  const hit = cache.get(name)
  if (hit) return hit
  const on: Array<[number, number]> = []
  const off: Array<[number, number]> = []
  GLYPHS[name].forEach((row, y) => {
    for (let x = 0; x < row.length; x++) (row[x] === '#' ? on : off).push([x, y])
  })
  const dots = { on, off }
  cache.set(name, dots)
  return dots
}

/** True when `name` is a known glyph (for "glyph:<name>" icon strings). */
export const isGlyph = (name: string): name is GlyphName => Object.hasOwn(GLYPHS, name)
