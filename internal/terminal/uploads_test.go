package terminal

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
)

func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"photo.png", "photo.png"},
		{"../../etc/passwd", "passwd"},
		{`C:\Users\me\shot.jpg`, "shot.jpg"},
		{".bashrc", "bashrc"},
		{"-rf", "rf"},
		{"a\x00b\nc.txt", "a_b_c.txt"},
		{"evil\u202egnp.exe", "evilgnp.exe"},
		{"  spaced  ", "spaced"},
		{"trailing...", "trailing"},
		{"", "upload"},
		{"...", "upload"},
		{"/", "upload"},
		{"\xff\xfe.bin", "_.bin"},
		{"Screenshot 2026-09-30 at 10.00.00.png", "Screenshot 2026-09-30 at 10.00.00.png"},
	}
	for _, tc := range cases {
		if got := sanitizeName(tc.in); got != tc.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	long := strings.Repeat("é", 300) + ".tar.gz"
	got := sanitizeName(long)
	if len(got) > maxNameBytes || !strings.HasSuffix(got, ".gz") || !strings.HasPrefix(got, "é") {
		t.Fatalf("long name %d bytes: %q", len(got), got)
	}
	if !utfValid(got) {
		t.Fatal("cut inside a rune")
	}
}

func utfValid(s string) bool { return strings.ToValidUTF8(s, "") == s }

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func (e *env) put(id string, offset int64, data []byte, out any) int {
	e.t.Helper()
	return e.call("PUT", fmt.Sprintf("/api/v1/uploads/%s?offset=%d", id, offset), data, out)
}

func TestUploadChunkedResumeAndComplete(t *testing.T) {
	e := newEnv(t, false, nil)
	data := randBytes(uploadChunk + uploadChunk/2)
	var up api.Upload
	if code := e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: "../shot.png", Size: int64(len(data)), Mime: "image/png"}, &up); code != 201 {
		t.Fatalf("start: %d", code)
	}
	if up.ChunkSize != uploadChunk || up.Received != 0 || !validUploadID(up.ID) {
		t.Fatalf("upload %+v", up)
	}
	// First chunk, partly: simulate a dropped connection after 1000 bytes.
	if code := e.put(up.ID, 0, data[:1000], &up); code != 200 || up.Received != 1000 {
		t.Fatalf("partial put: %d %+v", code, up)
	}
	// The client asks where to resume.
	var st api.Upload
	if e.call("GET", "/api/v1/uploads/"+up.ID, nil, &st); st.Received != 1000 {
		t.Fatalf("status %+v", st)
	}
	// A gap is refused.
	if code := e.put(up.ID, 5000, data[5000:6000], nil); code != http.StatusConflict {
		t.Fatalf("gap: %d", code)
	}
	// A retried chunk from an earlier offset overwrites.
	if code := e.put(up.ID, 0, data[:uploadChunk], &up); code != 200 || up.Received != uploadChunk {
		t.Fatalf("chunk 1: %d %+v", code, up)
	}
	// Completing early fails.
	if code := e.call("POST", "/api/v1/uploads/"+up.ID+"/complete", nil, nil); code != http.StatusConflict {
		t.Fatalf("early complete: %d", code)
	}
	if code := e.put(up.ID, uploadChunk, data[uploadChunk:], &up); code != 200 || up.Received != int64(len(data)) {
		t.Fatalf("chunk 2: %d %+v", code, up)
	}
	var res api.UploadResult
	if code := e.call("POST", "/api/v1/uploads/"+up.ID+"/complete", nil, &res); code != 200 {
		t.Fatalf("complete: %d", code)
	}
	day := time.Now().Format("2006-01-02")
	if res.Name != "shot.png" || res.Size != int64(len(data)) || res.Path != filepath.Join(e.paths.Uploads, day, "shot.png") {
		t.Fatalf("result %+v", res)
	}
	got, err := os.ReadFile(res.Path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("content mismatch (%v)", err)
	}
	if st, _ := os.Stat(res.Path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	// The upload is gone afterwards.
	if code := e.call("GET", "/api/v1/uploads/"+up.ID, nil, nil); code != 404 {
		t.Fatalf("after complete: %d", code)
	}
}

func TestUploadCollisionsAndDir(t *testing.T) {
	e := newEnv(t, false, nil)
	upload := func(name, dir string, content string) (api.UploadResult, int) {
		var up api.Upload
		if code := e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: name, Size: int64(len(content)), Dir: dir}, &up); code != 201 {
			return api.UploadResult{}, code
		}
		if content != "" {
			if code := e.put(up.ID, 0, []byte(content), nil); code != 200 {
				t.Fatalf("put %d", code)
			}
		}
		var res api.UploadResult
		code := e.call("POST", "/api/v1/uploads/"+up.ID+"/complete", nil, &res)
		return res, code
	}
	sub := filepath.Join(e.files, "proj")
	os.MkdirAll(sub, 0o700)
	os.WriteFile(filepath.Join(sub, "notes.txt"), []byte("original"), 0o600)

	r1, _ := upload("notes.txt", sub, "one")
	r2, _ := upload("notes.txt", sub, "two")
	r3, _ := upload("notes.txt", sub, "")
	want := []string{"notes (1).txt", "notes (2).txt", "notes (3).txt"}
	for i, r := range []api.UploadResult{r1, r2, r3} {
		if r.Name != want[i] || filepath.Dir(r.Path) != sub {
			t.Fatalf("upload %d: %+v", i, r)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(sub, "notes.txt")); string(b) != "original" {
		t.Fatal("existing file overwritten")
	}
	if st, _ := os.Stat(r1.Path); st.Mode().Perm() != 0o644 {
		t.Fatalf("dir upload mode %v", st.Mode())
	}

	// Outside the files root, via "..", or via a symlink: refused.
	outside := filepath.Join(e.paths.Home, "outside")
	os.MkdirAll(outside, 0o700)
	os.Symlink(outside, filepath.Join(e.files, "link"))
	os.WriteFile(filepath.Join(e.files, "file"), []byte("x"), 0o600)
	for _, dir := range []string{outside, e.files + "/../outside", filepath.Join(e.files, "link"), "relative/dir", filepath.Join(e.files, "missing"), filepath.Join(e.files, "file")} {
		if _, code := upload("x.txt", dir, "x"); code < 400 {
			t.Fatalf("dir %s accepted (%d)", dir, code)
		}
	}
	// "~" is the home directory, which is outside this files root.
	if _, code := upload("x.txt", "~", "x"); code != http.StatusForbidden {
		t.Fatalf("~: %d", code)
	}
}

func TestUploadLimits(t *testing.T) {
	e := newEnv(t, false, func(c *config.Config) { c.Terminal.UploadMaxMB = 1 })
	var eb api.ErrorBody
	if code := e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: "big", Size: 2 << 20}, &eb); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over limit: %d", code)
	}
	if code := e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: "neg", Size: -1}, nil); code != 400 {
		t.Fatalf("negative: %d", code)
	}
	var up api.Upload
	e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: "small", Size: 10}, &up)
	if code := e.put(up.ID, 0, []byte("0123456789EXTRA"), nil); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("more than declared: %d", code)
	}
	if st, _ := e.svc.up.status(up.ID); st.Received != 0 {
		t.Fatalf("overflowing chunk kept: %+v", st)
	}
	if code := e.put(up.ID, 0, []byte("0123456789"), nil); code != 200 {
		t.Fatalf("exact: %d", code)
	}
	if code := e.call("PUT", "/api/v1/uploads/"+up.ID, []byte("x"), nil); code != 400 {
		t.Fatalf("missing offset: %d", code)
	}
	for _, id := range []string{"u_bogus", "..%2F..%2Fetc", "u_aaaaaaaaaaaaaaaa"} {
		if code := e.put(id, 0, []byte("x"), nil); code != 404 {
			t.Fatalf("id %s: %d", id, code)
		}
	}
	// Abort removes it; a second abort is 404.
	if code := e.call("DELETE", "/api/v1/uploads/"+up.ID, nil, nil); code != 204 {
		t.Fatalf("abort: %d", code)
	}
	if code := e.call("DELETE", "/api/v1/uploads/"+up.ID, nil, nil); code != 404 {
		t.Fatalf("abort again: %d", code)
	}
}

func TestUploadChunkTooLarge(t *testing.T) {
	e := newEnv(t, false, nil)
	var up api.Upload
	size := int64(uploadChunk + 10)
	e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: "c", Size: size}, &up)
	if code := e.put(up.ID, 0, randBytes(uploadChunk+5), nil); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized chunk: %d", code)
	}
}

func TestUploadRateLimitAndSweep(t *testing.T) {
	e := newEnv(t, false, nil)
	now := time.Now()
	e.svc.up.now = func() time.Time { return now }
	var ids []string
	for i := 0; i < 3; i++ {
		var up api.Upload
		e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: "f", Size: 1}, &up)
		ids = append(ids, up.ID)
	}
	// Age the first upload past the stale limit; keep a foreign file.
	old := now.Add(-25 * time.Hour)
	for _, p := range []string{e.svc.up.dataPath(ids[0]), e.svc.up.metaPath(ids[0])} {
		os.Chtimes(p, old, old)
	}
	foreign := filepath.Join(e.svc.up.partial(), "keep.me")
	os.WriteFile(foreign, nil, 0o600)
	os.Chtimes(foreign, old, old)
	if n := e.svc.up.sweep(); n != 1 {
		t.Fatalf("swept %d", n)
	}
	if _, err := e.svc.up.status(ids[0]); err == nil {
		t.Fatal("stale upload survived")
	}
	if _, err := e.svc.up.status(ids[1]); err != nil {
		t.Fatal("fresh upload removed")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("foreign file removed")
	}

	// Rate limit: the window is full after uploadStartsPerMinute starts.
	e.svc.up.starts = nil
	for i := 0; i < uploadStartsPerMinute; i++ {
		if !e.svc.up.allowStart() {
			t.Fatalf("refused start %d", i)
		}
	}
	if code := e.call("POST", "/api/v1/uploads", api.StartUploadRequest{Name: "f", Size: 1}, nil); code != http.StatusTooManyRequests {
		t.Fatalf("rate limit: %d", code)
	}
	now = now.Add(61 * time.Second)
	if !e.svc.up.allowStart() {
		t.Fatal("window did not slide")
	}
}
