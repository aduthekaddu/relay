// Package workspaces discovers project workspaces (git repositories under
// the configured roots plus the working directories of agent sessions and
// terminals) and exposes git status, diffs, commits, push/pull and
// worktrees for the review screen.
//
// Every git call runs `git` with an argv slice, a timeout and capped
// output. Paths from clients are cleaned, symlink-resolved and must lie
// inside the files root or a configured workspace root.
package workspaces

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
	"github.com/aduthekaddu/relay/internal/core"
	"github.com/aduthekaddu/relay/internal/httpx"
	"github.com/aduthekaddu/relay/internal/ptyclient"
	"github.com/aduthekaddu/relay/internal/server"
)

// Tuning.
const (
	discoverDepth = 3                // levels below each workspace root
	discoverTTL   = time.Minute      // repo discovery cache
	briefTTL      = 10 * time.Second // GitBrief cache
	rootTTL       = time.Minute      // RootOf cache
	maxWorkspaces = 300
	briefWorkers  = 4
)

// skipDirs are never descended into during discovery.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, ".cache": true, ".git": true, ".worktrees": true,
	"target": true, "dist": true, "build": true, ".venv": true, "venv": true, "__pycache__": true,
	".next": true, "Library": true,
}

// migrations for the "workspaces" feature (append-only).
var migrations = []string{
	`CREATE TABLE workspace_pins (
		path TEXT PRIMARY KEY,
		pinned INTEGER NOT NULL DEFAULT 1,
		at INTEGER NOT NULL DEFAULT 0
	)`,
}

// ptyAPI is the subset of the ptyd client used here (faked in tests).
type ptyAPI interface {
	List(ctx context.Context) ([]api.TerminalSession, error)
	Create(ctx context.Context, spec ptyclient.CreateSpec) (*api.TerminalSession, error)
}

// cwdSource is implemented by internal/agents (d.Agents).
type cwdSource interface {
	AgentCwds(ctx context.Context, limit int) []api.CwdUsage
}

// Service implements the workspaces feature and core.WorkspaceService.
type Service struct {
	d     *core.Deps
	git   gitRunner
	pty   ptyAPI
	home  string
	roots []string // discovery roots (existing, cleaned)
	allow []string // allowed path prefixes for git operations

	mu        sync.Mutex
	repos     []string // discovered repo roots
	reposAt   time.Time
	briefs    map[string]briefEntry
	rootCache map[string]rootEntry
	discover  sync.Mutex // one discovery walk at a time
	opMu      sync.Map   // repo root -> *sync.Mutex serialising mutations
}

type briefEntry struct {
	b  *api.GitBrief
	at time.Time
}

type rootEntry struct {
	root string
	at   time.Time
}

var _ core.WorkspaceService = (*Service)(nil)

// New builds the service and migrates its table. It starts no goroutines.
func New(d *core.Deps) (*Service, error) {
	if d.Store != nil {
		if err := d.Store.Migrate(context.Background(), "workspaces", migrations); err != nil {
			return nil, fmt.Errorf("workspaces migrations: %w", err)
		}
	}
	home := d.Paths.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	bin, err := exec.LookPath("git")
	if err != nil {
		bin = "git" // reported per request when missing
	}
	s := &Service{d: d, git: gitRunner{bin: bin}, home: home,
		briefs: map[string]briefEntry{}, rootCache: map[string]rootEntry{}}
	if d.Pty != nil {
		s.pty = d.Pty
	}
	var roots []string
	fileRoot := home
	if d.Cfg != nil {
		roots = d.Cfg.Agents.WorkspaceRoots
		if d.Cfg.Files.Root != "" {
			fileRoot = d.Cfg.Files.Root
		}
	}
	s.allow = append(s.allow, cleanReal(fileRoot))
	for _, r := range roots {
		r = expandHome(r, home)
		if st, err := os.Stat(r); err == nil && st.IsDir() {
			rr := cleanReal(r)
			s.roots = append(s.roots, rr)
			s.allow = append(s.allow, rr)
		}
	}
	return s, nil
}

// Routes registers the HTTP API.
func (s *Service) Routes(rt *server.Router) {
	rt.Handle("GET /api/v1/workspaces", s.handleList)
	rt.Handle("POST /api/v1/workspaces/pin", s.handlePin)
	rt.Handle("GET /api/v1/workspaces/git/status", s.handleStatus)
	rt.Handle("GET /api/v1/workspaces/git/diff", s.handleDiff)
	rt.Handle("GET /api/v1/workspaces/git/log", s.handleLog)
	rt.Handle("POST /api/v1/workspaces/git/stage", s.handleStage)
	rt.Handle("POST /api/v1/workspaces/git/unstage", s.handleUnstage)
	rt.Handle("POST /api/v1/workspaces/git/discard", s.handleDiscard)
	rt.Handle("POST /api/v1/workspaces/git/commit", s.handleCommit)
	rt.Handle("POST /api/v1/workspaces/git/push", s.handlePush)
	rt.Handle("POST /api/v1/workspaces/git/pull", s.handlePull)
	rt.Handle("POST /api/v1/workspaces/git/worktrees", s.handleCreateWorktree)
	rt.Handle("DELETE /api/v1/workspaces/git/worktrees", s.handleRemoveWorktree)
}

func (s *Service) log() *slog.Logger {
	if s.d.Log != nil {
		return s.d.Log
	}
	return slog.Default()
}

func (s *Service) publish(t string, data any) {
	if s.d.Bus != nil {
		s.d.Bus.Publish(t, data)
	}
}

func expandHome(p, home string) string {
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	}
	return p
}

// cleanReal cleans p and resolves symlinks when it exists.
func cleanReal(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// within reports whether path is dir or below it.
func within(path, dir string) bool {
	if dir == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == dir || strings.HasPrefix(path, dir+"/")
}

// ---------------------------------------------------------------------------
// RootOf

// RootOf implements core.WorkspaceService: the git work-tree root that
// contains path (a linked worktree is its own root), or "". It only stats
// the filesystem (no git calls) and caches results.
func (s *Service) RootOf(path string) string {
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	path = filepath.Clean(path)
	s.mu.Lock()
	if e, ok := s.rootCache[path]; ok && time.Since(e.at) < rootTTL {
		s.mu.Unlock()
		return e.root
	}
	s.mu.Unlock()
	root := findRoot(path)
	s.mu.Lock()
	if len(s.rootCache) > 8192 {
		s.rootCache = map[string]rootEntry{}
	}
	s.rootCache[path] = rootEntry{root: root, at: time.Now()}
	s.mu.Unlock()
	return root
}

// findRoot walks up from path looking for a .git directory or file.
func findRoot(path string) string {
	for dir, i := path, 0; i < 64; i++ {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// ---------------------------------------------------------------------------
// Discovery

// discoverRepos walks the workspace roots (depth ≤ 3) for git repos.
func (s *Service) discoverRepos(ctx context.Context) []string {
	s.discover.Lock()
	defer s.discover.Unlock()
	s.mu.Lock()
	if time.Since(s.reposAt) < discoverTTL && s.repos != nil {
		r := s.repos
		s.mu.Unlock()
		return r
	}
	s.mu.Unlock()
	set := map[string]bool{}
	for _, root := range s.roots {
		walkRepos(ctx, root, discoverDepth, set)
	}
	repos := make([]string, 0, len(set))
	for r := range set {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	s.mu.Lock()
	s.repos, s.reposAt = repos, time.Now()
	s.mu.Unlock()
	return repos
}

// walkRepos adds git repos found under dir (depth levels) to set. A repo
// is not descended into (nested repos are found via their sessions).
func walkRepos(ctx context.Context, dir string, depth int, set map[string]bool) {
	if ctx.Err() != nil || len(set) >= maxWorkspaces {
		return
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
		set[dir] = true
		return
	}
	if depth == 0 {
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		if !e.IsDir() || skipDirs[name] || strings.HasPrefix(name, ".") {
			continue
		}
		walkRepos(ctx, filepath.Join(dir, name), depth-1, set)
	}
}

// Paths returns known workspace roots without computing git briefs (cheap;
// used by internal/agents to locate per-project history).
func (s *Service) Paths(ctx context.Context) []string {
	ws := s.collect(ctx)
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.Path)
	}
	return out
}

// collect merges discovered repos, pins, agent cwds and terminal cwds
// into workspaces (without git briefs).
func (s *Service) collect(ctx context.Context) []*api.Workspace {
	byPath := map[string]*api.Workspace{}
	get := func(root string) *api.Workspace {
		w, ok := byPath[root]
		if !ok {
			w = &api.Workspace{Path: root, Name: filepath.Base(root)}
			byPath[root] = w
		}
		return w
	}
	for _, r := range s.discoverRepos(ctx) {
		get(r)
	}
	for path, at := range s.pins(ctx) {
		if st, err := os.Stat(path); err == nil && st.IsDir() {
			w := get(path)
			w.Pinned = true
			if at.After(w.LastUsedAt) {
				w.LastUsedAt = at
			}
		}
	}
	if src, ok := s.d.Agents.(cwdSource); ok && s.d.Agents != nil {
		for _, u := range src.AgentCwds(ctx, 1000) {
			root := s.RootOf(u.Path)
			if root == "" || root == s.home {
				continue
			}
			w := get(root)
			w.Agents += u.Sessions
			if u.LastUsedAt.After(w.LastUsedAt) {
				w.LastUsedAt = u.LastUsedAt
			}
		}
	}
	if s.pty != nil {
		lctx, cancel := context.WithTimeout(ctx, time.Second)
		terms, _ := s.pty.List(lctx)
		cancel()
		for _, t := range terms {
			if t.Activity == api.ActivityExited {
				continue
			}
			cwd := t.CurrentCwd
			if cwd == "" {
				cwd = t.Cwd
			}
			root := s.RootOf(cwd)
			if root == "" || root == s.home {
				continue
			}
			w := get(root)
			w.Terminals++
			if at := latest(t.LastOutputAt, t.LastInputAt, t.CreatedAt); at.After(w.LastUsedAt) {
				w.LastUsedAt = at
			}
		}
	}
	out := make([]*api.Workspace, 0, len(byPath))
	for _, w := range byPath {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if !a.LastUsedAt.Equal(b.LastUsedAt) {
			return a.LastUsedAt.After(b.LastUsedAt)
		}
		return a.Path < b.Path
	})
	if len(out) > maxWorkspaces {
		out = out[:maxWorkspaces]
	}
	return out
}

func latest(ts ...time.Time) time.Time {
	var m time.Time
	for _, t := range ts {
		if t.After(m) {
			m = t
		}
	}
	return m
}

// List implements core.WorkspaceService: every workspace with its git
// brief and languages, pinned first, then most recently used.
func (s *Service) List(ctx context.Context) ([]api.Workspace, error) {
	ws := s.collect(ctx)
	sem := make(chan struct{}, briefWorkers)
	var wg sync.WaitGroup
	for _, w := range ws {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			w.Git = s.brief(ctx, w.Path)
			w.Languages = languages(w.Path)
		}()
	}
	wg.Wait()
	out := make([]api.Workspace, len(ws))
	for i, w := range ws {
		out[i] = *w
	}
	return out, nil
}

// languageMarkers maps marker files to languages.
var languageMarkers = []struct{ file, lang string }{
	{"go.mod", "Go"}, {"tsconfig.json", "TypeScript"}, {"package.json", "JavaScript"},
	{"Cargo.toml", "Rust"}, {"pyproject.toml", "Python"}, {"requirements.txt", "Python"},
	{"Gemfile", "Ruby"}, {"pom.xml", "Java"}, {"build.gradle", "Java"}, {"build.gradle.kts", "Kotlin"},
	{"composer.json", "PHP"}, {"Package.swift", "Swift"}, {"mix.exs", "Elixir"}, {"deno.json", "TypeScript"},
	{"CMakeLists.txt", "C++"}, {"pubspec.yaml", "Dart"}, {"build.zig", "Zig"},
}

// languages detects languages from marker files in the repo root.
func languages(dir string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range languageMarkers {
		if seen[m.lang] {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, m.file)); err == nil {
			seen[m.lang] = true
			out = append(out, m.lang)
		}
	}
	if seen["TypeScript"] && seen["JavaScript"] {
		out = removeString(out, "JavaScript")
	}
	return out
}

func removeString(list []string, s string) []string {
	out := list[:0]
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Pins

func (s *Service) pins(ctx context.Context) map[string]time.Time {
	out := map[string]time.Time{}
	if s.d.Store == nil {
		return out
	}
	rows, err := s.d.Store.DB.QueryContext(ctx, `SELECT path, at FROM workspace_pins WHERE pinned=1`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var at int64
		if rows.Scan(&p, &at) == nil {
			out[p] = time.UnixMilli(at).UTC()
		}
	}
	return out
}

// pin pins or unpins a directory as a workspace.
func (s *Service) pin(ctx context.Context, path string, pinned bool) (*api.Workspace, error) {
	dir, err := s.resolveDir(path)
	if err != nil {
		return nil, err
	}
	if root := s.RootOf(dir); root != "" {
		dir = root
	}
	if s.d.Store == nil {
		return nil, httpx.Unavailable("storage unavailable")
	}
	if pinned {
		_, err = s.d.Store.DB.ExecContext(ctx, `INSERT INTO workspace_pins(path, pinned, at) VALUES(?,1,?)
			ON CONFLICT(path) DO UPDATE SET pinned=1, at=excluded.at`, dir, time.Now().UnixMilli())
	} else {
		_, err = s.d.Store.DB.ExecContext(ctx, `DELETE FROM workspace_pins WHERE path=?`, dir)
	}
	if err != nil {
		return nil, fmt.Errorf("pin workspace: %w", err)
	}
	w := api.Workspace{Path: dir, Name: filepath.Base(dir), Pinned: pinned, Languages: languages(dir)}
	w.Git = s.brief(ctx, dir)
	s.publish(api.EvWorkspaceChanged, map[string]string{"path": dir})
	return &w, nil
}

// ---------------------------------------------------------------------------
// Path validation

// resolveDir validates a client path: absolute (or ~), cleaned, symlinks
// resolved, an existing directory inside an allowed root.
func (s *Service) resolveDir(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "path is required", Field: "path"}
	}
	p = expandHome(p, s.home)
	if !filepath.IsAbs(p) || strings.ContainsRune(p, 0) {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "path must be absolute", Field: "path"}
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(p))
	if err != nil {
		return "", httpx.NotFound("path not found")
	}
	st, err := os.Stat(real)
	if err != nil || !st.IsDir() {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "path is not a directory", Field: "path"}
	}
	for _, a := range s.allow {
		if within(real, a) {
			return real, nil
		}
	}
	return "", httpx.Forbidden("path is outside the allowed roots")
}

// repoRoot validates path and returns its git root.
func (s *Service) repoRoot(p string) (string, error) {
	dir, err := s.resolveDir(p)
	if err != nil {
		return "", err
	}
	root := findRoot(dir)
	if root == "" {
		return "", &httpx.Err{Status: 400, Code: "bad_request", Message: "not a git repository", Field: "path"}
	}
	return root, nil
}

// cleanFiles validates repo-relative paths from a client.
func cleanFiles(files []string) ([]string, error) {
	if len(files) > maxFiles {
		return nil, httpx.BadRequest("too many files")
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		if f == "" || filepath.IsAbs(f) || strings.ContainsRune(f, 0) {
			return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "invalid file path", Field: "files"}
		}
		c := filepath.Clean(f)
		if c == "." || c == ".." || strings.HasPrefix(c, "../") || c == ".git" || strings.HasPrefix(c, ".git/") {
			return nil, &httpx.Err{Status: 400, Code: "bad_request", Message: "invalid file path", Field: "files"}
		}
		out = append(out, c)
	}
	return out, nil
}

// lockRepo serialises mutations per repository.
func (s *Service) lockRepo(root string) func() {
	m, _ := s.opMu.LoadOrStore(root, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// gitErr turns git failures into client-safe HTTP errors.
func gitErr(err error) error {
	if err == nil {
		return nil
	}
	var he *httpx.Err
	if errors.As(err, &he) {
		return err
	}
	var ge *gitError
	if errors.As(err, &ge) {
		return httpx.Conflict(ge.Error())
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return httpx.Unavailable("git is not installed")
	}
	return httpx.Conflict(err.Error())
}
