package ptyd

import (
	"bytes"
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Size caps for OSC payloads (SECURITY.md rule 4).
const (
	// maxOSCBytes bounds any buffered OSC payload. It is large enough for
	// an OSC 52 carrying maxClipBytes of base64.
	maxOSCBytes  = 360 << 10
	maxClipBytes = 256 << 10
	maxTitle     = 512
	maxCwd       = 4096
	maxNotify    = 4096
)

// trackedModes are the DEC private modes re-asserted on attach, in the
// order they are re-sent. 25 (cursor visible) defaults to on and is only
// sent when the application hid the cursor.
var trackedModes = []int{1049, 1047, 47, 1, 1000, 1002, 1003, 1004, 1005, 1006, 1015, 2004, 2026}

// termEvent is something the output stream asked for, collected while a
// chunk is parsed and handled by the session afterwards.
type termEvent struct {
	kind  string // title | cwd | notify | bell | clip | prompt
	title string
	body  string
	mark  byte // OSC 133 mark: A B C D
}

// term combines the tokenizer, the screen model, the DEC mode tracker and
// OSC handling for one session. It is not safe for concurrent use; the
// session serialises access.
type term struct {
	parser       *vtParser
	scr          *screen
	modes        map[int]bool
	cursorHidden bool
	events       []termEvent

	// kitty OSC 99 notifications may arrive in chunks keyed by id.
	kittyTitle, kittyBody string
}

func newTerm(cols, rows, scrollbackLines int) *term {
	t := &term{scr: newScreen(cols, rows, scrollbackLines), modes: map[int]bool{}}
	t.parser = newVTParser(t, maxOSCBytes)
	return t
}

// Feed parses output and returns the events it produced. The returned
// slice is only valid until the next call.
func (t *term) Feed(b []byte) []termEvent {
	t.events = t.events[:0]
	t.parser.Feed(b)
	return t.events
}

// AltScreen reports whether the application is on the alternate screen.
func (t *term) AltScreen() bool {
	return t.modes[1049] || t.modes[1047] || t.modes[47]
}

// Mode reports whether DEC private mode m is set.
func (t *term) Mode(m int) bool { return t.modes[m] }

// Reassert returns the escape sequences that restore the tracked modes on
// a freshly reset terminal.
func (t *term) Reassert() []byte {
	var b bytes.Buffer
	for _, m := range trackedModes {
		if t.modes[m] {
			b.WriteString("\x1b[?")
			b.WriteString(strconv.Itoa(m))
			b.WriteByte('h')
		}
	}
	if t.cursorHidden {
		b.WriteString("\x1b[?25l")
	}
	return b.Bytes()
}

func (t *term) print(r rune)  { t.scr.print(r) }
func (t *term) esc(i, f byte) { t.escape(i, f) }
func (t *term) execute(b byte) {
	if b == 0x07 {
		t.events = append(t.events, termEvent{kind: "bell"})
		return
	}
	t.scr.execute(b)
}

func (t *term) escape(inter, final byte) {
	if inter == 0 && final == 'c' {
		clear(t.modes)
		t.cursorHidden = false
	}
	t.scr.esc(inter, final)
}

func (t *term) csi(c *csiSeq) {
	if c.private == '?' && c.intermediate == 0 && (c.final == 'h' || c.final == 'l') {
		on := c.final == 'h'
		for _, m := range c.params {
			switch m {
			case 25:
				t.cursorHidden = !on
			case 1049, 1047, 47, 1, 1000, 1002, 1003, 1004, 1005, 1006, 1015, 2004, 2026:
				if on {
					t.modes[m] = true
				} else {
					delete(t.modes, m)
				}
			}
		}
	}
	t.scr.csi(c)
}

func (t *term) osc(data []byte, overflow bool) {
	num, rest, ok := bytes.Cut(data, []byte{';'})
	if !ok {
		num, rest = data, nil
	}
	switch string(num) {
	case "0", "2":
		if !overflow {
			t.events = append(t.events, termEvent{kind: "title", title: cleanText(rest, maxTitle)})
		}
	case "7":
		if !overflow {
			if p := parseOSC7(rest); p != "" {
				t.events = append(t.events, termEvent{kind: "cwd", body: p})
			}
		}
	case "9":
		t.osc9(rest, overflow)
	case "777":
		// rxvt: 777;notify;title;body
		parts := bytes.SplitN(rest, []byte{';'}, 3)
		if len(parts) >= 2 && string(parts[0]) == "notify" && !overflow {
			ev := termEvent{kind: "notify", title: cleanText(parts[1], maxTitle)}
			if len(parts) == 3 {
				ev.body = cleanText(parts[2], maxNotify)
			}
			t.events = append(t.events, ev)
		}
	case "99":
		if !overflow {
			t.osc99(rest)
		}
	case "52":
		if ev, ok := parseOSC52(rest, overflow); ok {
			t.events = append(t.events, ev)
		}
	case "133":
		if len(rest) > 0 && !overflow {
			t.events = append(t.events, termEvent{kind: "prompt", mark: rest[0]})
		}
	}
	// 1337 (iTerm2 SetUserVar and friends), colours, hyperlinks: ignored.
}

// osc9 handles iTerm2 notifications (OSC 9;message). ConEmu uses OSC 9;N;…
// sub-commands (progress 9;4, cwd 9;9); those are not notifications.
func (t *term) osc9(rest []byte, overflow bool) {
	if overflow || len(rest) == 0 {
		return
	}
	if sub, arg, ok := bytes.Cut(rest, []byte{';'}); ok && isDigits(sub) {
		if string(sub) == "9" {
			if p := parseOSC7(arg); p != "" {
				t.events = append(t.events, termEvent{kind: "cwd", body: p})
			}
		}
		return
	}
	if isDigits(rest) {
		return
	}
	t.events = append(t.events, termEvent{kind: "notify", body: cleanText(rest, maxNotify)})
}

// osc99 handles the basic kitty desktop-notification protocol:
// OSC 99 ; metadata ; payload, with metadata keys d (done: 0 = more
// chunks follow) and p (title | body).
func (t *term) osc99(rest []byte) {
	meta, payload, _ := bytes.Cut(rest, []byte{';'})
	done, part, b64 := true, "title", false
	for _, kv := range bytes.Split(meta, []byte{':'}) {
		k, v, _ := bytes.Cut(kv, []byte{'='})
		switch string(k) {
		case "d":
			done = string(v) != "0"
		case "p":
			part = string(v)
		case "e":
			b64 = string(v) == "1"
		}
	}
	if b64 {
		dec, err := base64.StdEncoding.DecodeString(string(payload))
		if err != nil {
			return
		}
		payload = dec
	}
	text := cleanText(payload, maxNotify)
	switch part {
	case "title":
		t.kittyTitle = truncate(t.kittyTitle+text, maxTitle)
	case "body":
		t.kittyBody = truncate(t.kittyBody+text, maxNotify)
	default:
		return // icons, buttons, queries
	}
	if !done {
		return
	}
	ev := termEvent{kind: "notify", title: t.kittyTitle, body: t.kittyBody}
	t.kittyTitle, t.kittyBody = "", ""
	if ev.title == "" && ev.body == "" {
		return
	}
	t.events = append(t.events, ev)
}

// parseOSC52 decodes "c;<base64>" (any selection target). Queries ("?")
// are ignored: Relay never answers clipboard reads from applications.
func parseOSC52(rest []byte, overflow bool) (termEvent, bool) {
	_, payload, ok := bytes.Cut(rest, []byte{';'})
	if !ok || overflow || len(payload) == 0 || string(payload) == "?" {
		return termEvent{}, false
	}
	if base64.StdEncoding.DecodedLen(len(payload)) > maxClipBytes+3 {
		return termEvent{}, false
	}
	dec, err := base64.StdEncoding.DecodeString(string(payload))
	if err != nil {
		dec, err = base64.RawStdEncoding.DecodeString(string(payload))
		if err != nil {
			return termEvent{}, false
		}
	}
	if len(dec) == 0 || len(dec) > maxClipBytes || !utf8.Valid(dec) {
		return termEvent{}, false
	}
	return termEvent{kind: "clip", body: string(dec)}, true
}

// parseOSC7 turns file://host/path (percent-encoded) into a path.
func parseOSC7(b []byte) string {
	if len(b) == 0 || len(b) > maxCwd {
		return ""
	}
	s := string(b)
	if strings.HasPrefix(s, "/") {
		return cleanPath(s)
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "file" && u.Scheme != "kitty-shell-cwd") || u.Path == "" {
		return ""
	}
	return cleanPath(u.Path)
}

func cleanPath(p string) string {
	if !utf8.ValidString(p) || strings.ContainsAny(p, "\x00\n\r") || !strings.HasPrefix(p, "/") {
		return ""
	}
	return p
}

// cleanText makes an OSC string safe to show: valid UTF-8, no control
// characters, trimmed, at most max bytes.
func cleanText(b []byte, max int) string {
	var sb strings.Builder
	for len(b) > 0 && sb.Len() < max {
		r, n := utf8.DecodeRune(b)
		b = b[n:]
		if r == utf8.RuneError && n == 1 {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			if r == '\t' || r == '\n' {
				sb.WriteByte(' ')
			}
			continue
		}
		sb.WriteRune(r)
	}
	return truncate(strings.TrimSpace(sb.String()), max)
}

// truncate cuts s to at most max bytes on a rune boundary.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

func isDigits(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
