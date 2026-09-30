package ptyd

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func feedScreen(s *screen, in string) {
	newVTParser(s, 1024).Feed([]byte(in))
}

func TestScreenText(t *testing.T) {
	tests := []struct {
		name       string
		cols, rows int
		in         string
		want       string
	}{
		{"plain lines", 20, 5, "hello\r\nworld\r\n", "hello\nworld"},
		{"carriage return overwrite", 20, 3, "abcdef\rXY", "XYcdef"},
		{"backspace", 20, 3, "abc\b\bZ", "aZc"},
		{"tab", 20, 3, "a\tb", "a       b"},
		{"autowrap", 5, 3, "abcdefg", "abcde\nfg"},
		{"cursor position", 10, 3, "\x1b[2;4Hx\x1b[1;1Hy", "y\n   x"},
		{"erase line", 10, 2, "abcdef\x1b[1;3H\x1b[K", "ab"},
		{"erase display", 10, 3, "one\r\ntwo\x1b[2J\x1b[Hz", "z"},
		{"insert/delete chars", 10, 1, "abcdef\x1b[1;2H\x1b[2P\x1b[1@", "a def"},
		{"insert line", 10, 3, "1\r\n2\r\n3\x1b[2;1H\x1b[L", "1\n\n2"},
		{"delete line", 10, 3, "1\r\n2\r\n3\x1b[1;1H\x1b[M", "2\n3"},
		{"scroll region", 10, 4, "\x1b[2;3r\x1b[2;1Ha\r\nb\r\nc", "\nb\nc"},
		{"repeat", 10, 1, "x\x1b[3b", "xxxx"},
		{"wide runes", 6, 2, "日本語!", "日本語\n!"},
		{"alt screen hides main", 10, 3, "main\x1b[?1049hALT", "    ALT"},
		{"alt screen restore", 10, 3, "main\x1b[?1049hALT\x1b[?1049l", "main"},
		{"save/restore cursor", 10, 2, "ab\x1b7\r\ncd\x1b8X", "abX\ncd"},
		{"reverse index scrolls", 10, 2, "a\r\nb\x1b[H\x1bMz", "z\na"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newScreen(tc.cols, tc.rows, 50)
			feedScreen(s, tc.in)
			if got := s.Text(100); got != tc.want {
				t.Fatalf("Text = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScreenScrollbackAndPreview(t *testing.T) {
	s := newScreen(20, 3, 5)
	for i := 1; i <= 10; i++ {
		feedScreen(s, fmt.Sprintf("line %d\r\n", i))
	}
	// 3 rows: lines 9, 10 and the empty cursor line; scrollback keeps 5.
	if got, want := s.Text(4), "line 7\nline 8\nline 9\nline 10"; got != want {
		t.Fatalf("Text(4) = %q, want %q", got, want)
	}
	if got, want := s.Text(100), "line 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10"; got != want {
		t.Fatalf("Text(100) = %q, want %q", got, want)
	}
	if got := s.Preview(3); got != "line 9\nline 10" {
		t.Fatalf("Preview = %q", got)
	}
	feedScreen(s, "\x1b[3J")
	if got := s.Text(100); got != "line 9\nline 10" {
		t.Fatalf("after ED 3: %q", got)
	}
}

func TestScreenResizeKeepsCursorLine(t *testing.T) {
	s := newScreen(10, 5, 10)
	feedScreen(s, "a\r\nb\r\nc\r\nd\r\ne")
	s.Resize(4, 2)
	if got := s.Text(2); got != "d\ne" {
		t.Fatalf("after shrink: %q", got)
	}
	if got := s.Text(10); got != "a\nb\nc\nd\ne" {
		t.Fatalf("scrollback after shrink: %q", got)
	}
	feedScreen(s, "fghij")
	if got := s.Text(2); got != "efgh\nij" {
		t.Fatalf("wrap at new width: %q", got)
	}
}

func TestScreenFuzzNoPanic(t *testing.T) {
	// Arbitrary control-heavy input must never panic or leave the cursor
	// outside the grid.
	seed := []byte("\x1b[999;999H\x1b[999@\x1b[999P\x1b[999L\x1b[999M\x1b[0;0r\x1b[5;2r\x1b[?6h\x1b[99A\x1b[99B日\x1b[99X\x1bM\x1b[?1049h\x1b[3J\x1b[99S\x1b[99T\x1bc")
	s := newScreen(3, 2, 2)
	p := newVTParser(s, 64)
	for i := 0; i < 200; i++ {
		b := append([]byte(nil), seed...)
		for j := range b {
			b[j] ^= byte(i * j)
		}
		p.Feed(b)
		if i%50 == 0 {
			s.Resize(1+i%7, 1+i%5)
		}
		if s.x < 0 || s.x >= s.cols || s.y < 0 || s.y >= s.rows {
			t.Fatalf("cursor out of range: %d,%d in %dx%d", s.x, s.y, s.cols, s.rows)
		}
	}
}

func TestRingReplay(t *testing.T) {
	t.Run("not truncated returns all", func(t *testing.T) {
		r := newRing(4096)
		r.Write([]byte("\x1b[1mhello\n"))
		if got := string(r.Replay()); got != "\x1b[1mhello\n" {
			t.Fatalf("replay = %q", got)
		}
	})
	t.Run("truncated starts at a safe mark", func(t *testing.T) {
		r := newRing(4096)
		var total int64
		for i := 0; i < 500; i++ {
			line := []byte(fmt.Sprintf("\x1b[32mline %04d\x1b[0m\n", i))
			r.Write(line)
			total += int64(len(line))
			r.Mark(total)
		}
		out := r.Replay()
		if !r.Truncated() || len(out) == 0 || len(out) > 4096 {
			t.Fatalf("len=%d truncated=%v", len(out), r.Truncated())
		}
		if !bytes.HasPrefix(out, []byte("\x1b[32mline ")) {
			t.Fatalf("replay does not start at a line: %q", out[:20])
		}
		if !bytes.HasSuffix(out, []byte("line 0499\x1b[0m\n")) {
			t.Fatalf("replay misses the tail: %q", out[len(out)-20:])
		}
		if len(r.marks) > 4096/markSpacing+1 {
			t.Fatalf("marks not pruned: %d", len(r.marks))
		}
	})
	t.Run("huge single write", func(t *testing.T) {
		r := newRing(4096)
		big := bytes.Repeat([]byte("0123456789abcdef"), 1000)
		r.Write(big)
		r.Write([]byte("x"))
		if got := r.since(r.Start()); !bytes.Equal(got, append(big[len(big)-4095:], 'x')) {
			t.Fatal("ring content wrong after oversize write")
		}
	})
	t.Run("tail", func(t *testing.T) {
		r := newRing(1 << 16)
		var total int64
		for i := 0; i < 2000; i++ {
			line := []byte(fmt.Sprintf("row %d\n", i))
			r.Write(line)
			total += int64(len(line))
			r.Mark(total)
		}
		tail := r.Tail(4096).Replay()
		if len(tail) > 4096 || !bytes.HasPrefix(tail, []byte("row ")) || !bytes.HasSuffix(tail, []byte("row 1999\n")) {
			t.Fatalf("tail = %d bytes %q...", len(tail), tail[:10])
		}
	})
}

func TestRecorderFormat(t *testing.T) {
	dir := t.TempDir()
	start := time.Unix(1700000000, 0)
	rec, err := newRecorder(dir, "t_abcdefghij", 80, 24, "/bin/sh", start)
	if err != nil {
		t.Fatal(err)
	}
	rec.Output(start.Add(100*time.Millisecond), []byte("hello \xe2\x82")) // split €
	rec.Output(start.Add(200*time.Millisecond), []byte("\xac world\r\n")) // rest of €
	rec.Resize(start.Add(time.Second), 100, 30)
	rec.Output(start.Add(1500*time.Millisecond), []byte("\x1b[1mbold\x1b[0m"))
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	files := recordingFiles(dir, "t_abcdefghij")
	if len(files) != 1 || !strings.HasSuffix(files[0], ".001.cast.gz") {
		t.Fatalf("files = %v", files)
	}
	rc, err := openRecording(dir, "t_abcdefghij")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	lines := readLines(t, rc)
	if len(lines) != 5 {
		t.Fatalf("lines = %d: %q", len(lines), lines)
	}
	var h castHeader
	if err := json.Unmarshal([]byte(lines[0]), &h); err != nil || h.Version != 2 || h.Width != 80 || h.Height != 24 || h.Timestamp != 1700000000 {
		t.Fatalf("header = %s (%v)", lines[0], err)
	}
	wantEvents := []struct {
		t    float64
		code string
		data string
	}{
		{0.1, "o", "hello "},
		{0.2, "o", "€ world\r\n"},
		{1, "r", "100x30"},
		{1.5, "o", "\x1b[1mbold\x1b[0m"},
	}
	for i, w := range wantEvents {
		var ev []any
		if err := json.Unmarshal([]byte(lines[i+1]), &ev); err != nil || len(ev) != 3 {
			t.Fatalf("event %d = %s (%v)", i, lines[i+1], err)
		}
		if ev[0].(float64) != w.t || ev[1].(string) != w.code || ev[2].(string) != w.data {
			t.Errorf("event %d = %v, want %v", i, ev, w)
		}
	}
}

func TestRecorderRotationConcatenates(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	rec, err := newRecorder(dir, "t_rotaterota", 80, 24, "", start)
	if err != nil {
		t.Fatal(err)
	}
	rec.Output(start, []byte("part1"))
	if err := rec.rotate(start); err != nil {
		t.Fatal(err)
	}
	rec.Output(start.Add(time.Second), []byte("part2"))
	rec.Flush()
	rc, err := openRecording(dir, "t_rotaterota")
	if err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, rc)
	rc.Close()
	if len(lines) != 3 || !strings.Contains(lines[1], "part1") || !strings.Contains(lines[2], "part2") {
		t.Fatalf("concatenated = %q", lines)
	}
	_ = rec.Close()
	// Every part is itself a valid cast (header first).
	for _, f := range recordingFiles(dir, "t_rotaterota") {
		fh, _ := os.Open(f)
		zr, err := gzip.NewReader(fh)
		if err != nil {
			t.Fatal(err)
		}
		first, _ := bufio.NewReader(zr).ReadString('\n')
		fh.Close()
		if !strings.HasPrefix(first, `{"version":2`) {
			t.Fatalf("%s starts with %q", filepath.Base(f), first)
		}
	}
	if n := sweepRecordings(dir, time.Hour, nil, time.Now().Add(2*time.Hour)); n != 2 {
		t.Fatalf("sweep removed %d files, want 2", n)
	}
}

func readLines(t *testing.T, r io.Reader) []string {
	t.Helper()
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMatchPrompt(t *testing.T) {
	tests := []struct {
		agent, tail string
		want        bool
	}{
		{"claude", "Do you want to proceed?\n❯ 1. Yes\n  2. No", true},
		{"codex", "Allow command? (y/n)", true},
		{"gemini", "Apply this change? [Y/n]", true},
		{"aider", "Waiting for user confirmation...", true},
		{"claude", "Thinking… (esc to interrupt)", false},
		{"claude", "", false},
		{"opencode", "The function returns (y/n) values internally.", false},
	}
	for _, tc := range tests {
		if got := matchPrompt(tc.agent, tc.tail) != ""; got != tc.want {
			t.Errorf("matchPrompt(%q, %q) = %v, want %v", tc.agent, tc.tail, got, tc.want)
		}
	}
}

func TestIDs(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := newID()
		if !validID(id) || seen[id] {
			t.Fatalf("bad or duplicate id %q", id)
		}
		seen[id] = true
	}
	for _, bad := range []string{"", "t_", "x_abcdefghij", "t_ABCDEFGHIJ", "t_abc/../../x", "t_abcdefghi1"} {
		if validID(bad) {
			t.Errorf("validID(%q) = true", bad)
		}
	}
}
