package ptyd

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// castHeader is the first line of an asciicast v2 file.
type castHeader struct {
	Version   int               `json:"version"`
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	Timestamp int64             `json:"timestamp"`
	Title     string            `json:"title,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// maxCastPart is the size at which the live recording file is rotated
// into a gzip-compressed part.
const maxCastPart = 32 << 20

// recorder writes asciicast v2. Event times are seconds since the session
// recording started, across every rotated part, so the parts concatenate
// into one valid cast (see openRecording).
type recorder struct {
	dir   string
	id    string
	start time.Time
	shell string
	cols  int
	rows  int

	f       *os.File
	w       *bufio.Writer
	size    int64
	part    int
	pending []byte // incomplete UTF-8 sequence carried to the next write
	err     error
}

// newRecorder creates <dir>/<id>.cast and writes the header.
func newRecorder(dir, id string, cols, rows int, shell string, now time.Time) (*recorder, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("recordings dir: %w", err)
	}
	r := &recorder{dir: dir, id: id, start: now, shell: shell, cols: cols, rows: rows}
	r.part = r.nextPart()
	if err := r.open(cols, rows, now); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *recorder) livePath() string { return filepath.Join(r.dir, r.id+".cast") }

func (r *recorder) partPath(n int) string {
	return filepath.Join(r.dir, fmt.Sprintf("%s.%03d.cast.gz", r.id, n))
}

// nextPart returns the number the next rotated part will use.
func (r *recorder) nextPart() int {
	parts, _ := filepath.Glob(filepath.Join(r.dir, r.id+".*.cast.gz"))
	return len(parts) + 1
}

func (r *recorder) open(cols, rows int, now time.Time) error {
	f, err := os.OpenFile(r.livePath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create recording: %w", err)
	}
	r.f, r.w, r.size = f, bufio.NewWriterSize(f, 32<<10), 0
	env := map[string]string{"TERM": "xterm-256color"}
	if r.shell != "" {
		env["SHELL"] = r.shell
	}
	h, _ := json.Marshal(castHeader{Version: 2, Width: cols, Height: rows, Timestamp: now.Unix(), Env: env})
	return r.line(h)
}

func (r *recorder) line(b []byte) error {
	n, err := r.w.Write(b)
	r.size += int64(n)
	if err == nil {
		err = r.w.WriteByte('\n')
		r.size++
	}
	return err
}

// Output records pty output.
func (r *recorder) Output(now time.Time, b []byte) {
	if r.err != nil || len(b) == 0 {
		return
	}
	data := b
	if len(r.pending) > 0 {
		data = append(r.pending, b...)
		r.pending = nil
	}
	// Hold back a trailing incomplete rune so it is not replaced by U+FFFD.
	if cut := incompleteTail(data); cut > 0 {
		r.pending = append([]byte(nil), data[len(data)-cut:]...)
		data = data[:len(data)-cut]
	}
	if len(data) == 0 {
		return
	}
	r.event(now, "o", string(data))
}

// Resize records a terminal size change.
func (r *recorder) Resize(now time.Time, cols, rows int) {
	if r.err != nil {
		return
	}
	r.cols, r.rows = cols, rows
	r.event(now, "r", strconv.Itoa(cols)+"x"+strconv.Itoa(rows))
}

func (r *recorder) event(now time.Time, code, data string) {
	t := now.Sub(r.start).Seconds()
	if t < 0 {
		t = 0
	}
	ev, _ := json.Marshal([]any{roundTime(t), code, data})
	if err := r.line(ev); err != nil {
		r.fail(err)
		return
	}
	if r.size >= maxCastPart {
		r.fail(r.rotate(now))
	}
}

func roundTime(t float64) float64 {
	return float64(int64(t*1e6)) / 1e6
}

func (r *recorder) fail(err error) {
	if err != nil && r.err == nil {
		r.err = err
		_ = r.close()
	}
}

// rotate compresses the live file into the next part and starts a new
// live file (which gets its own header so every part is a valid cast).
func (r *recorder) rotate(now time.Time) error {
	if err := r.compress(); err != nil {
		return err
	}
	return r.open(r.cols, r.rows, now)
}

// compress closes the live file and moves it into the next gzip part.
func (r *recorder) compress() error {
	if err := r.close(); err != nil {
		return err
	}
	if err := gzipFile(r.livePath(), r.partPath(r.part)); err != nil {
		return err
	}
	r.part++
	return nil
}

// Flush writes buffered events to disk.
func (r *recorder) Flush() {
	if r.err == nil && r.w != nil {
		r.fail(r.w.Flush())
	}
}

// Close flushes the live file and compresses it into the final part.
func (r *recorder) Close() error {
	if r.err != nil {
		return nil
	}
	err := r.compress()
	r.err = errors.New("recorder closed")
	return err
}

func (r *recorder) close() error {
	if r.f == nil {
		return nil
	}
	err := r.w.Flush()
	if cerr := r.f.Close(); err == nil {
		err = cerr
	}
	r.f, r.w = nil, nil
	return err
}

// incompleteTail returns how many trailing bytes of b form the start of
// a UTF-8 sequence that is not complete yet.
func incompleteTail(b []byte) int {
	for i := 1; i <= utf8.UTFMax-1 && i <= len(b); i++ {
		c := b[len(b)-i]
		if c < 0x80 {
			return 0
		}
		if utf8.RuneStart(c) {
			if !utf8.FullRune(b[len(b)-i:]) {
				return i
			}
			return 0
		}
	}
	return 0
}

func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	_, err = io.Copy(zw, in)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

// recordingFiles lists the parts of a recording in playback order: the
// rotated gzip parts, then the live (or final) file.
func recordingFiles(dir, id string) []string {
	parts, _ := filepath.Glob(filepath.Join(dir, id+".*.cast.gz"))
	sort.Strings(parts)
	live := filepath.Join(dir, id+".cast")
	if _, err := os.Stat(live); err == nil {
		parts = append(parts, live)
	}
	return parts
}

// errNoRecording is returned when a session has no recording on disk.
var errNoRecording = errors.New("no recording")

// openRecording returns one asciicast v2 stream for the whole recording:
// the header of the first part followed by the events of every part.
func openRecording(dir, id string) (io.ReadCloser, error) {
	files := recordingFiles(dir, id)
	if len(files) == 0 {
		return nil, errNoRecording
	}
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(concatCasts(pw, files))
	}()
	return pr, nil
}

func concatCasts(w io.Writer, files []string) error {
	for i, name := range files {
		if err := copyCast(w, name, i == 0); err != nil {
			return err
		}
	}
	return nil
}

func copyCast(w io.Writer, name string, withHeader bool) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	var rd io.Reader = f
	if strings.HasSuffix(name, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer zr.Close()
		rd = zr
	}
	br := bufio.NewReaderSize(rd, 64<<10)
	first := true
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if !first || withHeader {
				if _, werr := w.Write(line); werr != nil {
					return werr
				}
				if line[len(line)-1] != '\n' {
					if _, werr := w.Write([]byte{'\n'}); werr != nil {
						return werr
					}
				}
			}
			first = false
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// removeRecording deletes every part of a recording.
func removeRecording(dir, id string) {
	for _, f := range recordingFiles(dir, id) {
		_ = os.Remove(f)
	}
}

// sweepRecordings deletes recordings whose last modification is older
// than the retention period, skipping ids in keep (live sessions).
func sweepRecordings(dir string, retention time.Duration, keep map[string]bool, now time.Time) int {
	if retention <= 0 {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".cast") && !strings.HasSuffix(name, ".cast.gz") {
			continue
		}
		id, _, _ := strings.Cut(name, ".")
		if keep[id] {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < retention {
			continue
		}
		if os.Remove(filepath.Join(dir, name)) == nil {
			removed++
		}
	}
	return removed
}
