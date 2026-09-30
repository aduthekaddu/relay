package files

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

func TestTrashRoundTrip(t *testing.T) {
	e := newEnv(t)
	p := e.write("work/report.txt", "draft")
	e.write("work/dir/inner.txt", "i")
	audits := e.bus.Subscribe(8, func(ev api.Event) bool { return ev.Type == AuditTopic })
	defer audits.Close()

	rec := e.do("POST", "/api/v1/files/delete", api.FileOpRequest{Paths: []string{"~/work/report.txt", "~/work/dir"}})
	if rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Lstat(p); err == nil {
		t.Fatal("file still present")
	}
	select {
	case ev := <-audits.C:
		a := ev.Data.(api.AuditEntry)
		if a.Event != "file.trash" || !strings.Contains(a.Detail, "report.txt") || a.Actor != "tester" {
			t.Fatalf("audit = %+v", a)
		}
	case <-time.After(time.Second):
		t.Fatal("no audit event")
	}
	// Spec layout: files/<name> + info/<name>.trashinfo with escaped Path.
	trashDir := filepath.Join(e.home, ".local/share/Trash")
	info, err := os.ReadFile(filepath.Join(trashDir, "info", "report.txt.trashinfo"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(info), "[Trash Info]\nPath="+escapeTrashPath(p)+"\nDeletionDate=") {
		t.Fatalf("trashinfo = %q", info)
	}

	// Same name again → unique trash name.
	e.write("work/report.txt", "second")
	if rec := e.do("POST", "/api/v1/files/delete", api.FileOpRequest{Paths: []string{"~/work/report.txt"}}); rec.Code != 204 {
		t.Fatalf("delete 2: %d", rec.Code)
	}
	items := decode[[]api.FileEntry](t, e.do("GET", "/api/v1/files/trash", nil))
	if len(items) != 3 {
		t.Fatalf("trash has %d items: %+v", len(items), items)
	}
	var names []string
	for _, it := range items {
		names = append(names, filepath.Base(it.Path))
		if it.Name == "report.txt" && it.Target != p {
			t.Fatalf("original = %q", it.Target)
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "dir,report.2.txt,report.txt" {
		t.Fatalf("trash names = %v", names)
	}

	// Restore by original path picks the newest; a clash gets a new name.
	e.write("work/report.txt", "blocking")
	rec = e.do("POST", "/api/v1/files/trash/restore", api.FileOpRequest{Paths: []string{p}})
	if rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	restored := decode[[]api.FileEntry](t, rec)
	if len(restored) != 1 || restored[0].Name != "report (2).txt" {
		t.Fatalf("restored = %+v", restored)
	}
	if b, _ := os.ReadFile(restored[0].Path); string(b) != "second" {
		t.Fatalf("restored content = %q", b)
	}
	// Restore by trash path into a now-missing parent recreates it.
	must(t, os.RemoveAll(filepath.Join(e.home, "work")))
	rec = e.do("POST", "/api/v1/files/trash/restore", api.FileOpRequest{Paths: []string{filepath.Join(trashDir, "files", "dir")}})
	if rec.Code != 200 {
		t.Fatalf("restore dir: %d %s", rec.Code, rec.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(e.home, "work/dir/inner.txt")); string(b) != "i" {
		t.Fatalf("inner = %q", b)
	}
	// Permanently delete an item from the trash, then empty.
	items = decode[[]api.FileEntry](t, e.do("GET", "/api/v1/files/trash", nil))
	if len(items) != 1 {
		t.Fatalf("remaining = %d", len(items))
	}
	if rec := e.do("POST", "/api/v1/files/delete", api.FileOpRequest{Paths: []string{items[0].Path}}); rec.Code != 204 {
		t.Fatalf("delete from trash: %d %s", rec.Code, rec.Body)
	}
	e.write("x", "x")
	e.do("POST", "/api/v1/files/delete", api.FileOpRequest{Paths: []string{"~/x"}})
	if rec := e.do("POST", "/api/v1/files/trash/empty", nil); rec.Code != 204 {
		t.Fatalf("empty: %d", rec.Code)
	}
	if items := decode[[]api.FileEntry](t, e.do("GET", "/api/v1/files/trash", nil)); len(items) != 0 {
		t.Fatalf("after empty: %d", len(items))
	}
	// Trashing the trash itself (or a parent of it) is refused.
	if rec := e.do("POST", "/api/v1/files/delete", api.FileOpRequest{Paths: []string{"~/.local"}}); rec.Code != 400 {
		t.Fatalf("trash parent of trash = %d", rec.Code)
	}
}

func TestPermanentDeleteAndRootRefusal(t *testing.T) {
	e := newEnv(t)
	p := e.write("gone.txt", "x")
	f := false
	if rec := e.do("POST", "/api/v1/files/delete", api.FileOpRequest{Paths: []string{"~/gone.txt"}, Trash: &f}); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if _, err := os.Lstat(p); err == nil {
		t.Fatal("not deleted")
	}
	if rec := e.do("POST", "/api/v1/files/delete", api.FileOpRequest{Paths: []string{"~"}, Trash: &f}); rec.Code != 403 {
		t.Fatalf("delete root = %d", rec.Code)
	}
	// Deleting a symlink removes the link, never the target outside.
	target := filepath.Join(e.out, "keep.txt")
	must(t, os.WriteFile(target, []byte("k"), 0o644))
	must(t, os.Symlink(target, filepath.Join(e.home, "link")))
	if rec := e.do("POST", "/api/v1/files/delete", api.FileOpRequest{Paths: []string{"~/link"}, Trash: &f}); rec.Code != 204 {
		t.Fatalf("delete link: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("symlink target was deleted")
	}
}

func TestOps(t *testing.T) {
	e := newEnv(t)
	e.write("a/one.txt", "1")
	e.write("b/one.txt", "clash")

	if rec := e.do("POST", "/api/v1/files/mkdir", api.FileOpRequest{Path: "~/new/deep/dir"}); rec.Code != 200 {
		t.Fatalf("mkdir: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/api/v1/files/mkdir", api.FileOpRequest{Path: "~/new"}); rec.Code != 409 {
		t.Fatalf("mkdir existing = %d", rec.Code)
	}
	rec := e.do("POST", "/api/v1/files/touch", api.FileOpRequest{Path: "~/new/empty.txt"})
	if en := decode[api.FileEntry](t, rec); rec.Code != 200 || en.Size != 0 || en.Type != "file" {
		t.Fatalf("touch: %d %+v", rec.Code, en)
	}
	rec = e.do("POST", "/api/v1/files/rename", api.FileOpRequest{Path: "~/a/one.txt", Name: "uno.txt"})
	if en := decode[api.FileEntry](t, rec); rec.Code != 200 || en.Name != "uno.txt" {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	for name, code := range map[string]int{"../x": 400, "": 400, "one.txt": 200} {
		if rec := e.do("POST", "/api/v1/files/rename", api.FileOpRequest{Path: "~/a/uno.txt", Name: name}); rec.Code != code {
			t.Errorf("rename to %q = %d, want %d", name, rec.Code, code)
		}
	}
	// Rename onto an existing name conflicts and keeps both files.
	e.write("a/two.txt", "2")
	if rec := e.do("POST", "/api/v1/files/rename", api.FileOpRequest{Path: "~/a/two.txt", Name: "one.txt"}); rec.Code != 409 {
		t.Fatalf("rename clash = %d", rec.Code)
	}
	// Move: clash → 409 before anything moves; into itself → 400.
	if rec := e.do("POST", "/api/v1/files/move", api.FileOpRequest{From: []string{"~/a/two.txt", "~/a/one.txt"}, To: "~/b"}); rec.Code != 409 {
		t.Fatalf("move clash = %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(e.home, "a/two.txt")); err != nil {
		t.Fatal("partial move happened")
	}
	if rec := e.do("POST", "/api/v1/files/move", api.FileOpRequest{From: []string{"~/a"}, To: "~/a/sub"}); rec.Code != 404 && rec.Code != 400 {
		t.Fatalf("move into missing child = %d", rec.Code)
	}
	must(t, os.Mkdir(filepath.Join(e.home, "a/sub"), 0o755))
	if rec := e.do("POST", "/api/v1/files/move", api.FileOpRequest{From: []string{"~/a"}, To: "~/a/sub"}); rec.Code != 400 {
		t.Fatalf("move into itself = %d", rec.Code)
	}
	rec = e.do("POST", "/api/v1/files/move", api.FileOpRequest{From: []string{"~/a/two.txt"}, To: "~/b"})
	if moved := decode[[]api.FileEntry](t, rec); rec.Code != 200 || len(moved) != 1 || moved[0].Path != filepath.Join(e.home, "b/two.txt") {
		t.Fatalf("move: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/api/v1/files/move", api.FileOpRequest{From: []string{"~/b/two.txt"}, To: e.out}); rec.Code != 403 {
		t.Fatalf("move outside = %d", rec.Code)
	}
}

func TestCopyJob(t *testing.T) {
	e := newEnv(t)
	e.write("src/x.txt", "xxxx")
	e.write("src/deep/y.txt", "yy")
	must(t, os.Symlink("x.txt", filepath.Join(e.home, "src/link")))
	must(t, os.Mkdir(filepath.Join(e.home, "dst"), 0o755))
	sub := e.bus.Subscribe(64, func(ev api.Event) bool { return ev.Type == api.EvFilesJob })
	defer sub.Close()

	rec := e.do("POST", "/api/v1/files/copy", api.FileOpRequest{From: []string{"~/src"}, To: "~/dst"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("copy: %d %s", rec.Code, rec.Body)
	}
	job := decode[api.FileJob](t, rec)
	var final api.FileJob
	deadline := time.After(5 * time.Second)
	for final.State == "" || final.State == "running" {
		select {
		case ev := <-sub.C:
			j := ev.Data.(api.FileJob)
			if j.ID == job.ID {
				final = j
			}
		case <-deadline:
			t.Fatal("copy job did not finish")
		}
	}
	if final.State != "done" || final.Files != 3 || final.Bytes != 6 || final.TotalFiles != 3 {
		t.Fatalf("final job = %+v", final)
	}
	if b, _ := os.ReadFile(filepath.Join(e.home, "dst/src/deep/y.txt")); string(b) != "yy" {
		t.Fatalf("copied content = %q", b)
	}
	if l, _ := os.Readlink(filepath.Join(e.home, "dst/src/link")); l != "x.txt" {
		t.Fatalf("symlink copied as %q", l)
	}
	// Copy into the same folder produces "name (2)".
	rec = e.do("POST", "/api/v1/files/copy", api.FileOpRequest{From: []string{"~/src/x.txt"}, To: "~/src"})
	job = decode[api.FileJob](t, rec)
	waitFor(t, 5*time.Second, func() bool {
		for _, j := range decode[[]api.FileJob](t, e.do("GET", "/api/v1/files/jobs", nil)) {
			if j.ID == job.ID && j.State == "done" {
				return len(j.Result) == 1 && strings.HasSuffix(j.Result[0], "x (2).txt")
			}
		}
		return false
	})
	if rec := e.do("POST", "/api/v1/files/copy", api.FileOpRequest{From: []string{"~/src"}, To: "~/src/deep"}); rec.Code != 400 {
		t.Fatalf("copy into itself = %d", rec.Code)
	}
}

func TestZip(t *testing.T) {
	e := newEnv(t)
	e.write("proj/a.txt", "alpha")
	e.write("proj/img.png", "not really a png")
	e.write("proj/sub/b.txt", "beta")
	e.write("other.txt", "other")
	must(t, os.WriteFile(filepath.Join(e.out, "secret"), []byte("s"), 0o644))
	must(t, os.Symlink(filepath.Join(e.out, "secret"), filepath.Join(e.home, "proj/outside-link")))
	must(t, os.Symlink(filepath.Join(e.home, "other.txt"), filepath.Join(e.home, "proj/inside-link")))
	must(t, os.Symlink(e.home, filepath.Join(e.home, "proj/loop")))
	unreadable := e.write("proj/locked.txt", "no")
	must(t, os.Chmod(unreadable, 0))

	rec := e.do("GET", "/api/v1/files/zip?paths=~/proj&paths=~/other.txt", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("zip: %d %v", rec.Code, rec.Header())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "files.zip") {
		t.Fatalf("disposition = %q", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			got[f.Name] = "<dir>"
			continue
		}
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(b)
		if f.Name == "proj/img.png" && f.Method != zip.Store {
			t.Error("png should be stored, not deflated")
		}
	}
	want := map[string]string{
		"proj/": "<dir>", "proj/sub/": "<dir>", "proj/a.txt": "alpha", "proj/img.png": "not really a png",
		"proj/sub/b.txt": "beta", "proj/inside-link": "other", "other.txt": "other",
	}
	if os.Geteuid() == 0 { // root can read mode-000 files
		want["proj/locked.txt"] = "no"
	}
	if len(got) != len(want) {
		t.Fatalf("zip entries = %v", keys(got))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if rec := e.do("GET", "/api/v1/files/zip?paths="+url.QueryEscape(e.out), nil); rec.Code != 403 {
		t.Fatalf("zip outside = %d", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/files/zip?paths=~/proj/sub", nil); !strings.Contains(rec.Header().Get("Content-Disposition"), "sub.zip") {
		t.Fatalf("single name = %q", rec.Header().Get("Content-Disposition"))
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestFitSize(t *testing.T) {
	for _, tc := range []struct{ w, h, max, ww, wh int }{
		{4000, 3000, 256, 256, 192},
		{3000, 4000, 256, 192, 256},
		{100, 50, 256, 100, 50},
		{10000, 10, 256, 256, 1},
		{256, 256, 256, 256, 256},
	} {
		if gw, gh := fitSize(tc.w, tc.h, tc.max); gw != tc.ww || gh != tc.wh {
			t.Errorf("fitSize(%d,%d,%d) = %d,%d want %d,%d", tc.w, tc.h, tc.max, gw, gh, tc.ww, tc.wh)
		}
	}
	for in, want := range map[int]int{32: 64, 64: 64, 100: 128, 256: 256, 300: 512, 5000: 1024} {
		if got := thumbBucket(in); got != want {
			t.Errorf("thumbBucket(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestDownscaleAverages(t *testing.T) {
	// 4×2 source: left half red, right half blue → 2×1 must be pure colours.
	src := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			c := color.NRGBA{255, 0, 0, 255}
			if x >= 2 {
				c = color.NRGBA{0, 0, 255, 255}
			}
			src.Set(x, y, c)
		}
	}
	out := Downscale(src, 2, 1)
	if got := out.RGBAAt(0, 0); got != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("left = %v", got)
	}
	if got := out.RGBAAt(1, 0); got != (color.RGBA{0, 0, 255, 255}) {
		t.Fatalf("right = %v", got)
	}
	// Averaging black and white gives mid grey.
	g := image.NewGray(image.Rect(0, 0, 2, 1))
	g.Pix[0], g.Pix[1] = 0, 255
	if got := Downscale(g, 1, 1).RGBAAt(0, 0); got.R != 128 || got.A != 255 {
		t.Fatalf("grey = %v", got)
	}
	// Premultiplied: half-transparent white over transparent → alpha halves.
	n := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	n.Set(0, 0, color.NRGBA{255, 255, 255, 255})
	n.Set(1, 0, color.NRGBA{0, 0, 0, 0})
	if got := Downscale(n, 1, 1).RGBAAt(0, 0); got.A != 128 || got.R != 128 {
		t.Fatalf("premultiplied = %v", got)
	}
}

func TestThumbnailEndpoint(t *testing.T) {
	e := newEnv(t)
	img := image.NewRGBA(image.Rect(0, 0, 800, 400))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	var buf bytes.Buffer
	must(t, jpeg.Encode(&buf, img, nil))
	e.write("photo.jpg", buf.String())
	buf.Reset()
	tr := image.NewNRGBA(image.Rect(0, 0, 300, 600)) // transparent → PNG
	must(t, png.Encode(&buf, tr))
	e.write("icon.png", buf.String())
	e.write("fake.png", "not an image")

	rec := e.do("GET", "/api/v1/files/thumb?path=~/photo.jpg&size=200", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumb: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(rec.Body.Bytes()))
	if err != nil || cfg.Width != 256 || cfg.Height != 128 {
		t.Fatalf("thumb size = %+v %v", cfg, err)
	}
	etag := rec.Header().Get("ETag")
	// Cached: the same file is served again with the same ETag, and a
	// conditional request gets 304.
	req, _ := http.NewRequest("GET", "/api/v1/files/thumb?path=~/photo.jpg&size=200", nil)
	req.Header.Set("If-None-Match", etag)
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatalf("conditional = %d", rr.Code)
	}
	entries, _ := filepath.Glob(filepath.Join(e.svc.thumbs.dir, "*", "*.jpg"))
	if len(entries) != 1 {
		t.Fatalf("cache entries = %v", entries)
	}

	rec = e.do("GET", "/api/v1/files/thumb?path=~/icon.png&size=64", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("png thumb: %d %v", rec.Code, rec.Header())
	}
	pc, _ := png.DecodeConfig(bytes.NewReader(rec.Body.Bytes()))
	if pc.Width != 32 || pc.Height != 64 {
		t.Fatalf("png thumb size = %+v", pc)
	}
	if rec := e.do("GET", "/api/v1/files/thumb?path=~/fake.png", nil); rec.Code != 415 {
		t.Fatalf("fake image = %d", rec.Code)
	}
}

func TestThumbnailRefusesHugeImages(t *testing.T) {
	e := newEnv(t)
	// A PNG header claiming 10000×5000 pixels (50 MP) is refused before
	// any pixel data is decoded.
	var buf bytes.Buffer
	buf.Write([]byte("\x89PNG\r\n\x1a\n"))
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 10000)
	binary.BigEndian.PutUint32(ihdr[4:], 5000)
	ihdr[8], ihdr[9] = 8, 2
	writePNGChunk(&buf, "IHDR", ihdr)
	e.write("huge.png", buf.String())
	if rec := e.do("GET", "/api/v1/files/thumb?path=~/huge.png", nil); rec.Code != 413 {
		t.Fatalf("huge = %d %s", rec.Code, rec.Body)
	}
}

func writePNGChunk(w *bytes.Buffer, typ string, data []byte) {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(data)))
	w.Write(l[:])
	w.WriteString(typ)
	w.Write(data)
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], crc32.ChecksumIEEE(append([]byte(typ), data...)))
	w.Write(c[:])
}

func TestExifOrientation(t *testing.T) {
	mk := func(order string, orient uint16) []byte {
		var tiff bytes.Buffer
		var bo binary.ByteOrder = binary.BigEndian
		if order == "II" {
			bo = binary.LittleEndian
		}
		tiff.WriteString(order)
		b := make([]byte, 6)
		bo.PutUint16(b[0:], 42)
		bo.PutUint32(b[2:], 8)
		tiff.Write(b)
		e := make([]byte, 2+12+4)
		bo.PutUint16(e[0:], 1)
		bo.PutUint16(e[2:], 0x0112)
		bo.PutUint16(e[4:], 3)
		bo.PutUint32(e[6:], 1)
		bo.PutUint16(e[10:], orient)
		tiff.Write(e)
		seg := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
		out := []byte{0xFF, 0xD8, 0xFF, 0xE1, byte((len(seg) + 2) >> 8), byte(len(seg) + 2)}
		out = append(out, seg...)
		return append(out, 0xFF, 0xDA, 0, 2)
	}
	for _, tc := range []struct {
		order string
		o     uint16
	}{{"MM", 6}, {"II", 3}, {"II", 8}, {"MM", 1}} {
		if got := exifOrientation(mk(tc.order, tc.o)); got != int(tc.o) {
			t.Errorf("%s orientation %d → %d", tc.order, tc.o, got)
		}
	}
	if got := exifOrientation([]byte{0xFF, 0xD8, 0xFF, 0xDA}); got != 1 {
		t.Errorf("no exif = %d", got)
	}
	// Orientation 6 (rotate 90° CW) swaps dimensions.
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})
	r := applyOrientation(img, 6)
	if r.Rect.Dx() != 2 || r.Rect.Dy() != 4 || r.RGBAAt(1, 0) != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("rotate: %v %v", r.Rect, r.RGBAAt(1, 0))
	}
}

func TestUsage(t *testing.T) {
	e := newEnv(t)
	e.write("big/a.bin", strings.Repeat("a", 1000))
	e.write("big/nested/b.bin", strings.Repeat("b", 500))
	e.write("small.txt", "12345")
	must(t, os.Symlink(e.out, filepath.Join(e.home, "outlink")))
	must(t, os.Link(filepath.Join(e.home, "big/a.bin"), filepath.Join(e.home, "big/hardlink.bin")))

	var u api.DiskUsage
	waitFor(t, 5*time.Second, func() bool {
		u = decode[api.DiskUsage](t, e.do("GET", "/api/v1/files/usage?path=~", nil))
		return !u.Pending
	})
	if !u.Complete || u.Children["big"] != 1500 || u.Children["small.txt"] != 5 || u.Children["outlink"] != 0 {
		t.Fatalf("usage = %+v", u)
	}
	if u.Size != 1505 || u.Files != 3 {
		t.Fatalf("totals size=%d files=%d", u.Size, u.Files)
	}
}

func TestSearch(t *testing.T) {
	e := newEnv(t)
	e.write("code/main.go", "package main\n\nfunc HelloWorld() {}\n")
	e.write("code/node_modules/dep/main.js", "HelloWorld")
	e.write("notes/Main ideas.md", "nothing here")
	e.write("readme.txt", "say helloworld")

	hits := ndjson(t, e.do("GET", "/api/v1/files/search?q=main", nil))
	var paths []string
	for _, h := range hits {
		paths = append(paths, strings.TrimPrefix(h.Path, e.home+"/"))
	}
	sort.Strings(paths)
	if strings.Join(paths, ",") != "code/main.go,notes/Main ideas.md" {
		t.Fatalf("name hits = %v", paths)
	}

	hits = ndjson(t, e.do("GET", "/api/v1/files/search?content=1&q=helloworld", nil))
	got := map[string]int{}
	for _, h := range hits {
		got[strings.TrimPrefix(h.Path, e.home+"/")] = h.Line
	}
	if got["code/main.go"] != 3 || got["readme.txt"] != 1 || len(got) != 2 {
		t.Fatalf("content hits = %v (rg=%q)", got, e.svc.rgPath)
	}
	// The Go fallback behaves the same.
	e.svc.rgPath = ""
	hits = ndjson(t, e.do("GET", "/api/v1/files/search?content=1&q=helloworld", nil))
	if len(hits) != 2 {
		t.Fatalf("fallback hits = %+v", hits)
	}
	if rec := e.do("GET", "/api/v1/files/search?q=", nil); rec.Code != 400 {
		t.Fatalf("empty query = %d", rec.Code)
	}
}

func ndjson(t *testing.T, rec interface{ Result() *http.Response }) []api.FileSearchHit {
	t.Helper()
	res := rec.Result()
	if res.StatusCode != 200 {
		t.Fatalf("search status %d", res.StatusCode)
	}
	var out []api.FileSearchHit
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		var h api.FileSearchHit
		must(t, json.Unmarshal(sc.Bytes(), &h))
		out = append(out, h)
	}
	return out
}

func TestSearchProvider(t *testing.T) {
	e := newEnv(t)
	e.write("project/relay/internal/files/list.go", "x")
	e.write("Documents/tax-2025.pdf", "x")
	e.svc.index.rebuild(t.Context(), e.svc.indexRoots(t.Context()))
	res := e.svc.SearchProvider().Search(t.Context(), "tax", 5)
	if len(res) != 1 || res[0].Title != "tax-2025.pdf" || res[0].Subtitle != "~/Documents" || res[0].Scope != "files" {
		t.Fatalf("results = %+v", res)
	}
	if !strings.HasPrefix(res[0].Link, "/files?path=") {
		t.Fatalf("link = %q", res[0].Link)
	}
	if res := e.svc.SearchProvider().Search(t.Context(), "l", 5); len(res) != 0 {
		t.Fatalf("1-char query returned %d", len(res))
	}
}
