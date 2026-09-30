package files

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aduthekaddu/relay/internal/httpx"
)

// storedExts are already compressed; deflating them wastes CPU.
var storedExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".avif": true, ".heic": true,
	".mp4": true, ".mov": true, ".mkv": true, ".webm": true, ".mp3": true, ".m4a": true, ".ogg": true, ".opus": true, ".flac": true,
	".zip": true, ".gz": true, ".tgz": true, ".xz": true, ".zst": true, ".bz2": true, ".7z": true, ".rar": true,
	".jar": true, ".apk": true, ".whl": true, ".pdf": true, ".woff": true, ".woff2": true,
}

type zipSource struct {
	real string // resolved path
	name string // top-level name inside the archive
}

// zipWriter streams selected trees into a zip archive.
type zipWriter struct {
	s       *Service
	zw      *zip.Writer
	ctx     context.Context
	skipped int
}

// planZip resolves the selection and assigns unique top-level names.
func (s *Service) planZip(paths []string) ([]zipSource, error) {
	if len(paths) == 0 {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "nothing selected", Field: "paths"}
	}
	if len(paths) > maxOpItems {
		return nil, httpx.BadRequest(fmt.Sprintf("at most %d items at once", maxOpItems))
	}
	used := map[string]int{}
	out := make([]zipSource, 0, len(paths))
	for _, p := range paths {
		real, err := s.res.Resolve(p)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(real)
		if real == "/" {
			name = "root"
		}
		if n := used[name]; n > 0 {
			ext := filepath.Ext(name)
			used[name]++
			name = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n+1, ext)
		} else {
			used[name] = 1
		}
		out = append(out, zipSource{real: real, name: name})
	}
	return out, nil
}

// archiveName picks the download name for a selection.
func archiveName(srcs []zipSource) string {
	if len(srcs) == 1 {
		return strings.TrimSuffix(srcs[0].name, filepath.Ext(srcs[0].name)) + ".zip"
	}
	return "files.zip"
}

func (z *zipWriter) addTree(src zipSource) error {
	return filepath.WalkDir(src.real, func(p string, d fs.DirEntry, err error) error {
		if cerr := z.ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			z.skipped++
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(src.real, p)
		if rerr != nil {
			return nil
		}
		name := path.Join(src.name, filepath.ToSlash(rel))
		switch {
		case d.IsDir():
			return z.addDir(p, name)
		case d.Type()&fs.ModeSymlink != 0:
			return z.addSymlinkTarget(p, name)
		case d.Type().IsRegular():
			return z.addFile(p, name)
		}
		z.skipped++ // device, socket, pipe
		return nil
	})
}

func (z *zipWriter) addDir(p, name string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		z.skipped++
		return filepath.SkipDir
	}
	h, err := zip.FileInfoHeader(fi)
	if err != nil {
		return nil
	}
	h.Name = name + "/"
	h.Method = zip.Store
	_, err = z.zw.CreateHeader(h)
	return err
}

// addSymlinkTarget includes the content of a symlink's target when it is
// a regular file inside the root. Links to folders are skipped (loops),
// links leaving the root are skipped (containment).
func (z *zipWriter) addSymlinkTarget(p, name string) error {
	real, err := filepath.EvalSymlinks(p)
	if err != nil || !z.s.res.Inside(real) {
		z.skipped++
		return nil
	}
	fi, err := os.Stat(real)
	if err != nil || !fi.Mode().IsRegular() {
		z.skipped++
		return nil
	}
	return z.addFile(real, name)
}

func (z *zipWriter) addFile(p, name string) error {
	f, fi, err := openRegular(p)
	if err != nil {
		z.skipped++
		return nil
	}
	defer f.Close()
	h, err := zip.FileInfoHeader(fi)
	if err != nil {
		z.skipped++
		return nil
	}
	h.Name = name
	h.Modified = fi.ModTime()
	if storedExts[strings.ToLower(filepath.Ext(name))] {
		h.Method = zip.Store
	} else {
		h.Method = zip.Deflate
	}
	w, err := z.zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, &ctxReader{ctx: z.ctx, r: f})
	return err
}

// ctxReader aborts long copies when the client goes away.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// WriteZip streams srcs as a zip archive to w.
func (s *Service) WriteZip(ctx context.Context, w io.Writer, srcs []zipSource) (skipped int, err error) {
	z := &zipWriter{s: s, zw: zip.NewWriter(w), ctx: ctx}
	for _, src := range srcs {
		if err := z.addTree(src); err != nil {
			return z.skipped, err
		}
	}
	if z.skipped > 0 {
		z.zw.SetComment(fmt.Sprintf("Relay: %d unreadable or special entries were skipped", z.skipped))
	}
	return z.skipped, z.zw.Close()
}

func (s *Service) handleZip(w http.ResponseWriter, r *http.Request) {
	srcs, err := s.planZip(r.URL.Query()["paths"])
	if err != nil {
		fail(w, err)
		return
	}
	h := w.Header()
	setSandboxHeaders(h)
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", contentDisposition("attachment", archiveName(srcs)))
	h.Set("Cache-Control", "no-store")
	// Headers are sent with the first bytes; after that an error can only
	// truncate the stream, which the client sees as a failed download.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	if _, err := s.WriteZip(r.Context(), w, srcs); err != nil && r.Context().Err() == nil {
		s.log.Warn("zip stream aborted", "err", err)
	}
}
