package workspaces

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

func TestGitStatusDiffAndRenamePaths(t *testing.T) {
	s, root := gitFixture(t, true)
	ctx := context.Background()
	paths := []string{"with space.txt", "line\nbreak.txt", "-leading.txt", "literal*.txt", ":(glob)*"}
	for _, p := range paths {
		writeFixture(t, root, p, "original\n")
	}
	gitOK(t, root, "add", "-A")
	gitOK(t, root, "commit", "-qm", "unusual paths")
	for _, p := range paths {
		writeFixture(t, root, p, "edited\nsecond line\n")
	}
	writeFixture(t, root, "untracked\nfile.txt", "new\nlast")
	writeFixture(t, root, "binary", "\x00binary")
	writeFixture(t, root, "large", strings.Repeat("x", maxDiff+1))
	st := statusFixture(t, s, root)
	if st.Root != root || st.Branch != "main" || st.Last == nil {
		t.Fatalf("status metadata: %+v", st)
	}
	for _, p := range paths {
		f := fileStatus(t, st, p)
		if f.Index != "." || f.Work != "M" || f.Added != 2 || f.Removed != 1 {
			t.Fatalf("%q status: %+v", p, f)
		}
		d, err := s.diff(ctx, root, p, false)
		if err != nil || !strings.Contains(d.Diff, "+edited") || strings.Count(d.Diff, "diff --git ") != 1 {
			t.Fatalf("literal %q diff: %+v, %v", p, d, err)
		}
	}
	if f := fileStatus(t, st, "untracked\nfile.txt"); f.Added != 2 || f.Index != "?" {
		t.Fatalf("untracked: %+v", f)
	}
	for _, p := range []string{"binary", "large"} {
		if f := fileStatus(t, st, p); f.Added != 0 {
			t.Fatalf("bounded line count %q: %+v", p, f)
		}
	}
	d, err := s.diff(ctx, root, "untracked\nfile.txt", false)
	if err != nil || !strings.Contains(d.Diff, "+last") {
		t.Fatalf("untracked diff: %+v, %v", d, err)
	}
	writeFixture(t, root, "with space.txt", "original\n")
	gitOK(t, root, "mv", "--", "with space.txt", "renamed\nfile.txt")
	f := fileStatus(t, statusFixture(t, s, root), "renamed\nfile.txt")
	if f.OrigPath != "with space.txt" || f.Index != "R" || !f.Staged {
		t.Fatalf("rename: %+v", f)
	}
	d, err = s.diff(ctx, root, "renamed\nfile.txt", true)
	if err != nil || !strings.Contains(d.Diff, "original") {
		t.Fatalf("staged rename diff: %+v, %v", d, err)
	}
}

func TestGitStageUnstageDiscardAndCommit(t *testing.T) {
	for _, path := range []string{"with space.txt", "line\nbreak.txt", "-leading.txt", "literal*.txt", ":(glob)*"} {
		t.Run(fmt.Sprintf("%q", path), func(t *testing.T) {
			s, root := gitFixture(t, true)
			writeFixture(t, root, path, "first\n")
			writeFixture(t, root, "unrelated", "keep\n")
			writeFixture(t, root, "literal-other.txt", "keep wildcard neighbor\n")
			req := api.GitActionRequest{Path: root, Files: []string{path}}
			callHandler(t, s.handleStage, "POST", "/stage", req, 200)
			if got := gitOK(t, root, "diff", "--cached", "--name-only", "-z"); got != path+"\x00" {
				t.Fatalf("staged other paths: %q", got)
			}
			callHandler(t, s.handleUnstage, "POST", "/unstage", req, 200)
			if got := gitOK(t, root, "diff", "--cached", "--name-only"); got != "" {
				t.Fatalf("index still dirty: %q", got)
			}
			wantContent(t, root, path, "first\n")
			callHandler(t, s.handleStage, "POST", "/stage", req, 200)
			writeFixture(t, root, path, "concurrent working edit\n")
			callHandler(t, s.handleCommit, "POST", "/commit", api.GitActionRequest{Path: root, Message: "  scoped commit\n\nbody  "}, 200)
			if got := gitOK(t, root, "show", "HEAD:"+path); got != "first" {
				t.Fatalf("commit consumed unstaged edit: %q", got)
			}
			wantContent(t, root, path, "concurrent working edit\n")
			callHandler(t, s.handleDiscard, "POST", "/discard", req, 200)
			wantContent(t, root, path, "first\n")
			wantContent(t, root, "unrelated", "keep\n")
			wantContent(t, root, "literal-other.txt", "keep wildcard neighbor\n")
		})
	}
}

func TestGitDiscardRenameAndNewFiles(t *testing.T) {
	s, root := gitFixture(t, true)
	gitOK(t, root, "mv", "--", "tracked.txt", "new\nname.txt")
	writeFixture(t, root, "new\nname.txt", "extra edit\n")
	writeFixture(t, root, "added", "new staged\n")
	gitOK(t, root, "add", "--", "added")
	writeFixture(t, root, "untracked", "new unstaged\n")
	writeFixture(t, root, "keep", "keep\n")
	req := api.GitActionRequest{Path: root, Files: []string{"new\nname.txt", "added", "untracked"}}
	callHandler(t, s.handleDiscard, "POST", "/discard", req, 200)
	wantContent(t, root, "tracked.txt", "base\n")
	wantContent(t, root, "keep", "keep\n")
	for _, p := range req.Files {
		wantMissing(t, filepath.Join(root, p))
	}
	if got := gitOK(t, root, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("rename discard left index changes: %q", got)
	}
}

func TestGitUnbornDiscardAndLockedAddedFile(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			s, root := gitFixture(t, committed)
			writeFixture(t, root, "added", "staged\n")
			gitOK(t, root, "add", "--", "added")
			writeFixture(t, root, "new", "untracked\n")
			writeFixture(t, root, "keep", "keep\n")
			req := api.GitActionRequest{Path: root, Files: []string{"added", "new"}}
			writeFixture(t, root, ".git/index.lock", "fixture lock")
			callHandler(t, s.handleDiscard, "POST", "/discard", req, 409)
			wantContent(t, root, "added", "staged\n")
			wantContent(t, root, "new", "untracked\n")
			if got := gitOK(t, root, "show", ":added"); got != "staged" {
				t.Fatal("index lost on locked discard")
			}
			if err := os.Remove(filepath.Join(root, ".git/index.lock")); err != nil {
				t.Fatal(err)
			}
			callHandler(t, s.handleDiscard, "POST", "/discard", req, 200)
			wantMissing(t, filepath.Join(root, "added"))
			wantMissing(t, filepath.Join(root, "new"))
			wantContent(t, root, "keep", "keep\n")
		})
	}
}

func TestGitUnbornAndDetachedHead(t *testing.T) {
	s, root := gitFixture(t, false)
	ctx := context.Background()
	if st := statusFixture(t, s, root); st.Branch != "main" || st.Last != nil || len(st.Files) != 0 {
		t.Fatalf("unborn status: %+v", st)
	}
	if cs, err := s.logOf(ctx, root, 30); err != nil || len(cs) != 0 {
		t.Fatalf("unborn log: %+v, %v", cs, err)
	}
	writeFixture(t, root, "new", "first\n")
	req := api.GitActionRequest{Path: root}
	callHandler(t, s.handleStage, "POST", "/stage", req, 200)
	callHandler(t, s.handleUnstage, "POST", "/unstage", req, 200)
	wantContent(t, root, "new", "first\n")
	callHandler(t, s.handleStage, "POST", "/stage", req, 200)
	writeFixture(t, root, "new", "second\n")
	// Git must refuse dropping the only copy of the original staged content.
	callHandler(t, s.handleUnstage, "POST", "/unstage", req, 409)
	wantContent(t, root, "new", "second\n")
	if got := gitOK(t, root, "show", ":new"); got != "first" {
		t.Fatalf("unstage failure lost index: %q", got)
	}
	callHandler(t, s.handleCommit, "POST", "/commit", api.GitActionRequest{Path: root, Message: "initial"}, 200)
	gitOK(t, root, "checkout", "--detach", "--quiet")
	if st := statusFixture(t, s, root); st.Branch != "" || st.Last == nil {
		t.Fatalf("detached: %+v", st)
	}
	if b := s.computeBrief(ctx, root); b == nil || b.Branch != gitOK(t, root, "rev-parse", "--short=7", "HEAD") {
		t.Fatalf("detached brief: %+v", b)
	}
	callHandler(t, s.handleStage, "POST", "/stage", req, 200)
	callHandler(t, s.handleCommit, "POST", "/commit", api.GitActionRequest{Path: root, Message: "detached commit"}, 200)
	wantContent(t, root, "new", "second\n")
}

func TestGitInvalidInputsAndFailuresKeepWork(t *testing.T) {
	s, root := gitFixture(t, true)
	writeFixture(t, root, "tracked.txt", "working edit\n")
	writeFixture(t, root, "untracked", "keep\n")
	before := gitOK(t, root, "status", "--porcelain=v2", "-z")
	for _, path := range []string{"", "..", "../escape", "/absolute", ".git/config", "a/../../escape", "nul\x00name"} {
		for _, handler := range []http.HandlerFunc{s.handleStage, s.handleUnstage, s.handleDiscard} {
			callHandler(t, handler, "POST", "/action", api.GitActionRequest{Path: root, Files: []string{path}}, 400)
		}
	}
	callHandler(t, s.handleDiscard, "POST", "/discard", api.GitActionRequest{Path: root}, 400)
	callHandler(t, s.handleStage, "POST", "/stage", api.GitActionRequest{Path: root, Files: []string{"missing"}}, 409)
	for _, message := range []string{"", " \n ", strings.Repeat("x", maxMessage+1)} {
		callHandler(t, s.handleCommit, "POST", "/commit", api.GitActionRequest{Path: root, Message: message}, 400)
	}
	callHandler(t, s.handleCommit, "POST", "/commit", api.GitActionRequest{Path: root, Message: "nothing staged"}, 409)
	if after := gitOK(t, root, "status", "--porcelain=v2", "-z"); before != after {
		t.Fatalf("failed validation mutated repository: %q -> %q", before, after)
	}
	writeFixture(t, root, ".git/index.lock", "owned elsewhere")
	callHandler(t, s.handleDiscard, "POST", "/discard", api.GitActionRequest{Path: root, Files: []string{"untracked", "tracked.txt"}}, 409)
	wantContent(t, root, "tracked.txt", "working edit\n")
	wantContent(t, root, "untracked", "keep\n")
	wantContent(t, root, ".git/index.lock", "owned elsewhere")
}

func TestGitHooksRejectCommitAndKeepIndex(t *testing.T) {
	s, root := gitFixture(t, true)
	writeFixture(t, root, "tracked.txt", "staged work\n")
	gitOK(t, root, "add", "-A")
	writeFixture(t, root, ".git/hooks/pre-commit", "#!/bin/sh\necho fixture-hook-rejected >&2\nexit 1\n")
	if err := os.Chmod(filepath.Join(root, ".git/hooks/pre-commit"), 0o700); err != nil {
		t.Fatal(err)
	}
	head := gitOK(t, root, "rev-parse", "HEAD")
	w := callHandler(t, s.handleCommit, "POST", "/commit", api.GitActionRequest{Path: root, Message: "rejected"}, 409)
	if !strings.Contains(w.Body.String(), "fixture-hook-rejected") || gitOK(t, root, "rev-parse", "HEAD") != head {
		t.Fatal("hook error hidden or HEAD changed")
	}
	wantContent(t, root, "tracked.txt", "staged work\n")
	if got := gitOK(t, root, "show", ":tracked.txt"); got != "staged work" {
		t.Fatalf("hook rejection lost index: %q", got)
	}
}

func TestGitMergeAndRebaseConflicts(t *testing.T) {
	for _, verb := range []string{"merge", "rebase"} {
		t.Run(verb, func(t *testing.T) {
			s, root := gitFixture(t, true)
			gitOK(t, root, "checkout", "-qb", "other")
			writeFixture(t, root, "tracked.txt", "other\n")
			gitOK(t, root, "commit", "-qam", "other edit")
			gitOK(t, root, "checkout", "-q", "main")
			writeFixture(t, root, "tracked.txt", "main\n")
			gitOK(t, root, "commit", "-qam", "main edit")
			if b, err := gitCall(t, root, verb, "other"); err == nil {
				t.Fatalf("fixture did not conflict: %s", b)
			}
			f := fileStatus(t, statusFixture(t, s, root), "tracked.txt")
			if f.Index != "U" || f.Work != "U" || f.Staged {
				t.Fatalf("conflict not exposed: %+v", f)
			}
			before := gitOK(t, root, "ls-files", "--unmerged")
			callHandler(t, s.handleCommit, "POST", "/commit", api.GitActionRequest{Path: root, Message: "must reject conflict"}, 409)
			if gitOK(t, root, "ls-files", "--unmerged") != before {
				t.Fatal("failed commit lost conflict stages")
			}
			writeFixture(t, root, "tracked.txt", "resolved\n")
			callHandler(t, s.handleStage, "POST", "/stage", api.GitActionRequest{Path: root, Files: []string{"tracked.txt"}}, 200)
			if gitOK(t, root, "ls-files", "--unmerged") != "" {
				t.Fatal("resolution not staged")
			}
			wantContent(t, root, "tracked.txt", "resolved\n")
			gitOK(t, root, verb, "--abort")
		})
	}
}

func TestGitConcurrentStagesKeepBothEdits(t *testing.T) {
	s, root := gitFixture(t, true)
	writeFixture(t, root, "one", "one\n")
	writeFixture(t, root, "two", "two\n")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, p := range []string{"one", "two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.stage(context.Background(), api.GitActionRequest{Path: root, Files: []string{p}})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := gitOK(t, root, "diff", "--cached", "--name-only"); got != "one\ntwo" {
		t.Fatalf("lost concurrent stage: %q", got)
	}
	for _, p := range []string{"one", "two"} {
		wantContent(t, root, p, p+"\n")
	}
}

func TestGitDiscoveryAndRootBoundaries(t *testing.T) {
	s, root := gitFixture(t, true)
	// A repository is a discovery boundary; nested repos remain reachable
	// through their cwd instead of being scanned recursively.
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	gitOK(t, nested, "init", "-q", "--template=")
	if got := s.discoverRepos(context.Background()); !slices.Equal(got, []string{root}) {
		t.Fatalf("discovery: %v", got)
	}
	if got := s.RootOf(nested); got != nested {
		t.Fatalf("nested root: %q", got)
	}
	outside := cleanReal(t.TempDir())
	writeFixture(t, outside, "keep", "outside\n")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{outside, filepath.Join(root, "escape"), "relative", "", root + "\x00"} {
		_, err := s.status(context.Background(), p)
		if err == nil {
			t.Fatalf("invalid repository path accepted: %q", p)
		}
	}
	for _, p := range []string{"../keep", "/absolute", ".git/config", "escape/keep"} {
		w := callHandler(t, s.handleDiff, "GET", "/diff?path="+url.QueryEscape(root)+"&file="+url.QueryEscape(p), nil, map[bool]int{true: 403, false: 400}[p == "escape/keep"])
		if strings.Contains(w.Body.String(), "outside\\n") {
			t.Fatal("outside file leaked")
		}
	}
	for _, handler := range []http.HandlerFunc{s.handleStage, s.handleUnstage, s.handleDiscard} {
		callHandler(t, handler, "POST", "/action", api.GitActionRequest{Path: root, Files: []string{"escape/keep"}}, 403)
	}
	wantContent(t, outside, "keep", "outside\n")
}

func TestGitRenameStageUnstageAndRecreatedSource(t *testing.T) {
	s, root := gitFixture(t, true)
	if err := os.Rename(filepath.Join(root, "tracked.txt"), filepath.Join(root, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	req := api.GitActionRequest{Path: root, Files: []string{"tracked.txt", "renamed.txt"}}
	callHandler(t, s.handleStage, "POST", "/stage", req, 200)
	f := fileStatus(t, statusFixture(t, s, root), "renamed.txt")
	if f.OrigPath != "tracked.txt" || !f.Staged {
		t.Fatalf("rename not staged: %+v", f)
	}
	callHandler(t, s.handleUnstage, "POST", "/unstage", req, 200)
	if gitOK(t, root, "diff", "--cached", "--name-only") != "" {
		t.Fatal("rename still staged")
	}
	wantContent(t, root, "renamed.txt", "base\n")
	wantMissing(t, filepath.Join(root, "tracked.txt"))
	callHandler(t, s.handleStage, "POST", "/stage", req, 200)
	writeFixture(t, root, "tracked.txt", "new source work\n")
	before := gitOK(t, root, "ls-files", "--stage")
	callHandler(t, s.handleDiscard, "POST", "/discard", api.GitActionRequest{Path: root, Files: []string{"renamed.txt"}}, 409)
	wantContent(t, root, "tracked.txt", "new source work\n")
	wantContent(t, root, "renamed.txt", "base\n")
	if gitOK(t, root, "ls-files", "--stage") != before {
		t.Fatal("failed rename discard changed index")
	}
}

func TestGitCommitHookConcurrentWorkingEdit(t *testing.T) {
	s, root := gitFixture(t, true)
	writeFixture(t, root, "tracked.txt", "staged snapshot\n")
	gitOK(t, root, "add", "--", "tracked.txt")
	writeFixture(t, root, ".git/hooks/pre-commit", "#!/bin/sh\n: > .git/hook-entered\ni=0\nwhile [ ! -f .git/hook-release ] && [ $i -lt 500 ]; do /bin/sleep 0.01; i=$((i+1)); done\n[ -f .git/hook-release ]\n")
	if err := os.Chmod(filepath.Join(root, ".git/hooks/pre-commit"), 0o700); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		defer close(done)
		_, err := s.commit(context.Background(), api.GitActionRequest{Path: root, Message: "hook success"})
		done <- err
	}()
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(root, ".git/hook-release"), nil, 0o600)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("fixture commit did not stop")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, ".git/hook-entered")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hook did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	writeFixture(t, root, "tracked.txt", "edited while hook ran\n")
	writeFixture(t, root, ".git/hook-release", "")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("commit did not finish")
	}
	if got := gitOK(t, root, "show", "HEAD:tracked.txt"); got != "staged snapshot" {
		t.Fatalf("commit consumed concurrent working edit: %q", got)
	}
	wantContent(t, root, "tracked.txt", "edited while hook ran\n")
	if f := fileStatus(t, statusFixture(t, s, root), "tracked.txt"); f.Work != "M" || f.Staged {
		t.Fatalf("remaining edit: %+v", f)
	}
}
