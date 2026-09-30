package system

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/aduthekaddu/relay/internal/api"
)

const (
	// logLineMax caps the text of one log line (bytes).
	logLineMax = 8 << 10
	// logTailWindow is how far back from the end of a file the initial
	// lines are looked for.
	logTailWindow = 1 << 20
	// logPoll is the file follow interval.
	logPoll = 500 * time.Millisecond
	// logBatchMax caps lines read per poll so one huge write cannot
	// monopolise a stream.
	logBatchMax = 2000
)

// logSink receives log lines; returning an error ends the stream.
type logSink func(api.LogLine) error

// journalFollow streams `journalctl --user -u unit -f -o json -n lines`
// until ctx is done or the sink fails. The process is always reaped.
func journalFollow(ctx context.Context, journalctl, unit string, lines int, sink logSink) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, journalctl, "--user", "-u", unit, "-f", "-o", "json",
		"-n", strconv.Itoa(lines), "--no-pager", "--output-fields=MESSAGE,PRIORITY,_SYSTEMD_USER_UNIT,USER_UNIT,__REALTIME_TIMESTAMP")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "SYSTEMD_COLORS=0")
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	serr := readJournal(out, unit, sink)
	cancel()
	werr := cmd.Wait()
	if serr != nil {
		return serr
	}
	if ctx.Err() != nil || werr == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(werr, &ee) && ee.ProcessState != nil && !ee.Exited() { // killed by our cancel
		return nil
	}
	return werr
}

// readJournal decodes journalctl JSON lines from r into the sink.
func readJournal(r io.Reader, unit string, sink logSink) error {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := readCappedLine(br, 1<<20)
		if len(line) > 0 {
			if ll, ok := parseJournalLine(line, unit); ok {
				if serr := sink(ll); serr != nil {
					return serr
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// parseJournalLine converts one journalctl -o json record. MESSAGE may be
// a string, null, or an array of bytes for non-UTF-8 payloads.
func parseJournalLine(b []byte, unit string) (api.LogLine, bool) {
	var rec map[string]json.RawMessage
	if err := json.Unmarshal(b, &rec); err != nil {
		return api.LogLine{}, false
	}
	str := func(k string) string {
		raw, ok := rec[k]
		if !ok {
			return ""
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		var bs []byte
		var ints []int
		if json.Unmarshal(raw, &ints) == nil {
			bs = make([]byte, 0, len(ints))
			for _, v := range ints {
				bs = append(bs, byte(v))
			}
			return string(bs)
		}
		return ""
	}
	ll := api.LogLine{Unit: unit, Prio: 6, Text: cleanLogText(str("MESSAGE"))}
	if u := str("_SYSTEMD_USER_UNIT"); u != "" {
		ll.Unit = u
	} else if u := str("USER_UNIT"); u != "" {
		ll.Unit = u
	}
	if p, err := strconv.Atoi(str("PRIORITY")); err == nil && p >= 0 && p <= 7 {
		ll.Prio = p
	}
	if us, err := strconv.ParseInt(str("__REALTIME_TIMESTAMP"), 10, 64); err == nil && us > 0 {
		ll.At = time.UnixMicro(us).UTC()
	} else {
		ll.At = time.Now().UTC()
	}
	return ll, true
}

// cleanLogText caps a line, replaces invalid UTF-8 and strips control
// characters other than tab (ANSI colour codes are removed with them).
func cleanLogText(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if len(s) > logLineMax {
		cut := logLineMax
		for cut > logLineMax-utf8.UTFMax && !utf8.RuneStart(s[cut]) {
			cut-- // cut on a rune boundary
		}
		s = s[:cut]
	}
	s = strings.ToValidUTF8(s, "\uFFFD")
	s = stripANSI(s)
	return strings.Map(func(r rune) rune {
		if r == '\t' || r >= 0x20 && r != 0x7f {
			return r
		}
		return -1
	}, s)
}

// stripANSI removes CSI escape sequences (ESC [ … final byte).
func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// readCappedLine reads one '\n'-terminated line, discarding bytes past max.
func readCappedLine(br *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		frag, err := br.ReadSlice('\n')
		if len(line)+len(frag) <= max {
			line = append(line, frag...)
		} else if len(line) < max {
			line = append(line, frag[:max-len(line)]...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimRight(line, "\r\n"), err
	}
}

// tailFile sends the last n lines of path, then follows appended data
// like `tail -F`: truncation restarts from the beginning, and a replaced
// file (log rotation) is reopened. It returns when ctx is done or the
// sink fails.
func tailFile(ctx context.Context, path string, n int, sink logSink) error {
	f, err := openLog(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	offset, err := lastLinesOffset(f, fi.Size(), n)
	if err != nil {
		return err
	}
	send := func(line []byte) error {
		return sink(api.LogLine{At: time.Now().UTC(), Prio: 6, Text: cleanLogText(string(line))})
	}
	t := time.NewTicker(logPoll)
	defer t.Stop()
	for {
		consumed, err := readLinesAt(f, offset, logBatchMax, send)
		offset += consumed
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		cur, err := f.Stat()
		if err != nil {
			return err
		}
		if nfi, err := os.Stat(path); err == nil && !os.SameFile(cur, nfi) {
			if nf, err := openLog(path); err == nil { // rotated: finish the old file, follow the new one
				if _, err := readLinesAt(f, offset, logBatchMax, send); err != nil {
					_ = nf.Close()
					return err
				}
				_ = f.Close()
				f, offset = nf, 0
				continue
			}
		}
		if cur.Size() < offset { // truncated
			offset = 0
		}
	}
}

// readLinesAt emits complete lines of f starting at offset (at most max
// lines, each capped at logLineMax) and returns the bytes consumed. An
// incomplete final line is left for the next call unless it already
// exceeds the cap, in which case it is emitted truncated.
func readLinesAt(f *os.File, offset int64, max int, emit func([]byte) error) (int64, error) {
	br := bufio.NewReaderSize(io.NewSectionReader(f, offset, 1<<62), 64<<10)
	var consumed, pending int64
	var line []byte
	for lines := 0; lines < max; {
		frag, err := br.ReadSlice('\n')
		pending += int64(len(frag))
		if room := logLineMax - len(line); room > 0 {
			if len(frag) > room {
				line = append(line, frag[:room]...)
			} else {
				line = append(line, frag...)
			}
		}
		if err == nil { // complete line
			if eerr := emit(bytes.TrimRight(line, "\r\n")); eerr != nil {
				return consumed, eerr
			}
			consumed += pending
			pending, line, lines = 0, line[:0], lines+1
			continue
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			if pending > logLineMax { // runaway line without newline
				if eerr := emit(line); eerr != nil {
					return consumed, eerr
				}
				consumed += pending
			}
			return consumed, nil
		}
		return consumed, err
	}
	return consumed, nil
}

// lastLinesOffset returns the offset where the last n lines of f begin,
// looking back at most logTailWindow bytes.
func lastLinesOffset(f *os.File, size int64, n int) (int64, error) {
	if n <= 0 || size == 0 {
		return size, nil
	}
	start := size - logTailWindow
	if start < 0 {
		start = 0
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	end := len(buf)
	if end > 0 && buf[end-1] == '\n' {
		end-- // the final newline terminates the last line
	}
	for i := end - 1; i >= 0; i-- {
		if buf[i] == '\n' {
			n--
			if n == 0 {
				return start + int64(i) + 1, nil
			}
		}
	}
	if start == 0 {
		return 0, nil
	}
	// Window exhausted: start at the first full line inside it.
	if i := bytes.IndexByte(buf, '\n'); i >= 0 {
		return start + int64(i) + 1, nil
	}
	return start, nil
}

// errNotRegular refuses devices, sockets, pipes and directories.
var errNotRegular = errors.New("system: not a regular file")

// openLog opens path without following a final symlink and without
// blocking on FIFOs, and refuses anything but a regular file.
func openLog(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		_ = f.Close()
		if err == nil {
			err = errNotRegular
		}
		return nil, err
	}
	return f, nil
}
