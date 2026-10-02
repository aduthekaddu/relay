package workspaces

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/ptyclient"
)

// brief returns the cached GitBrief of a repo root (10 s TTL); nil when
// the directory is not a readable repository.
func (s *Service) brief(ctx context.Context, root string) *api.GitBrief {
	s.mu.Lock()
	if e, ok := s.briefs[root]; ok && time.Since(e.at) < briefTTL {
		s.mu.Unlock()
		return e.b
	}
	s.mu.Unlock()
	b := s.computeBrief(ctx, root)
	s.mu.Lock()
	if len(s.briefs) > 2048 {
		s.briefs = map[string]briefEntry{}
	}
	s.briefs[root] = briefEntry{b: b, at: time.Now()}
	s.mu.Unlock()
	return b
}

func (s *Service) computeBrief(ctx context.Context, root string) *api.GitBrief {
	r, err := s.git.run(ctx, readTimeout, root, nil, []string{"status", "--porcelain=v2", "--branch", "-z", "--untracked-files=normal"})
	if err != nil {
		return nil
	}
	st := parseStatusV2(r.Out)
	b := &api.GitBrief{Branch: st.Branch, Dirty: len(st.Files), Ahead: st.Ahead, Behind: st.Behind, At: time.Now().UTC()}
	if b.Branch == "" && len(st.Head) >= 7 {
		b.Branch = st.Head[:7]
	}
	if c := s.lastCommit(ctx, root); c != nil {
		b.Last = c
	}
	if url, err := s.git.out(ctx, root, "remote", "get-url", "origin"); err == nil {
		b.Remote = stripCredentials(url)
	}
	return b
}

// invalidate drops cached briefs of root after a mutation.
func (s *Service) invalidate(root string) {
	s.mu.Lock()
	delete(s.briefs, root)
	s.mu.Unlock()
	s.publish(api.EvWorkspaceChanged, map[string]string{"path": root})
}

func (s *Service) lastCommit(ctx context.Context, root string) *api.Commit {
	r, err := s.git.run(ctx, readTimeout, root, nil, []string{"log", "-1", "--format=" + logFormat})
	if err != nil {
		return nil
	}
	if cs := parseLog(r.Out); len(cs) == 1 {
		return &cs[0]
	}
	return nil
}

// status returns the full review status of the repo containing path.
func (s *Service) status(ctx context.Context, path string) (*api.GitStatus, error) {
	root, err := s.repoRoot(path)
	if err != nil {
		return nil, err
	}
	return s.statusOf(ctx, root, path)
}

func (s *Service) statusOf(ctx context.Context, root, path string) (*api.GitStatus, error) {
	r, err := s.git.run(ctx, readTimeout*2, root, nil, []string{"status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all"})
	if err != nil {
		return nil, gitErr(err)
	}
	st := parseStatusV2(r.Out)
	out := &api.GitStatus{Path: path, Root: root, Branch: st.Branch, Upstream: st.Upstream, Ahead: st.Ahead,
		Behind: st.Behind, Files: st.Files}
	if out.Files == nil {
		out.Files = []api.GitFile{}
	}
	var wg sync.WaitGroup
	var unstaged, staged map[string]numstat
	var gitDir, commonDir, remotes, stashes string
	var wts []api.Worktree
	wg.Add(6)
	go func() {
		defer wg.Done()
		if r, err := s.git.run(ctx, readTimeout, root, nil, []string{"diff", "--numstat", "-z", "--no-ext-diff"}); err == nil {
			unstaged = parseNumstat(r.Out)
		}
	}()
	go func() {
		defer wg.Done()
		if r, err := s.git.run(ctx, readTimeout, root, nil, []string{"diff", "--cached", "--numstat", "-z", "--no-ext-diff"}); err == nil {
			staged = parseNumstat(r.Out)
		}
	}()
	go func() {
		defer wg.Done()
		out.Last = s.lastCommit(ctx, root)
	}()
	go func() {
		defer wg.Done()
		gitDir, _ = s.git.out(ctx, root, "rev-parse", "--path-format=absolute", "--git-dir")
		commonDir, _ = s.git.out(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	}()
	go func() {
		defer wg.Done()
		remotes, _ = s.git.out(ctx, root, "remote")
		stashes, _ = s.git.out(ctx, root, "stash", "list", "--format=%H")
	}()
	go func() {
		defer wg.Done()
		if r, err := s.git.run(ctx, readTimeout, root, nil, []string{"worktree", "list", "--porcelain", "-z"}); err == nil {
			wts = parseWorktrees(r.Out)
		}
	}()
	wg.Wait()
	for i := range out.Files {
		f := &out.Files[i]
		if n, ok := staged[f.Path]; ok {
			f.Added, f.Removed, f.Binary = n.Added, n.Removed, n.Binary
		}
		if n, ok := unstaged[f.Path]; ok {
			f.Added += n.Added
			f.Removed += n.Removed
			f.Binary = f.Binary || n.Binary
		}
		if f.Index == "?" {
			f.Added = countLines(filepath.Join(root, f.Path))
		}
	}
	out.Worktree = gitDir != "" && commonDir != "" && filepath.Clean(gitDir) != filepath.Clean(commonDir)
	if remotes != "" {
		out.Remotes = strings.Fields(remotes)
	}
	if stashes != "" {
		out.Stashes = len(strings.Split(stashes, "\n"))
	}
	if len(wts) > 1 {
		out.Worktrees = wts
	}
	return out, nil
}

// countLines counts lines of a small regular untracked text file (0 if skipped).
func countLines(path string) int {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return 0
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	if strings.IndexByte(string(b[:min(len(b), 8000)]), 0) >= 0 {
		return 0 // binary
	}
	n := strings.Count(string(b), "\n")
	if len(b) > 0 && b[len(b)-1] != '\n' {
		n++
	}
	return n
}

// diff returns a unified diff of the repo (or one file), ≤ 1 MiB.
func (s *Service) diff(ctx context.Context, path, file string, staged bool) (*api.GitDiff, error) {
	root, err := s.repoRoot(path)
	if err != nil {
		return nil, err
	}
	out := &api.GitDiff{Path: path, File: file, Staged: staged}
	args := []string{"diff", "--no-color", "--no-ext-diff", "--find-renames"}
	if staged {
		args = append(args, "--cached")
	}
	if file != "" {
		files, err := cleanFiles([]string{file})
		if err != nil {
			return nil, err
		}
		file = files[0]
		out.File = file
		if err := validateFileParents(root, files); err != nil {
			return nil, err
		}
		if !staged && s.isUntracked(ctx, root, file) {
			return s.untrackedDiff(ctx, root, out)
		}
		args = append(args, "--", file)
	}
	r, err := s.git.run(ctx, 5*time.Second, root, nil, args)
	if err != nil {
		return nil, gitErr(err)
	}
	out.Diff, out.Truncated = capDiff(r.Out, r.Truncated)
	return out, nil
}

func capDiff(b []byte, truncated bool) (string, bool) {
	if len(b) > maxDiff {
		cut := maxDiff
		if i := strings.LastIndexByte(string(b[:cut]), '\n'); i > 0 {
			cut = i + 1
		}
		return string(b[:cut]), true
	}
	return string(b), truncated
}

func (s *Service) isUntracked(ctx context.Context, root, file string) bool {
	out, err := s.git.out(ctx, root, "ls-files", "--others", "--exclude-standard", "--", file)
	return err == nil && out != ""
}

// untrackedDiff renders a new file as an all-added diff.
func (s *Service) untrackedDiff(ctx context.Context, root string, out *api.GitDiff) (*api.GitDiff, error) {
	r, err := s.git.run(ctx, 5*time.Second, root, nil,
		[]string{"diff", "--no-color", "--no-ext-diff", "--no-index", "--", "/dev/null", out.File}, 1)
	if err != nil {
		return nil, gitErr(err)
	}
	out.Diff, out.Truncated = capDiff(r.Out, r.Truncated)
	return out, nil
}

// logOf returns the newest commits of the repo containing path.
func (s *Service) logOf(ctx context.Context, path string, limit int) ([]api.Commit, error) {
	root, err := s.repoRoot(path)
	if err != nil {
		return nil, err
	}
	r, err := s.git.run(ctx, 3*time.Second, root, nil, []string{"log", "-n", strconv.Itoa(limit), "--format=" + logFormat})
	if err != nil {
		var ge *gitError
		if errors.As(err, &ge) && strings.Contains(ge.Stderr, "does not have any commits") {
			return []api.Commit{}, nil
		}
		return nil, gitErr(err)
	}
	return parseLog(r.Out), nil
}

// ---------------------------------------------------------------------------
// Mutations

// stage adds files (all changes when files is empty) to the index.
func (s *Service) stage(ctx context.Context, req api.GitActionRequest) (*api.GitStatus, error) {
	root, files, err := s.actionTarget(req, false)
	if err != nil {
		return nil, err
	}
	defer s.lockRepo(root)()
	args := []string{"add", "-A", "--"}
	if len(files) == 0 {
		args = append(args, ".")
	}
	if _, err := s.git.run(ctx, writeTimeout, root, nil, append(args, files...)); err != nil {
		return nil, gitErr(err)
	}
	s.invalidate(root)
	return s.statusOf(ctx, root, req.Path)
}

// unstage removes files (all when empty) from the index, keeping changes.
func (s *Service) unstage(ctx context.Context, req api.GitActionRequest) (*api.GitStatus, error) {
	root, files, err := s.actionTarget(req, false)
	if err != nil {
		return nil, err
	}
	defer s.lockRepo(root)()
	var args []string
	if s.hasHead(ctx, root) {
		args = []string{"reset", "-q", "--"}
	} else {
		args = []string{"rm", "-r", "-q", "--cached", "--"}
	}
	if len(files) == 0 {
		args = append(args, ".")
	}
	if _, err := s.git.run(ctx, writeTimeout, root, nil, append(args, files...)); err != nil {
		return nil, gitErr(err)
	}
	s.invalidate(root)
	return s.statusOf(ctx, root, req.Path)
}

func (s *Service) hasHead(ctx context.Context, root string) bool {
	_, err := s.git.out(ctx, root, "rev-parse", "--verify", "-q", "HEAD")
	return err == nil
}

// discard throws away all changes (staged and unstaged) to the given
// files: tracked files are restored from HEAD, new files are deleted.
// Files must be named explicitly.
func (s *Service) discard(ctx context.Context, req api.GitActionRequest) (*api.GitStatus, error) {
	root, files, err := s.actionTarget(req, true)
	if err != nil {
		return nil, err
	}
	defer s.lockRepo(root)()
	cur, err := s.statusOf(ctx, root, req.Path)
	if err != nil {
		return nil, err
	}
	byPath := map[string]api.GitFile{}
	for _, f := range cur.Files {
		byPath[f.Path] = f
	}
	head := s.hasHead(ctx, root)
	var untracked, added, restore []string
	for _, p := range files {
		f, ok := byPath[p]
		if !ok {
			continue // already clean
		}
		switch {
		case f.Index == "?":
			untracked = append(untracked, p)
		case f.OrigPath != "" && head:
			if _, err := os.Lstat(filepath.Join(root, f.OrigPath)); err == nil {
				return nil, httpx.Conflict("rename source has new work; discard it separately first")
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, httpx.Conflict("cannot inspect rename source")
			}
			// Restore both sides in one index-locking command. Paths absent
			// from HEAD are removed by git restore's default no-overlay mode.
			restore = append(restore, p, f.OrigPath)
		case !head:
			added = append(added, p)
		default:
			restore = append(restore, p)
		}
	}
	// Run index-locking operations before deleting untracked files so a busy
	// or broken index cannot cause an error after those files are lost.
	if len(restore) > 0 {
		if _, err := s.git.run(ctx, writeTimeout, root, nil,
			append([]string{"restore", "--source=HEAD", "--staged", "--worktree", "--"}, restore...)); err != nil {
			return nil, gitErr(err)
		}
	}
	if len(added) > 0 {
		if _, err := s.git.run(ctx, writeTimeout, root, nil, append([]string{"rm", "-f", "-q", "-r", "--"}, added...)); err != nil {
			return nil, gitErr(err)
		}
	}
	if len(untracked) > 0 {
		if _, err := s.git.run(ctx, writeTimeout, root, nil, append([]string{"clean", "-f", "-q", "--"}, untracked...)); err != nil {
			return nil, gitErr(err)
		}
	}
	s.invalidate(root)
	return s.statusOf(ctx, root, req.Path)
}

// commit records the index with message.
func (s *Service) commit(ctx context.Context, req api.GitActionRequest) (*api.GitStatus, error) {
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "commit message is required", Field: "message"}
	}
	if len(msg) > maxMessage {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "commit message too long", Field: "message"}
	}
	root, err := s.repoRoot(req.Path)
	if err != nil {
		return nil, err
	}
	defer s.lockRepo(root)()
	if _, err := s.git.run(ctx, readTimeout, root, nil, []string{"diff", "--cached", "--quiet"}); err == nil {
		return nil, httpx.Conflict("nothing staged to commit")
	}
	if _, err := s.git.run(ctx, hookTimeout, root, strings.NewReader(msg+"\n"),
		[]string{"commit", "-q", "--cleanup=strip", "-F", "-"}); err != nil {
		return nil, gitErr(err)
	}
	s.invalidate(root)
	return s.statusOf(ctx, root, req.Path)
}

// actionTarget validates the repo and file list of a mutation.
func (s *Service) actionTarget(req api.GitActionRequest, needFiles bool) (string, []string, error) {
	root, err := s.repoRoot(req.Path)
	if err != nil {
		return "", nil, err
	}
	files, err := cleanFiles(req.Files)
	if err != nil {
		return "", nil, err
	}
	if needFiles && len(files) == 0 {
		return "", nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "files are required", Field: "files"}
	}
	if err := validateFileParents(root, files); err != nil {
		return "", nil, err
	}
	return root, files, nil
}

// task runs a git command visibly in a ptyd task terminal (push/pull).
func (s *Service) task(ctx context.Context, path, verb string) (*api.TerminalSession, error) {
	root, err := s.repoRoot(path)
	if err != nil {
		return nil, err
	}
	if s.pty == nil {
		return nil, httpx.Unavailable("terminal daemon unavailable")
	}
	st, err := s.statusOf(ctx, root, path)
	if err != nil {
		return nil, err
	}
	argv := []string{s.git.bin}
	switch verb {
	case "push":
		argv = append(argv, "push")
		if st.Upstream == "" {
			if st.Branch == "" {
				return nil, httpx.Conflict("cannot push a detached HEAD")
			}
			if len(st.Remotes) == 0 {
				return nil, httpx.Conflict("repository has no remote")
			}
			remote := st.Remotes[0]
			for _, r := range st.Remotes {
				if r == "origin" {
					remote = r
				}
			}
			argv = append(argv, "-u", remote, "HEAD")
		}
	case "pull":
		if st.Upstream == "" {
			return nil, httpx.Conflict("branch has no upstream to pull from")
		}
		argv = append(argv, "pull", "--ff-only")
	default:
		return nil, fmt.Errorf("unknown git task %q", verb)
	}
	name := "git " + verb + " · " + filepath.Base(root)
	t, err := s.pty.Create(ctx, ptyclient.CreateSpec{
		CreateTerminalRequest: api.CreateTerminalRequest{Name: name, Command: argv, Cwd: root, Kind: api.KindTask,
			Meta: map[string]string{"task": "git." + verb, "workspace": root}},
		Workspace: root,
	})
	if err != nil {
		if errors.Is(err, ptyclient.ErrUnavailable) {
			return nil, httpx.Unavailable("terminal daemon unavailable")
		}
		return nil, err
	}
	s.invalidate(root)
	return t, nil
}

// ---------------------------------------------------------------------------
// Worktrees

var (
	branchRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)
	hashRe   = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
)

// validBranch applies git's ref rules plus a conservative character set
// (no leading dash, so it can never be parsed as an option).
func validBranch(b string) bool {
	if !branchRe.MatchString(b) || strings.Contains(b, "..") || strings.Contains(b, "//") ||
		strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".") || strings.HasSuffix(b, ".lock") ||
		strings.Contains(b, "/.") || strings.Contains(b, "@{") {
		return false
	}
	return true
}

// CreateWorktree adds a linked worktree on a new branch:
//
//	git worktree add -b <branch> <repo>/.worktrees/<branch> [base]
//
// .worktrees/ is added to the repository's local exclude file so the
// checkout does not show up as untracked. Used by the API and by agent
// launches (internal/agents via a type assertion).
func (s *Service) CreateWorktree(ctx context.Context, repo, branch, base string) (*api.Worktree, error) {
	root, err := s.repoRoot(repo)
	if err != nil {
		return nil, err
	}
	branch = strings.TrimSpace(branch)
	if !validBranch(branch) {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "invalid branch name", Field: "branch"}
	}
	base = strings.TrimSpace(base)
	if base != "" && (!validBranch(base) && !hashRe.MatchString(base)) {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "invalid base", Field: "base"}
	}
	if _, err := s.git.out(ctx, root, "check-ref-format", "--branch", branch); err != nil {
		return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "invalid branch name", Field: "branch"}
	}
	// A linked worktree's own root is not where new worktrees belong:
	// place them under the main checkout.
	if common, err := s.git.out(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil &&
		filepath.Base(common) == ".git" {
		root = filepath.Dir(common)
	}
	root, err = s.resolveDir(root)
	if err != nil {
		return nil, err
	}
	defer s.lockRepo(root)()
	dest := filepath.Join(root, ".worktrees", filepath.FromSlash(branch))
	if err := validateFileParents(root, []string{filepath.Join(".worktrees", filepath.FromSlash(branch))}); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(dest); err == nil {
		return nil, httpx.Conflict("worktree directory already exists")
	}
	if err := s.excludeWorktrees(ctx, root); err != nil {
		s.log().Debug("workspaces: exclude .worktrees", "err", err)
	}
	args := []string{"worktree", "add", "-q", "-b", branch, "--", dest}
	if base != "" {
		args = append(args, base)
	}
	if _, err := s.git.run(ctx, writeTimeout, root, nil, args); err != nil {
		return nil, gitErr(err)
	}
	head, _ := s.git.out(ctx, dest, "rev-parse", "HEAD")
	s.invalidate(root)
	s.forgetRoot(dest)
	return &api.Worktree{Path: cleanReal(dest), Branch: branch, Head: head}, nil
}

// excludeWorktrees adds "/.worktrees/" to .git/info/exclude once.
func (s *Service) excludeWorktrees(ctx context.Context, root string) error {
	dir, err := s.git.out(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "info", "exclude")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(l); t == "/.worktrees/" || t == ".worktrees/" || t == ".worktrees" {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	prefix := ""
	if len(b) > 0 && b[len(b)-1] != '\n' {
		prefix = "\n"
	}
	_, werr := f.WriteString(prefix + "# relay: agent worktrees\n/.worktrees/\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// removeWorktree removes a linked worktree (never the main checkout, and
// never with --force: uncommitted changes make git refuse).
func (s *Service) removeWorktree(ctx context.Context, path string) (string, error) {
	dir, err := s.resolveDir(path)
	if err != nil {
		return "", err
	}
	gitDir, err1 := s.git.out(ctx, dir, "rev-parse", "--path-format=absolute", "--git-dir")
	common, err2 := s.git.out(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err1 != nil || err2 != nil {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "not a git worktree", Field: "path"}
	}
	if filepath.Clean(gitDir) == filepath.Clean(common) {
		return "", httpx.Conflict("refusing to remove the main worktree")
	}
	top, err := s.git.out(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", gitErr(err)
	}
	if _, err := s.resolveDir(top); err != nil {
		return "", err
	}
	main := filepath.Dir(common)
	defer s.lockRepo(main)()
	if s.pty != nil {
		terms, err := s.pty.List(ctx)
		if err != nil {
			return "", httpx.Unavailable("cannot verify worktree terminal usage")
		}
		for _, term := range terms {
			if term.Activity != api.ActivityExited &&
				(within(cleanReal(term.Cwd), top) || within(cleanReal(term.CurrentCwd), top)) {
				return "", httpx.Conflict("worktree is in use by a terminal")
			}
		}
	}
	if _, err := s.git.run(ctx, writeTimeout, main, nil, []string{"worktree", "remove", "--", top}); err != nil {
		return "", gitErr(err)
	}
	s.invalidate(main)
	s.forgetRoot(top)
	return top, nil
}

// forgetRoot drops RootOf cache entries at or below dir.
func (s *Service) forgetRoot(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := range s.rootCache {
		if within(p, dir) {
			delete(s.rootCache, p)
		}
	}
}
