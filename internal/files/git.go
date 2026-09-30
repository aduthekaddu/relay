package files

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aduthekaddu/relay/internal/api"
)

const (
	gitCacheTTL = 5 * time.Second
	gitTimeout  = 2 * time.Second
	gitMaxOut   = 8 << 20 // porcelain output cap
	gitCacheMax = 64
)

// gitCache runs `git status --porcelain=v1 -z` at most once per repo per
// TTL and shares the result between concurrent listings.
type gitCache struct {
	mu      sync.Mutex
	gitPath string
	repos   map[string]*gitEntry // key: repo root
}

type gitEntry struct {
	at     time.Time
	done   chan struct{}
	status *gitStatus
}

// gitStatus is a parsed porcelain listing.
type gitStatus struct {
	files map[string]string // repo-relative path → letter ("dir/" for untracked folders)
	dirs  map[string]string // repo-relative folder → strongest letter below it
}

func newGitStatus(files map[string]string) *gitStatus {
	st := &gitStatus{files: files, dirs: map[string]string{}}
	for p, l := range files {
		p = strings.TrimSuffix(p, "/")
		for i := 0; i < len(p); i++ {
			if p[i] != '/' {
				continue
			}
			d := p[:i]
			if rank(l) > rank(st.dirs[d]) {
				st.dirs[d] = l
			}
		}
	}
	return st
}

func newGitCache() *gitCache {
	g := &gitCache{repos: map[string]*gitEntry{}}
	if p, err := exec.LookPath("git"); err == nil {
		g.gitPath = p
	}
	return g
}

// findRepoRoot walks up from dir looking for a .git entry (dir or file,
// for worktrees and submodules).
func findRepoRoot(dir string) string {
	for d := dir; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// statusFor returns the repo root containing dir and its status map. The
// root is "" when dir is not in a repo or git is unavailable.
func (g *gitCache) statusFor(ctx context.Context, dir string) (string, *gitStatus) {
	if g.gitPath == "" {
		return "", nil
	}
	root := findRepoRoot(dir)
	if root == "" {
		return "", nil
	}
	g.mu.Lock()
	e := g.repos[root]
	if e != nil && time.Since(e.at) < gitCacheTTL {
		g.mu.Unlock()
		<-e.done
		return root, e.status
	}
	e = &gitEntry{at: time.Now(), done: make(chan struct{})}
	if len(g.repos) >= gitCacheMax {
		for k, v := range g.repos {
			if time.Since(v.at) >= gitCacheTTL {
				delete(g.repos, k)
			}
		}
	}
	g.repos[root] = e
	g.mu.Unlock()

	e.status = g.run(ctx, root)
	close(e.done)
	return root, e.status
}

func (g *gitCache) run(ctx context.Context, root string) *gitStatus {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.gitPath, "-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=normal", "--ignore-submodules=dirty")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var out limitedBuffer
	out.max = gitMaxOut
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return newGitStatus(map[string]string{})
	}
	return newGitStatus(parsePorcelainZ(out.Bytes()))
}

// parsePorcelainZ parses `git status --porcelain=v1 -z` output into a map
// of repo-relative path → single status letter. Renames/copies consume
// the following NUL-separated original path.
func parsePorcelainZ(b []byte) map[string]string {
	out := map[string]string{}
	fields := bytes.Split(b, []byte{0})
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		x, y := f[0], f[1]
		p := string(f[3:])
		out[p] = statusLetter(x, y)
		if x == 'R' || x == 'C' {
			i++ // skip the original path
		}
	}
	return out
}

func statusLetter(x, y byte) string {
	switch {
	case x == '?' && y == '?':
		return "?"
	case x == '!':
		return "!"
	case x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D'):
		return "U"
	case x != ' ':
		return string(x)
	default:
		return string(y)
	}
}

// applyGit sets Git letters on entries: files by exact match, folders by
// the strongest status of anything below them.
func applyGit(entries []api.FileEntry, root string, st *gitStatus) {
	if st == nil || len(st.files) == 0 {
		return
	}
	for i := range entries {
		rel, err := filepath.Rel(root, entries[i].Path)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		rel = filepath.ToSlash(rel)
		if l, ok := st.files[rel]; ok {
			entries[i].Git = l
		} else if l, ok := st.files[rel+"/"]; ok { // untracked folder
			entries[i].Git = l
		} else if entries[i].Type == "dir" {
			entries[i].Git = st.dirs[rel]
		}
	}
}

func rank(l string) int {
	switch l {
	case "":
		return 0
	case "!":
		return 1
	case "?":
		return 2
	case "U":
		return 5
	case "D":
		return 4
	}
	return 3
}

// limitedBuffer is a bytes.Buffer that silently drops writes past max.
type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.Len(); room < len(p) {
		if room > 0 {
			l.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return l.Buffer.Write(p)
}
