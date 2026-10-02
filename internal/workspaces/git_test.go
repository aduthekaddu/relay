package workspaces

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

func TestGitOutputAndDiffLimits(t *testing.T) {
	for _, tc := range []struct {
		name, input         string
		upstream, truncated bool
	}{
		{"small", "one\n", false, false},
		{"exact", strings.Repeat("x", maxDiff), false, false},
		{"large", strings.Repeat("line\n", maxDiff/5+10), false, true},
		{"upstream truncated", "one\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated := capDiff([]byte(tc.input), tc.upstream)
			if len(got) > maxDiff || truncated != tc.truncated || !strings.HasPrefix(tc.input, got) {
				t.Fatalf("cap diff: len=%d, truncated=%v", len(got), truncated)
			}
		})
	}
	var w capWriter
	w.max = 4
	for _, p := range []string{"ab", "cde", "f"} {
		if n, err := w.Write([]byte(p)); err != nil || n != len(p) {
			t.Fatalf("write=%d,%v", n, err)
		}
	}
	if w.buf.String() != "abcd" || !w.truncated {
		t.Fatalf("cap writer: %+v", w)
	}
	s, root := gitFixture(t, true)
	writeFixture(t, root, "large.txt", strings.Repeat("line\n", maxDiff/5+50))
	d, err := s.diff(context.Background(), root, "large.txt", false)
	if err != nil || !d.Truncated || len(d.Diff) > maxDiff {
		t.Fatalf("real bounded diff: %+v, %v", d, err)
	}
}

func TestGitPorcelainLimitsAndParsers(t *testing.T) {
	var b strings.Builder
	b.WriteString("# branch.oid abcdef123456\x00# branch.head main\x00# branch.upstream origin/main\x00# branch.ab +2 -3\x00")
	for i := 0; i < maxFiles; i++ {
		fmt.Fprintf(&b, "? file-%d\x00", i)
	}
	b.WriteString("2 R. N... 100644 100644 100644 a b R100 new\x00? old name\x00? tail\x00")
	st := parseStatusV2([]byte(b.String()))
	if len(st.Files) != maxFiles || !st.Truncated || st.Ahead != 2 || st.Behind != 3 || st.Upstream != "origin/main" {
		t.Fatalf("status bounds: count=%d, %+v", len(st.Files), st)
	}
	for _, rec := range []string{"?", "1 bad", "u bad", "2 bad"} {
		if _, ok := parseEntry(rec); ok {
			t.Fatalf("malformed entry accepted: %q", rec)
		}
	}
	ns := parseNumstat([]byte("2\t1\twith space\x00-\t-\tbinary\x001\t0\t\x00old\nname\x00new\nname\x00bad\x00"))
	if ns["with space"].Added != 2 || !ns["binary"].Binary || ns["new\nname"].Added != 1 || len(ns) != 3 {
		t.Fatalf("numstat: %+v", ns)
	}
	wts := parseWorktrees([]byte("worktree /synthetic/main\x00HEAD abcdef\x00branch refs/heads/main\x00\x00worktree /synthetic/new\nline\x00HEAD defabc\x00detached\x00locked reason\x00\x00"))
	if len(wts) != 2 || !wts[0].Main || wts[1].Main || wts[1].Branch != "" || wts[1].Path != "/synthetic/new\nline" {
		t.Fatalf("worktrees: %+v", wts)
	}
}

func TestGitRunnerFailuresAndTimeout(t *testing.T) {
	s, root := gitFixture(t, true)
	_, err := s.git.run(context.Background(), time.Second, root, nil, []string{"show", "missing-ref"})
	var ge *gitError
	if !errors.As(err, &ge) || ge.Code == 0 || !strings.Contains(ge.Stderr, "missing-ref") {
		t.Fatalf("exit evidence: %v", err)
	}
	wantHTTPError(t, gitErr(err), 409, "missing-ref")
	s.git.bin = filepath.Join(root, "missing-git")
	_, err = s.status(context.Background(), root)
	wantHTTPError(t, err, 503, "not installed")
	script := filepath.Join(root, "blocked-git")
	// exec leaves no child process behind when the timeout kills this fixture.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec /bin/sleep 10\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = (gitRunner{bin: script}).run(context.Background(), 20*time.Millisecond, root, nil, []string{"status"})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout hidden: %v", err)
	}
}

func TestGitUntrackedSymlinkDoesNotReadTarget(t *testing.T) {
	s, root := gitFixture(t, true)
	outside := t.TempDir()
	writeFixture(t, outside, "private", "one\ntwo\nthree\n")
	if err := os.Symlink(filepath.Join(outside, "private"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if f := fileStatus(t, statusFixture(t, s, root), "link"); f.Added != 0 {
		t.Fatalf("symlink target was read: %+v", f)
	}
	d, err := s.diff(context.Background(), root, "link", false)
	if err != nil || strings.Contains(d.Diff, "+one") {
		t.Fatalf("symlink content leaked: %+v, %v", d, err)
	}
}

func TestGitStatusFileLimitAndMutationInputLimit(t *testing.T) {
	s, root := gitFixture(t, true)
	for i := 0; i < maxFiles+2; i++ {
		writeFixture(t, root, fmt.Sprintf("file-%05d", i), "small\n")
	}
	if st := statusFixture(t, s, root); len(st.Files) != maxFiles {
		t.Fatalf("status limit: %d", len(st.Files))
	}
	files := make([]string, maxFiles+1)
	for i := range files {
		files[i] = fmt.Sprintf("file-%05d", i)
	}
	callHandler(t, s.handleStage, "POST", "/stage", api.GitActionRequest{Path: root, Files: files}, 400)
	if gitOK(t, root, "diff", "--cached", "--name-only") != "" {
		t.Fatal("oversized request changed index")
	}
	wantContent(t, root, files[len(files)-1], "small\n")
}

func TestWorkspaceDiscoveryTraversalBounds(t *testing.T) {
	root := t.TempDir()
	// Synthetic .git markers isolate traversal policy from Git subprocesses.
	for _, p := range []string{"a/repo/.git", "a/repo/nested/.git", "b/c/d/repo/.git", "node_modules/repo/.git", ".hidden/repo/.git"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	set := map[string]bool{}
	walkRepos(context.Background(), root, discoverDepth, set)
	if len(set) != 1 || !set[filepath.Join(root, "a/repo")] {
		t.Fatalf("depth/skip/repo boundary: %v", set)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	set = map[string]bool{}
	walkRepos(ctx, root, discoverDepth, set)
	if len(set) != 0 {
		t.Fatal("canceled traversal continued")
	}
	for i := 0; i < maxWorkspaces+2; i++ {
		if err := os.MkdirAll(filepath.Join(root, fmt.Sprintf("repo-%03d/.git", i)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	set = map[string]bool{}
	walkRepos(context.Background(), root, discoverDepth, set)
	if len(set) != maxWorkspaces {
		t.Fatalf("discovery limit: %d", len(set))
	}
}
