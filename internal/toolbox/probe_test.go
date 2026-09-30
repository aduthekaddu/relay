package toolbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeExe(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseVersion(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ripgrep 14.1.0\n-SIMD -AVX", "14.1.0"},
		{"go version go1.27.1 linux/arm64", "1.27.1"},
		{"v24.11.1", "24.11.1"},
		{"2.0.14 (Claude Code)", "2.0.14"},
		{"Xvnc TigerVNC 1.13.1 - built Jan  1 2024", "1.13.1"},
		{"tmux 3.4", "3.4"},
		{"no version here", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := ParseVersion(tt.in); got != tt.want {
			t.Errorf("ParseVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFinderAndProber(t *testing.T) {
	dir := t.TempDir()
	writeExe(t, dir, "alpha", `echo "alpha 1.2.3"`)
	writeExe(t, dir, "beta2", `echo "beta version 4.5" >&2; exit 3`)
	writeExe(t, dir, "slow", `sleep 5; echo 9.9.9`)
	if err := os.WriteFile(filepath.Join(dir, "noexec"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := Finder{Dirs: []string{filepath.Join(dir, "missing"), dir, "/usr/bin", "/bin"}}
	p := &Prober{Finder: f, Run: ExecRunner, Timeout: 300 * time.Millisecond, Concurrency: 2}

	rec := func(check [][]string, version string) *Recipe {
		return &Recipe{ID: "x", Check: check, Version: strings.Fields(version)}
	}
	tests := []struct {
		name          string
		r             *Recipe
		wantInstalled bool
		wantVersion   string
	}{
		{"simple", rec([][]string{{"alpha"}}, "alpha --version"), true, "1.2.3"},
		{"alternative + stderr + nonzero exit", rec([][]string{{"beta", "beta2"}}, "{bin} --version"), true, "4.5"},
		{"all required, one missing", rec([][]string{{"alpha"}, {"gamma"}}, "alpha"), false, ""},
		{"not executable", rec([][]string{{"noexec"}}, ""), false, ""},
		{"absolute path", rec([][]string{{filepath.Join(dir, "alpha")}}, "{bin}"), true, "1.2.3"},
		{"timeout keeps installed", rec([][]string{{"slow"}}, "slow"), true, ""},
		{"no version command", rec([][]string{{"alpha"}}, ""), true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			st := p.Probe(context.Background(), tt.r)
			if st.Installed != tt.wantInstalled || st.Version != tt.wantVersion {
				t.Fatalf("got %+v, want installed=%v version=%q", st, tt.wantInstalled, tt.wantVersion)
			}
			if time.Since(start) > 3*time.Second {
				t.Fatalf("probe took %v; timeout not enforced", time.Since(start))
			}
		})
	}

	all := p.ProbeAll(context.Background(), []*Recipe{tests[0].r, tests[2].r, tests[1].r})
	if !all[0].Installed || all[1].Installed || all[2].Version != "4.5" {
		t.Fatalf("ProbeAll order/results wrong: %+v", all)
	}
}

func TestSearchPathIncludesUserDirs(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:relative/dir")
	dirs := SearchPath("/home/u")
	joined := strings.Join(dirs, ":")
	for _, want := range []string{"/usr/bin", "/home/u/.local/bin", "/home/u/.cargo/bin", "/home/u/.bun/bin", "/opt/homebrew/bin"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %s", want, joined)
		}
	}
	if strings.Contains(joined, "relative/dir") {
		t.Error("relative PATH entries must be dropped")
	}
	if strings.Count(joined, "/usr/bin:") > 1 {
		t.Error("duplicates not removed")
	}
}
