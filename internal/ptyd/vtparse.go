package ptyd

import "unicode/utf8"

// vtHandler receives the actions of the VT tokenizer. Implementations must
// not retain the slices they are given: the parser reuses them.
type vtHandler interface {
	// print is called for each decoded printable rune.
	print(r rune)
	// execute is called for C0 control bytes (BEL, BS, HT, LF, CR, ...).
	execute(b byte)
	// csi is called for a complete control sequence.
	csi(seq *csiSeq)
	// esc is called for a complete two/three byte escape sequence.
	esc(intermediate, final byte)
	// osc is called for a complete operating system command. overflow is
	// true when the payload exceeded the size cap and was truncated.
	osc(data []byte, overflow bool)
}

// csiSeq is one parsed control sequence: ESC [ <private> <params> <intermediate> <final>.
type csiSeq struct {
	private      byte // one of < = > ? or 0
	intermediate byte // 0x20-0x2f or 0
	final        byte
	params       []int // missing parameters are -1
}

// param returns parameter i, or def when it is missing or zero-defaulted.
func (c *csiSeq) param(i, def int) int {
	if i >= len(c.params) || c.params[i] < 0 {
		return def
	}
	return c.params[i]
}

// paramOr1 returns parameter i treating 0 and missing as 1 (cursor moves).
func (c *csiSeq) paramOr1(i int) int {
	v := c.param(i, 1)
	if v == 0 {
		return 1
	}
	return v
}

type vtState uint8

const (
	stGround vtState = iota
	stEscape
	stEscInter
	stCSI
	stCSIIgnore
	stOSC
	stOSCEsc
	stString    // DCS, SOS, PM, APC: ignored until ST
	stStringEsc // ESC seen inside a string
)

const (
	maxCSIParams = 16
	maxParam     = 65535
)

// vtParser is a streaming, allocation-free (in the steady state) tokenizer
// for the subset of ECMA-48 / xterm control sequences a terminal emits.
// It keeps state across Feed calls, so sequences and UTF-8 runes may be
// split between reads.
type vtParser struct {
	h      vtHandler
	state  vtState
	maxOSC int

	csi      csiSeq
	paramBuf [maxCSIParams]int
	curParam int  // value being accumulated, -1 = none yet
	escInter byte // intermediate of an ESC sequence

	osc         []byte
	oscOverflow bool

	utf     [utf8.UTFMax]byte
	utfLen  int
	utfNeed int
}

func newVTParser(h vtHandler, maxOSC int) *vtParser {
	return &vtParser{h: h, maxOSC: maxOSC}
}

// Ground reports whether the parser is between sequences and not in the
// middle of a UTF-8 rune: a safe point to start a replay from.
func (p *vtParser) Ground() bool { return p.state == stGround && p.utfLen == 0 }

// Feed tokenizes b.
func (p *vtParser) Feed(b []byte) {
	for i := 0; i < len(b); i++ {
		c := b[i]
		// Fast path: runs of printable ASCII in ground state.
		if p.state == stGround && p.utfLen == 0 && c >= 0x20 && c < 0x7f {
			p.h.print(rune(c))
			continue
		}
		p.step(c)
	}
}

func (p *vtParser) step(c byte) {
	// Controls that act in (almost) every state.
	switch p.state {
	case stOSC, stOSCEsc, stString, stStringEsc:
		// strings handle their own terminators below
	default:
		switch c {
		case 0x18, 0x1a: // CAN, SUB abort the sequence
			p.flushUTF()
			p.state = stGround
			return
		case 0x1b:
			p.flushUTF()
			p.enterEscape()
			return
		}
	}

	switch p.state {
	case stGround:
		p.ground(c)
	case stEscape:
		p.escape(c)
	case stEscInter:
		switch {
		case c < 0x20:
			p.h.execute(c)
		case c < 0x30:
			p.escInter = c
		default:
			p.h.esc(p.escInter, c)
			p.state = stGround
		}
	case stCSI:
		p.csiByte(c)
	case stCSIIgnore:
		switch {
		case c < 0x20:
			p.h.execute(c)
		case c >= 0x40 && c <= 0x7e:
			p.state = stGround
		}
	case stOSC:
		switch c {
		case 0x07:
			p.dispatchOSC()
		case 0x1b:
			p.state = stOSCEsc
		case 0x18, 0x1a:
			p.state = stGround
		default:
			p.oscAppend(c)
		}
	case stOSCEsc:
		p.dispatchOSC()
		if c != '\\' {
			// ESC started a new sequence; the OSC ended implicitly.
			p.enterEscape()
			p.escape(c)
		}
	case stString:
		switch c {
		case 0x1b:
			p.state = stStringEsc
		case 0x18, 0x1a:
			p.state = stGround
		}
	case stStringEsc:
		if c == '\\' {
			p.state = stGround
		} else if c != 0x1b {
			p.state = stString
		}
	}
}

func (p *vtParser) enterEscape() {
	p.state = stEscape
	p.escInter = 0
}

func (p *vtParser) ground(c byte) {
	if c < 0x20 || c == 0x7f {
		p.flushUTF()
		if c != 0x7f {
			p.h.execute(c)
		}
		return
	}
	if c < 0x80 && p.utfLen == 0 {
		p.h.print(rune(c))
		return
	}
	p.utfByte(c)
}

func (p *vtParser) utfByte(c byte) {
	if p.utfLen == 0 {
		switch {
		case c&0xe0 == 0xc0:
			p.utfNeed = 2
		case c&0xf0 == 0xe0:
			p.utfNeed = 3
		case c&0xf8 == 0xf0:
			p.utfNeed = 4
		default:
			p.h.print(utf8.RuneError)
			return
		}
		p.utf[0] = c
		p.utfLen = 1
		return
	}
	if c&0xc0 != 0x80 {
		// Broken sequence: emit a replacement and restart with c.
		p.utfLen = 0
		p.h.print(utf8.RuneError)
		p.ground(c)
		return
	}
	p.utf[p.utfLen] = c
	p.utfLen++
	if p.utfLen == p.utfNeed {
		r, _ := utf8.DecodeRune(p.utf[:p.utfLen])
		p.utfLen = 0
		p.h.print(r)
	}
}

// flushUTF emits a replacement for a rune cut short by a control byte.
func (p *vtParser) flushUTF() {
	if p.utfLen > 0 {
		p.utfLen = 0
		p.h.print(utf8.RuneError)
	}
}

func (p *vtParser) escape(c byte) {
	switch {
	case c < 0x20:
		p.h.execute(c)
	case c < 0x30:
		p.escInter = c
		p.state = stEscInter
	case c == '[':
		p.csi = csiSeq{params: p.paramBuf[:0]}
		p.curParam = -1
		p.state = stCSI
	case c == ']':
		p.osc = p.osc[:0]
		p.oscOverflow = false
		p.state = stOSC
	case c == 'P' || c == 'X' || c == '^' || c == '_':
		p.state = stString
	default:
		p.h.esc(0, c)
		p.state = stGround
	}
}

func (p *vtParser) csiByte(c byte) {
	switch {
	case c < 0x20:
		p.h.execute(c)
	case c >= '0' && c <= '9':
		if p.curParam < 0 {
			p.curParam = 0
		}
		if p.curParam <= maxParam {
			p.curParam = p.curParam*10 + int(c-'0')
		}
	case c == ';' || c == ':':
		p.pushParam()
	case c >= '<' && c <= '?':
		if len(p.csi.params) == 0 && p.curParam < 0 && p.csi.private == 0 {
			p.csi.private = c
		} else {
			p.state = stCSIIgnore
		}
	case c >= 0x20 && c <= 0x2f:
		p.csi.intermediate = c
	case c >= 0x40 && c <= 0x7e:
		if p.curParam >= 0 || len(p.csi.params) > 0 {
			p.pushParam()
		}
		p.csi.final = c
		p.state = stGround
		p.h.csi(&p.csi)
	default:
		p.state = stCSIIgnore
	}
}

func (p *vtParser) pushParam() {
	v := p.curParam
	if v > maxParam {
		v = maxParam
	}
	if len(p.csi.params) < maxCSIParams {
		p.csi.params = append(p.csi.params, v)
	}
	p.curParam = -1
}

func (p *vtParser) oscAppend(c byte) {
	if len(p.osc) >= p.maxOSC {
		p.oscOverflow = true
		return
	}
	p.osc = append(p.osc, c)
}

func (p *vtParser) dispatchOSC() {
	p.state = stGround
	p.h.osc(p.osc, p.oscOverflow)
	// Do not keep a huge buffer alive after a big OSC 52.
	if cap(p.osc) > 64<<10 {
		p.osc = nil
	} else {
		p.osc = p.osc[:0]
	}
	p.oscOverflow = false
}
