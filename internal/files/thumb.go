package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/webp"

	"github.com/aduthekaddu/relay/internal/httpx"
)

const (
	thumbMaxPixels  = 40_000_000 // refuse larger sources (memory)
	thumbMaxBytes   = 64 << 20
	thumbCacheLimit = 512 << 20 // total cache size before pruning oldest
)

var thumbSizes = []int{64, 128, 256, 512, 1024}

// thumbBucket rounds a requested size up to a cached size bucket.
func thumbBucket(n int) int {
	for _, b := range thumbSizes {
		if n <= b {
			return b
		}
	}
	return thumbSizes[len(thumbSizes)-1]
}

type thumbnailer struct {
	dir string
	sem chan struct{} // bounds concurrent decodes (memory)

	mu       sync.Mutex
	inflight map[string]chan struct{}
}

func newThumbnailer(dir string) *thumbnailer {
	return &thumbnailer{dir: dir, sem: make(chan struct{}, 2), inflight: map[string]chan struct{}{}}
}

// thumbFormat returns the decoder for a file name, or nil.
func thumbDecoder(name string) (decode func(io.Reader) (image.Image, error), config func(io.Reader) (image.Config, error)) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".jfif":
		return jpeg.Decode, jpeg.DecodeConfig
	case ".png":
		return png.Decode, png.DecodeConfig
	case ".gif":
		return gif.Decode, gif.DecodeConfig
	case ".webp":
		return webp.Decode, webp.DecodeConfig
	}
	return nil, nil
}

func thumbKey(real string, fi os.FileInfo, size int) string {
	h := sha256.New()
	h.Write([]byte(real))
	var b [24]byte
	binary.LittleEndian.PutUint64(b[0:], uint64(fi.ModTime().UnixNano()))
	binary.LittleEndian.PutUint64(b[8:], uint64(fi.Size()))
	binary.LittleEndian.PutUint64(b[16:], uint64(size))
	h.Write(b[:])
	return hex.EncodeToString(h.Sum(nil)[:20])
}

// get returns the cached thumbnail path for real, generating it if needed.
func (t *thumbnailer) get(ctx context.Context, real string, fi os.FileInfo, size int) (string, error) {
	decode, config := thumbDecoder(real)
	if decode == nil {
		return "", &httpx.Err{Status: 415, Code: "bad_request", Message: "no thumbnail for this file type"}
	}
	key := thumbKey(real, fi, size)
	base := filepath.Join(t.dir, key[:2], key)
	for {
		if p, ok := cached(base); ok {
			return p, nil
		}
		t.mu.Lock()
		ch, busy := t.inflight[key]
		if !busy {
			ch = make(chan struct{})
			t.inflight[key] = ch
		}
		t.mu.Unlock()
		if busy {
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		p, err := t.generate(ctx, real, base, size, decode, config)
		t.mu.Lock()
		delete(t.inflight, key)
		close(ch)
		t.mu.Unlock()
		return p, err
	}
}

func cached(base string) (string, bool) {
	for _, ext := range []string{".jpg", ".png"} {
		p := base + ext
		if st, err := os.Stat(p); err == nil {
			if time.Since(st.ModTime()) > 24*time.Hour { // keep hot entries from pruning
				now := time.Now()
				_ = os.Chtimes(p, now, now)
			}
			return p, true
		}
	}
	return "", false
}

func (t *thumbnailer) generate(ctx context.Context, real, base string, size int,
	decode func(io.Reader) (image.Image, error), config func(io.Reader) (image.Config, error)) (string, error) {
	select {
	case t.sem <- struct{}{}:
		defer func() { <-t.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	f, fi, err := openRegular(real)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if fi.Size() > thumbMaxBytes {
		return "", &httpx.Err{Status: 413, Code: "bad_request", Message: "image is too large for a thumbnail"}
	}
	data, err := io.ReadAll(io.LimitReader(f, thumbMaxBytes))
	if err != nil {
		return "", mapFSError(err)
	}
	cfg, err := config(bytes.NewReader(data))
	if err != nil {
		return "", &httpx.Err{Status: 415, Code: "bad_request", Message: "not a readable image"}
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > thumbMaxPixels {
		return "", &httpx.Err{Status: 413, Code: "bad_request", Message: "image has too many pixels for a thumbnail"}
	}
	img, err := decode(bytes.NewReader(data))
	if err != nil {
		return "", &httpx.Err{Status: 415, Code: "bad_request", Message: "not a readable image"}
	}
	orient := 1
	if isJPEG(data) {
		orient = exifOrientation(data)
	}
	data = nil //nolint:ineffassign // release the source bytes before resizing
	dw, dh := fitSize(img.Bounds().Dx(), img.Bounds().Dy(), size)
	out := applyOrientation(Downscale(img, dw, dh), orient)
	var buf bytes.Buffer
	ext := ".jpg"
	if opaque(out) {
		err = jpeg.Encode(&buf, out, &jpeg.Options{Quality: 82})
	} else {
		ext = ".png"
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		err = enc.Encode(&buf, out)
	}
	if err != nil {
		return "", err
	}
	p := base + ext
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	if err := writeAtomic(p, buf.Bytes(), 0o600); err != nil {
		return "", err
	}
	return p, nil
}

// fitSize scales (w, h) to fit in a max×max box, never upscaling.
func fitSize(w, h, max int) (int, int) {
	if w <= max && h <= max {
		return w, h
	}
	if w >= h {
		nh := int(float64(h)*float64(max)/float64(w) + 0.5)
		if nh < 1 {
			nh = 1
		}
		return max, nh
	}
	nw := int(float64(w)*float64(max)/float64(h) + 0.5)
	if nw < 1 {
		nw = 1
	}
	return nw, max
}

func opaque(img *image.RGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0xff {
			return false
		}
	}
	return true
}

// Downscale resizes src to dw×dh with a box (area-average) filter. Each
// destination pixel averages the source pixels it covers, which is exact
// for integer ratios and alias-free for large reductions. Rows are read
// one at a time, so memory stays O(width) beyond the source image.
// Averaging happens in premultiplied alpha.
func Downscale(src image.Image, dw, dh int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	if sw == 0 || sh == 0 || dw == 0 || dh == 0 {
		return dst
	}
	xs := make([]int, dw+1)
	for x := 0; x <= dw; x++ {
		xs[x] = x * sw / dw
	}
	for x := 0; x < dw; x++ { // upscaling: every column covers ≥ 1 source pixel
		if xs[x+1] <= xs[x] {
			xs[x+1] = xs[x] + 1
		}
	}
	if xs[dw] > sw {
		xs[dw] = sw
	}
	row := make([]uint8, sw*4)
	acc := make([]uint64, dw*4)
	pal := paletteCache(src)
	for y := 0; y < dh; y++ {
		y0, y1 := y*sh/dh, (y+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		if y1 > sh {
			y1 = sh
		}
		for i := range acc {
			acc[i] = 0
		}
		for sy := y0; sy < y1; sy++ {
			readRow(src, b.Min.Y+sy, row, pal)
			for x := 0; x < dw; x++ {
				a := acc[x*4 : x*4+4]
				for sx := xs[x]; sx < xs[x+1] && sx < sw; sx++ {
					p := row[sx*4 : sx*4+4]
					a[0] += uint64(p[0])
					a[1] += uint64(p[1])
					a[2] += uint64(p[2])
					a[3] += uint64(p[3])
				}
			}
		}
		rows := uint64(y1 - y0)
		o := dst.Pix[y*dst.Stride:]
		for x := 0; x < dw; x++ {
			n := rows * uint64(xs[x+1]-xs[x])
			if n == 0 {
				n = 1
			}
			a := acc[x*4 : x*4+4]
			o[x*4+0] = uint8((a[0] + n/2) / n)
			o[x*4+1] = uint8((a[1] + n/2) / n)
			o[x*4+2] = uint8((a[2] + n/2) / n)
			o[x*4+3] = uint8((a[3] + n/2) / n)
		}
	}
	return dst
}

// paletteCache premultiplies a paletted image's palette once.
func paletteCache(src image.Image) [][4]uint8 {
	p, ok := src.(*image.Paletted)
	if !ok {
		return nil
	}
	out := make([][4]uint8, len(p.Palette))
	for i, c := range p.Palette {
		r, g, b, a := c.RGBA()
		out[i] = [4]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
	}
	return out
}

// readRow fills row with premultiplied RGBA8 pixels of source row y.
func readRow(src image.Image, y int, row []uint8, pal [][4]uint8) {
	b := src.Bounds()
	w := b.Dx()
	switch s := src.(type) {
	case *image.RGBA:
		copy(row, s.Pix[(y-s.Rect.Min.Y)*s.Stride:][:w*4])
	case *image.NRGBA:
		in := s.Pix[(y-s.Rect.Min.Y)*s.Stride:]
		for x := 0; x < w; x++ {
			a := uint32(in[x*4+3])
			row[x*4+0] = uint8(uint32(in[x*4+0]) * a / 255)
			row[x*4+1] = uint8(uint32(in[x*4+1]) * a / 255)
			row[x*4+2] = uint8(uint32(in[x*4+2]) * a / 255)
			row[x*4+3] = uint8(a)
		}
	case *image.YCbCr:
		for x := 0; x < w; x++ {
			yi := s.YOffset(b.Min.X+x, y)
			ci := s.COffset(b.Min.X+x, y)
			r, g, bl := color.YCbCrToRGB(s.Y[yi], s.Cb[ci], s.Cr[ci])
			row[x*4+0], row[x*4+1], row[x*4+2], row[x*4+3] = r, g, bl, 0xff
		}
	case *image.Gray:
		in := s.Pix[(y-s.Rect.Min.Y)*s.Stride:]
		for x := 0; x < w; x++ {
			v := in[x]
			row[x*4+0], row[x*4+1], row[x*4+2], row[x*4+3] = v, v, v, 0xff
		}
	case *image.Paletted:
		in := s.Pix[(y-s.Rect.Min.Y)*s.Stride:]
		for x := 0; x < w; x++ {
			idx := int(in[x])
			if idx < len(pal) {
				c := pal[idx]
				row[x*4+0], row[x*4+1], row[x*4+2], row[x*4+3] = c[0], c[1], c[2], c[3]
			} else {
				row[x*4+0], row[x*4+1], row[x*4+2], row[x*4+3] = 0, 0, 0, 0
			}
		}
	default:
		for x := 0; x < w; x++ {
			r, g, bl, a := src.At(b.Min.X+x, y).RGBA()
			row[x*4+0], row[x*4+1], row[x*4+2], row[x*4+3] = uint8(r>>8), uint8(g>>8), uint8(bl>>8), uint8(a>>8)
		}
	}
}

func isJPEG(b []byte) bool { return len(b) > 3 && b[0] == 0xFF && b[1] == 0xD8 }

// exifOrientation returns the EXIF Orientation (1–8) of a JPEG, or 1.
func exifOrientation(b []byte) int {
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		if marker == 0xD8 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			i += 2
			continue
		}
		if marker == 0xDA || marker == 0xD9 { // start of scan: no EXIF before it
			return 1
		}
		segLen := int(b[i+2])<<8 | int(b[i+3])
		if segLen < 2 || i+2+segLen > len(b) {
			return 1
		}
		seg := b[i+4 : i+2+segLen]
		if marker == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + segLen
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(t[4:8]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[off:]))
	for k := 0; k < n; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			v := int(bo.Uint16(t[e+8:]))
			if v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// applyOrientation rotates/flips img per EXIF orientation.
func applyOrientation(img *image.RGBA, o int) *image.RGBA {
	if o <= 1 || o > 8 {
		return img
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	ow, oh := w, h
	if o >= 5 {
		ow, oh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			si := y*img.Stride + x*4
			di := ny*out.Stride + nx*4
			copy(out.Pix[di:di+4], img.Pix[si:si+4])
		}
	}
	return out
}

// prune removes thumbnails unused for maxAge and, if the cache is still
// above thumbCacheLimit, the least recently used ones.
func (t *thumbnailer) prune(maxAge time.Duration) {
	type item struct {
		path string
		at   time.Time
		size int64
	}
	var items []item
	var total int64
	_ = filepath.WalkDir(t.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if time.Since(fi.ModTime()) > maxAge {
			_ = os.Remove(p)
			return nil
		}
		items = append(items, item{p, fi.ModTime(), fi.Size()})
		total += fi.Size()
		return nil
	})
	if total <= thumbCacheLimit {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
	for _, it := range items {
		if total <= thumbCacheLimit*3/4 {
			break
		}
		if os.Remove(it.path) == nil {
			total -= it.size
		}
	}
}

func (s *Service) handleThumb(w http.ResponseWriter, r *http.Request) {
	real, err := s.res.Resolve(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	fi, err := os.Stat(real)
	if err != nil {
		fail(w, err)
		return
	}
	if !fi.Mode().IsRegular() {
		httpx.Fail(w, httpx.BadRequest("not a file"))
		return
	}
	size := thumbBucket(httpx.QueryInt(r, "size", 256, 32, 1024))
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	p, err := s.thumbs.get(ctx, real, fi, size)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = httpx.Unavailable("thumbnail timed out")
		}
		fail(w, err)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		fail(w, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		fail(w, err)
		return
	}
	h := w.Header()
	setSandboxHeaders(h)
	if strings.HasSuffix(p, ".png") {
		h.Set("Content-Type", "image/png")
	} else {
		h.Set("Content-Type", "image/jpeg")
	}
	h.Set("ETag", `"`+filepath.Base(strings.TrimSuffix(p, filepath.Ext(p)))+`"`)
	if r.URL.Query().Get("v") != "" {
		h.Set("Cache-Control", "private, max-age=604800, immutable")
	} else {
		h.Set("Cache-Control", "private, no-cache")
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
}
