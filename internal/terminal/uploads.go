package terminal

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
)

const (
	// uploadChunk is the chunk size clients should send (bytes per PUT).
	uploadChunk = 4 << 20
	// uploadStale is how long an unfinished upload survives without writes.
	uploadStale = 24 * time.Hour
	// uploadSweepEvery is the stale-partial sweep interval.
	uploadSweepEvery = time.Hour
	// maxActiveUploads bounds unfinished uploads kept at once.
	maxActiveUploads = 64
	// uploadStartsPerMinute rate-limits POST /uploads.
	uploadStartsPerMinute = 60
	// maxNameBytes bounds a sanitized file name.
	maxNameBytes = 200
	// partialDir holds in-progress uploads under the uploads root.
	partialDir = ".partial"
)

var uploadIDEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// uploadMeta is persisted next to the partial data so uploads survive a
// server restart and can be resumed.
type uploadMeta struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"` // sanitized
	Size    int64     `json:"size"`
	Dir     string    `json:"dir,omitempty"` // resolved target dir (inside files root) or "" for the dated default
	Day     string    `json:"day"`           // YYYY-MM-DD for the default dir
	Mime    string    `json:"mime,omitempty"`
	Created time.Time `json:"created"`
}

// uploads manages chunked uploads on disk.
type uploads struct {
	root      string // paths.Uploads
	filesRoot string // cfg.Files.Root
	max       int64  // bytes; 0 = unlimited
	now       func() time.Time

	mu    sync.Mutex
	locks map[string]*sync.Mutex // per-upload write serialisation
	// starts is a sliding one-minute window for rate limiting.
	starts []time.Time
}

func newUploads(root, filesRoot string, max int64) (*uploads, error) {
	if root == "" {
		return nil, errors.New("terminal: uploads directory not configured")
	}
	return &uploads{root: root, filesRoot: filesRoot, max: max, now: time.Now, locks: map[string]*sync.Mutex{}}, nil
}

func (u *uploads) partial() string { return filepath.Join(u.root, partialDir) }

func (u *uploads) dataPath(id string) string { return filepath.Join(u.partial(), id+".part") }
func (u *uploads) metaPath(id string) string { return filepath.Join(u.partial(), id+".json") }

func newUploadID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	return "u_" + uploadIDEncoding.EncodeToString(b[:])
}

func validUploadID(id string) bool {
	if len(id) != 18 || !strings.HasPrefix(id, "u_") {
		return false
	}
	for _, c := range id[2:] {
		if !(c >= 'a' && c <= 'z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}

// lock returns the mutex serialising writes to one upload.
func (u *uploads) lock(id string) *sync.Mutex {
	u.mu.Lock()
	defer u.mu.Unlock()
	m := u.locks[id]
	if m == nil {
		m = &sync.Mutex{}
		u.locks[id] = m
	}
	return m
}

func (u *uploads) forget(id string) {
	u.mu.Lock()
	delete(u.locks, id)
	u.mu.Unlock()
}

// allowStart applies the start rate limit.
func (u *uploads) allowStart() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := u.now()
	keep := u.starts[:0]
	for _, t := range u.starts {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	u.starts = keep
	if len(u.starts) >= uploadStartsPerMinute {
		return false
	}
	u.starts = append(u.starts, now)
	return true
}

// sanitizeName turns a client-supplied file name into a safe base name:
// no directories, no control or bidi-override characters, no leading
// dots or dashes, bounded length with the extension kept.
func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	if !utf8.ValidString(name) {
		name = strings.ToValidUTF8(name, "_")
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20, r == 0x7f, r == utf8.RuneError:
			b.WriteByte('_')
		case unicode.Is(unicode.Bidi_Control, r), r == '\u200b', r == '\ufeff':
			// invisible characters that disguise names; drop them
		default:
			b.WriteRune(r)
		}
	}
	name = strings.TrimSpace(b.String())
	name = strings.TrimLeft(name, ".-")
	name = strings.TrimRight(name, ". ")
	if name == "" {
		return "upload"
	}
	if len(name) > maxNameBytes {
		ext := filepath.Ext(name)
		if len(ext) > 20 || !utf8.ValidString(ext) {
			ext = ""
		}
		stem := name[:len(name)-len(ext)]
		cut := maxNameBytes - len(ext)
		for cut > 0 && !utf8.RuneStart(stem[cut]) {
			cut--
		}
		name = stem[:cut] + ext
	}
	return name
}

// resolveDir validates a target directory: it must exist, be a directory
// and resolve (following symlinks) inside the files root.
func (u *uploads) resolveDir(dir, home string) (string, error) {
	if strings.IndexByte(dir, 0) >= 0 {
		return "", httpx.BadRequest("invalid directory")
	}
	switch {
	case dir == "~":
		dir = home
	case strings.HasPrefix(dir, "~/"):
		dir = filepath.Join(home, dir[2:])
	case !filepath.IsAbs(dir):
		return "", httpx.BadRequest("directory must be an absolute path")
	}
	root := u.filesRoot
	if root == "" {
		root = home
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", httpx.Forbidden("files root is not accessible")
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		return "", httpx.NotFound("directory not found")
	}
	if !within(realRoot, real) {
		return "", httpx.Forbidden("directory is outside the files root")
	}
	st, err := os.Stat(real)
	if err != nil || !st.IsDir() {
		return "", httpx.BadRequest("not a directory")
	}
	return real, nil
}

// within reports whether p is root or below it (both cleaned, absolute).
func within(root, p string) bool {
	if root == "/" {
		return true
	}
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// start registers a new upload.
func (u *uploads) start(req api.StartUploadRequest, home string) (*api.Upload, error) {
	if req.Size < 0 {
		return nil, httpx.BadRequest("size must not be negative")
	}
	if u.max > 0 && req.Size > u.max {
		return nil, &httpx.Err{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: fmt.Sprintf("file is larger than the %d MB upload limit", u.max>>20)}
	}
	if len(req.Name) > 4096 || len(req.Mime) > 255 {
		return nil, httpx.BadRequest("name or type too long")
	}
	meta := uploadMeta{ID: newUploadID(), Name: sanitizeName(req.Name), Size: req.Size, Mime: req.Mime, Created: u.now().UTC()}
	meta.Day = u.now().Format("2006-01-02")
	if req.Dir != "" {
		dir, err := u.resolveDir(req.Dir, home)
		if err != nil {
			return nil, err
		}
		meta.Dir = dir
	}
	if err := os.MkdirAll(u.partial(), 0o700); err != nil {
		return nil, fmt.Errorf("uploads dir: %w", err)
	}
	if n := u.activeCount(); n >= maxActiveUploads {
		return nil, &httpx.Err{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "too many unfinished uploads", RetryIn: 60}
	}
	if free, ok := freeBytes(u.partial()); ok && uint64(req.Size) > free {
		return nil, &httpx.Err{Status: http.StatusInsufficientStorage, Code: "no_space", Message: "not enough free disk space"}
	}
	f, err := os.OpenFile(u.dataPath(meta.ID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create partial: %w", err)
	}
	f.Close()
	if err := writeJSONFile(u.metaPath(meta.ID), meta); err != nil {
		os.Remove(u.dataPath(meta.ID))
		return nil, err
	}
	return &api.Upload{ID: meta.ID, ChunkSize: uploadChunk, Received: 0, Size: meta.Size}, nil
}

func (u *uploads) activeCount() int {
	ents, err := os.ReadDir(u.partial())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}

func freeBytes(dir string) (uint64, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return st.Bavail * uint64(st.Bsize), true
}

func writeJSONFile(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return os.Rename(tmp, path)
}

// load reads an upload's metadata and current size.
func (u *uploads) load(id string) (*uploadMeta, int64, error) {
	if !validUploadID(id) {
		return nil, 0, httpx.NotFound("upload not found")
	}
	b, err := os.ReadFile(u.metaPath(id))
	if err != nil {
		return nil, 0, httpx.NotFound("upload not found")
	}
	var m uploadMeta
	if json.Unmarshal(b, &m) != nil || m.ID != id {
		return nil, 0, httpx.NotFound("upload not found")
	}
	st, err := os.Stat(u.dataPath(id))
	if err != nil {
		return nil, 0, httpx.NotFound("upload not found")
	}
	return &m, st.Size(), nil
}

// status returns the resumable state of an upload.
func (u *uploads) status(id string) (*api.Upload, error) {
	m, got, err := u.load(id)
	if err != nil {
		return nil, err
	}
	return &api.Upload{ID: m.ID, ChunkSize: uploadChunk, Received: got, Size: m.Size}, nil
}

// put writes a chunk at offset. A chunk may restart at any offset up to
// what was received (a retried chunk overwrites); gaps are refused.
func (u *uploads) put(id string, offset int64, body io.Reader) (*api.Upload, error) {
	l := u.lock(id)
	l.Lock()
	defer l.Unlock()
	m, got, err := u.load(id)
	if err != nil {
		return nil, err
	}
	if offset < 0 || offset > got {
		return nil, &httpx.Err{Status: http.StatusConflict, Code: "offset_mismatch", Message: fmt.Sprintf("expected offset %d", got)}
	}
	f, err := os.OpenFile(u.dataPath(id), os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open partial: %w", err)
	}
	defer f.Close()
	if offset < got {
		if err := f.Truncate(offset); err != nil {
			return nil, fmt.Errorf("truncate partial: %w", err)
		}
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek partial: %w", err)
	}
	room := min(m.Size-offset, uploadChunk)
	n, err := io.Copy(f, io.LimitReader(body, room))
	if err != nil {
		// Keep what arrived: the client resumes from the reported offset.
		_ = f.Sync()
		return nil, &httpx.Err{Status: http.StatusBadRequest, Code: "interrupted", Message: fmt.Sprintf("chunk interrupted after %d bytes", n)}
	}
	// Anything beyond the declared size (or the chunk size) is an error.
	var probe [1]byte
	if k, _ := body.Read(probe[:]); k > 0 {
		_ = f.Truncate(offset)
		if offset+n >= m.Size {
			return nil, &httpx.Err{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: "more data than the declared size"}
		}
		return nil, &httpx.Err{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: fmt.Sprintf("chunks must be at most %d bytes", uploadChunk)}
	}
	now := u.now()
	_ = os.Chtimes(u.metaPath(id), now, now)
	return &api.Upload{ID: id, ChunkSize: uploadChunk, Received: offset + n, Size: m.Size}, nil
}

// complete moves a fully received upload to its destination under a
// collision-free name.
func (u *uploads) complete(id, home string) (*api.UploadResult, error) {
	l := u.lock(id)
	l.Lock()
	defer l.Unlock()
	m, got, err := u.load(id)
	if err != nil {
		return nil, err
	}
	if got != m.Size {
		return nil, &httpx.Err{Status: http.StatusConflict, Code: "incomplete", Message: fmt.Sprintf("received %d of %d bytes", got, m.Size)}
	}
	dir := filepath.Join(u.root, m.Day)
	mode := os.FileMode(0o600)
	if m.Dir != "" {
		// Re-validate: the directory may have been replaced meanwhile.
		if dir, err = u.resolveDir(m.Dir, home); err != nil {
			return nil, err
		}
		mode = 0o644
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("uploads dir: %w", err)
	}
	dest, err := placeFile(u.dataPath(id), dir, m.Name, mode)
	if err != nil {
		return nil, err
	}
	os.Remove(u.metaPath(id))
	u.forget(id)
	return &api.UploadResult{Path: dest, Name: filepath.Base(dest), Size: m.Size}, nil
}

// placeFile moves src into dir as name, or "stem (n).ext" when taken. It
// never overwrites: hard links and O_EXCL fail on existing names.
func placeFile(src, dir, name string, mode os.FileMode) (string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem, ext = name, ""
	}
	for i := 0; i < 1000; i++ {
		cand := name
		if i > 0 {
			cand = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		dest := filepath.Join(dir, cand)
		err := os.Link(src, dest)
		if err == nil {
			os.Remove(src)
			_ = os.Chmod(dest, mode)
			return dest, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		// Different filesystem or no hard links: copy exclusively.
		err = copyExclusive(src, dest, mode)
		if err == nil {
			os.Remove(src)
			return dest, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return "", fmt.Errorf("store upload: %w", err)
	}
	return "", httpx.Conflict("too many files with this name")
}

func copyExclusive(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dest)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dest)
		return err
	}
	return nil
}

// abort discards an upload.
func (u *uploads) abort(id string) error {
	if !validUploadID(id) {
		return httpx.NotFound("upload not found")
	}
	l := u.lock(id)
	l.Lock()
	defer l.Unlock()
	errMeta := os.Remove(u.metaPath(id))
	errData := os.Remove(u.dataPath(id))
	u.forget(id)
	if errors.Is(errMeta, os.ErrNotExist) && errors.Is(errData, os.ErrNotExist) {
		return httpx.NotFound("upload not found")
	}
	return nil
}

// sweep removes partial uploads with no writes for uploadStale, plus
// orphaned temp files. It returns how many uploads were removed.
func (u *uploads) sweep() int {
	ents, err := os.ReadDir(u.partial())
	if err != nil {
		return 0
	}
	now := u.now()
	last := map[string]time.Time{} // id -> newest mtime of its files
	for _, e := range ents {
		name := e.Name()
		id := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(name, ".tmp"), ".json"), ".part")
		if !validUploadID(id) {
			continue // not ours
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if t := info.ModTime(); t.After(last[id]) {
			last[id] = t
		}
	}
	removed := 0
	for id, t := range last {
		if now.Sub(t) < uploadStale {
			continue
		}
		l := u.lock(id)
		l.Lock()
		os.Remove(u.metaPath(id))
		os.Remove(u.metaPath(id) + ".tmp")
		os.Remove(u.dataPath(id))
		l.Unlock()
		u.forget(id)
		removed++
	}
	return removed
}

func (u *uploads) sweepLoop(ctx context.Context) {
	u.sweep()
	t := time.NewTicker(uploadSweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			u.sweep()
		}
	}
}

// --- handlers ---

func (s *Service) hUploadStart(w http.ResponseWriter, r *http.Request) {
	if !s.up.allowStart() {
		httpx.Fail(w, &httpx.Err{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "too many uploads, slow down", RetryIn: 10})
		return
	}
	var req api.StartUploadRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	up, err := s.up.start(req, s.d.Paths.Home)
	if err != nil {
		s.failUpload(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, up)
}

func (s *Service) hUploadGet(w http.ResponseWriter, r *http.Request) {
	up, err := s.up.status(r.PathValue("id"))
	if err != nil {
		s.failUpload(w, err)
		return
	}
	httpx.OK(w, up)
}

func (s *Service) hUploadPut(w http.ResponseWriter, r *http.Request) {
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil {
		httpx.Fail(w, httpx.BadRequest("offset query parameter required"))
		return
	}
	body := http.MaxBytesReader(w, r.Body, uploadChunk+1)
	up, err := s.up.put(r.PathValue("id"), offset, body)
	if err != nil {
		s.failUpload(w, err)
		return
	}
	httpx.OK(w, up)
}

func (s *Service) hUploadComplete(w http.ResponseWriter, r *http.Request) {
	res, err := s.up.complete(r.PathValue("id"), s.d.Paths.Home)
	if err != nil {
		s.failUpload(w, err)
		return
	}
	httpx.OK(w, res)
}

func (s *Service) hUploadAbort(w http.ResponseWriter, r *http.Request) {
	if err := s.up.abort(r.PathValue("id")); err != nil {
		s.failUpload(w, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Service) failUpload(w http.ResponseWriter, err error) {
	var he *httpx.Err
	if !errors.As(err, &he) {
		s.d.Log.Warn("upload failed", "err", err)
	}
	httpx.Fail(w, err)
}
