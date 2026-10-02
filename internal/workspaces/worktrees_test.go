package workspaces

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

type fixturePty struct {
	terms  []api.TerminalSession
	err    error
	spec   ptyclient.CreateSpec
	create func(context.Context, ptyclient.CreateSpec) (*api.TerminalSession, error)
}

func (p *fixturePty) List(context.Context) ([]api.TerminalSession, error) { return p.terms, p.err }
func (p *fixturePty) Create(ctx context.Context, spec ptyclient.CreateSpec) (*api.TerminalSession, error) {
	p.spec = spec
	if p.create != nil {
		return p.create(ctx, spec)
	}
	if p.err != nil {
		return nil, p.err
	}
	return &api.TerminalSession{ID: "fixture-task", Cwd: spec.Cwd, Kind: api.KindTask}, nil
}

func TestGitWorktreeValidationAndLifecycle(t *testing.T) {
	s, root := gitFixture(t, true)
	ctx := context.Background()
	for _, branch := range []string{"", "-force", "../escape", "a//b", "a..b", "a.lock", "a/.hidden", "with space", "new\nline", "HEAD", strings.Repeat("a", 101)} {
		_, err := s.CreateWorktree(ctx, root, branch, "")
		wantHTTPError(t, err, 400, "branch")
	}
	for _, base := range []string{"-force", "../escape", "bad base", "a..b"} {
		_, err := s.CreateWorktree(ctx, root, "safe", base)
		wantHTTPError(t, err, 400, "base")
	}
	_, err := s.CreateWorktree(ctx, root, "safe", "does-not-exist")
	wantHTTPError(t, err, 409, "")
	if gitOK(t, root, "branch", "--list", "safe") != "" {
		t.Fatal("failed create left branch")
	}
	wantMissing(t, filepath.Join(root, ".worktrees", "safe"))
	wt, err := s.CreateWorktree(ctx, root, "feature/topic", "main")
	if err != nil {
		t.Fatal(err)
	}
	if wt.Path != filepath.Join(root, ".worktrees", "feature", "topic") || wt.Head != gitOK(t, root, "rev-parse", "HEAD") {
		t.Fatalf("worktree: %+v", wt)
	}
	if s.RootOf(wt.Path) != wt.Path {
		t.Fatal("linked root not discovered")
	}
	st := statusFixture(t, s, wt.Path)
	if !st.Worktree || st.Branch != "feature/topic" || len(st.Worktrees) != 2 {
		t.Fatalf("linked status: %+v", st)
	}
	_, err = s.CreateWorktree(ctx, root, "feature/topic", "")
	wantHTTPError(t, err, 409, "exists")
	second, err := s.CreateWorktree(ctx, wt.Path, "second", wt.Head[:8])
	if err != nil || second.Path != filepath.Join(root, ".worktrees", "second") {
		t.Fatalf("linked create: %+v,%v", second, err)
	}
	if n := strings.Count(gitOK(t, root, "status", "--porcelain"), ".worktrees"); n != 0 {
		t.Fatal("worktrees not excluded")
	}
	for _, p := range []string{second.Path, wt.Path} {
		removed, err := s.removeWorktree(ctx, p)
		if err != nil || removed != p {
			t.Fatalf("remove=%q,%v", removed, err)
		}
		wantMissing(t, p)
		if s.RootOf(p) != "" {
			t.Fatal("removed root cached")
		}
	}
	if got := gitOK(t, root, "worktree", "list", "--porcelain"); strings.Count(got, "worktree ") != 1 {
		t.Fatalf("stale registration: %s", got)
	}
}

func TestGitWorktreeDeletionSafety(t *testing.T) {
	for _, kind := range []string{"main", "dirty", "untracked", "locked", "live cwd", "live original cwd", "list failure", "exited"} {
		t.Run(kind, func(t *testing.T) {
			s, root := gitFixture(t, true)
			ctx := context.Background()
			wt, err := s.CreateWorktree(ctx, root, "feature", "")
			if err != nil {
				t.Fatal(err)
			}
			path := wt.Path
			code := 409
			switch kind {
			case "main":
				path = root
			case "dirty":
				writeFixture(t, path, "tracked.txt", "keep edit\n")
			case "untracked":
				writeFixture(t, path, "new", "keep new\n")
			case "locked":
				gitOK(t, root, "worktree", "lock", "--reason", "fixture in use", path)
			case "live cwd":
				writeFixture(t, path, "sub/.keep", "")
				gitOK(t, path, "add", "-A")
				gitOK(t, path, "commit", "-qm", "clean subdirectory")
				s.pty = &fixturePty{terms: []api.TerminalSession{{CurrentCwd: filepath.Join(path, "sub"), Activity: api.ActivityIdle}}}
			case "live original cwd":
				s.pty = &fixturePty{terms: []api.TerminalSession{{Cwd: path, CurrentCwd: root, Activity: api.ActivityIdle}}}
			case "list failure":
				s.pty = &fixturePty{err: ptyclient.ErrUnavailable}
				code = 503
			case "exited":
				s.pty = &fixturePty{terms: []api.TerminalSession{{Cwd: path, Activity: api.ActivityExited}}}
				code = 204
			}
			callHandler(t, s.handleRemoveWorktree, "DELETE", "/worktrees?path="+url.QueryEscape(path), nil, code)
			if code == 204 {
				wantMissing(t, path)
				return
			}
			if _, err := os.Stat(filepath.Join(path, "tracked.txt")); err != nil {
				t.Fatalf("unsafe removal: %v", err)
			}
			if kind == "dirty" {
				wantContent(t, path, "tracked.txt", "keep edit\n")
			}
			if kind == "untracked" {
				wantContent(t, path, "new", "keep new\n")
			}
			if got := gitOK(t, root, "worktree", "list", "--porcelain"); !strings.Contains(got, wt.Path) {
				t.Fatal("registration lost on rejection")
			}
		})
	}
}

func TestGitWorktreeRootAndSymlinkEscapes(t *testing.T) {
	for _, kind := range []string{"symlink parent", "restricted linked root", "remove ancestor boundary"} {
		t.Run(kind, func(t *testing.T) {
			s, root := gitFixture(t, true)
			ctx := context.Background()
			outside := cleanReal(t.TempDir())
			if kind == "symlink parent" {
				if err := os.Symlink(outside, filepath.Join(root, ".worktrees")); err != nil {
					t.Fatal(err)
				}
				_, err := s.CreateWorktree(ctx, root, "escape", "")
				wantHTTPError(t, err, 403, "")
				wantMissing(t, filepath.Join(outside, "escape"))
				if gitOK(t, root, "branch", "--list", "escape") != "" {
					t.Fatal("escape branch created")
				}
				return
			}
			gitOK(t, root, "worktree", "add", "-qb", "linked", "--", filepath.Join(outside, "linked"))
			linked := filepath.Join(outside, "linked")
			cfg := config.Clone(s.d.Cfg)
			cfg.Files.Root = linked
			cfg.Agents.WorkspaceRoots = []string{linked}
			s.d.Settings = config.NewRuntime(cfg, root)
			if kind == "restricted linked root" {
				_, err := s.CreateWorktree(ctx, linked, "escape", "")
				wantHTTPError(t, err, 403, "")
				wantMissing(t, filepath.Join(root, ".worktrees", "escape"))
			} else {
				sub := filepath.Join(linked, "sub")
				if err := os.Mkdir(sub, 0o700); err != nil {
					t.Fatal(err)
				}
				cfg.Files.Root = sub
				cfg.Agents.WorkspaceRoots = []string{sub}
				s.d.Settings = config.NewRuntime(cfg, root)
				_, err := s.removeWorktree(ctx, sub)
				wantHTTPError(t, err, 403, "")
				wantContent(t, linked, "tracked.txt", "base\n")
			}
		})
	}
}

func TestGitTaskPreflightFailures(t *testing.T) {
	s, root := gitFixture(t, true)
	ctx := context.Background()
	_, err := s.task(ctx, root, "push")
	wantHTTPError(t, err, 503, "daemon")
	p := &fixturePty{}
	s.pty = p
	_, err = s.task(ctx, root, "push")
	wantHTTPError(t, err, 409, "no remote")
	_, err = s.task(ctx, root, "pull")
	wantHTTPError(t, err, 409, "no upstream")
	gitOK(t, root, "checkout", "--detach", "-q")
	_, err = s.task(ctx, root, "push")
	wantHTTPError(t, err, 409, "detached")
	gitOK(t, root, "checkout", "main", "-q")
	gitOK(t, root, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
	p.err = ptyclient.ErrUnavailable
	callHandler(t, s.handlePush, "POST", "/push", api.GitActionRequest{Path: root}, 503)
	p.err = errors.New("fixture create failure")
	callHandler(t, s.handlePush, "POST", "/push", api.GitActionRequest{Path: root}, 500)
	if gitOK(t, root, "status", "--porcelain") != "" {
		t.Fatal("failed task changed work")
	}
}
