package ptyd

import (
	"strings"
	"unicode"
)

// screen is a compact VT emulator used only for plain-text snapshots and
// the one-line previews in session lists. It tracks characters, not
// attributes: printable text (with East Asian wide runes), CR/LF/BS/TAB,
// cursor movement, erase in display/line, scroll regions, insert/delete
// lines and characters, repeat, save/restore cursor and the alternate
// screen. Output from the real browser terminal never goes through it.
type screen struct {
	cols, rows int
	main, alt  [][]rune
	lines      [][]rune // active grid (main or alt)
	altActive  bool

	x, y        int
	wrapPending bool
	autowrap    bool
	origin      bool
	top, bottom int // scroll region, inclusive
	lastPrinted rune

	savedMain, savedAlt savedCursor

	scrollback    []string // ring of lines scrolled off the main screen
	sbHead, sbLen int
}

type savedCursor struct {
	x, y     int
	origin   bool
	autowrap bool
	valid    bool
}

// wideSpacer marks the right half of a double-width rune.
const wideSpacer rune = -1

func newScreen(cols, rows, scrollback int) *screen {
	if scrollback < 0 {
		scrollback = 0
	}
	s := &screen{autowrap: true, scrollback: make([]string, scrollback)}
	s.cols, s.rows = clampSize(cols, rows)
	s.main = newGrid(s.cols, s.rows)
	s.alt = newGrid(s.cols, s.rows)
	s.lines = s.main
	s.bottom = s.rows - 1
	return s
}

func newGrid(cols, rows int) [][]rune {
	g := make([][]rune, rows)
	for i := range g {
		g[i] = make([]rune, cols)
	}
	return g
}

// reset performs a full reset (RIS) but keeps the scrollback.
func (s *screen) reset() {
	s.main = newGrid(s.cols, s.rows)
	s.alt = newGrid(s.cols, s.rows)
	s.lines = s.main
	s.altActive = false
	s.x, s.y = 0, 0
	s.wrapPending = false
	s.autowrap = true
	s.origin = false
	s.top, s.bottom = 0, s.rows-1
	s.savedMain, s.savedAlt = savedCursor{}, savedCursor{}
}

// ---------------------------------------------------------------------------
// vtHandler

func (s *screen) print(r rune) {
	w := runeWidth(r)
	if w == 0 {
		return
	}
	if s.wrapPending {
		s.wrapPending = false
		if s.autowrap {
			s.x = 0
			s.lineFeed()
		}
	}
	if w == 2 && s.x == s.cols-1 {
		if !s.autowrap || s.cols < 2 {
			return
		}
		s.lines[s.y][s.x] = 0
		s.x = 0
		s.lineFeed()
	}
	line := s.lines[s.y]
	s.clearWideAt(line, s.x)
	line[s.x] = r
	if w == 2 {
		s.clearWideAt(line, s.x+1)
		line[s.x+1] = wideSpacer
	}
	s.lastPrinted = r
	s.x += w
	if s.x >= s.cols {
		s.x = s.cols - 1
		s.wrapPending = true
	}
}

// clearWideAt removes the other half of a wide rune overlapping column x.
func (s *screen) clearWideAt(line []rune, x int) {
	if x >= len(line) {
		return
	}
	if line[x] == wideSpacer && x > 0 {
		line[x-1] = 0
	}
	if x+1 < len(line) && line[x+1] == wideSpacer {
		line[x+1] = 0
	}
}

func (s *screen) execute(b byte) {
	switch b {
	case '\b':
		s.wrapPending = false
		if s.x > 0 {
			s.x--
		}
	case '\t':
		s.wrapPending = false
		nx := (s.x/8 + 1) * 8
		if nx >= s.cols {
			nx = s.cols - 1
		}
		s.x = nx
	case '\n', '\v', '\f':
		s.wrapPending = false
		s.lineFeed()
	case '\r':
		s.wrapPending = false
		s.x = 0
	}
}

func (s *screen) esc(inter, final byte) {
	if inter != 0 {
		return // charset designations, DECALN, ...
	}
	switch final {
	case '7':
		s.saveCursor()
	case '8':
		s.restoreCursor()
	case 'D':
		s.wrapPending = false
		s.lineFeed()
	case 'E':
		s.wrapPending = false
		s.x = 0
		s.lineFeed()
	case 'M':
		s.wrapPending = false
		if s.y == s.top {
			s.scrollDown(1)
		} else if s.y > 0 {
			s.y--
		}
	case 'c':
		s.reset()
	}
}

func (s *screen) osc([]byte, bool) {}

func (s *screen) csi(c *csiSeq) {
	if c.private == '?' {
		if c.final == 'h' || c.final == 'l' {
			for i := range c.params {
				s.decMode(c.params[i], c.final == 'h')
			}
		}
		return
	}
	if c.private != 0 || c.intermediate != 0 {
		return // DA/DSR replies, DECSCUSR, ... do not change the text
	}
	s.wrapPending = false
	switch c.final {
	case 'A':
		s.y = max(s.minY(), s.y-c.paramOr1(0))
	case 'B', 'e':
		s.y = min(s.maxY(), s.y+c.paramOr1(0))
	case 'C', 'a':
		s.x = min(s.cols-1, s.x+c.paramOr1(0))
	case 'D':
		s.x = max(0, s.x-c.paramOr1(0))
	case 'E':
		s.y = min(s.maxY(), s.y+c.paramOr1(0))
		s.x = 0
	case 'F':
		s.y = max(s.minY(), s.y-c.paramOr1(0))
		s.x = 0
	case 'G', '`':
		s.x = clamp(c.paramOr1(0)-1, 0, s.cols-1)
	case 'H', 'f':
		s.moveTo(c.paramOr1(1)-1, c.paramOr1(0)-1)
	case 'd':
		s.moveTo(s.x, c.paramOr1(0)-1)
	case 'J':
		s.eraseDisplay(c.param(0, 0))
	case 'K':
		s.eraseLine(c.param(0, 0))
	case 'L':
		s.insertLines(c.paramOr1(0))
	case 'M':
		s.deleteLines(c.paramOr1(0))
	case '@':
		s.insertChars(c.paramOr1(0))
	case 'P':
		s.deleteChars(c.paramOr1(0))
	case 'X':
		n := min(c.paramOr1(0), s.cols-s.x)
		clearRunes(s.lines[s.y][s.x : s.x+n])
	case 'S':
		s.scrollUp(c.paramOr1(0))
	case 'T':
		s.scrollDown(c.paramOr1(0))
	case 'b':
		if s.lastPrinted != 0 {
			for n := min(c.paramOr1(0), s.cols*s.rows); n > 0; n-- {
				s.print(s.lastPrinted)
			}
		}
	case 'r':
		top := c.paramOr1(0) - 1
		bottom := c.param(1, s.rows)
		if bottom == 0 {
			bottom = s.rows
		}
		bottom--
		if top < bottom && bottom < s.rows {
			s.top, s.bottom = top, bottom
			s.moveTo(0, 0)
		}
	case 's':
		s.saveCursor()
	case 'u':
		s.restoreCursor()
	}
}

func (s *screen) decMode(mode int, on bool) {
	switch mode {
	case 6:
		s.origin = on
		s.moveTo(0, 0)
	case 7:
		s.autowrap = on
	case 47, 1047:
		s.setAlt(on, mode == 1047 && !on, false)
	case 1049:
		s.setAlt(on, false, true)
	}
}

// setAlt switches between the main and alternate grids. clearOnExit clears
// the alternate grid when leaving it (1047); saveCursor is the 1049
// behaviour (save on enter, restore on exit, clear the alt grid on enter).
func (s *screen) setAlt(on, clearOnExit, saveCursor bool) {
	if on == s.altActive {
		return
	}
	if on {
		if saveCursor {
			s.saveCursor()
		}
		s.altActive = true
		s.lines = s.alt
		if saveCursor {
			for _, l := range s.alt {
				clearRunes(l)
			}
		}
		return
	}
	if clearOnExit {
		for _, l := range s.alt {
			clearRunes(l)
		}
	}
	s.altActive = false
	s.lines = s.main
	if saveCursor {
		s.restoreCursor()
	}
}

func (s *screen) saveCursor() {
	sc := savedCursor{x: s.x, y: s.y, origin: s.origin, autowrap: s.autowrap, valid: true}
	if s.altActive {
		s.savedAlt = sc
	} else {
		s.savedMain = sc
	}
}

func (s *screen) restoreCursor() {
	sc := s.savedMain
	if s.altActive {
		sc = s.savedAlt
	}
	s.wrapPending = false
	if !sc.valid {
		s.x, s.y = 0, 0
		return
	}
	s.x = clamp(sc.x, 0, s.cols-1)
	s.y = clamp(sc.y, 0, s.rows-1)
	s.origin, s.autowrap = sc.origin, sc.autowrap
}

// ---------------------------------------------------------------------------
// primitives

func (s *screen) minY() int {
	if s.y >= s.top && s.y <= s.bottom {
		return s.top
	}
	return 0
}

func (s *screen) maxY() int {
	if s.y >= s.top && s.y <= s.bottom {
		return s.bottom
	}
	return s.rows - 1
}

func (s *screen) moveTo(x, y int) {
	if s.origin {
		y += s.top
		s.y = clamp(y, s.top, s.bottom)
	} else {
		s.y = clamp(y, 0, s.rows-1)
	}
	s.x = clamp(x, 0, s.cols-1)
	s.wrapPending = false
}

func (s *screen) lineFeed() {
	switch {
	case s.y == s.bottom:
		s.scrollUp(1)
	case s.y < s.rows-1:
		s.y++
	}
}

// scrollUp moves the scroll region up n lines. Lines leaving the top of a
// full-height main-screen region go to the scrollback.
func (s *screen) scrollUp(n int) {
	height := s.bottom - s.top + 1
	n = min(n, height)
	toScrollback := !s.altActive && s.top == 0
	for i := 0; i < n; i++ {
		first := s.lines[s.top]
		if toScrollback {
			s.pushScrollback(first)
		}
		copy(s.lines[s.top:s.bottom], s.lines[s.top+1:s.bottom+1])
		clearRunes(first)
		s.lines[s.bottom] = first
	}
}

func (s *screen) scrollDown(n int) {
	height := s.bottom - s.top + 1
	n = min(n, height)
	for i := 0; i < n; i++ {
		last := s.lines[s.bottom]
		copy(s.lines[s.top+1:s.bottom+1], s.lines[s.top:s.bottom])
		clearRunes(last)
		s.lines[s.top] = last
	}
}

func (s *screen) insertLines(n int) {
	if s.y < s.top || s.y > s.bottom {
		return
	}
	saved := s.top
	s.top = s.y
	s.scrollDown(n)
	s.top = saved
	s.x = 0
}

func (s *screen) deleteLines(n int) {
	if s.y < s.top || s.y > s.bottom {
		return
	}
	saved := s.top
	s.top = s.y
	// Deleted lines never go to the scrollback.
	alt := s.altActive
	s.altActive = true
	s.scrollUp(n)
	s.altActive = alt
	s.top = saved
	s.x = 0
}

func (s *screen) insertChars(n int) {
	line := s.lines[s.y]
	n = min(n, s.cols-s.x)
	copy(line[s.x+n:], line[s.x:s.cols-n])
	clearRunes(line[s.x : s.x+n])
}

func (s *screen) deleteChars(n int) {
	line := s.lines[s.y]
	n = min(n, s.cols-s.x)
	copy(line[s.x:], line[s.x+n:])
	clearRunes(line[s.cols-n:])
}

func (s *screen) eraseDisplay(mode int) {
	switch mode {
	case 0:
		clearRunes(s.lines[s.y][s.x:])
		for y := s.y + 1; y < s.rows; y++ {
			clearRunes(s.lines[y])
		}
	case 1:
		clearRunes(s.lines[s.y][:s.x+1])
		for y := 0; y < s.y; y++ {
			clearRunes(s.lines[y])
		}
	case 2:
		for _, l := range s.lines {
			clearRunes(l)
		}
	case 3:
		s.sbHead, s.sbLen = 0, 0
		clear(s.scrollback)
	}
}

func (s *screen) eraseLine(mode int) {
	line := s.lines[s.y]
	switch mode {
	case 0:
		clearRunes(line[s.x:])
	case 1:
		clearRunes(line[:s.x+1])
	case 2:
		clearRunes(line)
	}
}

func (s *screen) pushScrollback(line []rune) {
	if len(s.scrollback) == 0 {
		return
	}
	text := lineText(line)
	idx := (s.sbHead + s.sbLen) % len(s.scrollback)
	if s.sbLen == len(s.scrollback) {
		s.sbHead = (s.sbHead + 1) % len(s.scrollback)
	} else {
		s.sbLen++
	}
	s.scrollback[idx] = text
}

// Resize changes the grid size without reflowing. When the main screen
// shrinks, lines above the cursor move to the scrollback so the cursor
// line stays visible.
func (s *screen) Resize(cols, rows int) {
	cols, rows = clampSize(cols, rows)
	if cols == s.cols && rows == s.rows {
		return
	}
	shift := max(0, s.y-(rows-1))
	if !s.altActive {
		for i := 0; i < shift && i < len(s.main); i++ {
			s.pushScrollback(s.main[i])
		}
	}
	s.main = resizeGrid(s.main, cols, rows, s.cursorShift(false, shift))
	s.alt = resizeGrid(s.alt, cols, rows, s.cursorShift(true, shift))
	if s.altActive {
		s.lines = s.alt
	} else {
		s.lines = s.main
	}
	s.cols, s.rows = cols, rows
	s.y = clamp(s.y-shift, 0, rows-1)
	s.x = clamp(s.x, 0, cols-1)
	s.top, s.bottom = 0, rows-1
	s.wrapPending = false
}

func (s *screen) cursorShift(alt bool, shift int) int {
	if alt == s.altActive {
		return shift
	}
	return 0
}

func resizeGrid(g [][]rune, cols, rows, drop int) [][]rune {
	if drop > len(g) {
		drop = len(g)
	}
	g = g[drop:]
	out := make([][]rune, rows)
	for i := range out {
		l := make([]rune, cols)
		if i < len(g) {
			copy(l, g[i])
			if cols > 0 && l[cols-1] != wideSpacer && len(g[i]) > cols && g[i][cols] == wideSpacer {
				l[cols-1] = 0 // a wide rune cut in half
			}
		}
		out[i] = l
	}
	return out
}

// ---------------------------------------------------------------------------
// text output

// Text returns up to n lines ending at the last non-empty line of the
// visible screen, preceded by scrollback on the main screen.
func (s *screen) Text(n int) string {
	if n <= 0 {
		return ""
	}
	visible := make([]string, 0, s.rows)
	for _, l := range s.lines {
		visible = append(visible, lineText(l))
	}
	end := len(visible)
	for end > 0 && visible[end-1] == "" {
		end--
	}
	visible = visible[:end]
	var all []string
	if !s.altActive && len(visible) < n {
		need := n - len(visible)
		sb := min(need, s.sbLen)
		all = make([]string, 0, sb+len(visible))
		for i := s.sbLen - sb; i < s.sbLen; i++ {
			all = append(all, s.scrollback[(s.sbHead+i)%len(s.scrollback)])
		}
		all = append(all, visible...)
	} else {
		all = visible
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return strings.Join(all, "\n")
}

// Preview returns the last n non-empty lines of the visible screen.
func (s *screen) Preview(n int) string {
	out := make([]string, 0, n)
	for y := len(s.lines) - 1; y >= 0 && len(out) < n; y-- {
		if t := strings.TrimSpace(lineText(s.lines[y])); t != "" {
			out = append(out, t)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return strings.Join(out, "\n")
}

// LastLine returns the last non-empty line of the visible screen.
func (s *screen) LastLine() string { return s.Preview(1) }

func lineText(l []rune) string {
	end := len(l)
	for end > 0 && (l[end-1] == 0 || l[end-1] == ' ' || l[end-1] == wideSpacer) {
		end--
	}
	var b strings.Builder
	b.Grow(end)
	for _, r := range l[:end] {
		switch r {
		case wideSpacer:
		case 0:
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func clearRunes(l []rune) { clear(l) }

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// clampSize bounds a terminal size to sane values.
func clampSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return clamp(cols, 2, 1000), clamp(rows, 1, 500)
}

// runeWidth approximates wcwidth: 0 for combining and format characters,
// 2 for East Asian wide/fullwidth and emoji presentation ranges.
func runeWidth(r rune) int {
	switch {
	case r < 0x300:
		return 1
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Cf, r):
		return 0
	case r >= 0x1100 && r <= 0x115f,
		r >= 0x2e80 && r <= 0xa4cf && r != 0x303f,
		r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff,
		r >= 0xfe30 && r <= 0xfe4f,
		r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f300 && r <= 0x1f64f,
		r >= 0x1f900 && r <= 0x1f9ff,
		r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}
