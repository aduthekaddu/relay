package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRelease struct {
	srv      *httptest.Server
	binary   []byte
	sums     string
	lastAuth string
}

func newFakeRelease(t *testing.T, tag string, binary []byte, sums func(sum string) string) *fakeRelease {
	t.Helper()
	f := &fakeRelease{binary: binary}
	h := sha256.Sum256(binary)
	f.sums = sums(hex.EncodeToString(h[:]))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /releases/latest", func(w http.ResponseWriter, r *http.Request) {
		f.lastAuth = r.Header.Get("Authorization")
		f.release(w, tag)
	})
	mux.HandleFunc("GET /releases/tags/{tag}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("tag") != tag {
			http.NotFound(w, r)
			return
		}
		f.release(w, tag)
	})
	mux.HandleFunc("GET /dl/relay_linux_amd64", func(w http.ResponseWriter, _ *http.Request) { w.Write(f.binary) })
	mux.HandleFunc("GET /dl/checksums.txt", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(f.sums)) })
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRelease) release(w http.ResponseWriter, tag string) {
	json.NewEncoder(w).Encode(map[string]any{
		"tag_name": tag, "html_url": "https://example.com/r/" + tag,
		"assets": []map[string]any{
			{"name": "relay_linux_amd64", "browser_download_url": f.srv.URL + "/dl/relay_linux_amd64"},
			{"name": "checksums.txt", "browser_download_url": f.srv.URL + "/dl/checksums.txt"},
		},
	})
}

func (f *fakeRelease) updater() Updater {
	return Updater{HTTP: f.srv.Client(), API: f.srv.URL + "/releases"}
}

func TestUpdaterDownloadVerifies(t *testing.T) {
	bin := []byte("#!/bin/sh\necho relay v1.2.3\n")
	tests := []struct {
		name    string
		sums    func(string) string
		wantErr string
	}{
		{name: "ok", sums: func(s string) string {
			return s + "  relay_linux_amd64\n" + strings.Repeat("0", 64) + "  relay_darwin_arm64\n"
		}},
		{name: "binary-mode marker", sums: func(s string) string { return strings.ToUpper(s) + " *relay_linux_amd64\n" }},
		{name: "mismatch", sums: func(string) string { return strings.Repeat("ab", 32) + "  relay_linux_amd64\n" }, wantErr: "checksum mismatch"},
		{name: "missing entry", sums: func(s string) string { return s + "  relay_darwin_arm64\n" }, wantErr: "no entry"},
		{name: "malformed", sums: func(string) string { return "nonsense\n" }, wantErr: "malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeRelease(t, "v1.2.3", bin, tt.sums)
			up := f.updater()
			rel, err := up.FetchRelease(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			path, err := up.Download(context.Background(), rel, "relay_linux_amd64", dir)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if left, _ := os.ReadDir(dir); len(left) != 0 {
					t.Errorf("left files after failure: %v", left)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if string(b) != string(bin) || mode(t, path) != 0o755 {
				t.Error("downloaded file content/mode")
			}
			target := filepath.Join(dir, "relay")
			if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := VerifyRuns(context.Background(), path, rel.Tag); err != nil {
				t.Fatal(err)
			}
			if err := VerifyRuns(context.Background(), path, "v9.9.9"); err == nil {
				t.Error("wrong version accepted")
			}
			if err := Replace(path, target); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(target); string(b) != string(bin) {
				t.Error("not replaced")
			}
		})
	}
}

func TestUpdaterMissingChecksums(t *testing.T) {
	f := newFakeRelease(t, "v1.0.0", []byte("x"), func(s string) string { return s + "  relay_linux_amd64\n" })
	rel := &Release{Tag: "v1.0.0", Assets: []Asset{{Name: "relay_linux_amd64", URL: f.srv.URL + "/dl/relay_linux_amd64"}}}
	if _, err := f.updater().Download(context.Background(), rel, "relay_linux_amd64", t.TempDir()); err == nil || !strings.Contains(err.Error(), "unverified") {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.updater().Download(context.Background(), rel, "relay_plan9_mips", t.TempDir()); err == nil {
		t.Fatal("missing asset accepted")
	}
}

func TestFetchReleaseByTag(t *testing.T) {
	f := newFakeRelease(t, "v1.4.0", []byte("x"), func(s string) string { return s + "  relay_linux_amd64\n" })
	up := f.updater()
	rel, err := up.FetchRelease(context.Background(), "1.4.0")
	if err != nil || rel.Tag != "v1.4.0" {
		t.Fatalf("%v %v", rel, err)
	}
	if _, err := up.FetchRelease(context.Background(), "v2.0.0"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing tag: %v", err)
	}
	if _, err := up.FetchRelease(context.Background(), "../../etc"); err == nil {
		t.Fatal("path traversal in tag accepted")
	}
	// Tokens are only sent to GitHub hosts.
	up.Token = "ghp_secret"
	if _, err := up.FetchRelease(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if f.lastAuth != "" {
		t.Error("token leaked to a non-GitHub host")
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"1.2.3", "v1.2.4", -1},
		{"v1.10.0", "v1.9.9", 1},
		{"v2.0.0-rc.1", "v2.0.0", -1},
		{"v2.0.0-rc.2", "v2.0.0-rc.10", -1},
		{"v2.0.0-beta", "v2.0.0-alpha", 1},
		{"v1.0.0+build.5", "v1.0.0", 0},
		{"dev", "v0.1.0", -1},
		{"v0.1.0", "dev", 1},
		{"v1.2", "v1.2.0", 0},
		{"garbage", "junk", 0},
	}
	for _, tt := range tests {
		if got := CompareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestParseChecksums(t *testing.T) {
	sum := strings.Repeat("a1", 32)
	m, err := ParseChecksums([]byte(fmt.Sprintf("# comment\n\n%s  relay_linux_arm64\n%s *relay_darwin_amd64\n", sum, strings.ToUpper(sum))))
	if err != nil || m["relay_linux_arm64"] != sum || m["relay_darwin_amd64"] != sum {
		t.Fatalf("%v %v", m, err)
	}
	for _, bad := range []string{"", "zz  relay\n", sum[:10] + "  relay\n", sum + "  a b\n"} {
		if _, err := ParseChecksums([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestAssetName(t *testing.T) {
	if AssetName("darwin", "arm64") != "relay_darwin_arm64" {
		t.Error(AssetName("darwin", "arm64"))
	}
}

// Atomic serve replacement and an explicit downgrade never rewrite an inode
// already held by the daemon. A failed replacement leaves the target intact.
func TestReplacePreservesDaemonInodeThroughRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "relay")
	if err := os.WriteFile(target, []byte("daemon-v1"), 0700); err != nil {
		t.Fatal(err)
	}
	daemon, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	for _, tc := range []struct{ name, content string }{{"serve update", "serve-v2"}, {"explicit rollback", "daemon-v1"}} {
		t.Run(tc.name, func(t *testing.T) {
			replacement := filepath.Join(dir, "replacement")
			if err := os.WriteFile(replacement, []byte(tc.content), 0700); err != nil {
				t.Fatal(err)
			}
			if err := Replace(replacement, target); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len("daemon-v1"))
			if _, err := daemon.ReadAt(got, 0); err != nil {
				t.Fatal(err)
			}
			if string(got) != "daemon-v1" {
				t.Fatalf("daemon inode changed: %s", got)
			}
			current, err := os.ReadFile(target)
			if err != nil || string(current) != tc.content {
				t.Fatalf("target %q: %v", current, err)
			}
		})
	}
	if err := Replace(filepath.Join(dir, "missing"), target); err == nil {
		t.Fatal("missing replacement accepted")
	}
	current, err := os.ReadFile(target)
	if err != nil || string(current) != "daemon-v1" {
		t.Fatalf("failed replacement changed rollback: %q %v", current, err)
	}
}
