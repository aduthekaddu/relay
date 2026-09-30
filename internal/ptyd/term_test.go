package ptyd

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

type recHandler struct {
	text   strings.Builder
	execs  []byte
	csis   []string
	oscs   []string
	escs   []string
	oflows int
}

func (h *recHandler) print(r rune)   { h.text.WriteRune(r) }
func (h *recHandler) execute(b byte) { h.execs = append(h.execs, b) }
func (h *recHandler) csi(c *csiSeq) {
	var b strings.Builder
	if c.private != 0 {
		b.WriteByte(c.private)
	}
	for i, p := range c.params {
		if i > 0 {
			b.WriteByte(';')
		}
		if p >= 0 {
			b.WriteString(strconv.Itoa(p))
		}
	}
	if c.intermediate != 0 {
		b.WriteByte(c.intermediate)
	}
	b.WriteByte(c.final)
	h.csis = append(h.csis, b.String())
}
func (h *recHandler) esc(i, f byte) { h.escs = append(h.escs, string([]byte{i, f})) }
func (h *recHandler) osc(d []byte, overflow bool) {
	h.oscs = append(h.oscs, string(d))
	if overflow {
		h.oflows++
	}
}

func TestVTParserSplitFeeds(t *testing.T) {
	input := []byte("a\x1b[1;32mb\x1b]0;tit\x07le\x07é\x1b[?1049h€\x1b]7;file:///x\x1b\\z")
	// Feeding byte by byte must give the same result as one feed.
	whole := &recHandler{}
	newVTParser(whole, 1024).Feed(input)
	split := &recHandler{}
	p := newVTParser(split, 1024)
	for i := range input {
		p.Feed(input[i : i+1])
	}
	for _, h := range []*recHandler{whole, split} {
		if got := h.text.String(); got != "able\u00e9€z" {
			t.Errorf("text = %q", got)
		}
		if strings.Join(h.csis, ",") != "1;32m,?1049h" {
			t.Errorf("csis = %v", h.csis)
		}
		if strings.Join(h.oscs, ",") != "0;tit,7;file:///x" {
			t.Errorf("oscs = %v", h.oscs)
		}
		if !bytes.Equal(h.execs, []byte{0x07}) {
			t.Errorf("execs = %q", h.execs)
		}
	}
}

func TestVTParserOSCOverflowAndStrings(t *testing.T) {
	h := &recHandler{}
	p := newVTParser(h, 8)
	p.Feed([]byte("\x1b]2;0123456789\x07ok\x1bP1$qm\x1b\\!\x1b_apc\x1b\\?"))
	if h.oflows != 1 || len(h.oscs) != 1 || h.oscs[0] != "2;012345" {
		t.Fatalf("oscs=%q overflows=%d", h.oscs, h.oflows)
	}
	if h.text.String() != "ok!?" {
		t.Fatalf("text = %q (DCS/APC must be swallowed)", h.text.String())
	}
	if !p.Ground() {
		t.Fatal("parser should be in ground state")
	}
	p.Feed([]byte("\x1b[12"))
	if p.Ground() {
		t.Fatal("parser inside CSI reported ground")
	}
}

func TestTermOSCEvents(t *testing.T) {
	big := strings.Repeat("QUFB", (maxClipBytes/3)+10) // decodes to > maxClipBytes
	tests := []struct {
		name string
		in   string
		want []termEvent
	}{
		{"osc0 title", "\x1b]0;hello world\x07", []termEvent{{kind: "title", title: "hello world"}}},
		{"osc2 title ST", "\x1b]2;vim a.go\x1b\\", []termEvent{{kind: "title", title: "vim a.go"}}},
		{"title strips controls", "\x1b]2;a\x01b\tc\x07", []termEvent{{kind: "title", title: "ab c"}}},
		{"osc7 cwd", "\x1b]7;file://host/home/u/my%20dir\x07", []termEvent{{kind: "cwd", body: "/home/u/my dir"}}},
		{"osc7 bad scheme", "\x1b]7;http://x/y\x07", nil},
		{"osc9 notify", "\x1b]9;Build done\x07", []termEvent{{kind: "notify", body: "Build done"}}},
		{"osc9;4 progress ignored", "\x1b]9;4;1;50\x07", nil},
		{"osc9;9 conemu cwd", "\x1b]9;9;/tmp/x\x07", []termEvent{{kind: "cwd", body: "/tmp/x"}}},
		{"osc777 notify", "\x1b]777;notify;Title;Body text\x07", []termEvent{{kind: "notify", title: "Title", body: "Body text"}}},
		{"osc777 other", "\x1b]777;precmd\x07", nil},
		{"osc99 simple", "\x1b]99;;Hello\x1b\\", []termEvent{{kind: "notify", title: "Hello"}}},
		{"osc99 chunked", "\x1b]99;i=1:d=0;Title\x1b\\\x1b]99;i=1:p=body;Body\x1b\\", []termEvent{{kind: "notify", title: "Title", body: "Body"}}},
		{"osc99 base64", "\x1b]99;e=1;SGk=\x1b\\", []termEvent{{kind: "notify", title: "Hi"}}},
		{"osc52 clip", "\x1b]52;c;aGVsbG8=\x07", []termEvent{{kind: "clip", body: "hello"}}},
		{"osc52 query ignored", "\x1b]52;c;?\x07", nil},
		{"osc52 bad base64", "\x1b]52;c;!!!\x07", nil},
		{"osc52 too big", "\x1b]52;c;" + big + "\x07", nil},
		{"osc133 prompt", "\x1b]133;A\x07", []termEvent{{kind: "prompt", mark: 'A'}}},
		{"osc1337 ignored", "\x1b]1337;SetUserVar=a=Yg==\x07", nil},
		{"bell", "x\x07y", []termEvent{{kind: "bell"}}},
		{"bel terminates osc, not a bell", "\x1b]0;t\x07", []termEvent{{kind: "title", title: "t"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tm := newTerm(80, 24, 10)
			got := append([]termEvent(nil), tm.Feed([]byte(tc.in))...)
			if len(got) != len(tc.want) {
				t.Fatalf("events = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("event %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestTermModesReassert(t *testing.T) {
	tm := newTerm(80, 24, 10)
	tm.Feed([]byte("\x1b[?1049h\x1b[?2004h\x1b[?1000;1006h\x1b[?25l\x1b[?1h"))
	if !tm.AltScreen() || !tm.Mode(2004) {
		t.Fatal("modes not tracked")
	}
	got := string(tm.Reassert())
	want := "\x1b[?1049h\x1b[?1h\x1b[?1000h\x1b[?1006h\x1b[?2004h\x1b[?25l"
	if got != want {
		t.Fatalf("reassert = %q, want %q", got, want)
	}
	tm.Feed([]byte("\x1b[?1049l\x1b[?1000l\x1b[?25h"))
	if tm.AltScreen() || tm.Mode(1000) {
		t.Fatal("reset modes still set")
	}
	tm.Feed([]byte("\x1bc"))
	if len(tm.Reassert()) != 0 {
		t.Fatalf("RIS should clear modes, got %q", tm.Reassert())
	}
}
