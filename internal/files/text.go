package files

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

// MaxText is the largest text payload read or written (SECURITY.md rule 4).
const MaxText = 5 << 20

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// decodeText classifies b (the first ≤ MaxText bytes of a file) and
// returns its text. truncated tells it that b was cut, so an incomplete
// final UTF-8 sequence is tolerated.
func decodeText(b []byte, truncated bool) (text, encoding string) {
	enc := "utf-8"
	if bytes.HasPrefix(b, utf8BOM) {
		b = b[len(utf8BOM):]
		enc = "utf-8-bom"
	}
	probe := b
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	if bytes.IndexByte(probe, 0) >= 0 {
		return "", "binary"
	}
	if truncated {
		// Drop at most 3 bytes of a rune split by the cut.
		for i := 0; i < 3 && len(b) > 0 && !utf8.Valid(b); i++ {
			b = b[:len(b)-1]
		}
	}
	if !utf8.Valid(b) {
		return "", "binary"
	}
	return string(b), enc
}

// ReadText returns up to MaxText bytes of a file as text.
func (s *Service) ReadText(p string) (*api.TextFile, error) {
	real, err := s.res.Resolve(p)
	if err != nil {
		return nil, err
	}
	f, fi, err := openRegular(real)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxText+1))
	if err != nil {
		return nil, mapFSError(err)
	}
	truncated := len(b) > MaxText
	if truncated {
		b = b[:MaxText]
	}
	text, enc := decodeText(b, truncated)
	return &api.TextFile{
		Path: real, Text: text, Size: fi.Size(), ModTime: fi.ModTime().UTC(),
		Truncated: truncated, Encoding: enc,
	}, nil
}

// parseMtime accepts RFC 3339 (any precision) or Unix milliseconds.
func parseMtime(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t, true
	}
	if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.UnixMilli(ms), true
	}
	return time.Time{}, false
}

// sameMtime compares a client-provided mtime with the file's. Browsers
// often keep only millisecond precision, so the client value matches when
// it equals the file time truncated to the client's precision.
func sameMtime(client, file time.Time) bool {
	if client.Equal(file) {
		return true
	}
	if client.Nanosecond()%int(time.Millisecond) == 0 {
		return client.Equal(file.Truncate(time.Millisecond))
	}
	if client.Nanosecond() == 0 {
		return client.Equal(file.Truncate(time.Second))
	}
	return false
}

// SaveText writes text to p. When ifMtime is non-empty and the file
// changed since, it returns 409. overwrote reports that an existing file
// was replaced without a precondition (audited by the handler).
func (s *Service) SaveText(p, ifMtime, text string) (entry *api.FileEntry, overwrote bool, err error) {
	if len(text) > MaxText {
		return nil, false, &httpx.Err{Status: 413, Code: "bad_request", Message: "text is larger than 5 MiB"}
	}
	if ifMtime != "" {
		if _, ok := parseMtime(ifMtime); !ok {
			return nil, false, &httpx.Err{Status: 400, Code: "bad_request", Message: "invalid mtime", Field: "mtime"}
		}
	}
	real, err := s.res.Resolve(p)
	exists := err == nil
	if err != nil {
		var he *httpx.Err
		if !errors.As(err, &he) || he.Status != http.StatusNotFound {
			return nil, false, err
		}
		if real, err = s.res.ResolveCreate(p); err != nil {
			return nil, false, err
		}
		if st, err := os.Stat(filepath.Dir(real)); err != nil || !st.IsDir() {
			return nil, false, httpx.NotFound("the folder does not exist")
		}
	}
	data := []byte(text)
	mode := fs.FileMode(0o644)
	var nlink uint64 = 1
	if exists {
		fi, err := os.Stat(real)
		if err != nil {
			return nil, false, mapFSError(err)
		}
		if !fi.Mode().IsRegular() {
			return nil, false, httpx.BadRequest("not a regular file")
		}
		if t, ok := parseMtime(ifMtime); ok && !sameMtime(t, fi.ModTime()) {
			return nil, false, &httpx.Err{Status: 409, Code: "conflict", Message: "the file changed on disk since you opened it", Field: "mtime"}
		}
		overwrote = ifMtime == ""
		mode = fi.Mode().Perm()
		if _, _, n, ok := fileID(fi); ok {
			nlink = n
		}
		if hasBOM(real) && !bytes.HasPrefix(data, utf8BOM) {
			data = append(append([]byte{}, utf8BOM...), data...)
		}
	} else if ifMtime != "" {
		return nil, false, &httpx.Err{Status: 409, Code: "conflict", Message: "the file was deleted since you opened it", Field: "mtime"}
	}
	if nlink > 1 {
		err = writeInPlace(real, data)
	} else {
		err = writeAtomic(real, data, mode)
	}
	if err != nil {
		return nil, false, mapFSError(err)
	}
	e, err := s.Stat(real)
	return e, overwrote, err
}

func hasBOM(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	var b [3]byte
	n, _ := io.ReadFull(f, b[:])
	return n == 3 && bytes.Equal(b[:], utf8BOM)
}

// writeAtomic replaces p via a temp file + rename in the same folder.
func writeAtomic(p string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".relay-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(name) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, p); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// writeInPlace truncates and rewrites p, keeping hard links intact.
func writeInPlace(p string, data []byte) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC|oNoFollow, 0)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (s *Service) handleText(w http.ResponseWriter, r *http.Request) {
	t, err := s.ReadText(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	httpx.OK(w, t)
}

func (s *Service) handleSaveText(w http.ResponseWriter, r *http.Request) {
	var req api.SaveTextRequest
	// JSON escaping can triple the size of text; the decoded text is
	// checked against MaxText afterwards.
	if err := httpx.DecodeLimit(r, &req, 3*MaxText+1024); err != nil {
		httpx.Fail(w, err)
		return
	}
	q := r.URL.Query()
	e, overwrote, err := s.SaveText(q.Get("path"), q.Get("mtime"), req.Text)
	if err != nil {
		fail(w, err)
		return
	}
	if overwrote {
		s.audit(r, "file.overwrite", e.Path)
	}
	httpx.OK(w, e)
}
